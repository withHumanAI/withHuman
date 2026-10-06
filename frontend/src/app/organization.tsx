import { useState } from "react";
import { useRouter } from "@tanstack/react-router";
import { Button } from "@/ds/components/button";
import { DurationField } from "@/ds/components/duration-field";
import { Callout } from "@/ds/components/callout";
import { RadioGroup, RadioOption } from "@/ds/components/radio-group";
import { SaveBar } from "@/ds/components/save-bar";
import { SettingRow } from "@/ds/components/setting-row";
import { SettingsPage } from "@/ds/components/settings-page";
import { SettingsSection } from "@/ds/components/settings-section";
import { Switch } from "@/ds/components/switch";
import { useToast } from "@/ds/components/toast";
import { UnsavedGuard } from "@/ds/components/unsaved-guard";
import { api } from "../api";
import type {
  MFAMode,
  OrganizationPolicy,
  RequestVisibilityPolicy,
  UnroutedRequestsPolicy,
} from "../types";
import { formatGoDuration, parseGoDuration } from "./duration";
import { ExtensionOrganizationPolicyRows } from "./extensions";
import { useTitle } from "./use-title";

/*
 * Settings: organization. The approval policy knobs, edited as a batch
 * behind one save bar: who may approve what, how fresh a reviewer's
 * sign-in must be, and where requests go when routing names nobody.
 * Dirty means different from what the server holds, so undoing an edit
 * sinks the bar again.
 */

/* The policy as the form holds it: durations in seconds so the fields
 * and the dirty check compare numbers, never two spellings of one
 * duration ("24h" and "24h0m0s"). Every knob the server holds is here,
 * including organizationAPIKeysAllowed, whose row an edition adds
 * through extensions.organizationPolicyRows: core serves the knob and
 * the edition ships the keys it governs. */
export interface OrganizationPolicyDraft {
  mfaMode: MFAMode;
  reauthSeconds: number;
  claimSeconds: number;
  unroutedRequests: UnroutedRequestsPolicy;
  requestVisibility: RequestVisibilityPolicy;
  personalAPIKeysAllowed: boolean;
  organizationAPIKeysAllowed: boolean;
}

/* A duration the server wrote is always parseable; the fallback only
 * keeps the form usable if it ever is not. */
function seconds(goDuration: string, fallback: number): number {
  const parsed = parseGoDuration(goDuration);
  return parsed === null || parsed <= 0 ? fallback : parsed;
}

function fromWire(policy: OrganizationPolicy): OrganizationPolicyDraft {
  return {
    mfaMode: policy.mfa_mode,
    reauthSeconds: seconds(policy.approval_reauth_window, 30 * 24 * 3600),
    claimSeconds: seconds(policy.claim_retrigger_window, 15 * 60),
    unroutedRequests: policy.unrouted_requests,
    requestVisibility: policy.request_visibility,
    personalAPIKeysAllowed: policy.personal_api_keys_allowed,
    organizationAPIKeysAllowed: policy.organization_api_keys_allowed,
  };
}

/* Only the fields that changed travel, so two administrators editing
 * different knobs at once do not overwrite each other. */
function patchBetween(saved: OrganizationPolicyDraft, draft: OrganizationPolicyDraft): Partial<OrganizationPolicy> {
  const patch: Partial<OrganizationPolicy> = {};
  if (draft.mfaMode !== saved.mfaMode) patch.mfa_mode = draft.mfaMode;
  if (draft.reauthSeconds !== saved.reauthSeconds) {
    patch.approval_reauth_window = formatGoDuration(draft.reauthSeconds);
  }
  if (draft.claimSeconds !== saved.claimSeconds) {
    patch.claim_retrigger_window = formatGoDuration(draft.claimSeconds);
  }
  if (draft.unroutedRequests !== saved.unroutedRequests) {
    patch.unrouted_requests = draft.unroutedRequests;
  }
  if (draft.requestVisibility !== saved.requestVisibility) {
    patch.request_visibility = draft.requestVisibility;
  }
  if (draft.personalAPIKeysAllowed !== saved.personalAPIKeysAllowed) {
    patch.personal_api_keys_allowed = draft.personalAPIKeysAllowed;
  }
  if (draft.organizationAPIKeysAllowed !== saved.organizationAPIKeysAllowed) patch.organization_api_keys_allowed = draft.organizationAPIKeysAllowed;
  return patch;
}

const DESCRIPTION = "Decide what guardrails to put in place for your organization.";

/* What the organization route loader delivers: the policy, or why it
 * could not be read. */
export interface OrganizationPage {
  policy: OrganizationPolicy | null;
  error: string;
}

export function loadOrganizationPage(): Promise<OrganizationPage> {
  return api.organizationPolicy().then(
    (result) => ({ policy: result.policy, error: "" }),
    (reason: unknown) => ({
      policy: null,
      error: reason instanceof Error ? reason.message : "Could not load the policy",
    }),
  );
}

/* The route keys this screen on the loaded policy, so a reload starts
 * the draft from what the server holds. */
