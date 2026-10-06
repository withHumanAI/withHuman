import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Button } from "@/ds/components/button";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import { DurationField } from "@/ds/components/duration-field";
import { TagInput } from "@/ds/components/tag-input";
import { RadioCard, RadioGroup, RadioOption } from "@/ds/components/radio-group";
import { SaveBar } from "@/ds/components/save-bar";
import { UnsavedGuard } from "@/ds/components/unsaved-guard";
import { SettingRow } from "@/ds/components/setting-row";
import { SettingsSection } from "@/ds/components/settings-section";
import { Badge } from "@/ds/components/badge";
import { EmptyState } from "@/ds/components/empty-state";
import { IconButton } from "@/ds/components/icon-button";
import { Switch } from "@/ds/components/switch";
import { TextField } from "@/ds/components/text-field";
import { useToast } from "@/ds/components/toast";
import { AgentIcon, ArrowLeftIcon, CheckIcon, MailIcon, MessageIcon, ToolIcon, PlusIcon, TrashIcon } from "@/ds/icons";
import { SlackIcon } from "@/ds/icons/brands";
import { Section, Demo } from "../section";

interface PolicyDraft {
  personalKeys: boolean;
  name: string;
}

/* Explicit-save demo: dirty means different-from-saved, so flipping a
 * switch back sinks the bar again. */
function SaveBarDemo() {
  const toast = useToast();
  const navigate = useNavigate();
  const [saved, setSaved] = useState<PolicyDraft>({
    personalKeys: false,
    name: "Acme",
  });
  const [draft, setDraft] = useState<PolicyDraft>(saved);
  const [saving, setSaving] = useState(false);

  const dirty =
    Number(draft.personalKeys !== saved.personalKeys) +
    Number(draft.name !== saved.name);

  function save(): Promise<void> {
    setSaving(true);
    return new Promise((resolve) => {
      window.setTimeout(() => {
        setSaved(draft);
        setSaving(false);
        toast.add({ type: "approve", title: "Policy updated" });
        resolve();
      }, 700);
    });
  }

  return (
    <>
      <SettingsSection title="Decisions" description="Edit anything and the save bar rises; put it back and the bar sinks.">
        <SettingRow
          label="Allow personal API keys"
          description="Let members create keys for their own scripts."
        >
          <Switch
            checked={draft.personalKeys}
            onCheckedChange={(next) =>
              setDraft((current) => ({ ...current, personalKeys: next }))
            }
            aria-label="Allow personal API keys"
          />
        </SettingRow>
        <SettingRow
          label="Organization name"
          description="Shown to reviewers and in notifications."
        >
          <TextField
            aria-label="Organization name"
            value={draft.name}
            onChange={(event) =>
              setDraft((current) => ({ ...current, name: event.target.value }))
            }
            className="w-52"
          />
        </SettingRow>
      </SettingsSection>
      <p className="mt-3 text-micro text-ink-faint">
        While dirty, leaving the page is intercepted:{" "}
        <button
          type="button"
          onClick={() => void navigate({ to: "/queue" })}
          className="cursor-pointer text-ink-secondary underline decoration-edge-strong underline-offset-2 hover:decoration-ink"
        >
          try to leave for the queue
        </button>
        . Tab close and refresh get the browser prompt.
      </p>
      <SaveBar
        count={dirty}
        saving={saving}
        onDiscard={() => setDraft(saved)}
        onSave={save}
      />
      <UnsavedGuard
        dirty={dirty > 0}
        saving={saving}
        onSave={save}
        onDiscard={() => setDraft(saved)}
      />
    </>
  );
}

function DurationTagDemo() {
  const [seconds, setSeconds] = useState(8 * 3600);
  const [scopes, setScopes] = useState(["production", "billing"]);
  return (
    <div className="flex flex-col">
      <SettingRow
        label="Reauthentication window"
        description={`Stored as ${seconds} seconds.`}
      >
        <DurationField
          aria-label="Reauthentication window"
          value={seconds}
          onChange={setSeconds}
        />
      </SettingRow>
      <SettingRow
        label="Policy scopes"
        description="Scopes this member may decide for."
      >
        <TagInput
          aria-label="Policy scopes"
          mono
          value={scopes}
          onChange={setScopes}
          placeholder="Add a scope"
          className="w-64"
        />
      </SettingRow>
      <SettingRow
        label="Reserved names (read only)"
        description="Disabled: the chips stay, nothing can be added or removed."
      >
        <TagInput
          aria-label="Reserved names"
          mono
          disabled
          value={["admin", "owner"]}
          onChange={() => undefined}
          className="w-64"
        />
      </SettingRow>
    </div>
  );
}

