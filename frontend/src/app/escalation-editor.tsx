import { useMemo } from "react";
import { Link } from "@tanstack/react-router";
import { Button } from "@/ds/components/button";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/ds/components/card";
import { FIXED_FIELDS } from "@/ds/components/condition-builder";
import { DurationField } from "@/ds/components/duration-field";
import {
  PathConditionEditor,
  PathLevelTargets,
  newPathCondition,
  type PathCondition,
  type TeamPath,
  type WorkingHoursOption,
} from "@/ds/components/escalation-tree";
import { IconButton } from "@/ds/components/icon-button";
import { Menu, MenuContent, MenuTrigger, type MenuAction } from "@/ds/components/menu";
import { type ReviewerTarget } from "@/ds/components/reviewer-target";
import {
  MultiSelectTrigger,
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
} from "@/ds/components/select";
import { Switch } from "@/ds/components/switch";
import { TextField } from "@/ds/components/text-field";
import { TimeField } from "@/ds/components/time-field";
import { Timeline, TimelineItem } from "@/ds/components/timeline";
import { Tooltip } from "@/ds/components/tooltip";
import { WeekdayPicker } from "@/ds/components/weekday-picker";
import {
  AlertIcon,
  ArrowUpRightIcon,
  BroadcastIcon,
  ChevronDownIcon,
  ChevronUpIcon,
  ClockIcon,
  PeopleIcon,
  PersonIcon,
  PlusIcon,
  TrashIcon,
} from "@/ds/icons";
import {
  NODE_META,
  newNode,
  newWorkingHoursSet,
  type EscalationDocument,
  type PathNode,
  type WorkingHoursSet,
} from "./escalation-document";
import type { TeamPaths } from "./team-policy-store";

/*
 * The escalation document editor: the Working hours, Steps, and Repeat
 * cards an organization escalation path and a team escalation policy
 * share. The vocabulary says which it is editing, which decides the
 * level picker's groups and a handful of sentences; everything else is
 * the same grammar.
 */

export interface EscalationVocabulary {
  kind: "organization" | "team";
  /* What a level may notify: teams and people for a path, the one
   * broadcast entry and the team's members for a policy. */
  targets: ReviewerTarget[];
}

export interface EscalationDocumentEditorProps {
  value: EscalationDocument;
  onChange: (next: EscalationDocument) => void;
  vocabulary: EscalationVocabulary;
  /* The active escalation policies of the organization's teams, keyed
   * by team id, so a path level that names a team shows what the team
   * does with the request. Path editing only. */
  teamPaths?: TeamPaths;
}

const TIMEZONES = [
  "Europe/London",
  "Europe/Berlin",
  "America/New_York",
  "America/Los_Angeles",
  "Asia/Tokyo",
  "UTC",
];

/* Field paths a branch condition may test: the fixed fields a pipeline
 * condition offers, since both run through the same validator. The
 * request builder accepts any path under arguments. */
const SUGGESTIONS = [...FIXED_FIELDS.map((field) => field.path), "/request/arguments/amount_cents"];

/* The sentences that differ between the two documents. */
const COPY = {
  organization: {
    stepsDescription:
      "Choose who gets notified, and in what order.",
    stepsEmpty:
      "No steps yet. Add at least one before saving.",
    notifyPlaceholder: "Teams or people to notify",
    repeatDescription:
      "When the last step finishes without a decision, run the path again.",
    groups: [
      { value: "Teams", type: "team" as const },
      { value: "People", type: "user" as const },
    ],
  },
  team: {
    stepsDescription:
      "Choose who on the team gets notified, and in what order.",
    stepsEmpty:
      "No steps yet. Without steps, everyone on the team is notified at once.",
    notifyPlaceholder: "Everyone or members to notify",
    repeatDescription:
      "When the last step finishes without a decision, run the policy again.",
    groups: [
      { value: "Team", type: "broadcast" as const },
      { value: "Members", type: "user" as const },
    ],
  },
};

interface TargetGroup {
  value: string;
  items: ReviewerTarget[];
}

function TargetIcon({ type }: { type: ReviewerTarget["type"] }) {
  const className = "text-ink-muted";
  if (type === "team") return <PeopleIcon width={13} height={13} className={className} />;
  if (type === "broadcast") return <BroadcastIcon width={13} height={13} className={className} />;
  return <PersonIcon width={13} height={13} className={className} />;
}

