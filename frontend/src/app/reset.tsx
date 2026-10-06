import { useEffect, useState, type FormEvent } from "react";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Logo } from "@/ds/components/logo";
import { Spinner } from "@/ds/components/spinner";
import { TextField } from "@/ds/components/text-field";
import { AlertIcon, CheckIcon } from "@/ds/icons";
import { api } from "../api";
import { useTitle } from "./use-title";

/* The public password reset pages. They run logged out, outside the
 * authenticated shell, so errors render inline instead of as toasts. */

function readEmailParam(): string {
  try {
    return new URLSearchParams(window.location.search).get("email") ?? "";
  } catch {
    return "";
  }
}

/* Step one: ask for the link. The server answers the same way for every
 * address, and so does this page. */
export function ResetRequestScreen() {
  useTitle("Reset password");
  const [email, setEmail] = useState(readEmailParam);
  const [submitting, setSubmitting] = useState(false);
  const [sent, setSent] = useState(false);
  const [error, setError] = useState("");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setSubmitting(true);
    try {
      await api.requestPasswordReset(email.trim());
      setSent(true);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "The request failed");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <PublicPage>
      <div className="flex w-full max-w-[380px] flex-col gap-4 rounded-lg border-hairline border-edge bg-surface p-6 shadow-sm">
        <div>
          <h1 className="text-title text-ink">Reset your password</h1>
          <p className="mt-1 text-body text-ink-muted">
            Enter your account's email to get a reset link.
          </p>
        </div>
        {sent ? (
          <Callout tone="approve" icon={<CheckIcon width={15} height={15} />}>
            If {email.trim()} has an account, a reset link is on its way.
          </Callout>
        ) : (
          <form onSubmit={submit} className="flex flex-col gap-4">
            <TextField
              name="email"
              label="Email"
              type="email"
              autoComplete="email"
              autoFocus
              required
              value={email}
              onChange={(event) => setEmail(event.currentTarget.value)}
            />
            <InlineError error={error} />
            <Button type="submit" variant="primary" size="lg" loading={submitting}>
              Send reset link
            </Button>
          </form>
        )}
        <p className="text-label font-normal text-ink-muted">
          <a href="/queue" className="underline decoration-edge-strong underline-offset-2 hover:text-ink hover:decoration-ink">
            Back to sign in
          </a>
        </p>
      </div>
    </PublicPage>
  );
}

/* Step two: the link. The token is checked before the form shows, so a
 * dead link says so instead of failing after the person has typed. */
export function ResetCompleteScreen({ token }: { token: string }) {
  useTitle("Set a new password");
  const [loading, setLoading] = useState(true);
  const [terminal, setTerminal] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void api
      .passwordResetPreview(token)
      .catch(() => {
        if (!cancelled) {
          setTerminal("This link has expired or was already used.");
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [token]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const password = String(data.get("password") ?? "");
    if (password !== String(data.get("password_confirmation") ?? "")) {
      setError("Passwords do not match");
      return;
    }
    setError("");
    setSubmitting(true);
    try {
      await api.completePasswordReset(token, password);
      // Every session was revoked; full navigation lands on the sign-in
      // form, which reads the notice from the query string.
      window.location.assign("/queue?password_reset=done");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "The reset failed");
      setSubmitting(false);
    }
  }

  return (
    <PublicPage>
      {loading ? (
        <div className="flex items-center gap-2.5">
          <Spinner size={16} />
          <span className="text-body text-ink-muted">Checking the link</span>
        </div>
      ) : terminal ? (
        <div className="flex w-full max-w-[380px] flex-col gap-3 rounded-lg border-hairline border-edge bg-surface p-6 shadow-sm">
          <h1 className="text-title text-ink">Link unavailable</h1>
          <p className="text-body text-ink-muted">{terminal}</p>
          <p className="text-label font-normal text-ink-muted">
            <a href="/reset" className="underline decoration-edge-strong underline-offset-2 hover:text-ink hover:decoration-ink">
              Request a new link
            </a>
          </p>
        </div>
      ) : (
        <div className="flex w-full max-w-[380px] flex-col gap-4 rounded-lg border-hairline border-edge bg-surface p-6 shadow-sm">
          <div>
            <h1 className="text-title text-ink">Set a new password</h1>
            <p className="mt-1 text-body text-ink-muted">
              At least 12 characters. Every device signed in to this account will be signed out.
            </p>
          </div>
          <form onSubmit={submit} className="flex flex-col gap-4">
            <TextField
              name="password"
              label="New password"
              type="password"
              minLength={12}
              autoComplete="new-password"
              autoFocus
              required
            />
            <TextField
              name="password_confirmation"
              label="Confirm new password"
              type="password"
              minLength={12}
              autoComplete="new-password"
              required
            />
            <InlineError error={error} />
            <Button type="submit" variant="primary" size="lg" loading={submitting}>
              Set password
            </Button>
          </form>
        </div>
      )}
    </PublicPage>
  );
}

function PublicPage({ children }: { children: React.ReactNode }) {
  return (
    <div className="ds-root flex min-h-screen flex-col items-center justify-center bg-canvas px-6 py-12">
      <Logo className="mb-8 text-[24px] text-ink" />
      {children}
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
