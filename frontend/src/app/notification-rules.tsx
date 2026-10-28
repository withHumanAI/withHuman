import { useMemo, useState, type ReactNode } from "react";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { IconButton } from "@/ds/components/icon-button";
import { SaveBar } from "@/ds/components/save-bar";
import { Select, SelectContent, SelectItem, SelectTrigger } from "@/ds/components/select";
import { SettingsSection } from "@/ds/components/settings-section";
import { useToast } from "@/ds/components/toast";
import { UnsavedGuard } from "@/ds/components/unsaved-guard";
import { PlusIcon, TrashIcon } from "@/ds/icons";
import { api } from "../api";
import type { ContactMethod, NotificationRule, NotificationRules, NotificationUrgency } from "../types";
import { contactKind } from "./extensions";

/* Rungs are chosen from a fixed set of delays rather than typed, the way
 * incident.io does it: the choices are the ones that make sense for a
 * request that has a deadline, and a fixed list keeps ladders comparable. */
const DELAYS: { seconds: number; label: string }[] = [
  { seconds: 0, label: "Immediately" },
  { seconds: 60, label: "After 1 minute" },
  { seconds: 180, label: "After 3 minutes" },
  { seconds: 300, label: "After 5 minutes" },
  { seconds: 600, label: "After 10 minutes" },
  { seconds: 900, label: "After 15 minutes" },
  { seconds: 1800, label: "After 30 minutes" },
  { seconds: 3600, label: "After 1 hour" },
];

/* The account email has no stored method, so it is the empty id on the
 * wire and here. */
const ACCOUNT_EMAIL = "";

const LADDERS: { urgency: NotificationUrgency; title: string; description: string }[] = [
  {
    urgency: "standard",
    title: "Standard requests",
    description: "Define how standard urgency requests should get to you.",
  },
  {
    urgency: "interrupt",
    title: "Interrupt Requests",
    description: "Define how urgent requests should get to you.",
  },
];

/* The methods a rung may name, each with the label the picker and the
 * summaries use: the confirmed ones, the account email first. A kind
 * the edition registered names its methods itself; one this bundle
 * does not know reads by its raw kind. */
export function ladderOptions(methods: ContactMethod[]): { id: string; label: string }[] {
  const items: { id: string; label: string }[] = [];
  for (const method of methods) {
    if (!method.verified) continue;
    if (method.kind === "slack") {
      items.push({ id: method.id, label: `Slack, ${method.label || method.value}${method.available ? "" : " (unavailable)"}` });
    } else if (method.kind === "discord") {
      items.push({ id: method.id, label: `Discord, ${method.label || method.value}` });
    } else if (method.kind === "email") {
      if (method.primary) items.unshift({ id: ACCOUNT_EMAIL, label: `Email, ${method.value}` });
      else items.push({ id: method.id, label: `Email, ${method.value}` });
    } else {
      const label = contactKind(method.kind)?.ladderLabel(method) ?? `${method.kind}, ${method.label || method.value}`;
      items.push({ id: method.id, label });
    }
  }
  if (!items.some((item) => item.id === ACCOUNT_EMAIL)) {
    items.unshift({ id: ACCOUNT_EMAIL, label: "Account email" });
  }
  return items;
}

/* One ladder as sentences, in rung order: "Immediately via Email,
 * you@example.com". For a step that says where a prompt is about to go. */
export function ladderLines(rules: NotificationRule[], methods: ContactMethod[]): string[] {
  const options = ladderOptions(methods);
  return sortRungs(rules).map((rule) => {
    const when = DELAYS.find((delay) => delay.seconds === rule.delay_seconds)?.label ?? `After ${rule.delay_seconds} seconds`;
    const method =
      options.find((option) => option.id === (rule.contact_method_id ?? ACCOUNT_EMAIL))?.label ?? "a removed method";
    return `${when} via ${method}`;
  });
}

interface LadderState {
  standard: NotificationRule[];
  interrupt: NotificationRule[];
}

function sameLadder(a: NotificationRule[], b: NotificationRule[]) {
  return (
    a.length === b.length &&
    a.every(
      (rule, index) =>
        rule.delay_seconds === b[index].delay_seconds &&
        (rule.contact_method_id ?? ACCOUNT_EMAIL) === (b[index].contact_method_id ?? ACCOUNT_EMAIL),
    )
  );
}