function AddNodeMenu({
  onAdd,
  sets,
  compact = false,
}: {
  onAdd: (node: PathNode) => void;
  sets: WorkingHoursOption[];
  compact?: boolean;
}) {
  const actions = useMemo<MenuAction[]>(
    () =>
      (
        [
          ["level", "Notify people or teams and wait for a decision", "notify-steps"],
          ["if_else", "Take different steps for different requests", "conditions"],
          ["defer", "Hold the request until working hours", "working-hours-and-deferral"],
        ] as const
      ).map(([type, info, anchor]) => ({
        id: type,
        label: NODE_META[type].label,
        info,
        docsLinks: [{ href: `https://withhuman.ai/docs/web-app/escalation-paths#${anchor}`, label: `${NODE_META[type].label} guide` }],
        action: () => onAdd(newNode(type, sets)),
      })),
    [onAdd, sets],
  );
  return (
    <Menu actions={actions}>
      <MenuTrigger
        render={
          <Button variant={compact ? "ghost" : "neutral"} size="sm">
            <PlusIcon width={13} height={13} />
            Add step
          </Button>
        }
      />
      <MenuContent />
    </Menu>
  );
}

function NodeFrame({
  title,
  summary,
  onRemove,
  onMove,
  canMoveUp,
  canMoveDown,
  children,
}: {
  title: string;
  summary?: string;
  onRemove: () => void;
  onMove: (delta: -1 | 1) => void;
  canMoveUp: boolean;
  canMoveDown: boolean;
  children: React.ReactNode;
}) {
  /* Recessed well, not a card: nested containers alternate surface
   * levels instead of stacking identical cards. */
  return (
    <div className="rounded-lg border-hairline border-edge bg-canvas">
      <div className="flex items-center gap-2 border-b-hairline border-edge py-1.5 pr-1.5 pl-3">
        <span className="text-label text-ink-secondary">{title}</span>
        {summary ? (
          <span className="truncate text-micro font-normal text-ink-faint">{summary}</span>
        ) : null}
        <div className="ml-auto flex items-center">
          <Tooltip content="Move up">
            <IconButton aria-label="Move up" size="sm" disabled={!canMoveUp} onClick={() => onMove(-1)}>
              <ChevronUpIcon width={13} height={13} />
            </IconButton>
          </Tooltip>
          <Tooltip content="Move down">
            <IconButton aria-label="Move down" size="sm" disabled={!canMoveDown} onClick={() => onMove(1)}>
              <ChevronDownIcon width={13} height={13} />
            </IconButton>
          </Tooltip>
          <Tooltip content="Remove step">
            <IconButton aria-label="Remove step" size="sm" onClick={onRemove}>
              <TrashIcon width={13} height={13} />
            </IconButton>
          </Tooltip>
        </div>
      </div>
      <div className="flex flex-col gap-3 p-3">{children}</div>
    </div>
  );
}

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
      <span className="w-28 shrink-0 text-label text-ink-muted">{label}</span>
      {children}
    </div>
  );
}

function WorkingHoursSelect({
  sets,
  value,
  onChange,
  "aria-label": ariaLabel,
}: {
  sets: WorkingHoursOption[];
  value: string;
  onChange: (id: string) => void;
  "aria-label": string;
}) {
  const selected = sets.find((set) => set.id === value) ?? null;
  return (
    <Select
      items={sets}
      itemToStringLabel={(item) => (item as WorkingHoursOption).name}
      value={selected}
      onValueChange={(next) => {
        if (next) onChange((next as WorkingHoursOption).id);
      }}
    >
      <SelectTrigger aria-label={ariaLabel} className="w-44" placeholder="Working hours" />
      <SelectContent<WorkingHoursOption>>
        {(item) => (
          <SelectItem key={item.id} value={item}>
            {item.name}
          </SelectItem>
        )}
      </SelectContent>
    </Select>
  );
}

