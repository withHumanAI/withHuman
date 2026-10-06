import { useState, type ReactNode } from "react";
import { cn } from "@/ds/lib/cn";
import { wellTone } from "@/ds/lib/well";
import {
  ConditionBuilder,
  newConditionLeaf,
  summarizeCondition,
  type ConditionNode,
} from "./condition-builder";
import { Badge } from "./badge";
import { TargetBadge, type ReviewerTarget } from "./reviewer-target";
import { Select, SelectContent, SelectItem, SelectTrigger } from "./select";
import { Timeline, TimelineItem } from "./timeline";
import { ToggleGroup, ToggleGroupItem } from "./toggle-group";
import { AlertIcon, ClockIcon, PersonIcon } from "@/ds/icons";

/*
 * An escalation document read as a tree: an organization escalation
 * path, or a team's escalation policy. Steps run in order; a level
 * notifies targets and waits; a branch tests the request, the clock, or
 * the urgency. A team target on a path may carry its own escalation
 * policy, which is walked inline while the level that named it keeps
 * its timer running; a team without one is notified all at once. The
 * same team is never walked twice on one path. Inside a team policy a
 * level names members of the team or everyone on it (broadcast).
 *
 * Working-hours sets are referenced by name here: the tree is a design
 * primitive, and the editor that owns the sets resolves ids to names at
 * its boundary.
 */

export type PathUrgency = "standard" | "interrupt";

export type PathCondition =
  | { kind: "working_hours"; set: string; active: boolean }
  | { kind: "urgency"; urgency: PathUrgency }
  | { kind: "request"; when: ConditionNode };

export type PathTreeNode =
  | {
      id: string;
      type: "level";
      targets: ReviewerTarget[];
      /* Go duration, "15m". */
      escalateAfter: string;
      raiseUrgency?: boolean;
    }
  | {
      id: string;
      type: "if_else";
      /* All must hold. */
      conditions: PathCondition[];
      then: PathTreeNode[];
      else: PathTreeNode[];
    }
  | { id: string; type: "defer"; set: string; standardOnly?: boolean };

/* One document with the name of what owns it: a team (its escalation
 * policy) or, for the organization tree, the path itself. */
export interface TeamPath {
  teamId: string;
  teamName: string;
  nodes: PathTreeNode[];
  repeat?: { times: number; after: string };
}

/* One condition as the middle of a sentence: "during UK office hours",
 * "urgency is interrupt", or the request condition in machine mono. */
export function describeCondition(condition: PathCondition): ReactNode {
  switch (condition.kind) {
    case "working_hours":
      return `${condition.active ? "during" : "outside"} ${condition.set}`;
    case "urgency":
      return `urgency is ${condition.urgency}`;
    case "request":
      return (
        <span className="font-mono text-[11px] text-ink-secondary">
          {summarizeCondition(condition.when)}
        </span>
      );
  }
}

export interface PathTreeProps {
  nodes: PathTreeNode[];
  repeat?: TeamPath["repeat"];
  /* Resolves a team target to its escalation policy so it can open
   * inline. Returning undefined means the team has no active policy:
   * everyone on it is simply notified. */
  teamPath?: (teamId: string) => TeamPath | undefined;
  /* Rendered at the right of an opened policy's header, for example a
   * link to edit it. */
  teamAction?: (path: TeamPath) => ReactNode;
  /* Teams already being walked above this subtree, the owning team
   * included. A team met again is notified but not walked again. */
  ancestors?: string[];
  depth?: number;
  className?: string;
}

const MONO_LABEL = "font-mono text-[10px] tracking-wide text-ink-faint uppercase";

/* Nested containers alternate surface levels rather than stacking
 * identical cards; depth decides which one this well sits on. */
function wellClass(depth: number): string {
  return cn(
    "flex flex-col gap-2 rounded-lg border-hairline border-edge px-3 py-2.5",
    wellTone(depth),
  );
}

export interface PathLevelTargetsProps {
  targets: ReviewerTarget[];
  teamPath?: (teamId: string) => TeamPath | undefined;
  teamAction?: (path: TeamPath) => ReactNode;
  /* Teams already being walked above this level, the owning team
   * included. */
  ancestors?: string[];
  depth?: number;
  /* The word before the chips. */
  label?: ReactNode;
}

/* A level's targets, with at most one team's escalation policy opened
 * beneath the whole row: the well spans the level, never sits between
 * two chips. The path editor reuses it under a level's picker so the
 * policy a team target carries is one click away. */
