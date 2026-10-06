import { useMemo, useState } from "react";
import { useRouter } from "@tanstack/react-router";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/ds/components/card";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogTitle } from "@/ds/components/dialog";
import { RevisionHistory } from "@/ds/components/revision-history";
import { EmptyState } from "@/ds/components/empty-state";
import { PathTree } from "@/ds/components/escalation-tree";
import { type ReviewerTarget } from "@/ds/components/reviewer-target";
import { SaveBar } from "@/ds/components/save-bar";
import { useToast } from "@/ds/components/toast";
import { Page } from "@/ds/components/page";
import { PageHeader, PageBackLink } from "@/ds/components/page-header";
import { AlertIcon, BroadcastIcon } from "@/ds/icons";
import { api, RequestError } from "../api";
import type {
  Team,
  TeamEscalationPolicyRevisionWire,
  TeamEscalationPolicyRevisionSummary,
  TeamEscalationPolicyWarning,
  TeamMember,
} from "../types";
import {
  countChanges,
  fromWire,
  newDraft,
  toWire,
  type EscalationDocument,
} from "./escalation-document";
import { EscalationDocumentEditor } from "./escalation-editor";
import { teamPolicyTreeFromWire } from "./reviewer-route";
import { useTitle } from "./use-title";
import { useRevisionActivation } from "./use-revision-activation";

/*
 * A team's escalation policy: what the team does with a request once an
 * escalation path hands it over. The same document grammar as a path,
 * whose levels name members of the team or everyone on it. Save creates
 * a new immutable revision and offers activation when the person may;
 * requests already routed keep the revision they pinned.
 */

export interface TeamEscalationPolicyPage {
  team: Team | null;
  members: TeamMember[];
  /* The active revision, or the latest saved revision for an archived team. */
  policy: TeamEscalationPolicyRevisionWire | null;
  revisions: TeamEscalationPolicyRevisionSummary[];
  error: string;
}

export async function loadTeamEscalationPolicyPage(teamId: string): Promise<TeamEscalationPolicyPage> {
  try {
    const [detail, policy, history] = await Promise.all([
      api.getTeam(teamId),
      api.teamEscalationPolicy(teamId).catch((reason: unknown) => {
        if (reason instanceof RequestError && reason.status === 404) return null;
        throw reason;
      }),
      api.teamEscalationPolicyRevisions(teamId),
    ]);
    const latestRevision = detail.team.escalation_policy.latest_revision;
    const displayedPolicy = detail.team.archived_at && latestRevision !== null
      ? await api.teamEscalationPolicyRevision(teamId, latestRevision)
      : policy;
    return { team: detail.team, members: detail.members, policy: displayedPolicy, revisions: history.revisions, error: "" };
  } catch (reason) {
    return {
      team: null,
      members: [],
      policy: null,
      revisions: [],
      error: reason instanceof Error ? reason.message : "Unable to load the escalation policy.",
    };
  }
}

/* The one broadcast entry, then the team's active members: what a level
 * may name. */
function vocabularyOf(team: Team, members: TeamMember[]): ReviewerTarget[] {
  return [
    { type: "broadcast", id: team.id, name: `Everyone on ${team.name}` },
    ...members
      .filter((member) => member.status === "active")
      .map<ReviewerTarget>((member) => ({
        type: "user",
        id: member.membership_id,
        name: member.display_name || member.email,
      })),
  ];
}

/* Departed targets the warnings still name, so the document labels
 * them instead of showing a bare id. They are not offered by the
 * picker. */
function departedOf(warnings: TeamEscalationPolicyWarning[]): ReviewerTarget[] {
  return warnings.flatMap((warning) =>
    warning.code === "target_not_member" && warning.membership_id
      ? [{ type: "user" as const, id: warning.membership_id, name: warning.display_name || "A former member" }]
      : [],
  );
}

function WorkingHoursList({ document }: { document: EscalationDocument }) {
  if (document.workingHours.length === 0) return null;
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Working hours</CardTitle>
          <CardDescription>Time windows that you can reference in this policy.</CardDescription>
        </div>
      </CardHeader>
      <CardBody className="flex flex-col gap-1.5">
        {document.workingHours.map((set) => (
          <p key={set.id} className="text-body text-ink-secondary">
            <span className="text-ink">{set.name}</span>
            <span className="text-ink-muted">
              {" "}
              {set.weekdays.join(", ")} {set.start} to {set.end} ({set.timezone})
            </span>
          </p>
        ))}
      </CardBody>
    </Card>
  );
}

/* canWrite covers saving a revision; canActivate covers making it the
 * live one and archiving the policy. Saving offers activation when both
 * are held; with write alone the revision is saved as a draft for someone
 * who can activate it. */
