import { useCallback, type ReactNode } from "react";
import { Link, type LinkProps } from "@tanstack/react-router";
import { Avatar } from "@/ds/components/avatar";
import { Badge, type Tone } from "@/ds/components/badge";
import { ConditionSummary } from "@/ds/components/condition-builder";
import {
  HoverCardBody,
  HoverCardFact,
  HoverCardFacts,
  HoverCardMeta,
  HoverCardNote,
  HoverCardTitle,
} from "@/ds/components/hover-card";
import { PathTree } from "@/ds/components/escalation-tree";
import { type PolicyBlockDraft } from "@/ds/components/policy-block";
import { Timeline, TimelineItem } from "@/ds/components/timeline";
import { summarizeWebhook } from "@/ds/components/webhook-block";
import { Referenced, ReferenceCardsProvider, type Reference } from "@/ds/components/reference";
import { TargetBadge, type ReviewerTarget } from "@/ds/components/reviewer-target";
import { ArrowUpRightIcon } from "@/ds/icons";
import { api } from "../api";
import type {
  AgentDetail,
  ApprovalRequest,
  ApprovalRouting,
  EscalationPathRevisionWire,
  EscalationPathSummary,
  Member,
  MembershipStatus,
  RequestApprovalPipelineRevision,
  Team,
  WebhookEndpointSummary,
} from "../types";
import { cn } from "@/ds/lib/cn";
import { CONNECTION_LABEL, CONNECTION_TONE, detailConnection } from "./agent-state";
import { STATUS_LABEL, STATUS_TONE } from "./approval-status";
import { useAccess } from "./authorization";
import { auditLinkProps, blockKind, extensions, requireBlockKind, useAuditLinks } from "./extensions";
import { formatRelative, formatRemaining, shortId } from "./format";
import { blockTypeOf, draftsFromBlocks, pathTarget, type BlockDraft } from "./pipeline-blocks";
import { useSession } from "./principal";
import { ReferenceChip } from "./reference-chip";
import { pipelineLink } from "./pipeline-link";
import { blockErrorLabel } from "./block-errors";
import { useShared, useSharedIf, type Loaded } from "./reference-store";
import { pathTreeFromWire, teamPolicyTreeFromWire } from "./reviewer-route";

/*
 * What a mention of one of the platform's things says on hover. The
 * design system's Referenced wrapper (and, through it, Avatar,
 * TargetBadge and the webhook block) asks this provider for a card; it
 * answers with one when the signed-in person may read the thing, and
 * with nothing otherwise, so a mention a reader cannot follow stays
 * plain text rather than a card that says "not visible". Cards load
 * what they show when they open, which is the deliberate exception to
 * loading in route loaders: a card is not a screen, it appears under
 * the pointer, and one muted pending line for a moment is the honest
 * shape of that.
 */

/* The permission that reads each kind of thing. holdsAnywhere, because
 * the card cannot know a scoped grant covers this one until it has
 * loaded it; a refused read renders as such inside the card. A tool
 * and a pipeline block need none: their cards are built from the
 * mention itself. */
const READ_PERMISSION: Record<Exclude<Reference["kind"], "tool" | "pipeline_block">, string> = {
  person: "membership.read",
  team: "team.read",
  team_escalation_policy: "team.read",
  escalation_path: "escalation_path.read",
  pipeline: "pipeline.read",
  webhook_endpoint: "webhook.read",
  agent: "agent.read",
  request: "request.read",
};

export function ReferenceCards({ children }: { children: ReactNode }) {
  const access = useAccess();
  const session = useSession();
  const selfMembershipId = session.principal.membership_id;
  const render = useCallback(
    (reference: Reference): ReactNode | null => {
      if (
        reference.kind !== "tool" &&
        reference.kind !== "pipeline_block" &&
        !access.holdsAnywhere(READ_PERMISSION[reference.kind])
      ) {
        return null;
      }
      /* An avatar drawn from a name alone has no agent to open. */
      if (reference.kind === "agent" && reference.slug === "") return null;
      switch (reference.kind) {
        case "person":
          return <PersonCard id={reference.id} name={reference.name} selfMembershipId={selfMembershipId} />;
        case "team":
          return <TeamCard id={reference.id} name={reference.name} />;
        case "team_escalation_policy":
          return <TeamPolicyCard teamId={reference.teamId} teamName={reference.teamName} />;
        case "escalation_path":
          return <EscalationPathCard pathKey={reference.key} name={reference.name} />;
        case "pipeline":
          return <PipelineCard agentSlug={reference.agentSlug} revisionNumber={reference.revision} />;
        case "webhook_endpoint":
          return <WebhookEndpointCard endpointKey={reference.key} name={reference.name} />;
        case "agent":
          return reference.slug
            ? <AgentCard slug={reference.slug} name={reference.name} />
            : <AgentNameCard name={reference.name} />;
        case "tool":
          return <ToolCard tool={reference.name} />;
        case "request":
          return <RequestCard id={reference.id} />;
        case "pipeline_block":
          return <PipelineBlockCard block={reference} />;
      }
    },
    [access, selfMembershipId],
  );
  return <ReferenceCardsProvider render={render}>{children}</ReferenceCardsProvider>;
}

/* Why a request sits in the default queue rather than on a path, for
 * the Sent to chip that names no path. Not a reference: the queue is
 * not a thing with a page, so the card is built where the routing is. */
