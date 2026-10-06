import { useEffect, useMemo, useState } from "react";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Logo } from "@/ds/components/logo";
import { RadioGroup, RadioOption } from "@/ds/components/radio-group";
import { Spinner } from "@/ds/components/spinner";
import { AlertIcon } from "@/ds/icons";
import { api, RequestError } from "../api";
import type { OAuthAuthorizationPreview, OAuthAuthorizationRequest, Session } from "../types";
import { AccessScreen } from "./access";
import { groupPermissions, PermissionChecklist } from "./permission-checklist";
import { useTitle } from "./use-title";

/*
 * The OAuth consent page for the management MCP server. An MCP client
 * (Claude Code, Cursor, Codex) sends the person here; they sign in if
 * they must, see who is asking, choose what the agent may do, and are
 * sent back to the client with a code. Approval mints a personal API
 * key named after the client, so it shows up on the keys page like any
 * other and can be revoked there.
 *
 * It runs outside the authenticated shell: a bare, centered card with
 * the access screen in place when there is no session, so the query
 * string the client built survives a password sign-in untouched.
 */

/* A provider sign-in leaves this page for the identity provider and
 * lands in the app afterwards; the app sends the person back here
 * while this note is fresh. */
export const OAUTH_PENDING_KEY = "wh-oauth-pending";
const PENDING_TTL_MS = 15 * 60 * 1000;

export function rememberPendingOAuth(search: string) {
  try {
    sessionStorage.setItem(OAUTH_PENDING_KEY, JSON.stringify({ search, at: Date.now() }));
  } catch {
    /* Storage can be unavailable; the password sign-in still works in place. */
  }
}

export function readPendingOAuth(): string | null {
  try {
    const raw = sessionStorage.getItem(OAUTH_PENDING_KEY);
    if (!raw) return null;
    const pending = JSON.parse(raw) as { search?: string; at?: number };
    if (typeof pending.search !== "string" || typeof pending.at !== "number" || Date.now() - pending.at > PENDING_TTL_MS) {
      sessionStorage.removeItem(OAUTH_PENDING_KEY);
      return null;
    }
    return pending.search;
  } catch {
    return null;
  }
}

export function forgetPendingOAuth() {
  try {
    sessionStorage.removeItem(OAUTH_PENDING_KEY);
  } catch {
    /* Nothing to forget. */
  }
}

const DEFAULT_EXPIRY_DAYS = 90;

type PermissionsMode = "inherit" | "scoped";
type ExpiryMode = "default" | "never";

/* The server's refusal in the page's own words; the same reasons the
 * create-key form answers, because consent mints the same kind of key. */
function decisionErrorMessage(reason: unknown): string {
  if (!(reason instanceof RequestError)) {
    return reason instanceof Error ? reason.message : "The agent could not be connected";
  }
  switch (reason.code) {
    case "api_keys_disabled":
      return "An administrator turned personal API keys off.";
    case "api_key_scoping_unavailable":
      return "This edition cannot scope keys. Choose everything you can do.";
    case "plan_required":
      return "Your plan does not include scoped keys. Choose everything you can do.";
    case "forbidden":
      if (reason.message.includes("too old")) {
        return "Sign in again to grant these permissions.";
      }
      if (reason.message.includes("insufficient")) {
        return "Granting these permissions needs a multi-factor sign-in.";
      }
      return reason.message;
    default:
      return reason.message;
  }
}

function hostOf(uri: string): string {
  try {
    const parsed = new URL(uri);
    return parsed.host || parsed.protocol.replace(/:$/, "");
  } catch {
    return uri;
  }
}

/* A request the client sent incompletely cannot be previewed or
 * answered: the required fields are what the server validates first. */
function completeRequest(search: OAuthAuthorizationRequest): OAuthAuthorizationRequest | null {
  return search.client_id && search.redirect_uri && search.code_challenge && search.code_challenge_method ? search : null;
}