function sortRungs(rules: NotificationRule[]) {
  return [...rules].sort((a, b) => a.delay_seconds - b.delay_seconds);
}

/* The person's two ladders as a draft over what the server holds. A
 * new value from the loader (after a save, or a change to the methods,
 * which can change the defaults and takes a removed method's rungs with
 * it on the server) starts a fresh draft. Methods are the confirmed ones
 * from the channels, including unavailable Slack links whose saved
 * references survive reconnection. save writes whichever ladder changed
 * and rejects when a write fails, so each caller says so its own way. */
export interface LadderDraft {
  rules: NotificationRules | null;
  draft: LadderState | null;
  dirty: NotificationUrgency[];
  saving: boolean;
  update: (urgency: NotificationUrgency, next: NotificationRule[]) => void;
  discard: () => void;
  save: () => Promise<void>;
}

export function useLadderDraft(
  rules: NotificationRules | null,
  methods: ContactMethod[],
  onSaved: () => Promise<void>,
): LadderDraft {
  const saved =
    JSON.stringify(rules) +
    methods.map((method) => `${method.id}:${method.verified}:${method.available}`).join(",");
  const [draftOf, setDraftOf] = useState(saved);
  const [draft, setDraft] = useState<LadderState | null>(ladderState(rules));
  const [saving, setSaving] = useState(false);
  if (draftOf !== saved) {
    setDraftOf(saved);
    setDraft(ladderState(rules));
  }

  const dirty = useMemo(() => {
    if (!rules || !draft) return [] as NotificationUrgency[];
    return LADDERS.map((ladder) => ladder.urgency).filter(
      (urgency) => !sameLadder(rules[urgency], draft[urgency]),
    );
  }, [rules, draft]);

  const save = async () => {
    if (!draft || dirty.length === 0) return;
    setSaving(true);
    try {
      for (const urgency of dirty) {
        await api.saveNotificationRules(urgency, sortRungs(draft[urgency]));
      }
      await onSaved();
    } finally {
      setSaving(false);
    }
  };

  return {
    rules,
    draft,
    dirty,
    saving,
    update: (urgency, next) =>
      setDraft((current) => (current ? { ...current, [urgency]: next } : current)),
    discard: () => setDraft(ladderState(rules)),
    save,
  };
}

function ladderState(rules: NotificationRules | null): LadderState | null {
  return rules ? { standard: rules.standard, interrupt: rules.interrupt } : null;
}

/* Both ladders over one draft. On a settings page each ladder is a
 * section of its own, saved through LadderSaveBar; embedded in a card
 * (the guided setup) each is a well, and the card's own way forward
 * saves the draft. */
export function NotificationLadders({
  ladders,
  methods,
  onRetry,
  embedded = false,
}: {
  ladders: LadderDraft;
  methods: ContactMethod[];
  /* Reloads the rules after they failed to load. */
  onRetry: () => void;
  embedded?: boolean;
}) {
  const { draft, update } = ladders;
  const options = useMemo(() => ladderOptions(methods), [methods]);
  const unavailableSlack = methods.some((method) => method.kind === "slack" && !method.available);

  if (!ladders.rules || !draft) {
    return (
      <Callout tone="warn" title="Could not load your rules">
        <Button size="sm" variant="ghost" onClick={onRetry}>
          Try again
        </Button>
      </Callout>
    );
  }

  const Frame = embedded ? LadderWell : SettingsSection;

  return (
    <>
      {unavailableSlack ? <Callout tone="neutral">Slack steps are skipped while Slack is unavailable.</Callout> : null}
      {LADDERS.map((ladder) => (
        <Frame
          key={ladder.urgency}
          title={ladder.title}
          description={ladder.description}
          inset="none"
          action={
            <Button
              size="sm"
              variant="neutral"
              disabled={draft[ladder.urgency].length >= 10}
              onClick={() => update(ladder.urgency, addRung(draft[ladder.urgency]))}
            >
              <PlusIcon />
              Add rule
            </Button>
          }
        >
          <Ladder
            urgency={ladder.urgency}
            rules={draft[ladder.urgency]}
            options={options}
            onChange={(next) => update(ladder.urgency, next)}
          />
        </Frame>
      ))}
    </>
  );
}

/* The settings page's way to save the ladders: one bar for both, and
 * the guard on leaving with edits. */
