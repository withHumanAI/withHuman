import {
  useEffect,
  useRef,
  useState,
  type ComponentType,
  type FormEvent,
  type ReactNode,
  type SVGProps,
} from "react";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Logo } from "@/ds/components/logo";
import { Spinner } from "@/ds/components/spinner";
import { TextField } from "@/ds/components/text-field";
import { AlertIcon, CheckIcon } from "@/ds/icons";
import { GitHubIcon, GoogleIcon } from "@/ds/icons/brands";
import { cn } from "@/ds/lib/cn";
import { api, RequestError } from "../api";
import type { LoginOrganization, LoginResponse, Principal, SocialProvider } from "../types";
import { lastLoginMethod, stageLoginMethod, type LoginMethod } from "./login-method";
import { ssoDoor, useSignupDoor, type SSODoor } from "./extensions";
import { useRuntimeConfig } from "./use-runtime-config";
import { useTitle } from "./use-title";

type Mode = "login" | "bootstrap";

const EMAIL_KEY = "wh-email";

function lastEmail(): string {
  try {
    return localStorage.getItem(EMAIL_KEY) ?? "";
  } catch {
    return "";
  }
}

function rememberEmail(email: string) {
  try {
    localStorage.setItem(EMAIL_KEY, email);
  } catch {
    /* storage unavailable */
  }
}

/* The social callback redirects here when the provider round trip itself
 * failed, before there was anything to sign in with. */
const SOCIAL_ERRORS: Record<string, string> = {
  provider_denied: "The sign-in was cancelled at the provider.",
  flow_invalid: "That sign-in attempt is no longer valid. Start again from this page.",
  social_not_configured: "That sign-in method is not enabled on this deployment",
  social_failed: "Signing in with the provider failed. Try again or use another method.",
};

const LOGIN_ERRORS: Record<string, string> = {
  no_organizations: "This account does not belong to any organization yet",
  sso_required: "This organization signs in with single sign-on",
  no_account: "No withHuman account uses this email yet.",
  email_unverified: "Your email address is not verified with this provider.",
  domain_captured: "Your team already uses withHuman. Sign in with your work email.",
  social_flow_invalid: "That sign-in attempt expired. Start again from this page.",
  invalid_credentials: "That email and password do not match.",
  unauthenticated: "The sign-in expired. Try again.",
};

/* What the reset page leaves in the query string after a new password is
 * set, so the sign-in form can say so. */
const RESET_NOTICE = "Your password is updated. Sign in with it.";

const SOCIAL_ICONS: Record<string, ComponentType<SVGProps<SVGSVGElement>>> = {
  google: GoogleIcon,
  github: GitHubIcon,
};

/* What the callbacks left in the query string. Read once, purely; the
 * effect strips the parameters so a refresh never replays a consumed
 * social flow or re-shows a stale error. The SSO callback's code means
 * what the edition's door says it means; an edition without one has
 * no callback to arrive from, so the code is ignored. */
function readArrival(sso: SSODoor | null): { continueSocial: boolean; error: string; notice: string } {
  try {
    const params = new URLSearchParams(window.location.search);
    const ssoCode = params.get("sso_error");
    const socialCode = params.get("social_error");
    return {
      continueSocial: params.get("social") === "continue",
      notice: params.get("password_reset") === "done" ? RESET_NOTICE : "",
      error:
        ssoCode && sso
          ? (sso.errors[ssoCode] ?? sso.errors.sso_failed ?? "")
          : socialCode
            ? (SOCIAL_ERRORS[socialCode] ?? SOCIAL_ERRORS.social_failed)
            : "",
    };
  } catch {
    return { continueSocial: false, error: "", notice: "" };
  }
}

function stripArrivalParams() {
  try {
    const url = new URL(window.location.href);
    let touched = false;
    for (const key of ["social", "social_error", "sso_error", "password_reset"]) {
      if (url.searchParams.has(key)) {
        url.searchParams.delete(key);
        touched = true;
      }
    }
    if (touched) window.history.replaceState(window.history.state, "", url);
  } catch {
    /* history unavailable */
  }
}

function loginErrorCode(reason: unknown): string {
  return reason instanceof RequestError ? reason.code : "";
}

function loginErrorMessage(reason: unknown): string {
  if (reason instanceof RequestError && LOGIN_ERRORS[reason.code]) {
    return LOGIN_ERRORS[reason.code];
  }
  return reason instanceof Error ? reason.message : "Authentication failed";
}