function MfaDemo() {
  const [mode, setMode] = useState("off");
  return (
    <RadioGroup
      aria-label="Multi-factor requirement"
      value={mode}
      onValueChange={(next) => setMode(next as string)}
    >
      <RadioOption
        value="off"
        label="Off"
        description="Any active session can decide."
      />
      <RadioOption
        value="reviewers"
        label="Reviewers"
        description="Deciding a request requires strong authentication; everything else does not."
      />
      <RadioOption
        value="everyone"
        label="Everyone"
        description="Every sign-in requires strong authentication."
      />
    </RadioGroup>
  );
}

function WayDemo() {
  const [way, setWay] = useState<string | null>(null);
  return (
    <RadioGroup
      aria-label="How to write the rules"
      value={way}
      onValueChange={(next) => setWay(next as string)}
      className="grid grid-cols-1 gap-3 sm:grid-cols-3"
    >
      <RadioCard
        value="agent"
        icon={<AgentIcon width={16} height={16} />}
        label="With my coding agent"
        description="Paste a prompt into your coding agent to write the rules."
      />
      <RadioCard
        value="describe"
        icon={<MessageIcon width={16} height={16} />}
        label="Describe it in words"
        description="Say what should wait for you."
      />
      <RadioCard
        value="manual"
        icon={<ToolIcon width={16} height={16} />}
        label="Pick tools myself"
        description="Tick the tools that need your approval."
      />
    </RadioGroup>
  );
}