function ConditionList({
  conditions,
  onChange,
  sets,
}: {
  conditions: PathCondition[];
  onChange: (next: PathCondition[]) => void;
  sets: WorkingHoursOption[];
}) {
  return (
    <div className="flex flex-col gap-2">
      {conditions.map((condition, index) => (
        <div
          key={index}
          className="flex items-start gap-2 rounded-md border-hairline border-edge bg-surface p-2.5"
        >
          <span className="mt-1.5 w-10 shrink-0 font-mono text-[10px] tracking-wide text-ink-faint uppercase">
            {index === 0 ? "if" : "and"}
          </span>
          <div className="min-w-0 flex-1">
            <PathConditionEditor
              value={condition}
              onChange={(next) => onChange(conditions.map((other, i) => (i === index ? next : other)))}
              workingHours={sets}
              suggestions={SUGGESTIONS}
            />
          </div>
          <Tooltip content="Remove condition">
            <IconButton
              aria-label="Remove condition"
              size="sm"
              disabled={conditions.length === 1}
              onClick={() => onChange(conditions.filter((_, i) => i !== index))}
            >
              <TrashIcon width={13} height={13} />
            </IconButton>
          </Tooltip>
        </div>
      ))}
      <div>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => onChange([...conditions, newPathCondition("request", sets)])}
        >
          <PlusIcon width={13} height={13} />
          Add condition
        </Button>
      </div>
    </div>
  );
}

/* Everything the node editors share, threaded once. */
interface EditorContext {
  sets: WorkingHoursOption[];
  vocabulary: EscalationVocabulary;
  groups: TargetGroup[];
  teamPaths?: TeamPaths;
}

function EditPolicyLink({ path }: { path: TeamPath }) {
  return (
    <Link
      to="/settings/teams/$teamId/escalation-policy"
      params={{ teamId: path.teamId }}
      target="_blank"
      rel="noopener noreferrer"
      className="flex items-center gap-1 text-label text-ink-muted transition-colors duration-100 hover:text-ink"
    >
      Edit policy
      <ArrowUpRightIcon width={12} height={12} />
    </Link>
  );
}

function LevelEditor({
  node,
  onChange,
  context,
}: {
  node: Extract<PathNode, { type: "level" }>;
  onChange: (next: PathNode) => void;
  context: EditorContext;
}) {
  const { vocabulary, groups, teamPaths } = context;
  const copy = COPY[vocabulary.kind];
  /* Selection matches by identity, so loaded targets resolve to
   * vocabulary entries when present. */
  const value = node.targets.map(
    (target) =>
      vocabulary.targets.find((entry) => entry.type === target.type && entry.id === target.id) ?? target,
  );
  const teamTargets = node.targets.filter((target) => target.type === "team");
  return (
    <>
      <FieldRow label="Notify">
        <Select
          multiple
          items={groups}
          itemToStringLabel={(item) => (item as ReviewerTarget).name}
          value={value}
          onValueChange={(next) => onChange({ ...node, targets: next as ReviewerTarget[] })}
        >
          <MultiSelectTrigger<ReviewerTarget>
            placeholder={copy.notifyPlaceholder}
            itemLabel={(item) => item.name}
            className="w-64"
          />
          <SelectContent<TargetGroup>>
            {(group) => (
              <SelectGroup<ReviewerTarget> key={group.value} items={group.items} label={group.value}>
                {(item) => (
                  <SelectItem key={`${item.type}:${item.id}`} value={item}>
                    <span className="flex items-center gap-2">
                      <TargetIcon type={item.type} />
                      {item.name}
                    </span>
                  </SelectItem>
                )}
              </SelectGroup>
            )}
          </SelectContent>
        </Select>
      </FieldRow>
      {teamPaths && teamTargets.length > 0 ? (
        <PathLevelTargets
          label="Team policies"
          targets={teamTargets}
          teamPath={(teamId) => teamPaths[teamId] ?? undefined}
          teamAction={(path) => <EditPolicyLink path={path} />}
        />
      ) : null}
      <FieldRow label="Escalate after">
        <DurationField
          aria-label="Escalate after"
          value={node.escalateAfterSeconds}
          onChange={(escalateAfterSeconds) => onChange({ ...node, escalateAfterSeconds })}
        />
      </FieldRow>
      <FieldRow label="Urgency">
        <Switch
          label="Raise to interrupt"
          checked={node.raiseUrgency}
          onCheckedChange={(raiseUrgency) => onChange({ ...node, raiseUrgency })}
        />
      </FieldRow>
    </>
  );
}

