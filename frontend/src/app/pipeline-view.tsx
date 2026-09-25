import type { ToolSchema } from "@/ds/lib/tool-schema";
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { cn } from "@/ds/lib/cn";
import { Badge } from "@/ds/components/badge";
import { IconButton } from "@/ds/components/icon-button";
import { Menu, MenuContent, MenuTrigger } from "@/ds/components/menu";
import { useAccess } from "./authorization";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Card } from "@/ds/components/card";
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
import {
  PipelineConnector,
  PipelineEntryCard,
  PipelineFoldedSection,
  PipelineTerminusCard,
} from "@/ds/components/policy-block";
import { Referenced } from "@/ds/components/reference";
import { ReferenceChip } from "./reference-chip";
import { TargetBadge, TargetSelect, type ReviewerTarget } from "@/ds/components/reviewer-target";
import type { WebhookEndpointOption } from "@/ds/components/webhook-block";
import { SaveBar } from "@/ds/components/save-bar";
import { TextArea } from "@/ds/components/text-area";
import { ToggleGroup, ToggleGroupItem } from "@/ds/components/toggle-group";
import { useToast } from "@/ds/components/toast";
import { Page } from "@/ds/components/page";
import { PageHeader, PageBackLink, PageBackButton } from "@/ds/components/page-header";
import { RevisionHistory } from "@/ds/components/revision-history";
import { AlertIcon, ChevronDownIcon, EllipsisIcon } from "@/ds/icons";
import { api, RequestError } from "../api";
import type {
  AgentSummary,
  PipelineReferences,
  RequestApprovalBlockDefinition,
  RequestApprovalPipelinePreview,
  RequestApprovalPipelinePreviewSample,
  RequestApprovalPipelineRevision,
  RequestApprovalPipelineRevisionSummary,
  RevisionPage,
} from "../types";
import { emptyRevisionPage, useRevisionPages } from "./revision-pages";
import { blockProblemLabel } from "./block-errors";
import { blockKind, extensions } from "./extensions";
import {
  defaultSample,
  documentFromDrafts,
  draftsFromBlocks,
  knownBlockTypes,
  loadPathDirectory,
  pathKeyOf,
  pathTarget,
  suggestPaths,
  takeTestSample,
  validateDrafts,
  type BlockDraft,
} from "./pipeline-blocks";
import {
  allConditions,
  changedBlockCount,
  countDrafts,
  findDraft,
  pipelineReachability,
  positionLabel,
  walkDrafts,
} from "./pipeline-tree";
import {
  carriedMetadata,
  runFromPreviews,
  type RailSection,
  type TestRun,
} from "./pipeline-replay";
import {
  PipelineRail,
  RailProvider,
  UnreachableNotice,
  useRailEditor,
  type RailState,
} from "./pipeline-rail";
import { useTitle } from "./use-title";
import { useRevisionActivation } from "./use-revision-activation";
import { conditionFieldPaths, conditionValueSuggestions, loadPipelineSuggestions, mergeValueSuggestions, sampleValueSuggestions } from "./pipeline-suggestions";
import { FIXED_FIELDS, type ConditionValueDetails, type ConditionValueSuggestions } from "@/ds/components/condition-builder";

const FIXED_FIELD_PATHS = new Set(FIXED_FIELDS.map((field) => field.path));

/*
 * One pipeline, viewed or edited in place. The rail keeps the same
 * scaffolding in both modes: entry, the folded organization section
 * (agent scope only), this pipeline's blocks with their exits and
 * branches, and the terminus. Edit swaps the read-only rows for
 * editable cards where they stand; Create revision commits a new
 * immutable revision, which stays inactive until activated from the
 * history below.
 */

const REVISIONS_INFO =
  "Every change you make to a pipeline creates a new revision. A revision is only running when it is made active. You can roll back to any revision at any time.";

type PendingAction = { kind: "activate"; revision: RequestApprovalPipelineRevisionSummary } | null;

/* The agent's display name when the inventory knows it, else the slug
 * in mono; the header shows the slug beside a named title. */
function PipelineTitle({ agentSlug, agentName }: { agentSlug: string; agentName?: string }) {
  if (agentSlug === "") return <>Organization pipeline</>;
  if (agentName) return <>{agentName} pipeline</>;
  return (
    <>
      <span className="font-mono text-[19px]">{agentSlug}</span> pipeline
    </>
  );
}

/* The organization pipeline's tail: no match does not decide, execution
 * continues to the agent's own pipeline, and this is where the
 * organization default lives. It covers every agent without a default of
 * its own. */
function ContinueCard({
  target,
  directory,
  onTargetChange,
  route,
}: {
  target: ReviewerTarget | null;
  directory?: ReviewerTarget[];
  onTargetChange?: (next: ReviewerTarget | null) => void;
  route?: ReactNode;
}) {
  const editing = Boolean(directory && onTargetChange);
  return (
    <div className="flex items-center gap-3 rounded-lg border-hairline border-edge bg-surface px-4 py-2.5 shadow-sm">
      <span className="flex text-ink-muted">
        <ChevronDownIcon width={14} height={14} />
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <p className="text-body text-ink-secondary">Next: check the agent's pipeline</p>
        <p className="text-micro text-ink-muted">
          If those blocks still leave the request undecided, ask these reviewers unless the
          agent has its own reviewers.
        </p>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-micro text-ink-muted">Default reviewers</span>
          {editing ? (
            <TargetSelect
              directory={directory ?? []}
              value={target}
              onChange={(next) => onTargetChange?.(next)}
              fallbackLabel="Any authorized reviewer"
              aria-label="Organization default reviewers"
              className="w-56"
            />
          ) : (
            <TargetBadge target={target} fallbackLabel="Any authorized reviewer" />
          )}
        </div>
        {route ? <div className="mt-1.5">{route}</div> : null}
      </div>
    </div>
  );
}

/* The test entry point: a dialog with the sample request. Running it
 * closes the dialog and replays the evaluation on the rail itself. The
 * overview reuses it with its own description, since there the run
 * opens the agent's pipeline first. */