export function DefaultRouteCard({
  routing,
  breakGlassOnly,
}: {
  routing: ApprovalRouting;
  breakGlassOnly: boolean;
}) {
  return (
    <>
      <Title kind="Routing" name="Default escalation path" />
      <HoverCardBody>
        <HoverCardFacts>
          <HoverCardFact term="Why">{DEFAULT_QUEUE_REASON[routing.reason]}</HoverCardFact>
          <HoverCardFact term="Waits for">
            {breakGlassOnly ? "A break-glass reviewer" : "Any authorized reviewer"}
          </HoverCardFact>
        </HoverCardFacts>
      </HoverCardBody>
    </>
  );
}

const DEFAULT_QUEUE_REASON: Record<ApprovalRouting["reason"], string> = {
  no_escalation: "The pipeline named no path",
  path_inactive: "The path named had no active revision",
  evaluation_error: "The pipeline could not be evaluated",
  pipeline_default: "The pipeline's default",
  block_escalation: "A block's human exit",
};

/* The shared reads. Every card that names people or teams draws on the
 * same roster, so the keys are per kind, not per card. */
const readMembers = () => api.members().then((result) => result.members);
const readTeams = () => api.teams().then((result) => result.teams);
const readAgents = () => api.agents().then((result) => result.agents);
const readPaths = () => api.escalationPaths("all").then((result) => result.escalation_paths);
const readEndpoints = () => api.webhookEndpoints().then((result) => result.webhook_endpoints);

/* The body while a read is on its way or went wrong; null once there
 * is a value to show, so each card renders its own facts. */
function Unready({ loaded, missing }: { loaded: Loaded<unknown>; missing: string }) {
  switch (loaded.status) {
    case "loading":
      return <HoverCardNote pending>Loading</HoverCardNote>;
    case "missing":
      return <HoverCardNote>{missing}</HoverCardNote>;
    case "forbidden":
      return <HoverCardNote>Not visible to you.</HoverCardNote>;
    case "failed":
      return <HoverCardNote>{loaded.message}</HoverCardNote>;
    case "ready":
      return null;
  }
}

/* Where the card's name leads: the thing's own page, or the list that
 * holds it when it has none of its own. */
type Destination = Pick<LinkProps, "to" | "params" | "search">;

/* The eyebrow: what kind of thing the card is about, in the mono label
 * the trees use for their own structure words. */
const KIND_LABEL = "font-mono text-[10px] tracking-wide text-ink-faint uppercase";

function Title({
  kind,
  mark,
  name,
  link,
  status,
  wrap = false,
  children,
}: {
  /* What kind of thing this is, at the head's right. */
  kind: string;
  /* A face beside the name, for a person or an agent. */
  mark?: ReactNode;
  name: ReactNode;
  /* The name is the way through to the thing, in a new tab so the card
   * never costs the reader the page they were on; absent, it is plain. */
  link?: Destination;
  /* A badge after the name for a state worth saying: archived, no
   * active revision, suspended, disabled. The healthy state says
   * nothing. */
  status?: ReactNode;
  /* A machine name breaks anywhere rather than truncating: its tail is
   * often the part that tells two apart. */
  wrap?: boolean;
  children?: ReactNode;
}) {
  const nameClass = wrap ? "break-all" : "truncate";
  return (
    <HoverCardTitle className="items-center">
      <span className="flex min-w-0 items-center gap-2.5">
        {mark}
        <span className="min-w-0">
          <span className="flex min-w-0 items-center gap-2">
            {link ? (
              <Link
                {...link}
                target="_blank"
                rel="noopener noreferrer"
                className="group/name flex min-w-0 items-center gap-1 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-accent"
              >
                <span className={cn("min-w-0 underline-offset-[3px] group-hover/name:underline", nameClass)}>
                  {name}
                </span>
                <ArrowUpRightIcon
                  width={12}
                  height={12}
                  className="shrink-0 text-ink-faint transition-colors duration-100 group-hover/name:text-ink"
                />
              </Link>
            ) : (
              <span className={cn("min-w-0", nameClass)}>{name}</span>
            )}
            {/* The badge's 24px box would set the row's height; the
                name's line does instead. */}
            {status ? <span className="-my-1 flex shrink-0">{status}</span> : null}
          </span>
          {children}
        </span>
      </span>
      <span className={cn("shrink-0 self-start", KIND_LABEL)}>{kind}</span>
    </HoverCardTitle>
  );
}

/* A one-line list of names inside a fact: "Alice, Bob and 3 more". */
function summarizeNames(names: string[], shown = 3): string {
  if (names.length === 0) return "";
  if (names.length <= shown) return names.join(", ");
  return `${names.slice(0, shown).join(", ")} and ${names.length - shown} more`;
}

/* The distinct permission summaries an assignment list grants, scoped ones
 * qualified by where. */
function permissionSummaries(assignments: Member["assignments"]): string[] {
  const names = assignments.map((assignment) =>
    assignment.scope_kind === "organization"
      ? `${assignment.permissions.length} permissions`
      : `${assignment.permissions.length} permissions (${assignment.scope_name ?? assignment.scope_kind.replace("_", " ")})`,
  );
  return names.filter((name, index) => names.indexOf(name) === index);
}

const MEMBERSHIP_STATUS: Record<MembershipStatus, { label: string; tone: Tone } | null> = {
  active: null,
  suspended: { label: "Suspended", tone: "warn" },
  deprovisioned: { label: "Removed", tone: "deny" },
};

/* A person: found by membership id when the mention carries one, else
 * by the name the mention shows (a decision or a claim names its
 * reviewer without an id). */