function NodeEditor({
  node,
  onChange,
  context,
}: {
  node: PathNode;
  onChange: (next: PathNode) => void;
  context: EditorContext;
}) {
  const { sets } = context;
  switch (node.type) {
    case "level":
      return <LevelEditor node={node} onChange={onChange} context={context} />;
    case "defer":
      return (
        <>
          <FieldRow label="Hold until">
            <WorkingHoursSelect
              aria-label="Defer working hours"
              sets={sets}
              value={node.workingHoursId}
              onChange={(workingHoursId) => onChange({ ...node, workingHoursId })}
            />
          </FieldRow>
          <FieldRow label="Applies to">
            <Switch
              label="Standard urgency only"
              checked={node.standardOnly}
              onCheckedChange={(standardOnly) => onChange({ ...node, standardOnly })}
            />
          </FieldRow>
        </>
      );
    case "if_else":
      return (
        <>
          <ConditionList
            conditions={node.conditions}
            onChange={(conditions) => onChange({ ...node, conditions })}
            sets={sets}
          />
          <Branch
            label="Then"
            nodes={node.thenNodes}
            onChange={(thenNodes) => onChange({ ...node, thenNodes })}
            context={context}
          />
          <Branch
            label="Otherwise"
            nodes={node.elseNodes}
            onChange={(elseNodes) => onChange({ ...node, elseNodes })}
            context={context}
          />
        </>
      );
  }
}

function Branch({
  label,
  nodes,
  onChange,
  context,
}: {
  label: string;
  nodes: PathNode[];
  onChange: (next: PathNode[]) => void;
  context: EditorContext;
}) {
  return (
    <div className="flex flex-col gap-2 rounded-md border-hairline border-edge bg-surface p-2.5">
      <div className="flex items-center justify-between gap-2">
        <span className="text-label text-ink-secondary">{label}</span>
        <AddNodeMenu compact sets={context.sets} onAdd={(node) => onChange([...nodes, node])} />
      </div>
      {nodes.length === 0 ? (
        <p className="text-micro font-normal text-ink-faint">
          No steps yet.
        </p>
      ) : (
        <NodeList nodes={nodes} onChange={onChange} context={context} />
      )}
    </div>
  );
}

function NodeList({
  nodes,
  onChange,
  context,
}: {
  nodes: PathNode[];
  onChange: (next: PathNode[]) => void;
  context: EditorContext;
}) {
  function move(index: number, delta: -1 | 1) {
    const next = [...nodes];
    const [node] = next.splice(index, 1);
    next.splice(index + delta, 0, node);
    onChange(next);
  }
  return (
    <Timeline>
      {nodes.map((node, index) => {
        const meta = NODE_META[node.type];
        return (
          <TimelineItem
            key={node.id}
            tone={node.type === "level" && node.raiseUrgency ? "warn" : meta.tone}
            marker={
              node.type === "defer" ? (
                <ClockIcon width={14} height={14} />
              ) : node.type === "level" ? (
                node.raiseUrgency ? (
                  <AlertIcon width={14} height={14} />
                ) : (
                  <PersonIcon width={14} height={14} />
                )
              ) : undefined
            }
          >
            <NodeFrame
              title={meta.label}
              summary={
                node.type === "level"
                  ? node.targets.map((target) => target.name).join(", ") || "no targets yet"
                  : node.type === "if_else"
                    ? `${node.conditions.length} condition${node.conditions.length === 1 ? "" : "s"}`
                    : undefined
              }
              onRemove={() => onChange(nodes.filter((other) => other.id !== node.id))}
              onMove={(delta) => move(index, delta)}
              canMoveUp={index > 0}
              canMoveDown={index < nodes.length - 1}
            >
              <NodeEditor
                node={node}
                context={context}
                onChange={(next) =>
                  onChange(nodes.map((other) => (other.id === next.id ? next : other)))
                }
              />
            </NodeFrame>
          </TimelineItem>
        );
      })}
    </Timeline>
  );
}