/* Leaves for the organization's identity provider through the
 * edition's door. */
function beginSSO(door: SSODoor, slug: string) {
  stageLoginMethod({ kind: "sso", organization: slug });
  window.location.assign(door.beginURL(slug));
}

/* The login half walks email -> methods -> organization selection. The
 * server reveals the password field and the SSO routing for an email;
 * a verified password either completes the login or hands back a
 * single-use ticket plus the organization list. Social login joins at
 * the same two exits through the continue step. */
type LoginStep =
  | { step: "email" }
  | { step: "methods"; password: boolean; ssoOrganizations: LoginOrganization[] }
  | { step: "select"; ticket: string; organizations: LoginOrganization[] };

export function AccessScreen({
  onAuthenticated,
  initialError,
}: {
  onAuthenticated: (principal: Principal) => void;
  initialError: string;
}) {
  /* The enterprise SSO door this edition serves, or none: the open
   * edition mounts no SSO routes, so it offers no SSO button. */
  const sso = ssoDoor();
  const [arrival] = useState(() => readArrival(sso));
  const [mode, setMode] = useState<Mode>("login");
  const [login, setLogin] = useState<LoginStep>({ step: "email" });
  const [email, setEmail] = useState(lastEmail);
  /* How this browser signed in last time, marked on the matching method
   * so a returning person does not have to remember. Read once: it only
   * changes after a sign-in, which leaves this screen. */
  const [lastUsed] = useState(lastLoginMethod);
  const [error, setError] = useState(() => initialError || arrival.error);
  const [errorCode, setErrorCode] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [continuing, setContinuing] = useState(arrival.continueSocial);
  const continueStarted = useRef(false);
  const passwordRef = useRef<HTMLInputElement>(null);
  /* Which organization-creation door this edition offers, and which
   * social providers it registered. Until the runtime config loads,
   * offer nothing rather than the wrong door; if it cannot load, fall
   * back to the open edition's ceremony link. */
  const runtime = useRuntimeConfig();
  const signupDoor = useSignupDoor(runtime.config);
  const doors = runtime.config
    ? {
        signup: signupDoor,
        bootstrap: runtime.config.bootstrap_enabled,
        social: runtime.config.social_providers ?? [],
      }
    : runtime.failed
      ? { signup: null, bootstrap: true, social: [] as SocialProvider[] }
      : null;
  useTitle(mode === "login" ? "Sign in" : "Set up");

  function applyLoginResponse(result: LoginResponse) {
    if (result.status === "complete") {
      onAuthenticated(result.principal);
      return;
    }
    if (result.status === "sso_required") {
      /* Every organization is connected: go straight to its SSO, or let
       * the person pick which one. The picker sends SSO organizations to
       * SSO, so it needs no ticket here. Without a door there is nowhere
       * to go, so say why the password did not sign them in. */
      if (!sso) {
        showSSORequired();
        return;
      }
      if (result.organizations.length === 1) {
        beginSSO(sso, result.organizations[0].slug);
        return;
      }
      setLogin({ step: "select", ticket: "", organizations: result.organizations });
      return;
    }
    setLogin({
      step: "select",
      ticket: result.ticket,
      organizations: result.organizations,
    });
  }

  function failLogin(reason: unknown) {
    setError(loginErrorMessage(reason));
    setErrorCode(loginErrorCode(reason));
  }

  /* The organization signs in only through SSO and this edition serves
   * no SSO door: the same sentence the server's refusal would carry. */
  function showSSORequired() {
    setError(LOGIN_ERRORS.sso_required);
    setErrorCode("sso_required");
  }

  /* Returning from a social provider: the callback stored the assertion
   * and sent the browser here; one same-origin call turns it into a
   * session or the organization picker. The flow is single use, so the
   * parameters go before the request does. */
  useEffect(() => {
    stripArrivalParams();
    if (!arrival.continueSocial || continueStarted.current) return;
    continueStarted.current = true;
    api
      .socialContinue()
      .then(applyLoginResponse)
      .catch(failLogin)
      .finally(() => setContinuing(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (login.step === "methods" && login.password) passwordRef.current?.focus();
  }, [login]);

  function clearError() {
    setError("");
    setErrorCode("");
  }

  async function submitLogin(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    clearError();
    const data = Object.fromEntries(
      new FormData(event.currentTarget).entries(),
    ) as Record<string, string>;
    setSubmitting(true);
    try {
      if (login.step === "email" || !data.password) {
        const methods = await api.loginMethods(data.email);
        rememberEmail(data.email);
        setLogin({
          step: "methods",
          password: methods.password,
          ssoOrganizations: methods.sso_organizations,
        });
        if (sso && !methods.password && methods.sso_organizations.length === 1) {
          beginSSO(sso, methods.sso_organizations[0].slug);
        }
        return;
      }
      stageLoginMethod({ kind: "password" });
      applyLoginResponse(await api.login(data.email, data.password));
    } catch (reason) {
      failLogin(reason);
    } finally {
      setSubmitting(false);
    }
  }

  async function beginSocial(provider: SocialProvider) {
    clearError();
    setSubmitting(true);
    try {
      const flow = await api.socialBegin(provider.key, { intent: "login" });
      stageLoginMethod({ kind: "social", provider: provider.key });
      window.location.assign(flow.authorization_url);
    } catch (reason) {
      failLogin(reason);
      setSubmitting(false);
    }
  }

  async function selectOrganization(ticket: string, organization: LoginOrganization) {
    if (organization.sso) {
      if (sso) beginSSO(sso, organization.slug);
      else showSSORequired();
      return;
    }
    clearError();
    setSubmitting(true);
    try {
      const result = await api.loginSelect(ticket, organization.id);
      onAuthenticated(result.principal);
    } catch (reason) {
      failLogin(reason);
      /* The ticket is single use: any failure means starting over. */
      setLogin({ step: "email" });
    } finally {
      setSubmitting(false);
    }
  }

  async function submitBootstrap(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    clearError();
    const data = Object.fromEntries(
      new FormData(event.currentTarget).entries(),
    ) as Record<string, string>;
    setSubmitting(true);
    try {
      if (data.password !== data.password_confirmation) {
        setError("Passwords do not match");
        return;
      }
      const { password_confirmation: _, ...bootstrap } = data;
      const result = await api.bootstrap(bootstrap);
      onAuthenticated(result.principal);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Authentication failed");
    } finally {
      setSubmitting(false);
    }
  }

  function switchMode(next: Mode) {
    setMode(next);
    setLogin({ step: "email" });
    clearError();
  }

  function resetToEmail() {
    setLogin({ step: "email" });
    clearError();
  }

  const noticeCallout =
    !error && arrival.notice && login.step !== "select" ? (
      <Callout tone="approve" icon={<CheckIcon width={15} height={15} />}>{arrival.notice}</Callout>
    ) : null;
  const errorCallout = error ? (
    <Callout tone="deny" icon={<AlertIcon width={15} height={15} />}>
      {error}
      {errorCode === "no_account" && doors?.signup ? (
        <>
          {" "}
          <a href={doors.signup.href} className="underline decoration-current underline-offset-2">
            Create an organization
          </a>{" "}
          to get started.
        </>
      ) : null}
    </Callout>
  ) : null;

  const socialButtons =
    doors && doors.social.length > 0 ? (
      <SocialButtons
        providers={doors.social}
        disabled={submitting}
        onBegin={beginSocial}
        lastUsed={lastUsed?.kind === "social" ? lastUsed.provider : undefined}
      />
    ) : null;
  /* The email field stands for the methods that start with an email: the
   * password, and SSO until its own button appears at the methods step. */
  const emailLastUsed =
    lastUsed?.kind === "password" || (lastUsed?.kind === "sso" && login.step === "email");

  return (
    <div className="ds-root flex min-h-screen flex-col items-center justify-center bg-canvas px-6 py-12">
      <a
        href="https://withhuman.ai/"
        target="_blank"
        rel="noopener"
        referrerPolicy="strict-origin-when-cross-origin"
        aria-label="withHuman.ai (opens in a new tab)"
        className="mb-8 text-[24px] text-ink"
      >
        <Logo />
      </a>

      {continuing ? (
        <div className="flex items-center gap-2.5">
          <Spinner size={16} />
          <span className="text-body text-ink-muted">Finishing sign-in</span>
        </div>
      ) : mode === "login" && login.step === "select" ? (
        <div className="flex w-full max-w-[360px] flex-col gap-4 rounded-lg border-hairline border-edge bg-surface p-6 shadow-sm">
          <div>
            <h1 className="text-title text-ink">Choose an organization</h1>
            <p className="mt-1 text-body text-ink-muted">Pick the organization to sign in to.</p>
          </div>
          <div className="flex flex-col gap-2">
            {login.organizations.map((organization) => (
              <button
                key={organization.id}
                type="button"
                disabled={submitting}
                onClick={() => void selectOrganization(login.ticket, organization)}
                className="flex cursor-pointer items-baseline justify-between gap-3 rounded-md border-hairline border-edge bg-surface px-4 py-3 text-left hover:border-edge-strong disabled:cursor-default disabled:opacity-60"
              >
                <span className="text-body font-medium text-ink">{organization.name}</span>
                <span className="font-mono text-[11px] text-ink-faint">
                  {organization.sso ? "SSO" : organization.slug}
                </span>
              </button>
            ))}
          </div>
          {errorCallout}
          <button
            type="button"
            onClick={resetToEmail}
            className="cursor-pointer self-start text-label font-normal text-ink-muted underline decoration-edge-strong underline-offset-2 hover:decoration-ink"
          >
            Use a different account
          </button>
        </div>
      ) : mode === "login" ? (
        <>
          <form
            key="login"
            onSubmit={submitLogin}
            className="flex w-full max-w-[360px] flex-col gap-4 rounded-lg border-hairline border-edge bg-surface p-6 shadow-sm"
          >
            <h1 className="text-title text-ink">Sign in</h1>
            {socialButtons}
            {socialButtons ? <OrDivider /> : null}
            <TextField
              name="email"
              label="Email"
              type="email"
              autoComplete="username"
              autoFocus={!socialButtons}
              required
              labelEnd={emailLastUsed ? <LastUsedBadge /> : null}
              value={email}
              onChange={(event) => {
                setEmail(event.currentTarget.value);
                if (login.step !== "email") resetToEmail();
              }}
            />
            {login.step === "methods" && login.password ? (
              <>
                <TextField
                  ref={passwordRef}
                  name="password"
                  label="Password"
                  type="password"
                  autoComplete="current-password"
                  required
                />
                <a
                  href={`/reset?email=${encodeURIComponent(email)}`}
                  className="-mt-2 self-start text-label font-normal text-ink-muted underline decoration-edge-strong underline-offset-2 hover:text-ink hover:decoration-ink"
                >
                  Forgot your password?
                </a>
              </>
            ) : null}
            {login.step === "methods" && !login.password ? (
              <Callout tone="accent">
                {sso
                  ? "This account signs in with single sign-on."
                  : "This account signs in with single sign-on. It is not available on this deployment."}
              </Callout>
            ) : null}
            {noticeCallout}
            {errorCallout}
            {login.step === "email" || (login.step === "methods" && login.password) ? (
              <Button type="submit" variant="primary" size="lg" loading={submitting}>
                Continue
              </Button>
            ) : null}
            {login.step === "methods" && sso
              ? login.ssoOrganizations.map((organization) => (
                  <Button
                    key={organization.id}
                    type="button"
                    variant={login.password ? "ghost" : "primary"}
                    size={login.password ? "md" : "lg"}
                    className="relative"
                    onClick={() => beginSSO(sso, organization.slug)}
                  >
                    {login.ssoOrganizations.length > 1 || login.password
                      ? `Continue with SSO (${organization.name})`
                      : "Continue with SSO"}
                    {isLastUsed(lastUsed, { kind: "sso", organization: organization.slug }) ? (
                      <LastUsedBadge floating onAccent={!login.password} />
                    ) : null}
                  </Button>
                ))
              : null}
          </form>
          {doors?.signup ? (
            <p className="mt-5 text-label font-normal text-ink-muted">
              New here?{" "}
              <a
                href={doors.signup.href}
                className="cursor-pointer text-ink underline decoration-edge-strong underline-offset-2 hover:decoration-ink"
              >
                Create an organization
              </a>
            </p>
          ) : doors?.bootstrap ? (
            <p className="mt-5 text-label font-normal text-ink-muted">
              First time on this instance?{" "}
              <button
                type="button"
                onClick={() => switchMode("bootstrap")}
                className="cursor-pointer text-ink underline decoration-edge-strong underline-offset-2 hover:decoration-ink"
              >
                Set up the first owner
              </button>
            </p>
          ) : null}
        </>
      ) : (
        <>
          <form
            key="bootstrap"
            onSubmit={submitBootstrap}
            className="flex w-full max-w-[400px] flex-col gap-4 rounded-lg border-hairline border-edge bg-surface p-6 shadow-sm"
          >
            <div>
              <h1 className="text-title text-ink">Set up this instance</h1>
              <p className="mt-1 text-body text-ink-muted">
                Create the organization and its first owner.
              </p>
            </div>
            <Callout tone="accent">
              Find the bootstrap secret in the{" "}
              <span className="font-mono text-[12px]">bootstrap-secret</span> file in the data
              directory (<span className="font-mono text-[12px]">~/.withhuman</span> by default).
            </Callout>

            <FieldGroup label="Instance">
              <TextField
                name="secret"
                label="Bootstrap secret"
                type="password"
                autoComplete="off"
                autoFocus
                required
              />
              <TextField name="organization_slug" label="Organization slug" mono required />
              <TextField name="organization_name" label="Organization name" required />
            </FieldGroup>

            <FieldGroup label="You">
              <TextField name="display_name" label="Your name" required />
              <TextField name="email" label="Email" type="email" autoComplete="email" required />
              <TextField
                name="password"
                label="Password"
                type="password"
                minLength={12}
                autoComplete="new-password"
                required
              />
              <TextField
                name="password_confirmation"
                label="Confirm password"
                type="password"
                minLength={12}
                autoComplete="new-password"
                required
              />
            </FieldGroup>

            {errorCallout}
            <Button type="submit" variant="primary" size="lg" loading={submitting}>
              Create the first owner
            </Button>
          </form>
          <p className="mt-5 text-label font-normal text-ink-muted">
            Already set up?{" "}
            <button
              type="button"
              onClick={() => switchMode("login")}
              className="cursor-pointer text-ink underline decoration-edge-strong underline-offset-2 hover:decoration-ink"
            >
              Back to sign in
            </button>
          </p>
        </>
      )}
    </div>
  );
}

/* One button per registered provider. Shared with the signup page, so
 * the two doors look the same. */
export function SocialButtons({
  providers,
  disabled,
  onBegin,
  lastUsed,
}: {
  providers: SocialProvider[];
  disabled: boolean;
  onBegin: (provider: SocialProvider) => void;
  /* The provider key this browser signed in with last, if any. */
  lastUsed?: string;
}) {
  return (
    <div className="flex flex-col gap-2">
      {providers.map((provider) => {
        const Icon = SOCIAL_ICONS[provider.key];
        return (
          <Button
            key={provider.key}
            type="button"
            variant="neutral"
            size="lg"
            className="relative"
            disabled={disabled}
            onClick={() => onBegin(provider)}
          >
            {Icon ? <Icon className="size-4 shrink-0" /> : null}
            Continue with {provider.label}
            {provider.key === lastUsed ? <LastUsedBadge floating /> : null}
          </Button>
        );
      })}
    </div>
  );
}

function isLastUsed(last: LoginMethod | null, method: LoginMethod): boolean {
  if (!last || last.kind !== method.kind) return false;
  switch (method.kind) {
    case "password":
      return true;
    case "social":
      return last.kind === "social" && last.provider === method.provider;
    case "sso":
      return last.kind === "sso" && last.organization === method.organization;
  }
}

/* The marker for the method used last: a quiet value pill. Floating, it
 * sits at the trailing edge of a (relatively positioned) button, over
 * the centred label; on an accent fill it turns to the on-accent ink. */
function LastUsedBadge({
  floating = false,
  onAccent = false,
}: {
  floating?: boolean;
  onAccent?: boolean;
}) {
  return (
    <Badge
      fill
      icon={false}
      className={cn(
        "h-5 px-1.5 text-micro",
        floating && "absolute top-1/2 right-2 -translate-y-1/2",
        onAccent && "bg-ink-on-accent/15 text-ink-on-accent",
      )}
    >
      Last used
    </Badge>
  );
}

export function OrDivider() {
  return (
    <div className="flex items-center gap-3" aria-hidden>
      <span className="h-px flex-1 bg-edge" />
      <span className="text-micro tracking-[0.06em] text-ink-faint uppercase">or</span>
      <span className="h-px flex-1 bg-edge" />
    </div>
  );
}

function FieldGroup({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-3">
      <p className="text-micro tracking-[0.06em] text-ink-faint uppercase">
        {label}
      </p>
      {children}
    </div>
  );
}