function PersonCard({
  id,
  name,
  selfMembershipId,
}: {
  id?: string;
  name: string;
  selfMembershipId?: string;
}) {
  const members = useShared("members", readMembers);
  const teams = useShared("teams", readTeams);
  const member =
    members.status === "ready"
      ? (members.value.find((entry) => entry.id === id) ??
        members.value.find((entry) => matchesName(entry, name)) ??
        null)
      : null;
  const status = member ? MEMBERSHIP_STATUS[member.status] : null;
  /* The member's teams as chips, each with the team's own card. */
  const memberTeams: ReviewerTarget[] =
    member && teams.status === "ready"
      ? member.team_ids.map((teamId) => ({
          type: "team",
          id: teamId,
          name: teams.value.find((team) => team.id === teamId)?.name ?? "A team",
        }))
      : [];
  return (
    <>
      <Title
        kind="Member"
        mark={<Avatar name={member?.display_name || name} size="lg" tooltip={false} />}
        link={{ to: "/settings/members" }}
        name={
          <>
            {member?.display_name || name}
            {member && member.id === selfMembershipId ? (
              <span className="ml-1.5 text-label font-normal text-ink-faint">you</span>
            ) : null}
          </>
        }
        status={status ? <Badge tone={status.tone}>{status.label}</Badge> : null}
      >
        {member ? <HoverCardMeta className="truncate">{member.email}</HoverCardMeta> : null}
      </Title>
      <HoverCardBody>
        {member ? (
          <HoverCardFacts>
            <HoverCardFact term="Teams">
              {member.team_ids.length === 0 ? (
                <span className="text-ink-muted">None</span>
              ) : teams.status === "ready" ? (
                memberTeams.map((team) => (
                  /* The chip's 24px box would stretch the fact row. */
                  <span key={team.id} className="-my-1 -mr-2 flex">
                    <TargetBadge target={team} />
                  </span>
                ))
              ) : (
                `${member.team_ids.length}`
              )}
            </HoverCardFact>
            <HoverCardFact term="Permissions">
              {member.assignments.length === 0 ? (
                <span className="text-ink-muted">None</span>
              ) : (
                summarizeNames(permissionSummaries(member.assignments))
              )}
            </HoverCardFact>
            <HoverCardFact term="Joined">{formatRelative(member.created_at)} ago</HoverCardFact>
          </HoverCardFacts>
        ) : members.status === "ready" ? (
          <HoverCardNote>Not a current member of this organization.</HoverCardNote>
        ) : (
          <Unready loaded={members} missing="Not a current member of this organization." />
        )}
      </HoverCardBody>
    </>
  );
}

function matchesName(member: Member, name: string): boolean {
  const needle = name.trim().toLowerCase();
  return needle !== "" && (member.display_name.toLowerCase() === needle || member.email.toLowerCase() === needle);
}

function TeamCard({ id, name }: { id: string; name: string }) {
  const teams = useShared("teams", readTeams);
  const team = teams.status === "ready" ? (teams.value.find((entry) => entry.id === id) ?? null) : null;
  return (
    <>
      <Title
        kind="Team"
        name={team?.name ?? name}
        link={{ to: "/settings/teams" }}
        status={team?.directory_managed ? <Badge>Directory</Badge> : null}
      >
        <HoverCardMeta>
          {team ? `${team.member_count} ${team.member_count === 1 ? "member" : "members"}` : "Team"}
        </HoverCardMeta>
      </Title>
      <HoverCardBody>
        {team ? (
          <HoverCardFacts>
            <HoverCardFact term="Escalation policy">
              <TeamPolicyLink team={team} />
            </HoverCardFact>
            <HoverCardFact term="Permissions">
              {team.assignments.length === 0 ? (
                <span className="text-ink-muted">None</span>
              ) : (
                summarizeNames(permissionSummaries(team.assignments))
              )}
            </HoverCardFact>
          </HoverCardFacts>
        ) : teams.status === "ready" ? (
          <HoverCardNote>This team no longer exists.</HoverCardNote>
        ) : (
          <Unready loaded={teams} missing="This team no longer exists." />
        )}
      </HoverCardBody>
    </>
  );
}

/* The team card's way into the policy: the value is the link, and it
 * carries the policy's own card so the tree is one hover away. */
function TeamPolicyLink({ team }: { team: Team }) {
  const policy = team.escalation_policy;
  /* A latest revision without an active one is a draft nobody activated
   * or a policy that was archived; the status does not tell them apart,
   * and either way every member is notified at once. */
  if (policy.active_revision === null) {
    return (
      <span className="text-ink-muted">
        {policy.latest_revision !== null
          ? "No active revision, everyone notified at once"
          : "None, everyone notified at once"}
      </span>
    );
  }
  return (
    <Referenced
      reference={{ kind: "team_escalation_policy", teamId: team.id, teamName: team.name }}
      inline
      focusable={false}
    >
      <Link
        to="/settings/teams/$teamId/escalation-policy"
        params={{ teamId: team.id }}
        target="_blank"
        rel="noopener noreferrer"
        className="group/name inline-flex items-center gap-1 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-accent"
      >
        <span className="underline-offset-[3px] group-hover/name:underline">Revision {policy.active_revision} active</span>
        <ArrowUpRightIcon
          width={12}
          height={12}
          className="shrink-0 text-ink-faint transition-colors duration-100 group-hover/name:text-ink"
        />
      </Link>
    </Referenced>
  );
}

/* A team's escalation policy: what the team does with a request a path
 * hands it, drawn as the tree the policy page edits. The team's own
 * roster names the targets, since a policy only ever names members. */