export function SettingsSection_() {
  const [personalKeys, setPersonalKeys] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const toast = useToast();
  return (
    <Section
      id="settings"
      title="Settings"
      lede="The settings toolkit, in the Linear mold: every settings screen is a SettingsPage (narrow by default, one title line, at most one action), every group on it is a SettingsSection, and every entry in a section is a SettingRow. Nothing else groups anything on a settings page."
    >
      <Demo
        label="Rules"
        hint="what every settings page follows, so the pages line up whatever they hold"
        bare
      >
        <ul className="flex max-w-[560px] flex-col gap-1.5 text-body text-ink-secondary">
          <li>Shared measures: SettingsPage defaults to narrow. Tables can use the standard Page list or wide measure when they need more columns.</li>
          <li>One heading scale: the page title, then section titles at body weight medium. No card headers, no bare h2s.</li>
          <li>One group: SettingsSection. Rows inset by default; inset none for a Table, an EmptyState, or a form with its own padding.</li>
          <li>Every section title is an anchor (its slug, or an explicit id): hover shows the link, the URL hash lands the page on it.</li>
          <li>One row: SettingRow, for options (label, control) and for things (icon, value, badges). A row that needs something from you unfolds a panel.</li>
          <li>Empty lists are an EmptyState inside the section. Missing permission is one neutral Callout under the title, with the page shape kept.</li>
          <li>Saving: organization policy commits through the SaveBar; a personal preference that cannot be wrong (theme) applies at once; anything that creates a thing goes through a dialog.</li>
        </ul>
      </Demo>

      <Demo label="Things in rows" hint="icon, value, state, one quiet action; the section title line carries the add affordance" bare>
        <div className="flex max-w-[560px] flex-col">
          <SettingsSection
            title="Channels"
            description="Every way an approval can reach you."
            action={
              <Button size="sm" variant="neutral">
                <PlusIcon />
                Add channel
              </Button>
            }
            note="Discord is not set up on this deployment."
          >
            <SettingRow
              icon={<MailIcon />}
              label={<span className="font-mono">chris@example.com</span>}
              description="Account email"
            >
              <Badge tone="neutral" icon={<CheckIcon />}>
                Account email
              </Badge>
            </SettingRow>
            <SettingRow
              icon={<MailIcon />}
              label={<span className="font-mono">chris@work.example</span>}
              description="Email"
            >
              <Badge tone="approve" icon={<CheckIcon />}>
                Confirmed
              </Badge>
              <IconButton aria-label="Remove chris@work.example" variant="ghost" size="sm">
                <TrashIcon />
              </IconButton>
            </SettingRow>
            <SettingRow icon={<SlackIcon />} label="Slack" description="Matched from your confirmed addresses when an admin syncs members.">
              <Badge tone="neutral">Not linked</Badge>
            </SettingRow>
          </SettingsSection>
        </div>
      </Demo>

      <Demo label="Empty section" hint="the section stays; the card holds the invitation" bare>
        <div className="flex max-w-[560px] flex-col">
          <SettingsSection title="Pending invitations" inset="none">
            <EmptyState
              title="No pending invitations"
              description="Invitations you send wait here until they are accepted or expire."
            />
          </SettingsSection>
        </div>
      </Demo>

      <Demo label="SettingsSection + SettingRow" bare>
        <div className="flex max-w-[560px] flex-col gap-8">
          <SettingsSection
            title="Decisions"
            description="How approvals are authorized."
          >
            <SettingRow
              label="Allow personal API keys"
              description="Let members create keys for their own scripts."
            >
              <Switch
                checked={personalKeys}
                onCheckedChange={setPersonalKeys}
                aria-label="Allow personal API keys"
              />
            </SettingRow>
            <SettingRow
              label="Organization name"
              description="Shown to reviewers and in notifications."
            >
              <TextField aria-label="Organization name" defaultValue="Acme" className="w-52" />
            </SettingRow>
            <SettingRow label="A row without a description">
              <Button size="sm" variant="neutral">
                Manage
              </Button>
            </SettingRow>
          </SettingsSection>

          <SettingsSection
            tone="danger"
            title="Danger zone"
            description="Actions here cannot be undone."
          >
            <SettingRow
              label="Delete organization"
              description="Permanently removes every agent, request, and member."
            >
              <Button variant="danger" onClick={() => setConfirmOpen(true)}>
                Delete organization
              </Button>
            </SettingRow>
          </SettingsSection>
          <ConfirmDialog
            open={confirmOpen}
            onOpenChange={setConfirmOpen}
            title="Delete this organization?"
            description="Every agent, pending request, decision record, and member of acme is removed permanently. There is no way back."
            confirmLabel="Delete organization"
            typeToConfirm="acme"
            onConfirm={() =>
              new Promise<void>((resolve) =>
                window.setTimeout(() => {
                  toast.add({ type: "deny", title: "Organization deleted (demo)" });
                  resolve();
                }, 600),
              )
            }
          />
        </div>
      </Demo>

      <Demo
        label="RadioGroup"
        hint="for consequential mutually exclusive choices; ToggleGroup stays for compact switches"
      >
        <div className="max-w-[440px]">
          <MfaDemo />
        </div>
      </Demo>

      <Demo
        label="RadioCard"
        hint="a few ways to do one thing, each opening its own panel below; starts with nothing chosen"
      >
        <WayDemo />
      </Demo>

      <Demo
        label="DurationField & TagInput"
        hint="durations emit seconds; tags add on Enter or comma, Backspace removes; disabled keeps the chips"
      >
        <div className="max-w-[440px]">
          <DurationTagDemo />
        </div>
      </Demo>

      <Demo
        label="SaveBar"
        hint="explicit save: a batch of edits commits as one policy update"
        bare
      >
        <div className="flex max-w-[560px] flex-col">
          <SaveBarDemo />
        </div>
      </Demo>

      <Demo
        label="Shell anatomy"
        hint="static preview; settings gets its own chrome, back link, grouped nav"
        bare
      >
        <div className="flex h-[300px] overflow-hidden rounded-lg border-hairline border-edge bg-canvas shadow-sm">
          <div className="flex w-[190px] shrink-0 flex-col gap-5 border-r-hairline border-edge px-3 py-3">
            <span className="flex h-8 items-center gap-2.5 rounded-sm px-2 text-body text-ink-secondary">
              <ArrowLeftIcon width={15} height={15} className="text-ink-muted" />
              Back to app
            </span>
            <div className="flex flex-col gap-1.5 px-1">
              <p className="px-2 text-micro tracking-[0.06em] text-ink-faint uppercase">
                Workspace
              </p>
              <div className="flex flex-col gap-0.5">
                <span className="flex h-7 items-center rounded-sm bg-fill-selected px-2 text-body text-ink">
                  General
                </span>
                <span className="flex h-7 items-center rounded-sm px-2 text-body text-ink-secondary">
                  Approval policy
                </span>
                <span className="flex h-7 items-center rounded-sm px-2 text-body text-ink-secondary">
                  Members
                </span>
              </div>
            </div>
          </div>
          <div className="min-w-0 flex-1 overflow-hidden px-8 py-6">
            <div className="flex flex-col gap-1.5">
              <p className="text-title-lg text-ink">General</p>
              <p className="text-body text-ink-muted">
                Workspace identity and appearance.
              </p>
            </div>
            <div className="mt-6">
              <SettingsSection title="Organization">
                <SettingRow
                  label="Name"
                  description="Shown to reviewers and in notifications."
                >
                  <TextField aria-label="Name" defaultValue="Acme" className="w-44" />
                </SettingRow>
              </SettingsSection>
            </div>
          </div>
        </div>
      </Demo>
    </Section>
  );
}
