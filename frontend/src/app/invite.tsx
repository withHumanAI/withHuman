import { useEffect, useState, type FormEvent } from "react";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Logo } from "@/ds/components/logo";
import { Spinner } from "@/ds/components/spinner";
import { TextField } from "@/ds/components/text-field";
import { AlertIcon } from "@/ds/icons";
import { api, RequestError } from "../api";
import { ssoDoor } from "./extensions";
import { stageLoginMethod } from "./login-method";
import { requestLoginSplash } from "./login-splash";
import type { InvitationPreview, Session } from "../types";
import { useTitle } from "./use-title";

/* The public invitation accept page. It runs logged out, outside the
 * authenticated shell, so errors render inline instead of as toasts. */
export function InviteScreen({ token }: { token: string }) {
  useTitle("Invitation");
  /* An SSO-only organization is accepted through the edition's SSO
   * door; the open edition serves none, so it can only say so. */
  const sso = ssoDoor();
  const [preview, setPreview] = useState<InvitationPreview | null>(null);
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);
  const [terminal, setTerminal] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void Promise.allSettled([api.invitationPreview(token), api.me()]).then(
      ([previewResult, sessionResult]) => {
        if (cancelled) return;
        if (previewResult.status === "fulfilled") {
          setPreview(previewResult.value);
        } else {
          const reason = previewResult.reason as unknown;
          if (reason instanceof RequestError && reason.status === 409) {
            setTerminal("This invitation was already accepted.");
          } else {
            setTerminal(
              "This invitation is no longer valid. Ask an administrator for a new one.",
            );
          }
        }
        if (sessionResult.status === "fulfilled") setSession(sessionResult.value);
        setLoading(false);
      },
    );
    return () => {
      cancelled = true;
    };
  }, [token]);

  async function accept(body: { display_name?: string; password?: string }) {
    setError("");
    setSubmitting(true);
    try {
      await api.acceptInvitation(token, body);
      // Full navigation so the session gate refetches with the new cookie.
      if (body.password) stageLoginMethod({ kind: "password" });
      requestLoginSplash();
      window.location.assign("/queue");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Acceptance failed");
      setSubmitting(false);
    }
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = Object.fromEntries(
      new FormData(event.currentTarget).entries(),
    ) as Record<string, string>;
    if (data.password_confirmation !== undefined && data.password !== data.password_confirmation) {
      setError("Passwords do not match");
      return;
    }
    void accept({ display_name: data.display_name, password: data.password });
  }

  const sessionEmail = session?.user?.email ?? "";
  /* The predefined role the invitation grants, when it grants one. */
  const roleName =
    preview?.grants.length === 1 ? preview.grants[0].role_name : undefined;
  const asRole = roleName ? ` as ${/^[AEIOU]/.test(roleName) ? "an" : "a"} ${roleName.toLowerCase()}` : "";
  const emailMatches =
    preview !== null &&
    sessionEmail.toLowerCase() === preview.email.toLowerCase();

  return (
    <div className="ds-root flex min-h-screen flex-col items-center justify-center bg-canvas px-6 py-12">
      <Logo className="mb-8 text-[24px] text-ink" />
      {loading ? (
        <div className="flex items-center gap-2.5">
          <Spinner size={16} />
          <span className="text-body text-ink-muted">Loading invitation</span>
        </div>
      ) : terminal ? (
        <TerminalCard message={terminal} />
      ) : preview ? (
        <div className="flex w-full max-w-[380px] flex-col gap-4 rounded-lg border-hairline border-edge bg-surface p-6 shadow-sm">
          <div>
            <h1 className="text-title text-ink">Join {preview.organization_name}</h1>
            <p className="mt-1.5 text-body text-ink-muted">
              {preview.inviter_display_name ? (
                <>
                  {preview.inviter_display_name} invited{" "}
                  <span className="text-ink">{preview.email}</span> to join{" "}
                  {preview.organization_name}
                  {asRole}.
                </>
              ) : (
                <>
                  This invitation lets{" "}
                  <span className="text-ink">{preview.email}</span> join{" "}
                  {preview.organization_name}
                  {asRole}.
                </>
              )}
            </p>
          </div>

          {preview.sso_required ? (
            <>
              <Callout tone="accent">
                {sso
                  ? "Continue with single sign-on to accept."
                  : "This organization requires single sign-on. It isn't available on this deployment."}
              </Callout>
              {sso ? (
                <Button
                  variant="primary"
                  size="lg"
                  onClick={() => {
                    stageLoginMethod({ kind: "sso", organization: preview.organization_slug });
                    window.location.assign(sso.beginURL(preview.organization_slug));
                  }}
                >
                  Continue with SSO
                </Button>
              ) : null}
            </>
          ) : session && emailMatches ? (
            <>
              <p className="text-body text-ink-muted">
                You are signed in as {sessionEmail}.
              </p>
              <InlineError error={error} />
              <Button
                variant="primary"
                size="lg"
                loading={submitting}
                onClick={() => void accept({})}
              >
                Accept invitation
              </Button>
            </>
          ) : session && sessionEmail ? (
            <>
              <Callout tone="warn" icon={<AlertIcon width={15} height={15} />}>
                You are signed in as {sessionEmail}, but this invitation is
                for {preview.email}. Sign out to continue.
              </Callout>
              <Button
                variant="neutral"
                onClick={() =>
                  void api.logout().finally(() => window.location.reload())
                }
              >
                Sign out and continue
              </Button>
            </>
          ) : preview.existing_user ? (
            <form onSubmit={submit} className="flex flex-col gap-4">
              <p className="text-body text-ink-muted">
                Confirm your password to join with your existing account.
              </p>
              <TextField
                name="password"
                label="Password"
                type="password"
                autoComplete="current-password"
                autoFocus
                required
              />
              <InlineError error={error} />
              <Button type="submit" variant="primary" size="lg" loading={submitting}>
                Accept invitation
              </Button>
            </form>
          ) : (
            <form onSubmit={submit} className="flex flex-col gap-4">
              <TextField name="display_name" label="Your name" autoFocus required />
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
              <InlineError error={error} />
              <Button type="submit" variant="primary" size="lg" loading={submitting}>
                Create account and join
              </Button>
            </form>
          )}
        </div>
      ) : null}
    </div>
  );
}

function InlineError({ error }: { error: string }) {
  if (!error) return null;
  return (
    <Callout tone="deny" icon={<AlertIcon width={15} height={15} />}>
      {error}
    </Callout>
  );
}

function TerminalCard({ message }: { message: string }) {
  return (
    <div className="flex w-full max-w-[380px] flex-col gap-3 rounded-lg border-hairline border-edge bg-surface p-6 shadow-sm">
      <h1 className="text-title text-ink">Invitation unavailable</h1>
      <p className="text-body text-ink-muted">{message}</p>
    </div>
  );
}