export function TeamEscalationPolicyScreen({
  teamId,
  page,
  canWrite,
  canActivate,
}: {
  teamId: string;
  page: TeamEscalationPolicyPage;
  canWrite: boolean;
  canActivate: boolean;
}) {
  useTitle("Escalation policy");
  const toast = useToast();
  const revisionActivation = useRevisionActivation();
  const router = useRouter();
  const { team, members, policy, error } = page;
  const activeRevision = team?.escalation_policy.active_revision ?? null;
  const latestRevision = team?.escalation_policy.latest_revision ?? null;

  const vocabulary = useMemo(
    () => ({ kind: "team" as const, targets: team ? vocabularyOf(team, members) : [] }),
    [team, members],
  );
  /* The warnings of the revision saved last, shown even when it is not
   * the active one the reloaded page describes. */
  const [created, setCreated] = useState<{
    teamId: string;
    warnings: TeamEscalationPolicyWarning[];
  } | null>(null);
  const warnings = created?.teamId === teamId ? created.warnings : (policy?.warnings ?? []);
  const directory = useMemo(
    () => [...vocabulary.targets, ...departedOf(policy?.warnings ?? [])],
    [vocabulary, policy],
  );
  const saved = useMemo<EscalationDocument | null>(
    () => (team ? (policy ? fromWire(policy, directory) : newDraft()) : null),
    [team, policy, directory],
  );
  const tree = useMemo(
    () => (team && policy ? teamPolicyTreeFromWire(team, policy, directory) : null),
    [team, policy, directory],
  );

  /* The draft belongs to the page it was edited from: a save or archive
   * reloads the page and the editor shows the saved document again. */
  const [edit, setEdit] = useState<{ base: TeamEscalationPolicyPage; draft: EscalationDocument } | null>(
    null,
  );
  const draft = edit?.base === page ? edit.draft : saved;
  const setDraft = (next: EscalationDocument) => setEdit({ base: page, draft: next });
  const [saving, setSaving] = useState(false);
  const [archiving, setArchiving] = useState(false);
  const [viewing, setViewing] = useState<TeamEscalationPolicyRevisionWire | null>(null);
  const [activating, setActivating] = useState<number | null>(null);

  async function openRevision(revision: TeamEscalationPolicyRevisionSummary, edit: boolean) {
    try {
      const savedRevision = await api.teamEscalationPolicyRevision(teamId, revision.revision);
      if (edit) {
        setDraft(fromWire(savedRevision, [...vocabulary.targets, ...departedOf(savedRevision.warnings)]));
      } else {
        setViewing(savedRevision);
      }
    } catch (reason) {
      toast.add({ type: "deny", title: "The revision could not be loaded", description: reason instanceof Error ? reason.message : undefined });
    }
  }

  async function activateRevision(revision: TeamEscalationPolicyRevisionSummary) {
    setActivating(revision.revision);
    try {
      await api.activateTeamEscalationPolicyRevision(teamId, revision.revision, activeRevision);
      toast.add({ title: `Revision ${revision.revision} active`, description: "New requests reaching this team use this policy revision." });
      await router.invalidate();
    } catch (reason) {
      if (reason instanceof RequestError && reason.status === 412) await router.invalidate();
      toast.add({ type: "deny", title: "Activation failed", description: reason instanceof Error ? reason.message : undefined });
    } finally {
      setActivating(null);
    }
  }

  const dirtyCount = useMemo(
    () => (draft && saved ? countChanges(draft, saved, activeRevision !== null) : 0),
    [draft, saved, activeRevision],
  );

  async function save(current: EscalationDocument) {
    if (!team) return;
    setSaving(true);
    try {
      const revision = await api.createTeamEscalationPolicyRevision(teamId, toWire(current));
      await revisionActivation.offer({
        revision: revision.revision,
        activeRevision,
        canActivate,
        activationDescription: "Once active, new requests reaching this team use this policy revision.",
        activate: () => api.activateTeamEscalationPolicyRevision(teamId, revision.revision, activeRevision),
      });
      setCreated({ teamId, warnings: revision.warnings });
      await router.invalidate();
    } catch (reason) {
      toast.add({
        title: "Save failed",
        description: reason instanceof Error ? reason.message : "Unable to save the escalation policy.",
        type: "deny",
      });
    } finally {
      setSaving(false);
    }
  }

  async function archive() {
    if (!team || activeRevision === null) return;
    try {
      await api.archiveTeamEscalationPolicy(teamId, activeRevision);
      toast.add({
        title: "Escalation policy archived",
        description: `A request reaching ${team.name} notifies everyone on it at once.`,
      });
      setArchiving(false);
      await router.invalidate();
    } catch (reason) {
      toast.add({
        title: "Archive failed",
        description: reason instanceof Error ? reason.message : undefined,
        type: "deny",
      });
    }
  }

  if (error || !team || !draft || !saved) {
    return (
      <Page width="narrow" className="gap-3">
        <PageHeader
          back={
            <PageBackLink to="/settings/teams/$teamId" params={{ teamId }}>
              {team?.name ?? "Team"}
            </PageBackLink>
          }
        />
        <p className="text-body text-ink-muted">{error || "Unable to load the escalation policy."}</p>
      </Page>
    );
  }

  return (
    <Page width="narrow">
      {revisionActivation.dialog}
      <PageHeader
        back={
          <PageBackLink to="/settings/teams/$teamId" params={{ teamId }}>
            {team.name}
          </PageBackLink>
        }
      >
        <div className="flex items-start justify-between gap-4">
          <div>
            <h1 className="text-title-lg text-ink">Escalation policy</h1>
            <p className="mt-1.5 max-w-[560px] text-body text-ink-muted">
              Who on {team.name} is asked when an escalation path hands a request to the team.
            </p>
          </div>
          {canActivate && activeRevision !== null ? (
            <Button variant="ghost" size="sm" onClick={() => setArchiving(true)}>
              Archive policy
            </Button>
          ) : null}
        </div>
      </PageHeader>

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-body font-medium text-ink">{team.name}</span>
        {team.archived_at ? <Badge>Archived team</Badge> : null}
        {activeRevision !== null ? (
          <Badge tone="approve">Revision {activeRevision} active</Badge>
        ) : (
          <Badge>No active revision</Badge>
        )}
        {latestRevision !== null && latestRevision > (activeRevision ?? 0) ? (
          <Badge>Revision {latestRevision} saved, not active</Badge>
        ) : null}
      </div>

      {(team.archived_at ? [] : warnings).map((warning) =>
        warning.code === "target_not_member" ? (
          <Callout
            key={`${warning.code}:${warning.membership_id ?? ""}`}
            tone="warn"
            icon={<AlertIcon width={15} height={15} />}
            title={`${warning.display_name || "A former member"} is no longer on ${team.name}`}
          >
            The active revision names them at a level. That level skips them when it runs.
            Remove them from the step, or add them back to the team.
          </Callout>
        ) : (
          <Callout
            key={warning.code}
            tone="warn"
            icon={<AlertIcon width={15} height={15} />}
            title={`${warning.count} of ${warning.member_count} members of ${team.name} cannot decide requests`}
          >
            The policy names them, but they do not have permission to decide the requests it
            carries. A request reaching the team waits for the members who can.
          </Callout>
        ),
      )}

      {canWrite ? (
        <EscalationDocumentEditor value={draft} onChange={setDraft} vocabulary={vocabulary} />
      ) : tree ? (
        <>
          <WorkingHoursList document={saved} />
          <Card>
            <CardHeader>
              <div>
                <CardTitle>Steps</CardTitle>
                <CardDescription>
                  Walked in order while the path level that named the team keeps its own timer
                  running.
                </CardDescription>
              </div>
            </CardHeader>
            <CardBody>
              <PathTree nodes={tree.nodes} repeat={tree.repeat} />
            </CardBody>
          </Card>
        </>
      ) : error === "" ? (
        <Card>
          <EmptyState
            icon={<BroadcastIcon width={18} height={18} />}
            title="No escalation policy"
            description={team.archived_at ? "This team is archived and cannot receive requests." : `Everyone on ${team.name} is notified at once when a path reaches it.`}
          />
        </Card>
      ) : null}

      <Card>
        <RevisionHistory
          aria-label="Team escalation policy revisions"
          items={page.revisions}
          totalCount={page.revisions.length}
          hasMore={false}
          activeRevision={activeRevision}
          description="Saved drafts stay here until you choose to activate them."
          onOpen={(revision) => void openRevision(revision, false)}
          onCreateFrom={canWrite && !team.archived_at ? (revision) => void openRevision(revision, true) : undefined}
          onActivate={canActivate && !team.archived_at ? (revision) => void activateRevision(revision) : undefined}
          activating={activating}
          authorName={(id) => members.find((member) => member.membership_id === id)?.display_name}
        />
      </Card>

      {viewing ? (
        <Dialog open onOpenChange={(open) => { if (!open) setViewing(null); }}>
          <DialogContent>
            <DialogTitle>Revision {viewing.revision}</DialogTitle>
            <DialogDescription>
              {viewing.is_active ? "The active policy revision." : "A saved revision. Activate it from the history to make it live."}
            </DialogDescription>
            <DialogBody>
              <WorkingHoursList document={fromWire(viewing, directory)} />
              {(() => {
                const viewedTree = teamPolicyTreeFromWire(team, viewing, [...vocabulary.targets, ...departedOf(viewing.warnings)]);
                return <PathTree nodes={viewedTree.nodes} repeat={viewedTree.repeat} />;
              })()}
            </DialogBody>
          </DialogContent>
        </Dialog>
      ) : null}

      {archiving ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && setArchiving(false)}
          title="Archive this escalation policy?"
          description={`The policy keeps its revision history. Until a revision is active again, a request reaching ${team.name} notifies everyone on it at once.`}
          confirmLabel="Archive policy"
          onConfirm={archive}
        />
      ) : null}

      {canWrite ? (
        <SaveBar
          count={dirtyCount}
          saving={saving}
          onDiscard={() => setDraft(saved)}
          onSave={() => void save(draft)}
        />
      ) : null}
    </Page>
  );
}