export function TestRequestDialog({
  open,
  onOpenChange,
  sampleText,
  onSampleTextChange,
  onRun,
  description = "See what would happen to a sample request, one block at a time. This does not create a real approval request.",
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sampleText: string;
  onSampleTextChange: (next: string) => void;
  /* Resolves to an error message to show, or null when the run
   * started and the dialog should close. */
  onRun: () => Promise<string | null>;
  description?: string;
}) {
  const [error, setError] = useState("");
  const [running, setRunning] = useState(false);
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setError("");
        onOpenChange(next);
      }}
    >
      <DialogContent className="w-[min(560px,calc(100vw-32px))]">
        <DialogTitle>Test a request</DialogTitle>
        <DialogDescription>{description}</DialogDescription>
        <DialogBody className="flex flex-col gap-3">
          {error ? (
            <Callout tone="warn" icon={<AlertIcon width={15} height={15} />}>
              {error}
            </Callout>
          ) : null}
          <TextArea
            aria-label="Test request"
            spellCheck={false}
            className="min-h-[260px] resize-y bg-canvas font-mono text-[11px] leading-[18px] shadow-none"
            value={sampleText}
            onChange={(event) => {
              onSampleTextChange(event.target.value);
              setError("");
            }}
          />
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" disabled={running} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={running}
            onClick={() => {
              try {
                JSON.parse(sampleText);
              } catch {
                setError("The sample is not valid JSON.");
                return;
              }
              setRunning(true);
              void onRun()
                .then((problem) => {
                  if (problem) setError(problem);
                })
                .finally(() => setRunning(false));
            }}
          >
            Run test
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/* What the pipeline view route loaders deliver: this pipeline's history
 * with its active (or, failing that, latest) revision in full, the
 * organization's active revision for the folded section, the
 * escalation paths a human exit may name with each path's active tree,
 * and which block types this deployment can run. A failed pipeline load
 * is reported by the screen rather than thrown, so an expired session
 * reaches the sign-in gate, not the router's error boundary. */
export interface PipelineViewPage {
  /* The first page of the scope's history, newest first; the screen
   * fetches the rest on demand. */
  history: RevisionPage<RequestApprovalPipelineRevisionSummary>;
  /* The active revision in full: what the read-only view shows. */
  displayed: RequestApprovalPipelineRevision | null;
  /* The immutable revision named by an incoming review link. */
  linkedRevision: RequestApprovalPipelineRevision | null;
  latest: RequestApprovalPipelineRevision | null;
  orgActive: RequestApprovalPipelineRevision | null;
  /* Every agent has a pipeline with at least its first revision, so an
   * empty history (or a 404) means no agent has this slug. */
  missing: boolean;
  /* When the pipeline was archived with its agent: read only, no active
   * revision, until the agent is restored. */
  archivedAt: string | null;
  error: string;
  /* The organization's escalation paths, the choices for a human exit
   * and for the pipeline default. */
  roster: ReviewerTarget[];
  /* The agent this pipeline belongs to, when the inventory lists it, so
   * the title can show its name. Empty for the organization scope. */
  sourceAgents: Pick<AgentSummary, "slug" | "name">[];
  valueSuggestions: ConditionValueSuggestions;
  valueDetails: ConditionValueDetails;
  /* Argument fields the scope's agents have sent, most common first. */
  argumentPaths: string[];
  toolSchemas?: ToolSchema[];
  /* The newest real request, seeding the test sample; null before the
   * first request, when the placeholder stands in. */
  sample: RequestApprovalPipelinePreviewSample | null;
  /* Which of the block kinds that depend on deployment configuration
   * (webhook, and every kind the edition adds) this deployment can run,
   * by type. */
  availability: Record<string, boolean>;
  /* The organization's endpoints, for the webhook card's picker. */
  endpointOptions: WebhookEndpointOption[];
}

/* The scope's active revision comes from its own read rather than from
 * the history, so a rollback far down the list still shows the right
 * document; the latest revision is fetched in full only when it differs.
 * A scope archived with its agent has no active revision (the read is a
 * 404): its latest revision stands in, read only, and archivedAt says
 * when. */
async function loadPipelineRevisions(agentSlug: string) {
  const [history, active] = await Promise.all([
    api.requestApprovalPipelineRevisions(agentSlug),
    api.activeRequestApprovalPipeline(agentSlug).catch((reason: unknown) => {
      if (reason instanceof RequestError && reason.status === 404) return null;
      throw reason;
    }),
  ]);
  const latestSummary = history.revisions[0] ?? null;
  const latest =
    latestSummary === null
      ? null
      : active !== null && latestSummary.revision === active.revision
        ? active
        : await api.requestApprovalPipelineRevision(agentSlug, latestSummary.revision);
  const archivedAt = active === null ? (latestSummary?.archived_at ?? null) : null;
  return { history, displayed: active ?? latest, latest, active, archivedAt };
}

/* Whether this deployment can run webhook blocks and the kinds the
 * edition adds decides what the add slot offers; assume yes when the
 * config cannot be read. */
async function loadBlockAvailability(): Promise<Pick<PipelineViewPage, "availability" | "endpointOptions">> {
  try {
    const config = await api.runtimeConfig();
    const endpointOptions: WebhookEndpointOption[] = config.webhook_enabled
      ? await api.webhookEndpoints().then(
          (result) =>
            result.webhook_endpoints.map((endpoint) => ({
              key: endpoint.endpoint_key,
              name: endpoint.name,
              url: endpoint.url,
              active: endpoint.active_revision !== undefined,
            })),
          () => [],
        )
      : [];
    return {
      availability: {
        webhook: config.webhook_enabled,
        ...Object.fromEntries(extensions.blockKinds.map((kind) => [kind.type, kind.available(config)])),
      },
      endpointOptions,
    };
  } catch {
    return { availability: {}, endpointOptions: [] };
  }
}

export async function loadPipelineView(agentSlug: string, revision?: number): Promise<PipelineViewPage> {
  const [pipeline, vocabulary, availability, suggestions] = await Promise.all([
    Promise.all([
      loadPipelineRevisions(agentSlug).then(async (own) => ({
        ...own,
        linkedRevision: revision === undefined ? null
          : own.active?.revision === revision ? own.active
          : own.latest?.revision === revision ? own.latest
          : await api.requestApprovalPipelineRevision(agentSlug, revision),
      })),
      /* The organization section folds above an agent pipeline; it is
       * decoration there, so its failure is not the page's. */
      agentSlug === ""
        ? Promise.resolve(null)
        : loadPipelineRevisions("").then((result) => result.active, () => null),
    ]).then(
      ([own, orgActive]) => ({
        history: own.history,
        displayed: own.displayed,
        linkedRevision: own.linkedRevision,
        latest: own.latest,
        orgActive,
        missing: own.history.revisions.length === 0,
        archivedAt: own.archivedAt,
        error: "",
      }),
      (reason: unknown) => ({
        history: emptyRevisionPage<RequestApprovalPipelineRevisionSummary>(),
        displayed: null,
        linkedRevision: null,
        latest: null,
        orgActive: null,
        missing: revision === undefined && reason instanceof RequestError && reason.status === 404,
        archivedAt: null,
        error:
          revision !== undefined && reason instanceof RequestError && reason.status === 404
            ? `Revision ${revision} could not be found.`
            : reason instanceof RequestError && reason.status === 404
            ? ""
            : reason instanceof Error
              ? reason.message
              : "Unable to load the pipeline.",
      }),
    ),
    loadPathDirectory().then((roster) => ({ roster })),
    loadBlockAvailability(),
    loadPipelineSuggestions(agentSlug),
  ]);
  return { ...pipeline, ...vocabulary, ...availability, ...suggestions };
}

