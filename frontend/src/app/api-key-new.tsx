import { useMemo, useState, type FormEvent } from "react";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { CodeBlock } from "@/ds/components/code-block";
import { DateTimeField } from "@/ds/components/date-time-field";
import { RadioGroup, RadioOption } from "@/ds/components/radio-group";
import { SettingRow } from "@/ds/components/setting-row";
import { SettingsPage } from "@/ds/components/settings-page";
import { SettingsSection } from "@/ds/components/settings-section";
import { TextField } from "@/ds/components/text-field";
import { PageBackLink } from "@/ds/components/page-header";
import { AlertIcon } from "@/ds/icons";
import { api, RequestError } from "../api";
import type { APIKey, APIKeyPermission, MyAPIKeys } from "../types";
import { groupPermissions, PermissionChecklist } from "./permission-checklist";
import { useTitle } from "./use-title";

/*
 * Settings: create an API key. Its own page, not a dialog: name the
 * key, choose what it may do and when it stops, then see the raw token
 * the one time it is visible, with the command that proves it works.
 */

/* What the route loader delivers: the same answer the list page reads,
 * because the form needs the organization's switch, whether scoping is
 * available here, and the permissions the person could place on a key. */
export interface NewAPIKeyPage {
  mine: MyAPIKeys | null;
  error: string;
}

const NAME_MAX = 100;

type PermissionsMode = "inherit" | "scoped";
type ExpiryMode = "never" | "on";

const DEFAULT_EXPIRY_DAYS = 90;

/* The server's refusal in the page's own words. A forbidden answer is
 * about the sign-in, not the permissions: it says whether the session
 * is too old or too weak for what the key would carry. */
function createErrorMessage(reason: unknown): string {
  if (!(reason instanceof RequestError)) {
    return reason instanceof Error ? reason.message : "The key could not be created";
  }
  switch (reason.code) {
    case "api_keys_disabled":
      return "An administrator turned personal API keys off.";
    case "api_key_scoping_unavailable":
      return "This edition cannot scope keys.";
    case "plan_required":
      return "Your plan does not include scoped keys.";
    case "authentication_stale":
      return "Sign in again to create a key with these permissions.";
    case "authentication_weak":
      return "Sign in with multi-factor authentication to create a key with these permissions.";
    default:
      return reason.message;
  }
}

const DESCRIPTION = "Let your scripts act with your permissions.";

export function NewAPIKeyScreen({ page }: { page: NewAPIKeyPage }) {
  useTitle("Create personal API key");
  const { mine, error } = page;

  if (!mine) {
    return (
      <SettingsPage title="Create personal API key" description={DESCRIPTION} back={<PageBackLink to="/settings/personal-api-keys">Personal API keys</PageBackLink>}>
        <Callout tone="warn" title="Unable to load your API keys">
          {error}
        </Callout>
      </SettingsPage>
    );
  }
  if (!mine.personal_api_keys_allowed) {
    return (
      <SettingsPage title="Create personal API key" description={DESCRIPTION} back={<PageBackLink to="/settings/personal-api-keys">Personal API keys</PageBackLink>}>
        <Callout tone="warn" title="Personal API keys are turned off">
          An administrator turned personal API keys off for this organization.
        </Callout>
      </SettingsPage>
    );
  }
  return (
    <CreateKeyForm
      scopingEnabled={mine.scoping_enabled}
      bearable={mine.bearable_permissions}
    />
  );
}

/* Minting a key: name, what it may do, when it stops, then the one
 * moment the raw token is visible. Done returns to the list, which
 * learns about the new key on the way. */