function TeamPolicyCard({ teamId, teamName }: { teamId: string; teamName: string }) {
  const team = useShared(`team:${teamId}`, () => api.getTeam(teamId));
  const policy = useShared(`team-policy:${teamId}`, () => api.teamEscalationPolicy(teamId));
  const names: ReviewerTarget[] =
    team.status === "ready"
      ? team.value.members.map(
          (member): ReviewerTarget => ({
            type: "user",
            id: member.membership_id,
            name: member.display_name || member.email,
          }),
        )
      : [];
  const tree =
    policy.status === "ready"
      ? teamPolicyTreeFromWire({ id: teamId, name: teamName }, policy.value, names)
      : null;
  return (
    <>
      <Title
        kind="Escalation policy"
        name={teamName}
        link={{ to: "/settings/teams/$teamId/escalation-policy", params: { teamId } }}
        status={policy.status === "missing" ? <Badge>None</Badge> : null}
      />
      <HoverCardBody>
        {tree ? (
          tree.nodes.length === 0 ? (
            <HoverCardNote>No steps: everyone on the team is notified at once.</HoverCardNote>
          ) : (
            <PathTree nodes={tree.nodes} repeat={tree.repeat} depth={1} />
          )
        ) : (
          <Unready
            loaded={policy}
            missing="No active policy. A path reaching the team notifies everyone on it at once."
          />
        )}
      </HoverCardBody>
    </>
  );
}

/* Where a path stands: active, waiting for a revision, or archived. */
function pathState(
  summary: EscalationPathSummary | null,
  revision: Loaded<EscalationPathRevisionWire>,
): { label: string; tone: Tone } | null {
  if (summary?.archived_at) return { label: "Archived", tone: "neutral" };
  if (revision.status === "ready" || summary?.active_revision) return { label: "Active", tone: "approve" };
  if (revision.status === "missing" || (summary && !summary.active_revision)) {
    return { label: "No active revision", tone: "warn" };
  }
  return null;
}

function EscalationPathCard({ pathKey, name }: { pathKey: string; name: string }) {
  const paths = useShared("escalation-paths", readPaths);
  const revision = useShared(`escalation-path:${pathKey}`, () => api.activeEscalationPath(pathKey));
  const access = useAccess();
  const members = useSharedIf(access.holdsAnywhere("membership.read"), "members", readMembers, [] as Member[]);
  const teams = useSharedIf(access.holdsAnywhere("team.read"), "teams", readTeams, [] as Team[]);
  const summary = paths.status === "ready" ? (paths.value.find((path) => path.path_key === pathKey) ?? null) : null;
  const state = pathState(summary, revision);
  /* The tree names its targets from the directory; a team or person
   * the reader cannot list shows as a short id. */
  const names: ReviewerTarget[] = [
    ...(teams.status === "ready" ? teams.value : []).map(
      (team): ReviewerTarget => ({ type: "team", id: team.id, name: team.name }),
    ),
    ...(members.status === "ready" ? members.value : []).map(
      (member): ReviewerTarget => ({ type: "user", id: member.id, name: member.display_name || member.email }),
    ),
  ];
  const tree = revision.status === "ready" ? pathTreeFromWire(revision.value, names) : null;
  return (
    <>
      <Title
        kind="Escalation path"
        name={revision.status === "ready" ? revision.value.name : (summary?.name ?? name)}
        link={{ to: "/escalation-paths/$pathKey", params: { pathKey } }}
        status={state && state.label !== "Active" ? <Badge tone={state.tone}>{state.label}</Badge> : null}
      />
      <HoverCardBody>
        {tree ? (
          tree.nodes.length === 0 ? (
            <HoverCardNote>No steps: the path reaches nobody.</HoverCardNote>
          ) : (
            <PathTree nodes={tree.nodes} repeat={tree.repeat} depth={1} />
          )
        ) : revision.status === "missing" && summary?.archived_at ? (
          <HoverCardNote>Archived {formatRelative(summary.archived_at)} ago. It can never be active again.</HoverCardNote>
        ) : revision.status === "missing" && summary ? (
          <HoverCardNote>
            No revision is active. A request reaching it waits for any authorized reviewer.
          </HoverCardNote>
        ) : (
          <Unready loaded={revision} missing="This escalation path no longer exists." />
        )}
      </HoverCardBody>
    </>
  );
}

const CORE_BLOCK_KIND: Record<string, string> = {
  always: "Always",
  cel: "Condition",
  webhook: "Webhook",
  branch: "Branch",
};

/* A block type as a noun: core's, a registered kind's, or the raw type
 * of a kind this bundle does not know. */
function kindLabel(type: string): string {
  return CORE_BLOCK_KIND[type] ?? blockKind(type)?.kindLabel ?? type;
}

const OUTCOME_LABEL: Record<PolicyBlockDraft["outcome"], string> = {
  approve: "Approve",
  deny: "Deny",
  human: "Ask a human",
};

const OUTCOME_TONE: Record<PolicyBlockDraft["outcome"], Tone> = {
  approve: "approve",
  deny: "deny",
  human: "warn",
};

/* A pipeline drawn the way the path card draws a path: its blocks in
 * order down a rail, each with what it tests and where its human exit
 * goes, and the terminus saying what happens when nothing matched. */