/* canWrite covers drafting, saving, and previewing a revision; canActivate
 * covers activating and rolling back. The routes compute both against
 * the pipeline's own resource (the organization or the agent). */
export function PipelineViewScreen({
  agentSlug,
  page,
  canWrite,
  canActivate,
  initiallyEditing = false,
  focusedBlock,
}: {
  agentSlug: string;
  page: PipelineViewPage;
  canWrite: boolean;
  canActivate: boolean;
  initiallyEditing?: boolean;
  focusedBlock?: string;
}) {
  const navigate = useNavigate();
  const { can } = useAccess();
  const router = useRouter();
  const toast = useToast();
  const revisionActivation = useRevisionActivation();
  const organization = agentSlug === "";
  const {
    displayed: activeRevision,
    linkedRevision,
    latest,
    orgActive,
    missing,
    archivedAt,
    error,
    roster,
    sourceAgents,
    availability,
    endpointOptions,
  } = page;
  const agentName = sourceAgents.find((agent) => agent.slug === agentSlug)?.name;
  useTitle(organization ? "Organization pipeline" : `${agentName ?? agentSlug} pipeline`);
  const history = useRevisionPages(page.history, (cursor) =>
    api.requestApprovalPipelineRevisions(agentSlug, cursor),
  );
  const [conflict, setConflict] = useState("");
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [pendingAction, setPendingAction] = useState<PendingAction>(null);
  /* What the revision about to be activated depends on, fetched when the
   * action is chosen; "unavailable" when the check itself failed, in
   * which case the server's own guard still applies. */
  const [references, setReferences] = useState<PipelineReferences | "unavailable" | null>(null);
  /* Selection belongs to this loaded page. Saving or activating reloads
   * the page and returns the view to the current active revision. */
  const [selection, setSelection] = useState<{
    page: PipelineViewPage;
    revision: RequestApprovalPipelineRevision;
  } | null>(null);
  const displayed = selection?.page === page ? selection.revision : linkedRevision ?? activeRevision;
  const revisionRequest = useRef(0);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const blockRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (pendingAction?.kind !== "activate") {
      setReferences(null);
      return;
    }
    let stale = false;
    const revision = pendingAction.revision.revision;
    api
      .requestApprovalPipelineRevisionReferences(agentSlug, revision)
      .then((result) => {
        if (!stale) setReferences(result);
      })
      .catch(() => {
        if (!stale) setReferences("unavailable");
      });
    return () => {
      stale = true;
    };
  }, [pendingAction, agentSlug]);

  /* Editing state: draft accumulates changes against a baseline
   * snapshot taken when editing starts. */
  const [editing, setEditing] = useState(initiallyEditing && canWrite && archivedAt === null);
  const highlightedBlock = !editing && selection?.page !== page ? focusedBlock : undefined;
  useEffect(() => {
    if (!highlightedBlock) return;
    blockRef.current?.scrollIntoView({ block: "center" });
    blockRef.current?.focus({ preventScroll: true });
  }, [page, highlightedBlock]);
  const [draft, setDraft] = useState<BlockDraft[]>(() =>
    editing ? draftsFromBlocks((latest ?? displayed)?.blocks ?? [], roster) : [],
  );
  const [baseline, setBaseline] = useState<BlockDraft[]>(draft);
  /* The pipeline default under edit, against its baseline. */
  const [draftDefault, setDraftDefault] = useState<ReviewerTarget | null>(() =>
    editing ? pathTarget((latest ?? displayed)?.default_escalation_path, roster) : null,
  );
  const [baselineDefault, setBaselineDefault] = useState<ReviewerTarget | null>(draftDefault);
  /* The one expanded card, as `${section}/${block id}`, so the folded
   * organization section and this one never open each other's cards. */
  const [expandedKey, setExpandedKey] = useState<string | null>(null);
  const [orgFoldOpen, setOrgFoldOpen] = useState(false);
  const [mode, setMode] = useState<"blocks" | "json">("blocks");
  const [jsonText, setJsonText] = useState("");
  const [jsonError, setJsonError] = useState("");
  const [saving, setSaving] = useState(false);

  /* Testing: a dialog collects the sample, then the rail replays the
   * evaluation block by block. */
  const [testOpen, setTestOpen] = useState(false);
  const [test, setTest] = useState<TestRun | null>(null);
  const [cursor, setCursor] = useState(0);
  const [sampleText, setSampleText] = useState(() =>
    JSON.stringify(page.sample ?? defaultSample(organization ? "example" : agentSlug), null, 2),
  );
  const settled = test !== null && cursor >= test.steps.length;

  /* Structural edits, drag reordering, and the confirmation before a
   * branch with blocks is removed. Dragging collapses the open card. */
  const railEditor = useRailEditor(setDraft, () => setExpandedKey(null));

  const directory = roster;
  /* This page's own section: the organization's, or an agent's. */
  const ownSection: RailSection = organization ? "organization" : "agent";

  /* An archived pipeline remains readable, but has no active revision. */
  const archived = archivedAt !== null;
  const editable = canWrite && !archived;
  const activeNumber = archived ? null : (activeRevision?.revision ?? null);
  const viewingInactive = displayed !== null && displayed.revision !== activeNumber;
  const orgBlocks = useMemo(
    () => draftsFromBlocks(orgActive?.blocks ?? [], directory),
    [orgActive, directory],
  );
  const viewBlocks = useMemo(
    () => draftsFromBlocks(displayed?.blocks ?? [], directory),
    [displayed, directory],
  );
  const orgDefault = useMemo(
    () => pathTarget(orgActive?.default_escalation_path, directory),
    [orgActive, directory],
  );
  const viewDefault = useMemo(
    () => pathTarget(displayed?.default_escalation_path, directory),
    [displayed, directory],
  );
  const jsonDraft = useMemo(() => {
    if (!editing || mode !== "json") return null;
    try {
      const document = parseDocument(jsonText);
      return { blocks: draftsFromBlocks(document.blocks, directory), target: pathTarget(document.default_escalation_path, directory) };
    } catch {
      return null;
    }
  }, [editing, mode, jsonText, directory]);
  const currentDefault = editing ? (jsonDraft ? jsonDraft.target : draftDefault) : viewDefault;

  const effectiveDefault = currentDefault ?? orgDefault;
  const describeTargetRoute = (target: ReviewerTarget | null) =>
    target ? "" : `Uses pipeline default: ${effectiveDefault?.name ?? "Any authorized reviewer"}.`;
  const defaultRoute = editing && effectiveDefault ? (
    <div className="flex flex-wrap items-center gap-1 text-micro text-ink-muted">
      {!currentDefault ? <span>Uses organization default:</span> : null}
      <TargetBadge target={effectiveDefault} />
    </div>
  ) : undefined;
  /* Every condition the saved pipelines test, branches' included. */
  const savedConditions = useMemo(
    () => [...allConditions(viewBlocks), ...allConditions(orgBlocks)],
    [viewBlocks, orgBlocks],
  );
  /* Fields to pick from: the fixed ones, then what the agents have
   * actually sent, then what saved conditions compare, then whatever the
   * current sample holds. */
  const suggestions = useMemo(() => [...new Set([
    ...page.argumentPaths,
    ...conditionFieldPaths(savedConditions),
    ...suggestPaths(sampleText),
  ])].sort((a, b) => Number(FIXED_FIELD_PATHS.has(b)) - Number(FIXED_FIELD_PATHS.has(a))), [page.argumentPaths, savedConditions, sampleText]);
  const valueSuggestions = useMemo(() => mergeValueSuggestions(
    page.valueSuggestions,
    sampleValueSuggestions(sampleText),
    conditionValueSuggestions(savedConditions),
  ), [page.valueSuggestions, sampleText, savedConditions]);
  const railBlocks = editing ? (jsonDraft?.blocks ?? draft) : viewBlocks;
  const orgReachability = pipelineReachability(orgBlocks);
  const reachability = pipelineReachability(railBlocks, organization ? undefined : orgReachability.stoppedBy);
  const organizationStop = organization ? reachability.stoppedBy : orgReachability.stoppedBy;
  /* Positions run across the effective pipeline: an agent's section
   * starts after the organization's top-level blocks. */
  const offset = organization ? 0 : orgBlocks.length;
  const orgBlockCount = countDrafts(orgBlocks);
  const finalContinuation = organization
    ? "Otherwise, continue to the agent's pipeline"
    : "Otherwise";

  /* A sample handed over by the overview's Test request replays as soon
   * as the pipeline is on screen. The ref keeps StrictMode's doubled
   * effect from running it twice. */
  const handedOff = useRef(false);
  useEffect(() => {
    if (handedOff.current) return;
    const text = takeTestSample(agentSlug);
    if (text === null) return;
    handedOff.current = true;
    setSampleText(text);
    void runTestWith(text).then((problem) => {
      if (problem) {
        toast.add({ type: "deny", title: "The test could not be run", description: problem });
      }
    });
  }, []);

  /* Advance the replay one block at a time. */
  useEffect(() => {
    if (!test || cursor >= test.steps.length) return;
    const timer = window.setTimeout(
      () => setCursor((current) => current + 1),
      cursor === 0 ? 400 : 550,
    );
    return () => window.clearTimeout(timer);
  }, [test, cursor]);

  /* Any edit, and a reloaded page, invalidates the run shown on the rail. */
  useEffect(() => {
    setTest(null);
    setCursor(0);
  }, [draft, mode, page]);

  const dirtyCount = useMemo(() => {
    if (!editing) return 0;
    let count = changedBlockCount(baseline, draft);
    if (count === 0 && (pathKeyOf(draftDefault) ?? "") !== (pathKeyOf(baselineDefault) ?? "")) {
      count = 1;
    }
    if (
      count === 0 &&
      mode === "json" &&
      jsonText !== JSON.stringify(documentFromDrafts(draft, draftDefault), null, 2)
    ) {
      count = 1;
    }
    return count;
  }, [editing, draft, baseline, draftDefault, baselineDefault, mode, jsonText]);

  /* Editing starts from the latest revision, which is also the baseline
   * the unsaved count is measured against. Starting from an earlier
   * revision keeps that baseline: the draft is that revision's blocks,
   * and the count says how far it stands from the latest. */
  function startEdit(from?: RequestApprovalPipelineRevision) {
    const revision = latest ?? displayed;
    const baselineDrafts = draftsFromBlocks(revision?.blocks ?? [], directory);
    const baselineTarget = pathTarget(revision?.default_escalation_path, directory);
    const origin = from ?? revision;
    setDraft(from ? draftsFromBlocks(origin?.blocks ?? [], directory) : baselineDrafts);
    setBaseline(baselineDrafts);
    setDraftDefault(from ? pathTarget(origin?.default_escalation_path, directory) : baselineTarget);
    setBaselineDefault(baselineTarget);
    setMode("blocks");
    setJsonError("");
    setExpandedKey(null);
    setEditing(true);
  }

  /* The list holds only summaries, so the revision's blocks are fetched
   * before the editor opens on them. */
  async function startEditFrom(summary: RequestApprovalPipelineRevisionSummary) {
    try {
      const revision = await api.requestApprovalPipelineRevision(agentSlug, summary.revision);
      startEdit(revision);
      toast.add({
        title: `Editing from revision ${summary.revision}`,
        description: "The editor holds its blocks now. Create revision saves them as a new one.",
      });
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "The revision could not be loaded",
        description: reason instanceof Error ? reason.message : undefined,
      });
    }
  }

  function exitEdit() {
    setEditing(false);
    setExpandedKey(null);
    setJsonError("");
    setTest(null);
    setCursor(0);
  }

  function discard() {
    setDraft(baseline);
    setDraftDefault(baselineDefault);
    setJsonText(JSON.stringify(documentFromDrafts(baseline, baselineDefault), null, 2));
    setJsonError("");
  }

  async function commit() {
    if (!activeRevision) return;
    let next = draft;
    let nextDefault = draftDefault;
    if (mode === "json") {
      try {
        const document = parseDocument(jsonText);
        next = draftsFromBlocks(document.blocks, directory);
        nextDefault = pathTarget(document.default_escalation_path, directory);
      } catch (reason) {
        setJsonError(reason instanceof Error ? reason.message : "The document is not valid JSON.");
        return;
      }
    }
    const problem = validateDrafts(next, { offset });
    if (problem) {
      toast.add({ type: "deny", title: "The revision is not ready", description: problem });
      return;
    }
    setSaving(true);
    try {
      const revision = await api.createRequestApprovalPipelineRevision(
        agentSlug,
        documentFromDrafts(next, nextDefault),
      );
      await revisionActivation.offer({
        revision: revision.revision,
        activeRevision: activeRevision?.revision ?? null,
        canActivate,
        activationDescription: "Once active, new requests use this pipeline revision.",
        activate: () => api.activateRequestApprovalPipelineRevision(agentSlug, revision.revision, activeRevision.revision),
      });
      setEditing(false);
      await router.invalidate();
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "The revision could not be created",
        description: reason instanceof Error ? reason.message : undefined,
      });
    } finally {
      setSaving(false);
    }
  }

  function runTest(): Promise<string | null> {
    return runTestWith(sampleText);
  }

  async function runTestWith(text: string): Promise<string | null> {
    try {
      const sample = JSON.parse(text) as ReturnType<typeof defaultSample>;
      const selfBlocks = editing ? draft : viewBlocks;
      const previews: { section: RailSection; preview: RequestApprovalPipelinePreview }[] = [];
      if (!organization && orgBlocks.length) {
        const orgPreview = await api.previewRequestApprovalPipeline(
          "",
          documentFromDrafts(orgBlocks, orgDefault),
          sample,
        );
        previews.push({ section: "organization", preview: orgPreview });
        /* Anything but running through ends the run in the organization
         * section: a decision, a failure, or the end of a branch. */
        if (orgPreview.reason_code !== "end_of_pipeline") {
          setRun(runFromPreviews(previews));
          return null;
        }
        /* What the organization blocks returned is visible to the
         * agent's blocks, exactly as at runtime. */
        const carried = carriedMetadata(orgPreview);
        if (Object.keys(carried).length) {
          sample.previous_metadata = { ...sample.previous_metadata, organization: carried };
        }
      }
      const preview = await api.previewRequestApprovalPipeline(
        agentSlug,
        documentFromDrafts(selfBlocks, currentDefault),
        sample,
      );
      previews.push({ section: ownSection, preview });
      setRun(runFromPreviews(previews, organization));
      return null;
    } catch (reason) {
      return reason instanceof Error ? reason.message : "The test could not be run.";
    }
  }

  function setRun(run: TestRun) {
    setTestOpen(false);
    if (!organization && run.steps.some((step) => step.section === "organization")) {
      setOrgFoldOpen(true);
    }
    setTest(run);
    setCursor(0);
  }

  function switchMode(next: string) {
    if (next === mode) return;
    if (next === "json") {
      setJsonText(JSON.stringify(documentFromDrafts(draft, draftDefault), null, 2));
      setJsonError("");
      setMode("json");
      return;
    }
    try {
      const document = parseDocument(jsonText);
      setDraft(draftsFromBlocks(document.blocks, directory));
      setDraftDefault(pathTarget(document.default_escalation_path, directory));
      setJsonError("");
      setMode("blocks");
    } catch (reason) {
      setJsonError(reason instanceof Error ? reason.message : "The document is not valid JSON.");
    }
  }

  async function activate(revision: RequestApprovalPipelineRevisionSummary) {
    if (activeNumber === null) return;
    try {
      await api.activateRequestApprovalPipelineRevision(agentSlug, revision.revision, activeNumber);
      setConflict("");
      toast.add({
        type: "approve",
        title:
          activeNumber !== null && revision.revision < activeNumber
            ? `Rolled back to revision ${revision.revision}`
            : `Revision ${revision.revision} activated`,
      });
      await router.invalidate();
    } catch (reason) {
      await handleMutationError(reason);
    }
  }

  async function handleMutationError(reason: unknown) {
    if (reason instanceof RequestError && reason.status === 412) {
      setConflict(
        "Another administrator changed the active revision after this page loaded. The latest history is shown below. Review it before trying again.",
      );
      await router.invalidate();
      return;
    }
    if (reason instanceof RequestError && reason.code === "escalation_path_inactive") {
      const keys = (reason.details?.escalation_paths as string[] | undefined) ?? [];
      toast.add({
        type: "deny",
        title: "Activation refused",
        description: `${keys.length === 1 ? "Escalation path" : "Escalation paths"} ${keys.join(", ")} ${keys.length === 1 ? "has" : "have"} no active revision. Activate one, then try again.`,
      });
      return;
    }
    if (reason instanceof RequestError && reason.code === "webhook_endpoint_inactive") {
      const keys = (reason.details?.webhook_endpoints as string[] | undefined) ?? [];
      toast.add({
        type: "deny",
        title: "Activation refused",
        description: `${keys.length === 1 ? "Webhook endpoint" : "Webhook endpoints"} ${keys.join(", ")} ${keys.length === 1 ? "has" : "have"} no active revision. Activate one, then try again.`,
      });
      return;
    }
    toast.add({
      type: "deny",
      title: "The pipeline could not be changed",
      description: reason instanceof Error ? reason.message : undefined,
    });
  }

  async function openRevision(summary: RequestApprovalPipelineRevisionSummary) {
    const request = ++revisionRequest.current;
    try {
      const revision = summary.revision === activeNumber && activeRevision
        ? activeRevision
        : await api.requestApprovalPipelineRevision(agentSlug, summary.revision);
      if (request !== revisionRequest.current) return;
      setSelection({ page, revision });
      setTest(null);
      setCursor(0);
      setExpandedKey(null);
      headingRef.current?.scrollIntoView({ block: "start" });
      headingRef.current?.focus({ preventScroll: true });
    } catch (reason) {
      if (request !== revisionRequest.current) return;
      toast.add({
        type: "deny",
        title: "The revision could not be loaded",
        description: reason instanceof Error ? reason.message : undefined,
      });
    }
  }

  /* Path key to display name, for the dependency list. */
  const pathNames = useMemo(() => {
    const names = new Map<string, string>();
    for (const target of roster) if (target.type === "escalation_path") names.set(target.id, target.name);
    return names;
  }, [roster]);

  /* What every rail on the page shares; each section adds its own. */
  const railShared = {
    directory,
    endpoints: endpointOptions,
    tools: page.toolSchemas,
    suggestions,
    valueSuggestions,
    valueDetails: page.valueDetails,
    defaultTarget: effectiveDefault,
    describeRoute: describeTargetRoute,
    expandedKey,
    onExpandedKeyChange: setExpandedKey,
    run: test,
    cursor,
    availability,
    onManageEndpoints: () => window.open("/webhooks", "_blank", "noopener,noreferrer"),
  } satisfies Omit<RailState, "mode" | "section" | "offset" | "reachability">;
  /* This page's own section, viewed or edited. */
  const ownRail: RailState = {
    ...railShared,
    mode: editing ? "edit" : "view",
    section: ownSection,
    offset,
    reachability,
    highlight: highlightedBlock ? { id: highlightedBlock, ref: blockRef } : undefined,
    editor: editing ? railEditor.editor : undefined,
  };
  /* The organization section folded above an agent's pipeline: always
   * read only, whatever this page is doing. */
  const orgRail: RailState = {
    ...railShared,
    mode: "view",
    section: "organization",
    offset: 0,
    reachability: orgReachability,
  };

  if (missing) {
    return (
      <Page width="wide">
        <PageHeader back={<PageBackLink to="/pipelines">Approval pipelines</PageBackLink>}>
          <h1 className="text-title-lg text-ink">
            <PipelineTitle agentSlug={agentSlug} />
          </h1>
        </PageHeader>
        <Callout tone="warn" icon={<AlertIcon width={15} height={15} />} title="No agent has this slug">
          A pipeline appears here when an agent with this slug is created. Check the slug on the
          agent, or open one of the pipelines on the overview.
        </Callout>
      </Page>
    );
  }

  const blocksArea =
    editing && mode === "json" ? (
      <div className="flex flex-col gap-3">
        {jsonError ? (
          <Callout tone="deny" icon={<AlertIcon width={15} height={15} />} title="The document has a problem">
            {jsonError}
          </Callout>
        ) : null}
        {jsonDraft ? walkDrafts(jsonDraft.blocks).map(({ block, path }) => {
          const blocker = reachability.blockedBy.get(block.id);
          return blocker ? (
            <UnreachableNotice key={block.id} block={blocker}>
              Block {positionLabel(path, offset)} “{block.name || block.id}” cannot run.
            </UnreachableNotice>
          ) : null;
        }) : null}
        <TextArea
          aria-label="Pipeline document"
          spellCheck={false}
          className="min-h-[420px] resize-y font-mono text-[12px] leading-5"
          value={jsonText}
          onChange={(event) => {
            setJsonText(event.target.value);
            setJsonError("");
          }}
        />
      </div>
    ) : (
      <RailProvider value={ownRail}>
        <div className="flex flex-col">
          <PipelineRail
            blocks={railBlocks}
            lastLabel={finalContinuation}
            emptyText={organization
              ? "No checks here yet. Requests continue to their agent's pipeline."
              : "No pipeline for this agent yet. Add steps by editing."}
          />
        </div>
      </RailProvider>
    );

  return (
    <Page width="wide">
      {revisionActivation.dialog}
      {railEditor.dialog}
      <PageHeader
        back={editing
          ? <PageBackButton onClick={exitEdit}>Stop editing</PageBackButton>
          : <PageBackLink to="/pipelines">Approval pipelines</PageBackLink>}
      >
        <div className="flex flex-col items-start justify-between gap-4 sm:flex-row sm:gap-6">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 ref={headingRef} tabIndex={-1} className="scroll-mt-8 text-title-lg text-ink">
                <PipelineTitle agentSlug={agentSlug} agentName={agentName} />
              </h1>
              {agentName ? (
                <Badge>
                  <span className="font-mono text-[12px]">{agentSlug}</span>
                </Badge>
              ) : null}
              {editing ? (
                <Badge tone="accent">
                  Draft revision {(latest?.revision ?? 0) + 1}
                </Badge>
              ) : displayed?.archived_at ? (
                <Badge tone="warn">Archived revision {displayed.revision}</Badge>
              ) : displayed ? (
                <Badge tone={viewingInactive ? "neutral" : "approve"}>
                  {viewingInactive ? "Inactive revision" : "Active revision"} {displayed.revision}
                </Badge>
              ) : null}
            </div>
            <p className="mt-1.5 max-w-[620px] text-body text-ink-muted">
              {organization
                ? "Every request starts here. Blocks are checked from top to bottom until one approves, denies, or asks a human. Otherwise, the request continues to its agent's pipeline."
                : <>After the <Referenced reference={{ kind: "pipeline", agentSlug: "" }} inline>organization pipeline</Referenced> runs without an action, this pipeline then runs. Define rules that automatically approve, deny or route requests to a human.</>}
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-2 self-end sm:self-auto">
            {editing ? (
              <ToggleGroup aria-label="Editor mode" value={mode} onValueChange={switchMode}>
                <ToggleGroupItem value="blocks">Blocks</ToggleGroupItem>
                <ToggleGroupItem value="json">JSON</ToggleGroupItem>
              </ToggleGroup>
            ) : null}
            {!editing && !archived && viewingInactive && activeRevision ? (
              <Button variant="ghost" onClick={() => void openRevision(activeRevision)}>
                View active revision
              </Button>
            ) : null}
            {editing ? (
              <Button variant="ghost" onClick={exitEdit}>
                Cancel
              </Button>
            ) : (
              editable ? (
                <Button variant="primary" onClick={() => startEdit(viewingInactive ? displayed ?? undefined : undefined)}>
                  {viewingInactive ? "Create revision from this" : "Edit pipeline"}
                </Button>
              ) : null
            )}
            {editable || (!organization && !archived && !editing) ? (
              <Menu
                actions={[
                  ...(editable
                    ? [{
                        id: "test",
                        label: "Test request",
                        action: () => setTestOpen(true),
                      }]
                    : []),
                  ...(!organization && !archived && !editing
                    ? [{
                        id: "archive",
                        label: "Archive pipeline",
                        danger: true,
                        action: () => setArchiveOpen(true),
                      }]
                    : []),
                ]}
              >
                <MenuTrigger
                  render={
                    <IconButton aria-label="Pipeline actions" variant="neutral">
                      <EllipsisIcon width={15} height={15} />
                    </IconButton>
                  }
                />
                <MenuContent align="end" />
              </Menu>
            ) : null}
          </div>
        </div>
      </PageHeader>

      {archived ? (
        <Callout tone="warn" title="This pipeline is archived">
          The agent{" "}
          <Referenced reference={{ kind: "agent", slug: agentSlug, name: agentName ?? agentSlug }} inline>
            <span className="font-mono text-[12px]">{agentSlug}</span>
          </Referenced>{" "}
          was archived, and its pipeline went with it. Its revisions can still be viewed, but none
          can be activated. Restoring the agent brings the pipeline back.
        </Callout>
      ) : null}
      {conflict ? (
        <Callout tone="warn" icon={<AlertIcon width={15} height={15} />} title="The pipeline changed">
          {conflict}
        </Callout>
      ) : null}
      {highlightedBlock && displayed && !findDraft(viewBlocks, highlightedBlock) ? (
        <Callout tone="warn" title="The referenced block is not in this revision">
          Viewing revision {displayed.revision}. The linked block could not be found.
        </Callout>
      ) : null}
      {error ? (
        <Callout tone="deny" title="The pipeline could not be loaded">
          <div className="flex items-center justify-between gap-4">
            <span>{error}</span>
            <Button size="sm" onClick={() => void router.invalidate()}>
              Try again
            </Button>
          </div>
        </Callout>
      ) : null}

      <div className="flex min-w-0 flex-col">
        <PipelineEntryCard agentSlug={organization ? undefined : agentSlug} />
        <PipelineConnector label="First, check organization rules" />

        {!organization ? (
          <>
            <PipelineFoldedSection
              title={
                <span onClick={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()}>
                  <Referenced reference={{ kind: "pipeline", agentSlug: "" }} inline>
                    Organization pipeline
                  </Referenced>
                </span>
              }
              positionLabel={orgBlocks.length ? (orgBlocks.length === 1 ? "1" : `1–${orgBlocks.length}`) : undefined}
              summary={
                orgBlockCount
                  ? `${orgBlockCount} ${orgBlockCount === 1 ? "block applies" : "blocks apply"} to every agent. Expand to see the checks.`
                  : "No checks configured. Requests continue to the blocks below."
              }
              action={
                <Menu
                  actions={[
                    {
                      id: "edit",
                      label: "Edit",
                      disabled: !can("pipeline.write", { kind: "organization" }),
                      action: () => void navigate({ to: "/pipelines/organization", search: { edit: true } }),
                    },
                    {
                      id: "view",
                      label: "View",
                      action: () => void navigate({ to: "/pipelines/organization" }),
                    },
                  ]}
                >
                  <MenuTrigger
                    render={
                      <IconButton aria-label="Organization pipeline actions" size="sm">
                        <EllipsisIcon width={15} height={15} />
                      </IconButton>
                    }
                  />
                  <MenuContent align="end" />
                </Menu>
              }
              expanded={orgFoldOpen}
              onExpandedChange={setOrgFoldOpen}
            >
              <RailProvider value={orgRail}>
                <PipelineRail
                  blocks={orgBlocks}
                  lastLabel={<>Otherwise, continue to the <Referenced reference={{ kind: "pipeline", agentSlug }} inline>{agentName ?? <span className="font-mono text-[12px]">{agentSlug}</span>} pipeline</Referenced></>}
                  emptyText="No organization blocks. Requests start here."
                />
              </RailProvider>
            </PipelineFoldedSection>
            {!organizationStop ? <PipelineConnector label={<>If no decision is made, continue to the <Referenced reference={{ kind: "pipeline", agentSlug }} inline>{agentName ?? <span className="font-mono text-[12px]">{agentSlug}</span>} pipeline</Referenced></>} /> : null}
          </>
        ) : null}

        {organizationStop ? (
          <Callout tone="warn" icon={<AlertIcon width={15} height={15} />} title="Agent pipelines are unreachable">
            The organization pipeline stops at “{organizationStop.name || "Untitled block"}”. Requests cannot continue to agent pipelines.
          </Callout>
        ) : null}
        {blocksArea}

        {!reachability.stoppedBy && (editing || railBlocks.length === 0) ? <PipelineConnector /> : null}
        {reachability.stoppedBy ? (
          <UnreachableNotice block={reachability.stoppedBy}>
            Requests cannot reach this fallback. Its default escalation path still applies to human outcomes that inherit it.
          </UnreachableNotice>
        ) : null}
        <div
          className={cn(
            "rounded-lg transition-shadow duration-200",
            settled &&
              test &&
              (test.reasonCode === "end_of_pipeline" || test.reasonCode === "end_of_section") &&
              (organization ? "ring-2 ring-edge-strong" : "ring-2 ring-warn"),
          )}
        >
          {organization ? (
            <ContinueCard
              target={currentDefault}
              directory={editing ? directory : undefined}
              onTargetChange={editing ? setDraftDefault : undefined}
              route={defaultRoute}
            />
          ) : (
            <PipelineTerminusCard
              target={editing ? currentDefault : effectiveDefault}
              directory={editing ? directory : undefined}
              onTargetChange={editing ? setDraftDefault : undefined}
              fallbackLabel={
                orgDefault ? `Organization default: ${orgDefault.name}` : "Any authorized reviewer"
              }
              route={defaultRoute}
            />
          )}
        </div>
      </div>

      {!editing ? (
        <Card>
          <RevisionHistory
            aria-label="Pipeline revisions"
            info={REVISIONS_INFO}
            docsLinks={[{ href: "https://withhuman.ai/docs/web-app/approval-pipelines#revision-history-and-rollback", label: "Revision history and rollback" }]}
            description={viewingInactive ? `Viewing revision ${displayed.revision}` : undefined}
            items={history.items}
            totalCount={history.totalCount}
            hasMore={history.hasMore}
            loadingMore={history.loadingMore}
            onLoadMore={history.loadMore}
            activeRevision={activeNumber}
            onOpen={(revision) => void openRevision(revision)}
            onActivate={
              canActivate && !archived
                ? (revision) => setPendingAction({ kind: "activate", revision })
                : undefined
            }
            onCreateFrom={editable ? (revision) => void startEditFrom(revision) : undefined}
          />
        </Card>
      ) : null}

      {editing ? (
        <SaveBar
          count={dirtyCount}
          saveLabel="Create revision"
          saving={saving}
          onDiscard={discard}
          onSave={() => void commit()}
        />
      ) : null}

      <Dialog open={archiveOpen} onOpenChange={setArchiveOpen}>
        <DialogContent>
          <DialogTitle>Archive pipeline</DialogTitle>
          <DialogDescription>
            An agent's pipeline is archived with the agent and restored with it. Open the agent to
            archive it.
          </DialogDescription>
          <DialogFooter>
            <DialogClose render={<Button variant="ghost">Close</Button>} />
            <Button
              variant="primary"
              onClick={() => {
                setArchiveOpen(false);
                void navigate({ to: "/agents/$agentSlug", params: { agentSlug } });
              }}
            >
              View agent
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <TestRequestDialog
        open={testOpen}
        onOpenChange={setTestOpen}
        sampleText={sampleText}
        onSampleTextChange={setSampleText}
        onRun={runTest}
      />

      {pendingAction && references !== null && references !== "unavailable" && !references.activatable ? (
        <Dialog open onOpenChange={(open) => !open && setPendingAction(null)}>
          <DialogContent>
            <DialogTitle>
              {activeNumber !== null && pendingAction.revision.revision < activeNumber
                ? `Revision ${pendingAction.revision.revision} cannot be rolled back to yet`
                : `Revision ${pendingAction.revision.revision} cannot be activated yet`}
            </DialogTitle>
            <DialogDescription>
              Something this revision depends on is no longer in place. Restore it, or create a new
              revision that does not need it.
            </DialogDescription>
            <DialogBody>
              <RevisionDependencies references={references} pathNames={pathNames} />
            </DialogBody>
            <DialogFooter>
              <DialogClose render={<Button variant="ghost">Close</Button>} />
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : (
        <ConfirmDialog
          open={pendingAction !== null && references !== null}
          onOpenChange={(open) => !open && setPendingAction(null)}
          title={
            pendingAction && activeNumber !== null && pendingAction.revision.revision < activeNumber
              ? `Roll back to revision ${pendingAction.revision.revision}`
              : `Activate revision ${pendingAction?.revision.revision ?? ""}`
          }
          description="New requests will use these rules immediately. Requests already in progress will keep their existing rules."
          confirmLabel={
            pendingAction && activeNumber !== null && pendingAction.revision.revision < activeNumber
              ? "Roll back"
              : "Activate revision"
          }
          tone="approve"
          onConfirm={() => {
            if (pendingAction) return activate(pendingAction.revision);
          }}
        />
      )}
    </Page>
  );
}