function CreateKeyForm({
  scopingEnabled,
  bearable,
}: {
  scopingEnabled: boolean;
  bearable: APIKeyPermission[];
}) {
  const router = useRouter();
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [permissionsMode, setPermissionsMode] = useState<PermissionsMode>("inherit");
  const [checked, setChecked] = useState<Set<string>>(() => new Set());
  /* A key expires by default, ninety days out; never expiring is the
   * choice that has to be made on purpose. */
  const [expiryMode, setExpiryMode] = useState<ExpiryMode>("on");
  const [expiresAt, setExpiresAt] = useState<string | null>(() =>
    new Date(Date.now() + DEFAULT_EXPIRY_DAYS * 24 * 60 * 60 * 1000).toISOString(),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [minted, setMinted] = useState<{ key: APIKey; token: string } | null>(null);

  const areas = useMemo(() => groupPermissions(bearable), [bearable]);

  const trimmedName = name.trim();
  const scoped = scopingEnabled && permissionsMode === "scoped";
  const expiryValid =
    expiryMode === "never" ||
    (expiresAt !== null && new Date(expiresAt).getTime() > Date.now());
  const valid =
    trimmedName !== "" &&
    trimmedName.length <= NAME_MAX &&
    (!scoped || checked.size > 0) &&
    expiryValid;

  function toggle(key: string, next: boolean) {
    setChecked((current) => {
      const result = new Set(current);
      if (next) result.add(key);
      else result.delete(key);
      return result;
    });
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!valid || busy) return;
    setBusy(true);
    setError("");
    try {
      const result = await api.createAPIKey({
        name: trimmedName,
        ...(scoped ? { permissions: Array.from(checked) } : {}),
        ...(expiryMode === "on" && expiresAt ? { expires_at: expiresAt } : {}),
      });
      setMinted(result);
    } catch (reason) {
      setError(createErrorMessage(reason));
    } finally {
      setBusy(false);
    }
  }

  async function done() {
    await router.invalidate();
    await navigate({ to: "/settings/personal-api-keys" });
  }

  if (minted) {
    const verifyCommand = `curl "${window.location.origin}/api/v1/api_keys/verify" -H "Authorization: Bearer ${minted.token}"`;
    return (
      <SettingsPage
        back={<PageBackLink to="/settings/personal-api-keys">Personal API keys</PageBackLink>}
        title="Personal API key created"
        description={`${minted.key.name} is ready to use.`}
        action={
          <Button variant="primary" onClick={() => void done()}>
            Done
          </Button>
        }
      >
        <SettingsSection
          title="Your personal API key"
          inset="none"
        >
          <div className="flex flex-col gap-3 px-4 py-4">
            <Callout tone="warn" title="Copy this key now">
              You won't see it again after you leave this page.
            </Callout>
            <CodeBlock label="API key" code={minted.token} />
          </div>
        </SettingsSection>
        <SettingsSection
          title="Check that it works"
          description="Run this from a terminal to see the key's name and permissions."
          inset="none"
        >
          <div className="px-4 py-4">
            <CodeBlock label="verify" code={verifyCommand} />
          </div>
        </SettingsSection>
      </SettingsPage>
    );
  }

  return (
    <SettingsPage title="Create personal API key" description={DESCRIPTION} back={<PageBackLink to="/settings/personal-api-keys">Personal API keys</PageBackLink>}>
      <form onSubmit={submit} className="flex flex-col gap-8">
        <SettingsSection title="Name" description="How the key appears in your list.">
          <SettingRow label="Name" description="The more descriptive, the better.">
            <TextField
              aria-label="Name"
              className="w-[260px]"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="nightly report"
              maxLength={NAME_MAX}
              autoComplete="off"
              autoFocus
              required
            />
          </SettingRow>
        </SettingsSection>

        <SettingsSection
          title="Permissions"
          description="What the key can do."
          inset="none"
        >
          <div className="flex flex-col gap-3 px-4 py-4">
            {scopingEnabled ? (
              <>
                <RadioGroup
                  aria-label="Permissions"
                  value={permissionsMode}
                  onValueChange={(next) => setPermissionsMode(next as PermissionsMode)}
                >
                  <RadioOption
                    value="inherit"
                    label="Everything I can do"
                    description="The key follows your permissions as they change."
                  />
                  <RadioOption
                    value="scoped"
                    label="Only these permissions"
                    description="Choose which of your permissions the key can use."
                  />
                </RadioGroup>
                {scoped ? (
                  <PermissionChecklist
                    areas={areas}
                    checked={checked}
                    onToggle={toggle}
                    onSelectAll={() =>
                      setChecked(new Set(bearable.map((permission) => permission.key)))
                    }
                    onClear={() => setChecked(new Set())}
                  />
                ) : null}
              </>
            ) : (
              <p className="text-body text-ink-muted">
                This key has all of your permissions.
              </p>
            )}
          </div>
        </SettingsSection>

        <SettingsSection
          title="Expiry"
          description="The key will stop working after this point."
          inset="none"
        >
          <div className="px-4 py-4">
            <RadioGroup
              aria-label="Expiry"
              value={expiryMode}
              onValueChange={(next) => setExpiryMode(next as ExpiryMode)}
            >
              <RadioOption
                value="on"
                label="Expires on"
                description={
                  expiryMode === "on" ? (
                    <span className="mt-1.5 flex flex-col gap-1">
                      <DateTimeField
                        aria-label="Expiry date and time"
                        value={expiresAt}
                        onChange={setExpiresAt}
                        placeholder="Pick a date and time"
                      />
                      {expiresAt && !expiryValid ? (
                        <span className="text-label text-deny">
                          Pick a moment in the future.
                        </span>
                      ) : null}
                    </span>
                  ) : (
                    `${DEFAULT_EXPIRY_DAYS} days by default.`
                  )
                }
              />
              <RadioOption value="never" label="Never expires" />
            </RadioGroup>
            {expiryMode === "never" ? (
              <Callout tone="warn" title="A key that never expires is not best practice" className="mt-3">
                A leaked key stays usable until someone revokes it.
              </Callout>
            ) : null}
          </div>
        </SettingsSection>

        {error ? (
          <Callout tone="deny" icon={<AlertIcon width={15} height={15} />}>
            {error}
          </Callout>
        ) : null}

        <div className="flex items-center justify-end gap-4">
          <span className="flex shrink-0 gap-2.5">
            <Button
              variant="ghost"
              type="button"
              onClick={() => void navigate({ to: "/settings/personal-api-keys" })}
            >
              Cancel
            </Button>
            <Button variant="primary" type="submit" loading={busy} disabled={!valid}>
              Create personal API key
            </Button>
          </span>
        </div>
      </form>
    </SettingsPage>
  );
}