export function PathLevelTargets({
  targets,
  teamPath,
  teamAction,
  ancestors = [],
  depth = 0,
  label = "Notify",
}: PathLevelTargetsProps) {
  const [openTeam, setOpenTeam] = useState<string | null>(null);
  const open = targets.find((target) => target.type === "team" && target.id === openTeam);
  const walking = open ? ancestors.includes(open.id) : false;
  const path = open && !walking ? teamPath?.(open.id) : undefined;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-x-1 gap-y-1">
        {label ? <span className="text-body text-ink-secondary">{label}</span> : null}
        {targets.length === 0 ? (
          <span className="text-body text-ink-faint">nobody yet</span>
        ) : (
          targets.map((target) =>
            target.type === "team" && teamPath ? (
              <TargetBadge
                key={`${target.type}:${target.id}`}
                target={target}
                onClick={() => setOpenTeam(openTeam === target.id ? null : target.id)}
                expanded={openTeam === target.id}
              />
            ) : (
              <TargetBadge key={`${target.type}:${target.id}`} target={target} />
            ),
          )
        )}
      </div>
      {open ? (
        <div className={wellClass(depth + 1)}>
          {walking ? (
            <p className="text-micro font-normal text-ink-muted">
              Everyone on {open.name} is notified here. Its policy already runs above.
            </p>
          ) : path ? (
            <>
              <div className="flex flex-wrap items-baseline gap-2">
                <span className="text-label text-ink-secondary">
                  {path.teamName}: escalation policy
                </span>
                {teamAction ? <span className="ml-auto">{teamAction(path)}</span> : null}
              </div>
              <PathTree
                nodes={path.nodes}
                repeat={path.repeat}
                teamPath={teamPath}
                teamAction={teamAction}
                ancestors={[...ancestors, open.id]}
                depth={depth + 1}
              />
            </>
          ) : (
            <p className="text-micro font-normal text-ink-muted">
              Everyone on {open.name} is notified at once.
            </p>
          )}
        </div>
      ) : null}
    </div>
  );
}

function BranchWell({
  label,
  nodes,
  depth,
  teamPath,
  teamAction,
  ancestors,
}: {
  label: string;
  nodes: PathTreeNode[];
  depth: number;
  teamPath?: (teamId: string) => TeamPath | undefined;
  teamAction?: (path: TeamPath) => ReactNode;
  ancestors: string[];
}) {
  return (
    <div className={wellClass(depth + 1)}>
      <span className={MONO_LABEL}>{label}</span>
      {nodes.length === 0 ? (
        <p className="text-micro font-normal text-ink-faint">
          Continues to the next step.
        </p>
      ) : (
        <PathTree
          nodes={nodes}
          depth={depth + 1}
          teamPath={teamPath}
          teamAction={teamAction}
          ancestors={ancestors}
        />
      )}
    </div>
  );
}

export function PathTree({
  nodes,
  repeat,
  teamPath,
  teamAction,
  ancestors = [],
  depth = 0,
  className,
}: PathTreeProps) {
  return (
    <Timeline className={className}>
      {nodes.map((node) => {
        switch (node.type) {
          case "level":
            return (
              <TimelineItem
                key={node.id}
                tone={node.raiseUrgency ? "warn" : "accent"}
                marker={
                  node.raiseUrgency ? (
                    <AlertIcon width={14} height={14} />
                  ) : (
                    <PersonIcon width={14} height={14} />
                  )
                }
              >
                <div className="flex flex-col gap-1">
                  <PathLevelTargets
                    targets={node.targets}
                    teamPath={teamPath}
                    teamAction={teamAction}
                    ancestors={ancestors}
                    depth={depth}
                  />
                  {/* What happens when nobody decides, as the sentence the
                      editor's fields spell out, with the values as badges. */}
                  {/* No gap between words and badges: a badge's own inset
                      is the space. */}
                  <p className="flex flex-wrap items-center gap-y-1 text-body text-ink-secondary">
                    Escalate after
                    <Badge className="gap-1" icon={<ClockIcon width={13} height={13} />}>
                      {node.escalateAfter}
                    </Badge>
                    {node.raiseUrgency ? (
                      <>
                        {/* The comma belongs to the badge before it, not
                            to the gap. */}
                        <span className="-ml-1.5">, raise to</span>
                        <Badge className="gap-1" tone="warn" icon={<AlertIcon width={13} height={13} />}>
                          Interrupt
                        </Badge>
                      </>
                    ) : null}
                  </p>
                </div>
              </TimelineItem>
            );
          case "if_else":
            return (
              <TimelineItem key={node.id}>
                <div className="flex flex-col gap-2">
                  <p className="text-body text-ink-secondary">
                    If{" "}
                    {node.conditions.map((condition, index) => (
                      <span key={index}>
                        {index > 0 ? " and " : ""}
                        {describeCondition(condition)}
                      </span>
                    ))}
                  </p>
                  <BranchWell
                    label="then"
                    nodes={node.then}
                    depth={depth}
                    teamPath={teamPath}
                    teamAction={teamAction}
                    ancestors={ancestors}
                  />
                  <BranchWell
                    label="otherwise"
                    nodes={node.else}
                    depth={depth}
                    teamPath={teamPath}
                    teamAction={teamAction}
                    ancestors={ancestors}
                  />
                </div>
              </TimelineItem>
            );
          case "defer":
            return (
              <TimelineItem key={node.id} marker={<ClockIcon width={14} height={14} />}>
                <p className="text-body text-ink-secondary">Hold until {node.set} opens</p>
                {node.standardOnly ? (
                  <p className="font-mono text-[11px] text-ink-muted">standard urgency only</p>
                ) : null}
              </TimelineItem>
            );
        }
      })}
      {repeat ? (
        <TimelineItem>
          {/* A 16px line centres 2px above the 20px marker box that the
              body-sized rows centre on. */}
          <p className="pt-0.5 text-micro font-normal text-ink-muted">
            Then repeat up to {repeat.times} more {repeat.times === 1 ? "time" : "times"}, waiting{" "}
            {repeat.after} between runs.
          </p>
        </TimelineItem>
      ) : null}
    </Timeline>
  );
}