export function OAuthConsentScreen({ search }: { search: OAuthAuthorizationRequest }) {
  const request = useMemo(() => completeRequest(search), [search]);
  useTitle("Connect an agent");
  const [session, setSession] = useState<Session | null>(null);
  const [resolved, setResolved] = useState(false);
  const [preview, setPreview] = useState<OAuthAuthorizationPreview | null>(null);
  const [loadError, setLoadError] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [permissionsMode, setPermissionsMode] = useState<PermissionsMode>("inherit");
  const [checked, setChecked] = useState<Set<string>>(() => new Set());
  const [expiryMode, setExpiryMode] = useState<ExpiryMode>("default");

  /* Every visit remembers itself, so a provider sign-in can find the
   * way back; a visit that already has a session is the way back. */
  useEffect(() => {
    if (typeof window !== "undefined") rememberPendingOAuth(window.location.search);
  }, []);

  const load = async () => {
    setResolved(false);
    setLoadError("");
    let current: Session | null = null;
    try {
      current = await api.me();
    } catch (reason) {
      if (!(reason instanceof RequestError && reason.status === 401)) {
        setLoadError(reason instanceof Error ? reason.message : "withHuman could not be reached.");
      }
    }
    setSession(current);
    if (current && request) {
      forgetPendingOAuth();
      try {
        const answer = await api.previewOAuthAuthorization(request);
        setPreview(answer);
        setPermissionsMode(answer.scoping_enabled && answer.requested_permissions.length > 0 ? "scoped" : "inherit");
        setChecked(new Set(answer.requested_permissions));
      } catch (reason) {
        setLoadError(reason instanceof Error ? reason.message : "The authorization request could not be read.");
      }
    }
    setResolved(true);
  };

  useEffect(() => {
    void load();
    // The request comes from the URL and does not change while mounted.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const areas = useMemo(() => groupPermissions(preview?.bearable_permissions ?? []), [preview]);
  const scoped = preview?.scoping_enabled === true && permissionsMode === "scoped";
  const canAuthorize = preview !== null && preview.personal_api_keys_allowed && (!scoped || checked.size > 0);

  const decide = async (approve: boolean) => {
    if (!request) return;
    setSubmitting(true);
    setError("");
    try {
      const expiresAt =
        approve && expiryMode === "default"
          ? new Date(Date.now() + DEFAULT_EXPIRY_DAYS * 24 * 60 * 60 * 1000).toISOString()
          : undefined;
      const answer = await api.createOAuthAuthorization({
        ...request,
        approve,
        ...(approve && scoped ? { permissions: Array.from(checked) } : {}),
        ...(expiresAt ? { expires_at: expiresAt } : {}),
      });
      forgetPendingOAuth();
      window.location.assign(answer.redirect_url);
    } catch (reason) {
      setError(decisionErrorMessage(reason));
      setSubmitting(false);
    }
  };

  if (!resolved) {
    return (
      <Frame>
        <div className="flex items-center gap-2.5">
          <Spinner size={16} />
          <span className="text-body text-ink-muted">Loading the request</span>
        </div>
      </Frame>
    );
  }
  if (!session) {
    return <AccessScreen onAuthenticated={() => void load()} initialError={loadError} />;
  }
  if (!request) {
    return (
      <Frame>
        <TerminalCard
          title="This link is incomplete"
          message="Start the connection again from the agent."
        />
      </Frame>
    );
  }
  if (loadError || !preview) {
    return (
      <Frame>
        <TerminalCard title="This request cannot be authorized" message={loadError || "The request could not be read."} />
      </Frame>
    );
  }

  const person = session.user?.display_name || session.user?.email || "you";
  const organization = session.organization?.name ?? "your organization";
  return (
    <Frame>
      <div className="flex w-full max-w-[560px] flex-col gap-5 rounded-lg border-hairline border-edge bg-surface p-6 shadow-sm">
        <div>
          <h1 className="text-title text-ink">Connect {preview.client.name}</h1>
          <p className="mt-1.5 text-body text-ink-muted">
            <span className="text-ink">{preview.client.name}</span> wants to use withHuman as{" "}
            <span className="text-ink">{person}</span> in <span className="text-ink">{organization}</span>. It
            gets the permissions you choose below.
          </p>
          <p className="mt-1.5 text-label text-ink-faint">
            You will be sent back to {hostOf(preview.client.redirect_uri)}.
          </p>
        </div>

        {!preview.personal_api_keys_allowed ? (
          <Callout tone="warn" title="Personal API keys are turned off">
            Ask an administrator to turn them on for {organization}.
          </Callout>
        ) : preview.scoping_enabled ? (
          <div className="flex flex-col gap-3">
            <RadioGroup
              aria-label="Permissions"
              value={permissionsMode}
              onValueChange={(next) => setPermissionsMode(next as PermissionsMode)}
            >
              <RadioOption
                value="inherit"
                label="Everything I can do"
                description="The agent follows your permissions as they change."
              />
              <RadioOption
                value="scoped"
                label="Only these permissions"
                description="The agent can use only the permissions you pick."
              />
            </RadioGroup>
            {scoped ? (
              <PermissionChecklist
                areas={areas}
                checked={checked}
                onToggle={(key, next) =>
                  setChecked((current) => {
                    const updated = new Set(current);
                    if (next) updated.add(key);
                    else updated.delete(key);
                    return updated;
                  })
                }
                onSelectAll={() => setChecked(new Set(preview.bearable_permissions.map((permission) => permission.key)))}
                onClear={() => setChecked(new Set())}
              />
            ) : null}
          </div>
        ) : (
          <p className="text-body text-ink-muted">
            The agent will be able to do everything you can do.
          </p>
        )}

        {preview.personal_api_keys_allowed ? (
          <RadioGroup
            aria-label="Access lasts"
            value={expiryMode}
            onValueChange={(next) => setExpiryMode(next as ExpiryMode)}
          >
            <RadioOption
              value="default"
              label={`${DEFAULT_EXPIRY_DAYS} days`}
              description="The agent asks you to connect again afterwards."
            />
            <RadioOption
              value="never"
              label="Until revoked"
              description="Revoke it any time from Personal API keys in Settings."
            />
          </RadioGroup>
        ) : null}

        {error ? (
          <Callout tone="deny" icon={<AlertIcon width={16} height={16} />}>
            {error}
          </Callout>
        ) : null}

        <div className="flex items-center justify-end gap-2">
          <Button variant="neutral" disabled={submitting} onClick={() => void decide(false)}>
            Cancel
          </Button>
          <Button variant="primary" disabled={!canAuthorize} loading={submitting} onClick={() => void decide(true)}>
            Authorize
          </Button>
        </div>
        <p className="text-micro text-ink-faint">
          Not {session.user?.email ?? person}?{" "}
          <button
            type="button"
            className="underline"
            onClick={() => {
              void api.logout().finally(() => {
                setSession(null);
                setPreview(null);
              });
            }}
          >
            Sign in as someone else
          </button>
        </p>
      </div>
    </Frame>
  );
}

function Frame({ children }: { children: React.ReactNode }) {
  return (
    <div className="ds-root flex min-h-screen flex-col items-center justify-center bg-canvas px-6 py-12">
      <Logo className="mb-8 text-[24px] text-ink" />
      {children}
    </div>
  );
}

function TerminalCard({ title, message }: { title: string; message: string }) {
  return (
    <div className="flex w-full max-w-[380px] flex-col gap-3 rounded-lg border-hairline border-edge bg-surface p-6 shadow-sm">
      <h1 className="text-title text-ink">{title}</h1>
      <p className="text-body text-ink-muted">{message}</p>
    </div>
  );
}
