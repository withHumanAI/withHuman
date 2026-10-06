import { useState, type FormEvent } from "react";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { SettingsPage } from "@/ds/components/settings-page";
import { SettingsSection } from "@/ds/components/settings-section";
import { TextField } from "@/ds/components/text-field";
import { useToast } from "@/ds/components/toast";
import { AlertIcon } from "@/ds/icons";
import { api, RequestError } from "../api";
import { useSession } from "./principal";
import { useRuntimeConfig } from "./use-runtime-config";
import { useTitle } from "./use-title";

const CHANGE_ERRORS: Record<string, string> = {
  password_mismatch: "That is not your current password.",
  invalid_request: "The new password must be at least 12 characters and differ from the current one.",
};

/* Settings: account. Everything on this page acts on the signed-in
 * account, so it has no permission gate. */
export function AccountScreen() {
  useTitle("Account");
  const toast = useToast();
  const session = useSession();
  const runtime = useRuntimeConfig();
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [formKey, setFormKey] = useState(0);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const current = String(data.get("current_password") ?? "");
    const next = String(data.get("new_password") ?? "");
    if (next !== String(data.get("new_password_confirmation") ?? "")) {
      setError("New passwords do not match");
      return;
    }
    setError("");
    setSubmitting(true);
    try {
      await api.changePassword(current, next);
      toast.add({
        type: "approve",
        title: "Password changed",
        description: "Every other device was signed out.",
      });
      setFormKey((key) => key + 1);
    } catch (reason) {
      const code = reason instanceof RequestError ? reason.code : "";
      setError(
        CHANGE_ERRORS[code] ?? (reason instanceof Error ? reason.message : "The change failed"),
      );
    } finally {
      setSubmitting(false);
    }
  }

  const description = `${session.user?.email ?? ""}${
    session.organization ? `, ${session.organization.name}` : ""
  }`;

  /* An account founded through a social provider has no password, so a
   * change form could only ever fail. Name the way in instead. */
  if (session.user && !session.user.has_password) {
    const providers = (runtime.config?.social_providers ?? []).map((provider) => provider.label);
    const via = providers.length ? ` through ${joinNames(providers)}` : " through an identity provider";
    return (
      <SettingsPage title="Account" description={description}>
        <SettingsSection
          title="Password"
          description={`You sign in${via}.`}
        >
          <p className="py-3 text-body text-ink-muted">
            This account has no password to change.
          </p>
        </SettingsSection>
      </SettingsPage>
    );
  }

  return (
    <SettingsPage title="Account" description={description}>
      <SettingsSection
        title="Password"
        description="At least 12 characters. Changing it signs out every other device."
        inset="none"
      >
        <form key={formKey} onSubmit={submit}>
          <div className="flex flex-col gap-4 px-4 py-4">
            <TextField
              name="current_password"
              label="Current password"
              type="password"
              autoComplete="current-password"
              required
            />
            <TextField
              name="new_password"
              label="New password"
              type="password"
              minLength={12}
              autoComplete="new-password"
              required
            />
            <TextField
              name="new_password_confirmation"
              label="Confirm new password"
              type="password"
              minLength={12}
              autoComplete="new-password"
              required
            />
            {error ? (
              <Callout tone="deny" icon={<AlertIcon width={15} height={15} />}>
                {error}
              </Callout>
            ) : null}
          </div>
          <div className="flex justify-end border-t-hairline border-edge px-4 py-3">
            <Button type="submit" variant="primary" loading={submitting}>
              Change password
            </Button>
          </div>
        </form>
      </SettingsSection>
    </SettingsPage>
  );
}

/* "Google", "Google or GitHub", "Google, GitHub or Microsoft". */
function joinNames(names: string[]): string {
  if (names.length <= 1) return names.join("");
  return `${names.slice(0, -1).join(", ")} or ${names[names.length - 1]}`;
}