/* ---------------------------------------------------------------- */
/* Condition editing                                                */
/* ---------------------------------------------------------------- */

const KINDS: { value: PathCondition["kind"]; label: string }[] = [
  { value: "request", label: "Request" },
  { value: "working_hours", label: "Working hours" },
  { value: "urgency", label: "Urgency" },
];

export function newPathCondition(kind: PathCondition["kind"], sets: WorkingHoursOption[]): PathCondition {
  switch (kind) {
    case "request":
      return { kind, when: { id: crypto.randomUUID(), all: [newConditionLeaf()] } };
    case "working_hours":
      return { kind, set: sets[0]?.id ?? "", active: true };
    case "urgency":
      return { kind, urgency: "interrupt" };
  }
}

/* A working-hours set as the editor offers it: the id the document
 * stores and the name the person reads. */
export interface WorkingHoursOption {
  id: string;
  name: string;
}

export interface PathConditionEditorProps {
  value: PathCondition;
  onChange: (next: PathCondition) => void;
  /* Working-hours sets the path defines; a condition stores the id. */
  workingHours: WorkingHoursOption[];
  /* Field paths for the request condition builder. */
  suggestions?: string[];
}

/*
 * One branch condition. The kind picker comes first because it decides
 * the rest of the row: a request condition is the same builder the
 * pipeline blocks use, so "which human" and "whether a human" are
 * written in one language.
 */
export function PathConditionEditor({
  value,
  onChange,
  workingHours,
  suggestions,
}: PathConditionEditorProps) {
  return (
    <div className="flex flex-col gap-2.5">
      <ToggleGroup
        aria-label="Condition kind"
        value={value.kind}
        onValueChange={(kind) => {
          if (kind !== value.kind) onChange(newPathCondition(kind as PathCondition["kind"], workingHours));
        }}
        className="self-start"
      >
        {KINDS.map((kind) => (
          <ToggleGroupItem key={kind.value} value={kind.value} className="px-2">
            {kind.label}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>

      {value.kind === "request" ? (
        <ConditionBuilder
          value={value.when}
          onChange={(when) => onChange({ kind: "request", when })}
          suggestions={suggestions}
        />
      ) : value.kind === "working_hours" ? (
        <div className="flex flex-wrap items-center gap-2">
          <ToggleGroup
            aria-label="Inside or outside the window"
            value={value.active ? "during" : "outside"}
            onValueChange={(next) => onChange({ ...value, active: next === "during" })}
          >
            <ToggleGroupItem value="during" className="px-2">
              During
            </ToggleGroupItem>
            <ToggleGroupItem value="outside" className="px-2">
              Outside
            </ToggleGroupItem>
          </ToggleGroup>
          <Select
            items={workingHours}
            itemToStringLabel={(item) => (item as WorkingHoursOption).name}
            value={workingHours.find((set) => set.id === value.set) ?? null}
            onValueChange={(next) => {
              if (next) onChange({ ...value, set: (next as WorkingHoursOption).id });
            }}
          >
            <SelectTrigger aria-label="Working hours" className="w-44" placeholder="Working hours" />
            <SelectContent<WorkingHoursOption>>
              {(item) => (
                <SelectItem key={item.id} value={item}>
                  {item.name}
                </SelectItem>
              )}
            </SelectContent>
          </Select>
        </div>
      ) : (
        <ToggleGroup
          aria-label="Urgency"
          value={value.urgency}
          onValueChange={(urgency) => onChange({ kind: "urgency", urgency: urgency as PathUrgency })}
          className="self-start"
        >
          <ToggleGroupItem value="standard" className="px-2">
            Standard
          </ToggleGroupItem>
          <ToggleGroupItem value="interrupt" className="px-2">
            <AlertIcon width={13} height={13} />
            Interrupt
          </ToggleGroupItem>
        </ToggleGroup>
      )}
    </div>
  );
}
