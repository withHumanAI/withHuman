import type { ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import type { ConditionNode } from "@/ds/components/condition-builder";
import {
  PathTree,
  type PathCondition,
  type PathTreeNode,
  type TeamPath,
} from "@/ds/components/escalation-tree";
import type { ReviewerTarget } from "@/ds/components/reviewer-target";
import { ArrowUpRightIcon } from "@/ds/icons";
import type {
  EscalationDocumentBodyWire,
  EscalationPathConditionWire,
  EscalationPathNodeWire,
  EscalationPathRevisionWire,
  EscalationTargetWire,
  Team,
} from "../types";
import type { TeamPaths } from "./team-policy-store";

/*
 * What handing a request to an escalation path means, shown next to the
 * picker and under an exit chip in the pipeline editor: the path's
 * active revision as a tree, or a word when it has none, with each team
 * target's escalation policy one click away. The trees are loaded once
 * per pipeline view.
 */

/* Active path trees by path key: a tree, null for a path with no active
 * revision, and absent while still loading. */
export type PathTrees = Record<string, TeamPath | null>;

function conditionFromWire(
  condition: EscalationPathConditionWire,
  setNames: Record<string, string>,
): PathCondition {
  switch (condition.type) {
    case "working_hours":
      return {
        kind: "working_hours",
        set: setNames[condition.working_hours_id ?? ""] ?? condition.working_hours_id ?? "",
        active: condition.active === true,
      };
    case "urgency":
      return { kind: "urgency", urgency: condition.urgency ?? "interrupt" };
    case "request":
      return {
        kind: "request",
        when: (condition.when ?? { id: "", all: [] }) as unknown as ConditionNode,
      };
  }
}

/* A wire target named from the directory; a broadcast is the one such
 * entry the directory holds, since only a team policy carries one. */
function targetFromWire(target: EscalationTargetWire, names: ReviewerTarget[]): ReviewerTarget {
  if (target.type === "broadcast") {
    return (
      names.find((entry) => entry.type === "broadcast") ?? {
        type: "broadcast",
        id: "",
        name: "Everyone on the team",
      }
    );
  }
  return (
    names.find((entry) => entry.type === target.type && entry.id === target.id) ?? {
      type: target.type,
      id: target.id,
      name: `${target.id.slice(0, 8)}…`,
    }
  );
}

function nodeFromWire(
  node: EscalationPathNodeWire,
  names: ReviewerTarget[],
  setNames: Record<string, string>,
): PathTreeNode {
  switch (node.type) {
    case "level":
      return {
        id: node.id,
        type: "level",
        targets: (node.targets ?? []).map((target) => targetFromWire(target, names)),
        escalateAfter: node.escalate_after ?? "",
        raiseUrgency: node.raise_urgency === true,
      };
    case "if_else":
      return {
        id: node.id,
        type: "if_else",
        conditions: (node.conditions ?? []).map((condition) => conditionFromWire(condition, setNames)),
        then: (node.then ?? []).map((child) => nodeFromWire(child, names, setNames)),
        else: (node.else ?? []).map((child) => nodeFromWire(child, names, setNames)),
      };
    case "defer":
      return {
        id: node.id,
        type: "defer",
        set: setNames[node.working_hours_id ?? ""] ?? node.working_hours_id ?? "",
        standardOnly: node.standard_only === true,
      };
  }
}

function setNamesOf(body: EscalationDocumentBodyWire): Record<string, string> {
  const setNames: Record<string, string> = {};
  for (const set of body.working_hours) setNames[set.id] = set.name;
  return setNames;
}

/* The design system's tree reads working-hours sets and targets by
 * name; the wire carries ids. names holds the teams and people. */
export function pathTreeFromWire(revision: EscalationPathRevisionWire, names: ReviewerTarget[]): TeamPath {
  const setNames = setNamesOf(revision);
  return {
    teamId: revision.path_key,
    teamName: revision.name,
    nodes: revision.nodes.map((node) => nodeFromWire(node, names, setNames)),
    repeat: revision.repeat,
  };
}

/* A team's escalation policy revision as the tree a path level opens
 * inline. A broadcast level names everyone on the team, keyed by the
 * team id so its chip sits beside the members' without a collision. */
export function teamPolicyTreeFromWire(
  team: Pick<Team, "id" | "name">,
  revision: EscalationDocumentBodyWire,
  names: ReviewerTarget[],
): TeamPath {
  const setNames = setNamesOf(revision);
  const everyone: ReviewerTarget = { type: "broadcast", id: team.id, name: `Everyone on ${team.name}` };
  const directory = [everyone, ...names.filter((entry) => entry.type !== "broadcast")];
  return {
    teamId: team.id,
    teamName: team.name,
    nodes: revision.nodes.map((node) => nodeFromWire(node, directory, setNames)),
    repeat: revision.repeat,
  };
}

function Well({ children }: { children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2 rounded-lg border-hairline border-edge bg-canvas px-3 py-2.5">
      {children}
    </div>
  );
}

function EditPathLink({ pathKey, label }: { pathKey: string; label: string }) {
  return (
    <Link
      to="/escalation-paths/$pathKey"
      params={{ pathKey }}
      target="_blank"
      rel="noopener noreferrer"
      className="flex items-center gap-1 text-label text-ink-muted transition-colors duration-100 hover:text-ink"
    >
      {label}
      <ArrowUpRightIcon width={12} height={12} />
    </Link>
  );
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

export function EscalationRoute({
  target,
  trees,
  /* The teams' active escalation policies, so a team chip in the tree
   * opens what the team does with the request. */
  teamPaths,
  /* What no path means here, e.g. the pipeline default it inherits. */
  fallback,
}: {
  target: ReviewerTarget | null;
  trees: PathTrees;
  teamPaths?: TeamPaths;
  fallback?: ReactNode;
}) {
  if (!target || (target.type !== "escalation_path" && target.type !== "agent_owner")) {
    return fallback ? (
      <Well>
        <p className="text-micro font-normal text-ink-muted">{fallback}</p>
      </Well>
    ) : null;
  }
  const tree = trees[target.id];
  if (tree === undefined) {
    return (
      <Well>
        <p className="text-micro font-normal text-ink-faint">Loading the escalation path</p>
      </Well>
    );
  }
  if (tree === null) {
    return (
      <Well>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p className="text-micro font-normal text-ink-muted">
            {target.name} has no active revision.
          </p>
          <EditPathLink pathKey={target.id} label="Open escalation path" />
        </div>
      </Well>
    );
  }
  return (
    <Well>
      {target.type === "agent_owner" ? (
        <p className="text-micro font-normal text-ink-muted">
          The agent's owner reviews. If there is no owner, this path is used:
        </p>
      ) : null}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-label text-ink-secondary">{tree.teamName}</span>
        <EditPathLink pathKey={target.id} label="Edit escalation path" />
      </div>
      <PathTree
        nodes={tree.nodes}
        repeat={tree.repeat}
        depth={1}
        teamPath={(teamId) => teamPaths?.[teamId] ?? undefined}
        teamAction={(path) => <EditPolicyLink path={path} />}
      />
    </Well>
  );
}
