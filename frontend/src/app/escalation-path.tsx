import { useMemo, useState } from "react";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Card, CardBody, CardHeader, CardTitle, CardDescription } from "@/ds/components/card";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import {
  Dialog,
  DialogBody,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/ds/components/dialog";
import { PathTree } from "@/ds/components/escalation-tree";
import { type ReviewerTarget } from "@/ds/components/reviewer-target";
import { SaveBar } from "@/ds/components/save-bar";
import { TextField } from "@/ds/components/text-field";
import { useToast } from "@/ds/components/toast";
import { Page } from "@/ds/components/page";
import { PageHeader, PageBackLink } from "@/ds/components/page-header";
import { RevisionHistory } from "@/ds/components/revision-history";
import { AlertIcon } from "@/ds/icons";
import { api, RequestError } from "../api";
import type {
  EscalationPathRevisionSummary,
  EscalationPathRevisionWire,
  EscalationPathWarning,
  Member,
  PipelineUse,
  RevisionPage,
  Team,
} from "../types";
import {
  countChanges,
  fromWire,
  newDraft,
  toWire,
  type EscalationDocument,
} from "./escalation-document";
import { EscalationDocumentEditor } from "./escalation-editor";
import { PipelineUseList } from "./pipeline-uses";
import { pathTreeFromWire } from "./reviewer-route";
import { emptyRevisionPage, useRevisionPages } from "./revision-pages";
import { loadTeamPaths, type TeamPaths } from "./team-policy-store";
import { useTitle } from "./use-title";
import { useRevisionActivation } from "./use-revision-activation";

/*
 * The organization escalation path screen: the name and key, the
 * shared document editor, the warnings the server attached, the
 * revision history, archiving, and saving. Save creates a new immutable
 * revision and offers activation. Requests already routed to the path keep
 * the revision they pinned.
 */

/* The key is what pipelines reference; it is fixed once the path exists.
 * "new" is the editor's own route and can never be a key. */
export const PATH_KEY_PATTERN = /^[a-z0-9][a-z0-9._-]{0,62}$/;
export const RESERVED_PATH_KEY = "new";

export function validPathKey(key: string): boolean {
  return PATH_KEY_PATTERN.test(key) && key !== RESERVED_PATH_KEY;
}

/* The path as the screen edits it: the key and name beside the
 * document. The key is only editable while the path is being created. */
export interface EscalationPathDraft {
  pathKey: string;
  name: string;
  document: EscalationDocument;
}

/* What the escalation path route loader delivers: the reviewer directory
 * the targets are named from, the teams' escalation policies, the active
 * revision translated to the editor's shape (a fresh draft when the key
 * has no revision yet), and the failure when the path could not be
 * read. */
export interface EscalationPathPage {
  directory: ReviewerTarget[];
  teamPaths: TeamPaths;
  activeRevision: number | null;
  saved: EscalationPathDraft | null;
  /* What the server said about the active revision: team targets whose
   * members cannot all decide. The path narrows and never grants, so
   * this is advice, not a refusal. */
  warnings: EscalationPathWarning[];
  /* The active pipeline revisions naming the path; archiving is refused
   * while any exist, so the dialog explains instead of confirming. */
  uses: PipelineUse[];
  /* The first page of the key's history, newest first; the screen
   * fetches the rest on demand. Activating an earlier revision rolls
   * the path back. */
  history: RevisionPage<EscalationPathRevisionSummary>;
  /* The path does not exist yet: the editor also asks for its key and
   * the first save creates it. */
  isNew: boolean;
  /* When the path was archived: read only, never active again. The
   * page shows its latest revision as saved. */
  archivedAt: string | null;
  archivedRevision: EscalationPathRevisionWire | null;
  error: string;
}

/* A key with no active revision borrows its key as the name; a path
 * being created has neither. */
function newPathDraft(pathKey: string): EscalationPathDraft {
  return { pathKey, name: pathKey, document: newDraft() };
}

function draftFromWire(revision: EscalationPathRevisionWire, directory: ReviewerTarget[]): EscalationPathDraft {
  return { pathKey: revision.path_key, name: revision.name, document: fromWire(revision, directory) };
}

/* The reviewer directory the targets are named from, and the teams'
 * escalation policies a team chip opens inline. Targets still render by
 * id without the directory, so a failed listing is not an error. */
async function loadVocabulary(): Promise<{ directory: ReviewerTarget[]; teamPaths: TeamPaths }> {
  const [members, teams] = await Promise.all([
    api.members().then((result) => result.members, () => [] as Member[]),
    api.teams().then((result) => result.teams, () => [] as Team[]),
  ]);
  const directory: ReviewerTarget[] = [
    ...teams.map<ReviewerTarget>((entry) => ({ type: "team", id: entry.id, name: entry.name })),
    ...members
      .filter((member) => member.status === "active")
      .map<ReviewerTarget>((member) => ({
        type: "user",
        id: member.id,
        name: member.display_name || member.email,
      })),
  ];
  const teamPaths = await loadTeamPaths(teams, directory);
  return { directory, teamPaths };
}

/* The editor for a path that does not exist yet: the vocabulary only. */
export async function loadNewEscalationPathPage(): Promise<EscalationPathPage> {
  const { directory, teamPaths } = await loadVocabulary();
  return {
    directory,
    teamPaths,
    activeRevision: null,
    saved: newPathDraft(""),
    warnings: [],
    uses: [],
    history: emptyRevisionPage(),
    isNew: true,
    archivedAt: null,
    archivedRevision: null,
    error: "",
  };
}

export async function loadEscalationPathPage(pathKey: string): Promise<EscalationPathPage> {
  const [{ directory, teamPaths }, history, uses, revision] = await Promise.all([
    loadVocabulary(),
    api.escalationPathRevisions(pathKey).then(
      (result) => result,
      () => emptyRevisionPage<EscalationPathRevisionSummary>(),
    ),
    api.escalationPathUses(pathKey).then((result) => result.uses, () => [] as PipelineUse[]),
    api.activeEscalationPath(pathKey).then(
      (result) => ({ revision: result, error: "" }),
      (reason: unknown) => ({
        revision: null,
        error:
          reason instanceof RequestError && reason.status === 404
            ? ""
            : reason instanceof Error
              ? reason.message
              : "Unable to load the escalation path.",
      }),
    ),
  ]);
  const revisions = history.revisions;
  const archivedAt = revisions[0]?.archived_at ?? null;
  if (revision.error) {
    return {
      directory,
      teamPaths,
      activeRevision: null,
      saved: null,
      warnings: [],
      uses,
      history,
      isNew: false,
      archivedAt,
      archivedRevision: null,
      error: revision.error,
    };
  }
  if (!revision.revision) {
    /* An archived path has no active revision; show its latest one. */
    const latest =
      archivedAt && revisions.length > 0
        ? await api.escalationPathRevision(pathKey, revisions[0].revision).catch(() => null)
        : null;
    return {
      directory,
      teamPaths,
      activeRevision: null,
      saved: latest ? draftFromWire(latest, directory) : newPathDraft(pathKey),
      warnings: [],
      uses,
      history,
      isNew: false,
      archivedAt,
      archivedRevision: latest,
      error: "",
    };
  }
  return {
    directory,
    teamPaths,
    activeRevision: revision.revision.revision,
    saved: draftFromWire(revision.revision, directory),
    warnings: revision.revision.warnings,
    uses,
    history,
    isNew: false,
    archivedAt,
    archivedRevision: null,
    error: "",
  };
}

/* canWrite covers saving a revision; canActivate covers making it the
 * live one and archiving the path. An archived path is read only for
 * everyone. Saving offers activation when both are held; with write
 * alone the revision is saved as a draft for someone who can activate it. */
export function EscalationPathScreen({
  pathKey,
  page,
  canWrite,
  canActivate,
}: {
  pathKey: string;
  page: EscalationPathPage;
  canWrite: boolean;
  canActivate: boolean;
}) {
  const {
    directory,
    teamPaths,
    activeRevision,
    saved,
    uses,
    isNew,
    archivedAt,
    archivedRevision,
    error,
  } = page;
  const history = useRevisionPages(page.history, (cursor) => api.escalationPathRevisions(pathKey, cursor));
  const archived = archivedAt !== null;
  const archivedTree = archivedRevision ? pathTreeFromWire(archivedRevision, directory) : null;
  const editable = canWrite && !archived;
  const switchable = canActivate && !archived;
  useTitle(isNew ? "New escalation path" : "Escalation path");
  const toast = useToast();
  const revisionActivation = useRevisionActivation();
  const router = useRouter();
  const navigate = useNavigate();
  /* The draft belongs to the page it was edited from: a save or archive
   * reloads the page and the editor shows the saved document again. */
  const [edit, setEdit] = useState<{
    base: EscalationPathPage;
    draft: EscalationPathDraft;
  } | null>(null);
  const draft = edit?.base === page ? edit.draft : saved;
  const setDraft = (next: EscalationPathDraft) => setEdit({ base: page, draft: next });
  /* The warnings of the revision saved last, shown even when it is not
   * the active one the reloaded page describes. */
  const [created, setCreated] = useState<{
    pathKey: string;
    warnings: EscalationPathWarning[];
  } | null>(null);
  const warnings = created?.pathKey === pathKey ? created.warnings : page.warnings;
  const [saving, setSaving] = useState(false);
  const [archiving, setArchiving] = useState(false);
  /* History actions: which revision is being activated, and the one
   * opened read-only. */
  const [activating, setActivating] = useState<number | null>(null);
  const [viewing, setViewing] = useState<EscalationPathRevisionWire | null>(null);
  const vocabulary = useMemo(() => ({ kind: "organization" as const, targets: directory }), [directory]);

  function personName(membershipId: string): string {
    return directory.find((entry) => entry.type === "user" && entry.id === membershipId)?.name ?? "";
  }

  async function activateRevision(revision: EscalationPathRevisionSummary) {
    setActivating(revision.revision);
    try {
      await api.activateEscalationPathRevision(pathKey, revision.revision, activeRevision);
      toast.add({
        title:
          activeRevision !== null && revision.revision < activeRevision
            ? `Rolled back to revision ${revision.revision}`
            : `Revision ${revision.revision} active`,
        description: "New requests use this revision.",
      });
      await router.invalidate();
    } catch (reason) {
      if (reason instanceof RequestError && reason.status === 412) {
        await router.invalidate();
        toast.add({
          title: "Activation refused",
          description:
            "Someone else changed the active revision. Review the history and try again.",
          type: "deny",
        });
        return;
      }
      toast.add({
        title: "Activation failed",
        description: reason instanceof Error ? reason.message : undefined,
        type: "deny",
      });
    } finally {
      setActivating(null);
    }
  }

  async function openRevision(revision: EscalationPathRevisionSummary) {
    try {
      setViewing(await api.escalationPathRevision(pathKey, revision.revision));
    } catch (reason) {
      toast.add({
        title: "The revision could not be loaded",
        description: reason instanceof Error ? reason.message : undefined,
        type: "deny",
      });
    }
  }

  /* The editor takes this revision's document as its draft; saving
   * creates a new revision from it. The list holds only summaries, so
   * the full document is fetched first. */
  async function startFrom(revision: EscalationPathRevisionSummary) {
    try {
      const wire = await api.escalationPathRevision(pathKey, revision.revision);
      setDraft(draftFromWire(wire, directory));
      toast.add({
        title: `Editing from revision ${revision.revision}`,
        description: "Save to create a new revision.",
      });
    } catch (reason) {
      toast.add({
        title: "The revision could not be loaded",
        description: reason instanceof Error ? reason.message : undefined,
        type: "deny",
      });
    }
  }

  /* A new path is created outright. For an existing key the page cannot
   * tell a path with no active revision from one that was archived
   * (both read as 404), so the next revision is tried first and a
   * missing path is created on the way. */
  async function createRevision(current: EscalationPathDraft) {
    const wire = { name: current.name, ...toWire(current.document) };
    if (isNew) {
      return await api.createEscalationPath(current.pathKey, wire);
    }
    try {
      return await api.createEscalationPathRevision(pathKey, wire);
    } catch (reason) {
      if (activeRevision === null && reason instanceof RequestError && reason.status === 404) {
        return await api.createEscalationPath(pathKey, wire);
      }
      throw reason;
    }
  }

  async function save(current: EscalationPathDraft) {
    setSaving(true);
    try {
      const created = await createRevision(current);
      const key = created.path_key;
      await revisionActivation.offer({
        revision: created.revision,
        activeRevision,
        canActivate,
        activationDescription: "New requests will use this revision.",
        activate: () => api.activateEscalationPathRevision(key, created.revision, activeRevision),
      });
      setCreated({ pathKey: key, warnings: created.warnings });
      if (isNew) {
        await navigate({ to: "/escalation-paths/$pathKey", params: { pathKey: key }, replace: true });
      } else {
        await router.invalidate();
      }
    } catch (reason) {
      toast.add({
        title: "Save failed",
        description: reason instanceof Error ? reason.message : "Unable to save the escalation path.",
        type: "deny",
      });
    } finally {
      setSaving(false);
    }
  }

  async function archive() {
    try {
      await api.archiveEscalationPath(pathKey);
      toast.add({
        title: "Escalation path archived",
        description: "Pipelines can no longer use it.",
      });
      setArchiving(false);
      await router.invalidate();
    } catch (reason) {
      if (reason instanceof RequestError && reason.code === "escalation_path_in_use") {
        /* Another administrator activated a revision naming this path
         * after the page loaded; reload so the dialog lists it. */
        await router.invalidate();
        toast.add({
          title: "Archive refused",
          description: "An active pipeline uses this path.",
          type: "deny",
        });
        return;
      }
      toast.add({
        title: "Archive failed",
        description: reason instanceof Error ? reason.message : undefined,
        type: "deny",
      });
    }
  }

  const dirtyCount = useMemo(() => {
    if (!draft || !saved) return 0;
    return (
      (draft.name !== saved.name ? 1 : 0) +
      countChanges(draft.document, saved.document, activeRevision !== null)
    );
  }, [draft, saved, activeRevision]);

  /* A path being created also needs a name and a usable key; the save
   * bar waits for both. */
  const keyError =
    !isNew || draft === null || draft.pathKey === ""
      ? ""
      : !PATH_KEY_PATTERN.test(draft.pathKey)
        ? "Lowercase letters, digits, dots, dashes, and underscores; up to 63 characters."
        : draft.pathKey === RESERVED_PATH_KEY
          ? "This key is reserved."
          : "";
  const canSave =
    !isNew || (draft !== null && draft.name.trim() !== "" && validPathKey(draft.pathKey));

  if (error || !draft) {
    return (
      <Page width="narrow" className="gap-3">
        <PageHeader back={<PageBackLink to="/escalation-paths">Escalation paths</PageBackLink>} />
        <p className="text-body text-ink-muted">{error || "Unable to load the escalation path."}</p>
      </Page>
    );
  }

  return (
    <Page width="narrow">
      {revisionActivation.dialog}
      <PageHeader back={<PageBackLink to="/escalation-paths">Escalation paths</PageBackLink>}>
        <div className="flex items-start justify-between gap-4">
          <div>
            <h1 className="text-title-lg text-ink">
              {isNew ? "New escalation path" : "Escalation path"}
            </h1>
            <p className="mt-1.5 max-w-[560px] text-body text-ink-muted">
              Which humans get asked when an approval pipeline routes a request to this path.{" "}
              {archived || canActivate
                ? null
                : isNew
                  ? "Someone with the activate permission must make it active."
                  : "Someone with the activate permission must make new revisions active."}
            </p>
          </div>
          {canActivate && !isNew && !archived ? (
            <Button variant="ghost" size="sm" onClick={() => setArchiving(true)}>
              Archive path
            </Button>
          ) : null}
        </div>
      </PageHeader>

      {archived ? (
        <Callout tone="warn" title={`Archived ${new Date(archivedAt).toLocaleString()}`}>
          This path can't be changed or used by pipelines.
        </Callout>
      ) : null}

      <div className="flex flex-wrap items-start gap-4">
        <TextField
          label="Name"
          value={draft.name}
          onChange={(event) => setDraft({ ...draft, name: event.target.value })}
          placeholder="Payments review"
          className="w-72"
          autoFocus={isNew}
          readOnly={archived}
        />
        {isNew ? (
          <TextField
            label="Key"
            mono
            value={draft.pathKey}
            onChange={(event) =>
              setDraft({ ...draft, pathKey: event.target.value.trim().toLowerCase() })
            }
            placeholder="payments"
            className="w-72"
            error={keyError}
            info="The path's unique identifier. It can't be changed later."
            docsLinks={[{ href: "https://withhuman.ai/docs/web-app/escalation-paths#paths", label: "Creating escalation paths" }]}
          />
        ) : (
          <div className="flex flex-col gap-1.5">
            <span className="text-label text-ink-secondary">Key</span>
            <div className="flex h-[30px] items-center gap-3">
              <span className="font-mono text-[12px] text-ink-faint">{draft.pathKey}</span>
              <span className="text-micro font-normal text-ink-faint">
                {archived
                  ? "archived"
                  : activeRevision === null
                    ? "no active revision"
                    : `active revision ${activeRevision}`}
              </span>
            </div>
          </div>
        )}
      </div>

      {warnings.map((warning) =>
        warning.code === "path_may_reach_nobody" ? (
          <Callout
            key={warning.code}
            tone="warn"
            icon={<AlertIcon width={15} height={15} />}
            title="Some requests will reach nobody"
          >
            Add a level outside the branches, or an else branch with one.
          </Callout>
        ) : (
          <Callout
            key={`${warning.code}:${warning.team_id}`}
            tone="warn"
            icon={<AlertIcon width={15} height={15} />}
            title={`${warning.count} of ${warning.member_count} members of ${warning.team_name} cannot decide requests`}
          >
            Give them permission to decide requests.
          </Callout>
        ),
      )}

      {archived ? (
        <Card>
          <CardHeader>
            <div>
              <CardTitle>Steps</CardTitle>
              <CardDescription>
                {archivedRevision
                  ? `Revision ${archivedRevision.revision}, as it was saved.`
                  : "The path's last revision could not be loaded."}
              </CardDescription>
            </div>
          </CardHeader>
          <CardBody>
            {archivedTree && archivedTree.nodes.length > 0 ? (
              <PathTree nodes={archivedTree.nodes} repeat={archivedTree.repeat} depth={1} />
            ) : (
              <p className="text-body text-ink-muted">This revision has no steps.</p>
            )}
          </CardBody>
        </Card>
      ) : (
        <EscalationDocumentEditor
          value={draft.document}
          onChange={(document) => setDraft({ ...draft, document })}
          vocabulary={vocabulary}
          teamPaths={teamPaths}
        />
      )}

      {!isNew ? (
        <Card>
          <RevisionHistory
            aria-label="Escalation path revisions"
            description={archived ? "None can be activated again." : undefined}
            items={history.items}
            totalCount={history.totalCount}
            hasMore={history.hasMore}
            loadingMore={history.loadingMore}
            onLoadMore={history.loadMore}
            summaryLabel="Name"
            summary={(revision) => <span title={revision.name}>{revision.name}</span>}
            authorName={(membershipId) => personName(membershipId) || undefined}
            activeRevision={activeRevision}
            onOpen={(revision) => void openRevision(revision)}
            onActivate={switchable ? (revision) => void activateRevision(revision) : undefined}
            activating={activating}
            onCreateFrom={editable ? (revision) => void startFrom(revision) : undefined}
          />
        </Card>
      ) : null}

      {viewing ? (
        <Dialog open onOpenChange={(open) => !open && setViewing(null)}>
          <DialogContent>
            <DialogTitle>
              Revision {viewing.revision} · {viewing.name}
            </DialogTitle>
            <DialogDescription>
              {viewing.is_active
                ? "This revision is active."
                : "This revision is inactive."}
            </DialogDescription>
            <DialogBody>
              {viewing.nodes.length === 0 ? (
                <p className="text-body text-ink-muted">This revision has no steps.</p>
              ) : (
                (() => {
                  const tree = pathTreeFromWire(viewing, directory);
                  return <PathTree nodes={tree.nodes} repeat={tree.repeat} depth={1} />;
                })()
              )}
            </DialogBody>
            <DialogFooter>
              <DialogClose render={<Button variant="ghost">Close</Button>} />
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}

      {archiving && uses.length > 0 ? (
        <Dialog open onOpenChange={(open) => !open && setArchiving(false)}>
          <DialogContent>
            <DialogTitle>This path is in use</DialogTitle>
            <DialogDescription>
              {uses.length === 1
                ? "An active pipeline uses this path. Remove it from that pipeline, then archive it."
                : `${uses.length} active pipelines use this path. Remove it from those pipelines, then archive it.`}
            </DialogDescription>
            <DialogBody>
              <PipelineUseList uses={uses} />
            </DialogBody>
            <DialogFooter>
              <DialogClose render={<Button variant="ghost">Close</Button>} />
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : archiving ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && setArchiving(false)}
          title="Archive this escalation path?"
          description="Pipelines will no longer be able to use it. This can't be undone."
          confirmLabel="Archive path"
          onConfirm={archive}
        />
      ) : null}

      {editable ? (
        <SaveBar
          count={canSave ? dirtyCount : 0}
          saving={saving}
          onDiscard={() => saved && setDraft(saved)}
          onSave={() => void save(draft)}
        />
      ) : null}
    </Page>
  );
}