function WorkingHoursEditor({
  sets,
  onChange,
}: {
  sets: WorkingHoursSet[];
  onChange: (next: WorkingHoursSet[]) => void;
}) {
  function update(next: WorkingHoursSet) {
    onChange(sets.map((set) => (set.id === next.id ? next : set)));
  }
  return (
    <div className="flex flex-col gap-3">
      {sets.map((set) => (
        <div
          key={set.id}
          className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-md border-hairline border-edge bg-canvas p-2.5"
        >
          <TextField
            aria-label="Working hours name"
            value={set.name}
            onChange={(event) => update({ ...set, name: event.target.value })}
            className="w-40"
          />
          <WeekdayPicker
            aria-label={`${set.name} days`}
            value={set.weekdays}
            onChange={(weekdays) => update({ ...set, weekdays })}
          />
          <div className="flex items-center gap-2">
            <TimeField
              aria-label={`${set.name} start`}
              value={set.start}
              onChange={(start) => update({ ...set, start })}
            />
            <span className="text-label text-ink-muted">to</span>
            <TimeField
              aria-label={`${set.name} end`}
              value={set.end}
              onChange={(end) => update({ ...set, end })}
            />
          </div>
          <Select
            items={TIMEZONES}
            value={set.timezone}
            onValueChange={(timezone) => {
              if (timezone) update({ ...set, timezone: timezone as string });
            }}
          >
            <SelectTrigger aria-label={`${set.name} timezone`} className="w-48" />
            <SelectContent<string>>
              {(item) => (
                <SelectItem key={item} value={item}>
                  {item}
                </SelectItem>
              )}
            </SelectContent>
          </Select>
          <Tooltip content="Remove working hours">
            <IconButton
              aria-label="Remove working hours"
              size="sm"
              disabled={sets.length === 1}
              onClick={() => onChange(sets.filter((other) => other.id !== set.id))}
            >
              <TrashIcon width={13} height={13} />
            </IconButton>
          </Tooltip>
        </div>
      ))}
      <div>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => onChange([...sets, newWorkingHoursSet("New working hours")])}
        >
          <PlusIcon width={13} height={13} />
          Add working hours
        </Button>
      </div>
    </div>
  );
}

export function EscalationDocumentEditor({
  value,
  onChange,
  vocabulary,
  teamPaths,
}: EscalationDocumentEditorProps) {
  const copy = COPY[vocabulary.kind];
  const sets = useMemo<WorkingHoursOption[]>(
    () => value.workingHours.map((set) => ({ id: set.id, name: set.name })),
    [value.workingHours],
  );
  /* The picker's groups hold the vocabulary's own entries, so a chosen
   * item is identical to a listed one. */
  const groups = useMemo<TargetGroup[]>(
    () =>
      copy.groups.map((group) => ({
        value: group.value,
        items: vocabulary.targets.filter((target) => target.type === group.type),
      })),
    [copy, vocabulary.targets],
  );
  const context: EditorContext = { sets, vocabulary, groups, teamPaths };

  return (
    <>
      <Card>
        <CardHeader>
          <div>
            <CardTitle>Working hours</CardTitle>
            <CardDescription>Time windows you can use in your steps.</CardDescription>
          </div>
        </CardHeader>
        <CardBody>
          <WorkingHoursEditor
            sets={value.workingHours}
            onChange={(workingHours) => onChange({ ...value, workingHours })}
          />
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>Steps</CardTitle>
            <CardDescription>{copy.stepsDescription}</CardDescription>
          </div>
          <AddNodeMenu sets={sets} onAdd={(node) => onChange({ ...value, nodes: [...value.nodes, node] })} />
        </CardHeader>
        <CardBody>
          {value.nodes.length === 0 ? (
            <p className="text-body text-ink-muted">{copy.stepsEmpty}</p>
          ) : (
            <NodeList
              nodes={value.nodes}
              onChange={(nodes) => onChange({ ...value, nodes })}
              context={context}
            />
          )}
        </CardBody>
      </Card>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>Repeat</CardTitle>
            <CardDescription>{copy.repeatDescription}</CardDescription>
          </div>
          <Switch
            label="Repeat"
            checked={value.repeat.enabled}
            onCheckedChange={(enabled) => onChange({ ...value, repeat: { ...value.repeat, enabled } })}
          />
        </CardHeader>
        {value.repeat.enabled ? (
          <CardBody className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <span className="text-label text-ink-muted">Up to</span>
            <TextField
              aria-label="Repeat times"
              inputMode="numeric"
              value={String(value.repeat.times)}
              onChange={(event) => {
                const times = Number.parseInt(event.target.value.replace(/[^0-9]/g, ""), 10);
                onChange({
                  ...value,
                  repeat: { ...value.repeat, times: Number.isFinite(times) ? times : 0 },
                });
              }}
              className="w-14 text-right"
            />
            <span className="text-label text-ink-muted">more times, waiting</span>
            <DurationField
              aria-label="Repeat after"
              value={value.repeat.afterSeconds}
              onChange={(afterSeconds) => onChange({ ...value, repeat: { ...value.repeat, afterSeconds } })}
            />
            <span className="text-label text-ink-muted">between runs</span>
          </CardBody>
        ) : null}
      </Card>
    </>
  );
}