/* What blocks activating a revision: the paths, endpoints, and block
 * runtimes that are no longer in place. Healthy dependencies are not
 * listed; the dialog is for what to restore. */
function RevisionDependencies({
  references,
  pathNames,
}: {
  references: PipelineReferences;
  pathNames: Map<string, string>;
}) {
  const paths = references.escalation_paths.filter((path) => !path.active);
  const endpoints = references.webhook_endpoints.filter((endpoint) => !endpoint.active);
  return (
    <ul className="flex flex-col gap-1.5">
      {paths.map((path) => (
        <li key={`path-${path.path_key}`} className="flex items-center gap-2 text-body text-ink-secondary">
          <Badge tone="deny">{path.exists ? "Archived" : "Missing"}</Badge>
          <span>
            Escalation path{" "}
            <ReferenceChip
              reference={{
                kind: "escalation_path",
                key: path.path_key,
                name: pathNames.get(path.path_key) ?? path.path_key,
              }}
              link={{ to: "/escalation-paths/$pathKey", params: { pathKey: path.path_key } }}
              className="-my-1"
            />
            {describeReferrers(path.block_keys, path.default)}
          </span>
        </li>
      ))}
      {endpoints.map((endpoint) => (
        <li key={`endpoint-${endpoint.endpoint_key}`} className="flex items-center gap-2 text-body text-ink-secondary">
          <Badge tone="deny">{endpoint.exists ? "Archived" : "Missing"}</Badge>
          <span>
            Webhook endpoint{" "}
            {endpoint.exists ? (
              <ReferenceChip
                reference={{
                  kind: "webhook_endpoint",
                  key: endpoint.endpoint_key,
                  name: endpoint.name || endpoint.endpoint_key,
                }}
                link={{ to: "/webhooks/$endpointKey", params: { endpointKey: endpoint.endpoint_key } }}
                className="-my-1"
              />
            ) : (
              <span className="font-mono text-[12px]">{endpoint.endpoint_key}</span>
            )}
            {describeReferrers(endpoint.block_keys, false)}
          </span>
        </li>
      ))}
      {references.blocks.map((block) => (
        <li key={`block-${block.block_key}`} className="flex items-center gap-2 text-body text-ink-secondary">
          <Badge tone="deny">Cannot run</Badge>
          <span>
            Block <span className="font-mono text-[12px]">{block.block_key}</span>: {blockProblemLabel(block.problem)}
          </span>
        </li>
      ))}
    </ul>
  );
}