function PipelineCard({ agentSlug, revisionNumber }: { agentSlug: string; revisionNumber?: number }) {
  const revision = useShared(
    revisionNumber === undefined ? `pipeline:${agentSlug}` : `pipeline:${agentSlug}@${revisionNumber}`,
    () => revisionNumber === undefined
      ? api.activeRequestApprovalPipeline(agentSlug)
      : api.requestApprovalPipelineRevision(agentSlug, revisionNumber),
  );
  const organization = agentSlug === "";
  return (
    <>
      <Title kind="Approval pipeline" name={organization ? "Organization" : <span className="font-mono text-[12px]">{agentSlug}</span>} link={pipelineLink(agentSlug, { revision: revisionNumber })}>
        {revisionNumber !== undefined ? <HoverCardMeta>Revision {revisionNumber}, as it ran</HoverCardMeta> : null}
      </Title>
      <HoverCardBody>
        {revision.status === "ready" ? (
          <PipelineRail revision={revision.value} organization={organization} />
        ) : (
          <Unready loaded={revision} missing="No pipeline exists for this agent." />
        )}
      </HoverCardBody>
    </>
  );
}

/* The one block (or branch end) a card picks out, and the note beneath
 * it. end picks out the end of the branch named by key rather than the
 * branch itself: the request ran out of it. */
export interface RailHighlight {
  key: string;
  note: ReactNode;
  end?: boolean;
}

/* The pipeline's blocks down a rail. With a highlight, that block is
 * the one the reader came for: it keeps its ink and carries the note,
 * the others step back. */