export function OrganizationScreen({
  page,
  canRead,
  canEdit,
}: {
  page: OrganizationPage;
  canRead: boolean;
  canEdit: boolean;
}) {
  useTitle("Organization policies");
  const toast = useToast();
  const router = useRouter();
  const { error } = page;
  const [saved, setSaved] = useState<OrganizationPolicyDraft | null>(() =>
    page.policy ? fromWire(page.policy) : null,
  );
  const [draft, setDraft] = useState<OrganizationPolicyDraft | null>(saved);
  const [saving, setSaving] = useState(false);

  const dirty = saved && draft ? Object.keys(patchBetween(saved, draft)).length : 0;

  function update(change: Partial<OrganizationPolicyDraft>) {
    setDraft((current) => (current ? { ...current, ...change } : current));
  }

  function discard() {
    if (saved) setDraft(saved);
  }

  async function save() {
    if (!saved || !draft || dirty === 0) return;
    setSaving(true);
    try {
      const result = await api.setOrganizationPolicy(patchBetween(saved, draft));
      const next = fromWire(result.policy);
      setSaved(next);
      setDraft(next);
      toast.add({ type: "approve", title: "Organization settings saved" });
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "Could not save the settings",
        description: reason instanceof Error ? reason.message : undefined,
      });
    } finally {
      setSaving(false);
    }
  }

  if (!canRead) {
    return (
      <SettingsPage title="Organization policies" description={DESCRIPTION}>
        <Callout tone="neutral" title="Organization settings are visible to administrators">
          You need the organization.read permission to view this page.
        </Callout>
      </SettingsPage>
    );
  }

  return (
    <SettingsPage title="Organization policies" description={DESCRIPTION}>
      {error ? (
        <Callout tone="warn" title="Could not load the policy">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span>{error}</span>
            <Button size="sm" variant="ghost" onClick={() => void router.invalidate()}>
              Try again
            </Button>
          </div>
        </Callout>
      ) : !draft ? null : (
        <>
          <SettingsSection
            title="Approvals"
            description={
              canEdit
                ? "Rules for reviewers who decide requests."
                : "Rules for reviewers who decide requests. You don't have permission to edit these."
            }
          >
            <SettingRow
              label="Multi-factor authentication"
              description="Whether deciding a request requires a strong sign-in."
              className="items-start"
            >
              <RadioGroup
                aria-label="Multi-factor authentication"
                className="w-[300px]"
                value={draft.mfaMode}
                onValueChange={(next) => update({ mfaMode: next as MFAMode })}
              >
                <RadioOption value="optional" label="Optional" disabled={!canEdit} />
                <RadioOption
                  value="reviewers_required"
                  label="Required for reviewers"
                  disabled={!canEdit}
                />
                <RadioOption
                  value="everyone_required"
                  label="Required for everyone"
                  disabled={!canEdit}
                />
              </RadioGroup>
            </SettingRow>
            <SettingRow
              label="Ask reviewers to sign in again after"
              description="How long reviewers can decide requests before signing in again."
            >
              <DurationField
                aria-label="Ask reviewers to sign in again after"
                value={draft.reauthSeconds}
                disabled={!canEdit}
                onChange={(next) => update({ reauthSeconds: next })}
              />
            </SettingRow>
            <SettingRow
              label="Release a claim after"
              description="How long a reviewer can claim a request without deciding it."
            >
              <DurationField
                aria-label="Release a claim after"
                value={draft.claimSeconds}
                disabled={!canEdit}
                onChange={(next) => update({ claimSeconds: next })}
              />
            </SettingRow>
          </SettingsSection>

          <SettingsSection
            title="Routing"
            description="Policies around approval routing."
          >
            <SettingRow
              label="Unrouted requests"
              description="Where a request goes when no escalation path matches it."
              className="items-start"
            >
              <RadioGroup
                aria-label="Unrouted requests"
                className="w-[300px]"
                value={draft.unroutedRequests}
                onValueChange={(next) => update({ unroutedRequests: next as UnroutedRequestsPolicy })}
              >
                <RadioOption
                  value="any_reviewer"
                  label="Any reviewer"
                  description="Requests that match no escalation path go to every reviewer."
                  disabled={!canEdit}
                />
                <RadioOption
                  value="break_glass_only"
                  label="Break-glass only"
                  description="Only admins can review these requests."
                  disabled={!canEdit}
                />
              </RadioGroup>
            </SettingRow>
            <SettingRow
              label="Request visibility"
              description="Which requests a person with request access can open."
              className="items-start"
            >
              <RadioGroup
                aria-label="Request visibility"
                className="w-[300px]"
                value={draft.requestVisibility}
                onValueChange={(next) =>
                  update({ requestVisibility: next as RequestVisibilityPolicy })
                }
              >
                <RadioOption
                  value="all_readers"
                  label="Everyone with request access"
                  description="Anyone who can read requests sees every request."
                  disabled={!canEdit}
                />
                <RadioOption
                  value="routed_only"
                  label="Only the people it was routed to"
                  description="People see requests routed to them and requests their own agents made."
                  disabled={!canEdit}
                />
              </RadioGroup>
            </SettingRow>
          </SettingsSection>

          <SettingsSection
            title="Programmatic access"
            description="Policies about API keys."
          >
            <SettingRow
              label="Let members create personal API keys"
              description="Turning this off stops all personal API keys from working."
            >
              <Switch
                aria-label="Let members create personal API keys"
                checked={draft.personalAPIKeysAllowed}
                disabled={!canEdit}
                onCheckedChange={(next) => update({ personalAPIKeysAllowed: next })}
              />
            </SettingRow>
            <ExtensionOrganizationPolicyRows draft={draft} update={update} canEdit={canEdit} />
          </SettingsSection>
        </>
      )}

      <SaveBar count={dirty} saving={saving} onDiscard={discard} onSave={() => void save()} />
      <UnsavedGuard dirty={dirty > 0} saving={saving} onSave={save} onDiscard={discard} />
    </SettingsPage>
  );
}