function describeReferrers(blockKeys: string[], isDefault: boolean): string {
  const parts: string[] = [];
  if (blockKeys.length) parts.push(`${blockKeys.length === 1 ? "block" : "blocks"} ${blockKeys.join(", ")}`);
  if (isDefault) parts.push("the pipeline default");
  return parts.length ? ` (${parts.join(" and ")})` : "";
}

/* Parses a JSON-mode document into wire block definitions; throws a
 * readable error for the callout. A block is named by its place in the
 * document ("Block 3.2" is the second block inside the third), and
 * block keys must be unique across the whole tree, branches included. */
export function parseDocument(text: string): {
  blocks: RequestApprovalBlockDefinition[];
  default_escalation_path?: string;
} {
  const parsed = JSON.parse(text) as { blocks?: unknown; default_escalation_path?: unknown };
  if (!parsed || typeof parsed !== "object" || !Array.isArray(parsed.blocks)) {
    throw new Error("The pipeline document must be an object with a blocks array.");
  }
  checkDocumentBlocks(parsed.blocks, [], new Map());
  const defaultPath = parsed.default_escalation_path;
  if (defaultPath !== undefined && typeof defaultPath !== "string") {
    throw new Error("default_escalation_path must be an escalation path key.");
  }
  return { blocks: parsed.blocks as RequestApprovalBlockDefinition[], default_escalation_path: defaultPath };
}