function PipelineRail({
  revision,
  organization,
  highlight,
}: {
  revision: RequestApprovalPipelineRevision;
  organization: boolean;
  highlight?: RailHighlight;
}) {
  const access = useAccess();
  const paths = useSharedIf(
    access.holdsAnywhere("escalation_path.read"),
    "escalation-paths",
    readPaths,
    [] as EscalationPathSummary[],
  );
  const directory: ReviewerTarget[] =
    paths.status === "ready"
      ? paths.value.map((path) => ({ type: "escalation_path", id: path.path_key, name: path.name }))
      : [];
  const blocks = draftsFromBlocks(revision.blocks, directory);
  const fallback = pathTarget(revision.default_escalation_path, directory);
  return (
    <PipelineRailTimeline
      blocks={blocks}
      fallback={fallback}
      highlight={highlight}
      end={
        <TimelineItem>
          <p className="flex flex-wrap items-center gap-y-1 text-body text-ink-secondary">
            {blocks.length === 0 ? "Every request" : "No match"}
            {fallback ? (
              <>
                <span className="mr-1">: escalate via</span>
                <TargetBadge className="-my-1" target={fallback} />
              </>
            ) : (
              <span>: {organization ? "any authorized reviewer" : <Referenced reference={{ kind: "pipeline", agentSlug: "" }} inline>the organization pipeline's default</Referenced>}</span>
            )}
          </p>
        </TimelineItem>
      }
    />
  );
}

const PICKED = "-mx-2 -my-1.5 rounded-md border-hairline border-edge-strong bg-fill-hover px-2 py-1.5";

/* One rail of blocks as a timeline, markers numbering the blocks on this
 * rail. A branch's blocks nest as a timeline of their own under it,
 * ending where the branch sends a request nothing decided. */
export function PipelineRailTimeline({
  blocks,
  fallback,
  highlight,
  end,
  className,
}: {
  blocks: BlockDraft[];
  /* The pipeline default, for the ends of branches that name no path. */
  fallback: ReviewerTarget | null;
  highlight?: RailHighlight;
  /* The rail's last item: what happens when it runs out. */
  end: ReactNode;
  className?: string;
}) {
  return (
    <Timeline className={className}>
      {blocks.map((block, index) => {
        const picked = highlight?.key === block.id && !(highlight.end && block.kind === "branch");
        const dimmed = highlight !== undefined && !picked;
        return (
          <TimelineItem
            key={block.id}
            tone={dimmed ? "neutral" : "accent"}
            marker={<span className="font-mono text-[11px]">{index + 1}</span>}
          >
            <div className={cn("flex flex-col gap-1", picked && PICKED)}>
              <div className="flex items-baseline gap-3">
                <span className={cn("min-w-0 flex-1 truncate text-body", dimmed ? "text-ink-muted" : "text-ink")}>
                  {block.name || "Untitled block"}
                </span>
                <span className="shrink-0 text-label font-normal text-ink-faint">{kindLabel(blockTypeOf(block))}</span>
              </div>
              <p className={cn("truncate font-mono text-[11px]", dimmed ? "text-ink-faint" : "text-ink-muted")}>
                <BlockSummary block={block} />
              </p>
              <BlockExit block={block} />
              {picked ? highlight.note : null}
            </div>
            {block.kind === "branch" ? (
              <PipelineRailTimeline
                className="mt-3"
                blocks={block.blocks}
                fallback={fallback}
                highlight={highlight}
                end={<BranchEndItem branch={block} fallback={fallback} highlight={highlight} />}
              />
            ) : null}
          </TimelineItem>
        );
      })}
      {end}
    </Timeline>
  );
}

/* Where a branch sends a request no block inside it decided. */
function BranchEndItem({
  branch,
  fallback,
  highlight,
}: {
  branch: Extract<BlockDraft, { kind: "branch" }>;
  fallback: ReviewerTarget | null;
  highlight?: RailHighlight;
}) {
  const picked = highlight?.end === true && highlight.key === branch.id;
  const dimmed = highlight !== undefined && !picked;
  return (
    <TimelineItem tone={picked ? "accent" : "neutral"}>
      <div className={cn("flex flex-col gap-1", picked && PICKED)}>
        <p className={cn("flex flex-wrap items-center gap-y-1 text-body", dimmed ? "text-ink-muted" : "text-ink-secondary")}>
          <span className="mr-1">No decision: ask a human via</span>
          <TargetBadge className="-my-1" target={branch.target ?? fallback} />
        </p>
        {picked ? highlight.note : null}
      </div>
    </TimelineItem>
  );
}

/* The block that ended a request's pipeline run, shown in its place:
 * the pipeline that held it, at the revision that ran, with the block
 * picked out and the sentence its author wrote beneath it. Without the
 * revision (the reader cannot see pipelines, or the run recorded none)
 * the block speaks for itself. */
function PipelineBlockCard({ block }: { block: Extract<Reference, { kind: "pipeline_block" }> }) {
  const access = useAccess();
  const pipeline = block.pipeline;
  const readable = pipeline !== undefined && access.holdsAnywhere("pipeline.read");
  const revision = useSharedIf(
    readable,
    pipeline ? `pipeline:${pipeline.agentSlug}@${pipeline.revision}` : "pipeline:none",
    () => api.requestApprovalPipelineRevision(pipeline?.agentSlug ?? "", pipeline?.revision ?? 0),
    null as RequestApprovalPipelineRevision | null,
  );
  const note = (
    <div className="flex flex-col gap-1 pt-1">
      {block.endOfBranch ? (
        <p className="text-body text-ink-secondary">
          No block in this branch decided, so the request went to a human.
        </p>
      ) : null}
      {block.errorCode ? (
        <p className="text-body text-warn">
          This block failed ({blockErrorLabel(block.errorCode)}), so the request went to a human. It did not match.
        </p>
      ) : null}
      {block.reason ? <p className="text-body text-ink">{block.reason}</p> : null}
      {block.endpointReason ? <p className="text-body text-ink-secondary">The endpoint said: {block.endpointReason}</p> : null}
      {readable ? (
        <Link
          {...pipelineLink(pipeline.agentSlug, { revision: pipeline.revision, block: block.key })}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 text-label text-accent hover:underline"
        >
          View this block in the pipeline <ArrowUpRightIcon width={12} height={12} />
        </Link>
      ) : null}
    </div>
  );
  if (!readable) {
    return (
      <>
        <Title kind="Pipeline block" name={block.name || block.key} />
        <HoverCardBody className="flex flex-col gap-2">
          <HoverCardFacts>
            <HoverCardFact term="Kind">{kindLabel(block.type)}</HoverCardFact>
            <HoverCardFact term="Outcome">{OUTCOME_LABEL[block.outcome]}</HoverCardFact>
            <HoverCardFact term="Key" mono>
              <span className="truncate">{block.key}</span>
            </HoverCardFact>
          </HoverCardFacts>
          {note}
        </HoverCardBody>
      </>
    );
  }
  const organization = pipeline.agentSlug === "";
  return (
    <>
      <Title kind="Approval pipeline" name={organization ? "Organization" : <span className="font-mono text-[12px]">{pipeline.agentSlug}</span>} link={pipelineLink(pipeline.agentSlug, { revision: pipeline.revision })}>
        <HoverCardMeta>Revision {pipeline.revision}, as it ran</HoverCardMeta>
      </Title>
      {/* The picked block's box reaches 6px above its row; the extra top
        * padding keeps it clear of the hairline when it is the first. */}
      <HoverCardBody className="pt-4">
        {revision.status === "ready" && revision.value ? (
          <PipelineRail
            revision={revision.value}
            organization={organization}
            highlight={{ key: block.key, note, end: block.endOfBranch }}
          />
        ) : (
          <>
            <Unready loaded={revision} missing="That revision no longer exists." />
            {note}
          </>
        )}
      </HoverCardBody>
    </>
  );
}

function BlockSummary({ block }: { block: BlockDraft }) {
  switch (block.kind) {
    case "always":
      return <>Always · Every remaining request</>;
    case "cel":
      return <>Condition · <ConditionSummary node={block.when} /></>;
    case "branch":
      return <>Branch · <ConditionSummary node={block.when} /></>;
    case "edition":
      return requireBlockKind(block.type).summarize(block);
    case "unknown":
      return `${block.type} · not available in this edition`;
    case "webhook":
      return block.endpointKey ? (
        <>
          Webhook ·{" "}
          <Referenced reference={{ kind: "webhook_endpoint", key: block.endpointKey, name: block.endpointKey }} inline>
            {block.endpointKey}
          </Referenced>
        </>
      ) : summarizeWebhook(block);
  }
}

/* Where a block's match goes. A condition block has one outcome; a
 * webhook, or a kind the edition adds, answers for itself, so only its
 * human exit's path is worth a line. A branch's blocks and end nest
 * beneath it instead. */
function BlockExit({ block }: { block: BlockDraft }) {
  const target = block.target;
  switch (block.kind) {
    case "cel":
    case "always":
      return (
        <p className="flex flex-wrap items-center gap-y-1 text-body text-ink-secondary">
          <span className="mr-1">{block.kind === "always" ? "Always" : "On match"}</span>
          <Badge className="-my-1" tone={OUTCOME_TONE[block.outcome]}>
            {OUTCOME_LABEL[block.outcome]}
          </Badge>
          {block.outcome === "human" && target ? (
            <>
              <span className="mx-1">via</span>
              <TargetBadge className="-my-1" target={target} />
            </>
          ) : null}
        </p>
      );
    case "edition":
    case "unknown":
    case "webhook":
      if (!target) return null;
      return (
        <p className="flex flex-wrap items-center gap-y-1 text-body text-ink-secondary">
          <span className="mr-1">Human exit via</span>
          <TargetBadge className="-my-1" target={target} />
        </p>
      );
    case "branch":
      return null;
  }
}

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

function WebhookEndpointCard({ endpointKey, name }: { endpointKey: string; name: string }) {
  const endpoints = useShared("webhook-endpoints", readEndpoints);
  const endpoint: WebhookEndpointSummary | null =
    endpoints.status === "ready"
      ? (endpoints.value.find((entry) => entry.endpoint_key === endpointKey) ?? null)
      : null;
  const pipelines = endpoint
    ? endpoint.uses.filter(
        (use, index) => endpoint.uses.findIndex((other) => other.pipeline_id === use.pipeline_id) === index,
      ).length
    : 0;
  return (
    <>
      <Title
        kind="Webhook endpoint"
        name={endpoint?.name || name || endpointKey}
        link={{ to: "/webhooks/$endpointKey", params: { endpointKey } }}
        status={
          endpoint?.archived_at ? (
            <Badge>Archived</Badge>
          ) : endpoint && endpoint.active_revision === undefined ? (
            <Badge>Draft</Badge>
          ) : null
        }
      />
      <HoverCardBody>
        {endpoint ? (
          <HoverCardFacts>
            <HoverCardFact term="Posts to" mono>
              <span className="truncate">{hostOf(endpoint.url)}</span>
            </HoverCardFact>
            <HoverCardFact term="Pipelines">{pipelines}</HoverCardFact>
            <HoverCardFact term="Secret rotated">{formatRelative(endpoint.secret_rotated_at)} ago</HoverCardFact>
          </HoverCardFacts>
        ) : endpoints.status === "ready" ? (
          <HoverCardNote>No endpoint with this key exists.</HoverCardNote>
        ) : (
          <Unready loaded={endpoints} missing="No endpoint with this key exists." />
        )}
      </HoverCardBody>
    </>
  );
}

/* Conditions can name an agent by display name rather than slug. Only
 * resolve to one agent when the name is unambiguous. */
function AgentNameCard({ name }: { name: string }) {
  const agents = useShared("agents", readAgents);
  const matches = agents.status === "ready" ? agents.value.filter((agent) => agent.name === name) : [];
  if (matches.length === 1) return <AgentCard slug={matches[0].slug} name={name} />;
  return (
    <>
      <Title kind="Agent" name={name} />
      <HoverCardBody>
        {agents.status !== "ready" ? <Unready loaded={agents} missing="No agent with this name exists." />
          : matches.length === 0 ? <HoverCardNote>No agent with this name exists.</HoverCardNote>
          : <div className="flex flex-wrap gap-1">{matches.map((agent) => (
            <FactChip key={agent.slug} reference={{ kind: "agent", slug: agent.slug, name: agent.name }} />
          ))}</div>}
      </HoverCardBody>
    </>
  );
}

/* An agent in three numbers: how many instances it has, when it last
 * asked, and how much it asks. The head carries its slug under the
 * name; a state badge appears only when it cannot call in at all. */
function AgentCard({ slug, name }: { slug: string; name: string }) {
  const agent = useShared(`agent:${slug}`, () => api.agent(slug));
  const detail: AgentDetail | null = agent.status === "ready" ? agent.value : null;
  const connection = detail ? detailConnection(detail) : null;
  const lastSeen = detail
    ? detail.instances
        .map((instance) => instance.last_seen_at)
        .filter(Boolean)
        .sort()
        .at(-1)
    : undefined;
  return (
    <>
      <Title
        kind="Agent"
        mark={<Avatar name={detail?.name ?? name} kind="agent" tooltip={false} />}
        name={detail?.name ?? name}
        link={{ to: "/agents/$agentSlug", params: { agentSlug: slug } }}
        status={
          connection && connection !== "active" ? (
            <Badge tone={CONNECTION_TONE[connection]}>{CONNECTION_LABEL[connection]}</Badge>
          ) : null
        }
      >
        <HoverCardMeta className="truncate font-mono">{slug}</HoverCardMeta>
      </Title>
      <HoverCardBody>
        {detail ? (
          <HoverCardFacts>
            <HoverCardFact term="Instances">{detail.instance_count}</HoverCardFact>
            <HoverCardFact term="Last request">
              {lastSeen ? `${formatRelative(lastSeen)} ago` : <span className="text-ink-muted">Never</span>}
            </HoverCardFact>
            <HoverCardFact term="Weekly requests">{detail.recent_request_count}</HoverCardFact>
          </HoverCardFacts>
        ) : (
          <Unready loaded={agent} missing="This agent no longer exists." />
        )}
      </HoverCardBody>
    </>
  );
}

/* A mention inside a fact's value: a chip with its own card, pulled in
 * so its 24px box does not stretch the row. */
function FactChip({ reference }: { reference: Reference }) {
  return (
    <span className="-my-1 -mr-2 flex">
      <ReferenceChip reference={reference} />
    </span>
  );
}

/* Where a request was handed for review, from its routing snapshot. A
 * request the pipeline decided never had one, and the routing read
 * says so with a 404. */
function sentTo(request: ApprovalRequest, routing: Loaded<ApprovalRouting>): ReactNode {
  switch (routing.status) {
    case "ready":
      if (routing.value.outcome === "escalated" && routing.value.escalation_path_key) {
        return (
          <FactChip
            reference={{
              kind: "escalation_path",
              key: routing.value.escalation_path_key,
              name: routing.value.escalation_path_name || routing.value.escalation_path_key,
            }}
          />
        );
      }
      if (routing.value.outcome === "escalated") return "An escalation path";
      return "Default escalation path";
    case "missing":
      return request.decision?.channel === "pipeline" ? (
        "Nobody, decided automatically"
      ) : (
        <span className="text-ink-muted">Not routed yet</span>
      );
    case "loading":
      return <span className="text-ink-muted">Loading</span>;
    case "forbidden":
    case "failed":
      return <span className="text-ink-muted">Unknown</span>;
  }
}

/* How the request ended, or where it stands: one labelled fact, with
 * the deadline beside it while it is still open. Expiry and withdrawal
 * are not a reviewer's decision, so neither names one. */
function outcomeFacts(request: ApprovalRequest): ReactNode {
  const decision = request.decision;
  if (decision) {
    const when = `${formatRelative(decision.decided_at)} ago`;
    if (decision.status === "cancelled") {
      return <HoverCardFact term="Withdrawn">by the agent, {when}</HoverCardFact>;
    }
    if (decision.status === "expired") {
      return <HoverCardFact term="Expired">nobody decided, {when}</HoverCardFact>;
    }
    const term = decision.status === "approved" ? "Approved by" : "Denied by";
    if (decision.channel === "pipeline") {
      return <HoverCardFact term={term}>the pipeline, {when}</HoverCardFact>;
    }
    return (
      <HoverCardFact term={term}>
        {decision.reviewer_display_name ? (
          <FactChip reference={{ kind: "person", name: decision.reviewer_display_name }} />
        ) : (
          "a reviewer"
        )}
        <span>
          {decision.channel ? `via ${decision.channel}, ` : ""}
          {when}
        </span>
      </HoverCardFact>
    );
  }
  return (
    <>
      <HoverCardFact term="Claimed by">
        {request.claim ? (
          <FactChip
            reference={{
              kind: "person",
              id: request.claim.membership_id,
              name: request.claim.display_name || "Reviewer",
            }}
          />
        ) : (
          <span className="text-ink-muted">Nobody yet</span>
        )}
      </HoverCardFact>
      <HoverCardFact term="Deadline">in {formatRemaining(request.deadline_at)}</HoverCardFact>
    </>
  );
}

/* A request: what the agent asked to do, why, where it went, and how
 * it ended. The head is the tool alone, since a request has no name of
 * its own and a machine name wants the whole line; the body quotes the
 * agent's own words the way the detail page does, then the facts, state
 * first. Read
 * only, like every card: deciding stays on the detail page, where the
 * full context and the reader's standing are known. */
function RequestCard({ id }: { id: string }) {
  const request = useShared(`request:${id}`, () => api.approval(id));
  const routing = useShared(`request-routing:${id}`, () => api.approvalRouting(id));
  const value: ApprovalRequest | null = request.status === "ready" ? request.value : null;
  return (
    <>
      <Title
        kind="Request"
        name={<span className="font-mono text-[12px]">{value ? value.tool : shortId(id)}</span>}
        link={{ to: "/queue/$requestId", params: { requestId: id } }}
        wrap
      />
      <HoverCardBody>
        {value ? (
          <>
            {value.agent_reasoning ? (
              <blockquote className="mb-2.5 flex flex-col gap-1 border-l-2 border-edge-strong pl-3">
                <p className="text-micro text-ink-faint">Agent's explanation</p>
                <p className="line-clamp-3 text-label text-ink-secondary">{value.agent_reasoning}</p>
              </blockquote>
            ) : null}
            <HoverCardFacts>
              <HoverCardFact term="Status">
                {/* The badge's 24px box would set the row's height. */}
                <span className="-my-1 flex">
                  <Badge tone={STATUS_TONE[value.status]}>{STATUS_LABEL[value.status]}</Badge>
                </span>
              </HoverCardFact>
              <HoverCardFact term="Agent">
                <FactChip reference={{ kind: "agent", slug: value.agent_slug, name: value.agent_name }} />
                <span className="truncate">on {value.agent_instance_name}</span>
              </HoverCardFact>
              <HoverCardFact term="Created">{formatRelative(value.created_at)} ago</HoverCardFact>
              <HoverCardFact term="Sent to">{sentTo(value, routing)}</HoverCardFact>
              {outcomeFacts(value)}
            </HoverCardFacts>
            {value.decision?.note ? (
              <p className="mt-2 line-clamp-2 text-label text-ink-muted">{value.decision.note}</p>
            ) : null}
          </>
        ) : (
          <Unready loaded={request} missing="This request no longer exists." />
        )}
      </HoverCardBody>
    </>
  );
}

/* A tool is the name its server defines, with the server the request
 * or the page recorded beside it. Whether a call to it waits for a
 * person is the pipeline's decision, so the card names the tool and its
 * server and nothing more; the edition may know the server's display
 * name. */
function ToolCard({ tool, server }: { tool: string; server?: string }) {
  const auditLinkFor = useAuditLinks();
  const edition = extensions.toolReference({ tool, server });
  return (
    <>
      <Title
        kind="Tool"
        name={<span className="font-mono text-[12px]">{tool}</span>}
        link={auditLinkProps(auditLinkFor({ q: tool }))}
        wrap
      />
      <HoverCardBody>
        {server ? (
          <HoverCardFacts>
            <HoverCardFact term="Server" mono>
              <span className="truncate">{edition ? edition.serverName : server}</span>
            </HoverCardFact>
          </HoverCardFacts>
        ) : (
          <HoverCardNote>The runtime's own tool, named as the agent asked.</HoverCardNote>
        )}
      </HoverCardBody>
    </>
  );
}