export function LadderSaveBar({ ladders }: { ladders: LadderDraft }) {
  const toast = useToast();
  const { dirty, saving, discard } = ladders;

  const save = async () => {
    try {
      await ladders.save();
      toast.add({ type: "approve", title: "Notification rules saved" });
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "Could not save your rules",
        description: reason instanceof Error ? reason.message : undefined,
      });
      throw reason;
    }
  };

  return (
    <>
      <SaveBar count={dirty.length} onDiscard={discard} onSave={() => void save().catch(() => undefined)} saving={saving} />
      <UnsavedGuard dirty={dirty.length > 0} saving={saving} onSave={save} onDiscard={discard} />
    </>
  );
}

/* A ladder inside a card: the title line with its Add rule, then the
 * rows in a canvas well, the platform's nesting rule for content inside
 * a card. Takes the SettingsSection props it shares so the two frames
 * are interchangeable above. */
function LadderWell({
  title,
  description,
  action,
  children,
}: {
  title: string;
  description: string;
  action: ReactNode;
  inset?: "rows" | "none";
  children: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h4 className="text-body font-semibold text-ink">{title}</h4>
          <p className="text-label font-normal text-ink-muted">{description}</p>
        </div>
        {action}
      </div>
      <div className="overflow-hidden rounded-lg border-hairline border-edge bg-canvas">
        {children}
      </div>
    </div>
  );
}

function addRung(rules: NotificationRule[]) {
  const last = rules[rules.length - 1];
  const next = DELAYS.find((delay) => delay.seconds > (last?.delay_seconds ?? -1)) ?? DELAYS[0];
  return [...rules, { delay_seconds: next.seconds, contact_method_id: null }];
}

function Ladder({
  urgency,
  rules,
  options,
  onChange,
}: {
  urgency: NotificationUrgency;
  rules: NotificationRule[];
  options: { id: string; label: string }[];
  onChange: (rules: NotificationRule[]) => void;
}) {
  const delayItems = DELAYS.map((delay) => String(delay.seconds));
  const methodItems = options.map((option) => option.id);
  const delayLabel = (item: string) =>
    DELAYS.find((delay) => String(delay.seconds) === item)?.label ?? item;
  const methodLabel = (item: string) =>
    options.find((option) => option.id === item)?.label ?? "Removed method";

  const replace = (index: number, patch: Partial<NotificationRule>) =>
    onChange(rules.map((rule, at) => (at === index ? { ...rule, ...patch } : rule)));
  const remove = (index: number) => onChange(rules.filter((_, at) => at !== index));

  return (
    <ul className="flex flex-col" aria-label={`${urgency} rules`}>
      {rules.map((rule, index) => (
        <li
          key={index}
          className="flex flex-wrap items-center gap-2 border-b-hairline border-edge px-4 py-2.5 last:border-b-0"
        >
          <Select
            items={delayItems}
            itemToStringLabel={(item) => delayLabel(item as string)}
            value={String(rule.delay_seconds)}
            onValueChange={(next) => replace(index, { delay_seconds: Number(next) })}
          >
            <SelectTrigger aria-label="When" className="w-40" />
            <SelectContent<string>>
              {(item) => (
                <SelectItem key={item} value={item}>
                  {delayLabel(item)}
                </SelectItem>
              )}
            </SelectContent>
          </Select>
          <span className="text-body text-ink-muted">via</span>
          <Select
            items={methodItems}
            itemToStringLabel={(item) => methodLabel(item as string)}
            value={rule.contact_method_id ?? ACCOUNT_EMAIL}
            onValueChange={(next) =>
              replace(index, {
                contact_method_id: (next as string) === ACCOUNT_EMAIL ? null : (next as string),
              })
            }
          >
            <SelectTrigger aria-label="Method" className="w-64" />
            <SelectContent<string>>
              {(item) => (
                <SelectItem key={item || "account"} value={item}>
                  {methodLabel(item)}
                </SelectItem>
              )}
            </SelectContent>
          </Select>
          <span className="ml-auto">
            <IconButton
              aria-label="Remove rule"
              variant="ghost"
              size="sm"
              disabled={rules.length === 1}
              onClick={() => remove(index)}
            >
              <TrashIcon />
            </IconButton>
          </span>
        </li>
      ))}
    </ul>
  );
}