/* Checks one rail of a JSON document, and every branch's rail inside it.
 * keys maps each block key met so far to the block that used it first. */
function checkDocumentBlocks(blocks: unknown[], path: number[], keys: Map<string, string>) {
  for (const [index, raw] of blocks.entries()) {
    const label = `Block ${[...path, index + 1].join(".")}`;
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
      throw new Error(`${label} must be an object.`);
    }
    const block = raw as {
      id?: unknown;
      type?: string;
      config?: {
        when?: unknown;
        on_match?: string;
        outcome?: string;
        endpoint_key?: unknown;
        allowed_outcomes?: unknown;
        blocks?: unknown;
      };
    };
    if (typeof block.id === "string") {
      const first = keys.get(block.id);
      if (first) {
        throw new Error(`${label} reuses the key “${block.id}” of ${first}. Block keys must be unique across the pipeline, including inside branches.`);
      }
      keys.set(block.id, label);
    }
    switch (block.type) {
      case "always":
        if (!["approve", "deny", "human"].includes(block.config?.outcome ?? "")) {
          throw new Error(`${label} needs config.outcome set to "approve", "deny", or "human".`);
        }
        break;
      case "webhook":
        if (typeof block.config?.endpoint_key !== "string") {
          throw new Error(`${label} is missing config.endpoint_key.`);
        }
        if (!Array.isArray(block.config.allowed_outcomes)) {
          throw new Error(`${label} is missing config.allowed_outcomes.`);
        }
        break;
      case "cel": {
        if (!block.config?.when) {
          throw new Error(`${label} is missing config.when.`);
        }
        const outcome = block.config.on_match;
        if (outcome !== "approve" && outcome !== "deny" && outcome !== "human") {
          throw new Error(`${label} needs on_match of approve, deny, or human.`);
        }
        break;
      }
      case "branch":
        if (!block.config?.when) {
          throw new Error(`${label} is missing config.when.`);
        }
        if (!Array.isArray(block.config.blocks)) {
          throw new Error(`${label} is missing config.blocks, the blocks the branch runs.`);
        }
        checkDocumentBlocks(block.config.blocks, [...path, index + 1], keys);
        break;
      default: {
        /* A kind the edition registered checks its own config; anything
         * else is a type this deployment cannot run. */
        const kind = block.type === undefined ? undefined : blockKind(block.type);
        if (!kind) throw new Error(`${label} must have type ${listTypes(knownBlockTypes())}.`);
        const problem = kind.checkDocument({ config: block.config as Record<string, unknown> | undefined }, label);
        if (problem) throw new Error(problem);
      }
    }
  }
}

/* The types as a sentence lists them: "always", "branch", or "cel". */
function listTypes(types: string[]): string {
  const quoted = types.map((type) => `"${type}"`);
  return `${quoted.slice(0, -1).join(", ")}, or ${quoted[quoted.length - 1]}`;
}
