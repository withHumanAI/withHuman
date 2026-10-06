import {
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { cn } from "@/ds/lib/cn";
import { Badge } from "@/ds/components/badge";
import { IconButton } from "@/ds/components/icon-button";
import { Menu, MenuContent, MenuTrigger } from "@/ds/components/menu";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Card } from "@/ds/components/card";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import type { PipelineOption } from "@/ds/components/delegate-block";
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
  PipelineTerminusCard,
} from "@/ds/components/pipeline-flow";
import { ReferenceChip } from "./reference-chip";
import { TargetBadge, agentOwnerLabel, type ReviewerTarget } from "@/ds/components/reviewer-target";
import type { WebhookEndpointOption } from "@/ds/components/webhook-block";
import { SaveBar } from "@/ds/components/save-bar";
import { TextArea } from "@/ds/components/text-area";
import { TextField } from "@/ds/components/text-field";
import { ToggleGroup, ToggleGroupItem } from "@/ds/components/toggle-group";
import { useToast } from "@/ds/components/toast";
import { Page } from "@/ds/components/page";
import { PageHeader, PageBackLink, PageBackButton } from "@/ds/components/page-header";
import { RevisionHistory } from "@/ds/components/revision-history";
import { AlertIcon, EllipsisIcon } from "@/ds/icons";
import { api, RequestError } from "../api";
import {
  ENTRY_PIPELINE_KEY,
  type PipelineReferences,
  type PipelineUse,
  type RequestApprovalBlockDefinition,
  type RequestApprovalPipelineRevision,
  type RequestApprovalPipelineRevisionSummary,
  type RequestApprovalPipelineSummary,
  type RevisionPage,
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
  pathTarget,
  routeKeyOf,
  suggestPaths,
  takeTestSample,
  validateDrafts,
  type BlockDraft,
} from "./pipeline-blocks";
import {
  allConditions,
  changedBlockCount,
  findDraft,
  pipelineReachability,
  positionLabel,
  walkDrafts,
} from "./pipeline-tree";
import { pipelineLink } from "./pipeline-link";
import { PipelineUseList } from "./pipeline-uses";
import { foldDelegations, runFromPreview, type TestRun } from "./pipeline-replay";
import {
  PipelineRail,
  RailProvider,
  UnreachableNotice,
  useRailEditor,
  type RailState,
} from "./pipeline-rail";
import { useTitle } from "./use-title";
import { useRevisionActivation } from "./use-revision-activation";
import {
  NO_SUGGESTIONS,
  conditionFieldPaths,
  conditionValueSuggestions,
  mergeValueSuggestions,
  requestPipelineSuggestions,
  sampleValueSuggestions,
  usePipelineSuggestions,
} from "./pipeline-suggestions";
import { FIXED_FIELDS } from "@/ds/components/condition-builder";
import { TestRequestDialog } from "./test-request-dialog";

export { TestRequestDialog } from "./test-request-dialog";

const FIXED_FIELD_PATHS = new Set(FIXED_FIELDS.map((field) => field.path));

/*
 * One pipeline, viewed or edited in place. The rail keeps the same
 * scaffolding in both modes: entry, this pipeline's blocks with their
 * exits, branches' wells and delegates' exits, and the terminus that owns the
 * pipeline default. Edit swaps the read-only rows for editable cards
 * where they stand; Create revision commits a new immutable revision,
 * which stays inactive until activated from the history below. Every
 * pipeline is keyed; "entry" is the one every request enters, and the
 * others take what delegate blocks hand them.
 */

const REVISIONS_INFO =
  "Every change creates a new revision. Only the active revision runs.";

type PendingAction = { kind: "activate"; revision: RequestApprovalPipelineRevisionSummary } | null;

/* What the pipeline view route loaders deliver: this pipeline's history
 * with its active (or, failing that, latest) revision in full, the
 * escalation paths a human exit may name, the pipelines a delegate may
 * hand to with the active revisions of the ones this pipeline hands to,
 * who hands to this one, and which block types this deployment can run.
 * The editors' suggestions are not here: the page never waits for them
 * (see requestPipelineSuggestions). A failed pipeline load is reported
 * by the screen rather than thrown, so an expired session reaches the
 * sign-in gate, not the router's error boundary. */
export interface PipelineViewPage {
  /* The first page of the pipeline's history, newest first; the screen
   * fetches the rest on demand. */
  history: RevisionPage<RequestApprovalPipelineRevisionSummary>;
  /* The active revision in full: what the read-only view shows. */
  displayed: RequestApprovalPipelineRevision | null;
  /* The immutable revision named by an incoming review link. */
  linkedRevision: RequestApprovalPipelineRevision | null;
  latest: RequestApprovalPipelineRevision | null;
  /* An empty history (or a 404) means no pipeline has this key. */
  missing: boolean;
  /* When the pipeline was archived: read only, no active revision, for
   * good. */
  archivedAt: string | null;
  error: string;
  /* The organization's escalation paths, the choices for a human exit
   * and for the pipeline default. */
  roster: ReviewerTarget[];
  /* Every pipeline the viewer may read, live or archived, for the
   * delegate picker and for naming targets. */
  pipelines: PipelineOption[];
  /* The active pipeline revisions whose delegate blocks hand requests
   * here. Empty for the entry pipeline. */
  uses: PipelineUse[];
  /* Which of the block kinds that depend on deployment configuration
   * (webhook, and every kind the edition adds) this deployment can run,
   * by type. */
  availability: Record<string, boolean>;
  /* The organization's endpoints, for the webhook card's picker. */
  endpointOptions: WebhookEndpointOption[];
}

/* The pipeline's active revision comes from its own read rather than
 * from the history, so a rollback far down the list still shows the
 * right document; the latest revision is fetched in full only when it
 * differs. An archived pipeline has no active revision (the read is a
 * 404): its latest revision stands in, read only, and archivedAt says
 * when. */
async function loadPipelineRevisions(pipelineKey: string) {
  const [history, active] = await Promise.all([
    api.requestApprovalPipelineRevisions(pipelineKey),
    api.activeRequestApprovalPipeline(pipelineKey).catch((reason: unknown) => {
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
        : await api.requestApprovalPipelineRevision(pipelineKey, latestSummary.revision);
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

/* Every pipeline the viewer may read, as picker options. The entry
 * pipeline is never a target, so it is left out. */
export function pipelineOptions(summaries: RequestApprovalPipelineSummary[]): PipelineOption[] {
  return summaries
    .filter((summary) => summary.pipeline_key !== ENTRY_PIPELINE_KEY)
    .map((summary) => ({
      key: summary.pipeline_key,
      name: summary.name,
      active: summary.active_revision !== undefined,
    }));
}

/* editing starts the editors' suggestions loading beside the page, for
 * a link that opens the pipeline already in the editor. */
export async function loadPipelineView(pipelineKey: string, revision?: number, editing = false): Promise<PipelineViewPage> {
  if (editing) void requestPipelineSuggestions();
  const [pipeline, roster, summaries, uses, availability] = await Promise.all([
    loadPipelineRevisions(pipelineKey).then(async (own) => ({
      ...own,
      linkedRevision: revision === undefined ? null
        : own.active?.revision === revision ? own.active
        : own.latest?.revision === revision ? own.latest
        : await api.requestApprovalPipelineRevision(pipelineKey, revision),
    })).then(
      (own) => ({
        history: own.history,
        displayed: own.displayed,
        linkedRevision: own.linkedRevision,
        latest: own.latest,
        missing: own.history.revisions.length === 0,
        archivedAt: own.archivedAt,
        error: "",
      }),
      (reason: unknown) => ({
        history: emptyRevisionPage<RequestApprovalPipelineRevisionSummary>(),
        displayed: null,
        linkedRevision: null,
        latest: null,
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
    loadPathDirectory(),
    api.requestApprovalPipelines("all").then((result) => result.request_approval_pipelines, () => []),
    pipelineKey === ENTRY_PIPELINE_KEY
      ? Promise.resolve([] as PipelineUse[])
      : api.requestApprovalPipelineUses(pipelineKey).then((result) => result.uses, () => [] as PipelineUse[]),
    loadBlockAvailability(),
  ]);
  return {
    ...pipeline,
    roster,
    pipelines: pipelineOptions(summaries),
    uses,
    ...availability,
  };
}

/* canWrite covers drafting, saving, and previewing a revision; canActivate
 * covers activating, rolling back, and archiving. The route computes both
 * against the pipeline's own resource. */
export function PipelineViewScreen({
  pipelineKey,
  page,
  canWrite,
  canActivate,
  initiallyEditing = false,
  focusedBlock,
}: {
  pipelineKey: string;
  page: PipelineViewPage;
  canWrite: boolean;
  canActivate: boolean;
  initiallyEditing?: boolean;
  focusedBlock?: string;
}) {
  const navigate = useNavigate();
  const router = useRouter();
  const toast = useToast();
  const revisionActivation = useRevisionActivation();
  const entry = pipelineKey === ENTRY_PIPELINE_KEY;
  const {
    displayed: activeRevision,
    linkedRevision,
    latest,
    missing,
    archivedAt,
    error,
    roster,
    pipelines,
    uses,
    availability,
    endpointOptions,
  } = page;
  const pipelineName = (latest ?? activeRevision)?.name ?? pipelineKey;
  useTitle(pipelineName);
  const history = useRevisionPages(page.history, (cursor) =>
    api.requestApprovalPipelineRevisions(pipelineKey, cursor),
  );
  const [conflict, setConflict] = useState("");
  const [archiving, setArchiving] = useState(false);
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
      .requestApprovalPipelineRevisionReferences(pipelineKey, revision)
      .then((result) => {
        if (!stale) setReferences(result);
      })
      .catch(() => {
        if (!stale) setReferences("unavailable");
      });
    return () => {
      stale = true;
    };
  }, [pendingAction, pipelineKey]);

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
    editing ? pathTarget((latest ?? displayed)?.default_escalation_path, roster, (latest ?? displayed)?.default_ask_agent_owner) : null,
  );
  const [baselineDefault, setBaselineDefault] = useState<ReviewerTarget | null>(draftDefault);
  /* The pipeline's name under edit: renaming is a revision too. */
  const [draftName, setDraftName] = useState(pipelineName);
  const [baselineName, setBaselineName] = useState(pipelineName);
  /* The one expanded card, as `${pipeline key}/${block id}`. */
  const [expandedKey, setExpandedKey] = useState<string | null>(null);
  const [mode, setMode] = useState<"blocks" | "json">("blocks");
  const [jsonText, setJsonText] = useState("");
  const [jsonError, setJsonError] = useState("");
  const [saving, setSaving] = useState(false);

  /* Testing: a dialog collects the sample, then the rail replays the
   * evaluation block by block; a delegated pipeline's run shows on the
   * delegate that handed the request there. */
  const [testOpen, setTestOpen] = useState(false);
  const [test, setTest] = useState<TestRun | null>(null);
  const [cursor, setCursor] = useState(0);
  /* Tool, field and value suggestions for the editors, and the newest
   * real request, which seeds the test sample until someone edits it. */
  const offered = usePipelineSuggestions() ?? NO_SUGGESTIONS;
  const [sampleDraft, setSampleText] = useState<string | null>(null);
  const sampleText = sampleDraft ?? JSON.stringify(offered.sample ?? defaultSample(), null, 2);
  const settled = test !== null && cursor >= test.steps.length;

  /* Structural edits, drag reordering, and the confirmation before a
   * branch with blocks is removed. Dragging collapses the open card. */
  const railEditor = useRailEditor(setDraft, () => setExpandedKey(null));

  const directory = roster;

  /* An archived pipeline remains readable, but has no active revision. */
  const archived = archivedAt !== null;
  const editable = canWrite && !archived;
  const activeNumber = archived ? null : (activeRevision?.revision ?? null);
  const viewingInactive = displayed !== null && displayed.revision !== activeNumber;
  const viewBlocks = useMemo(
    () => draftsFromBlocks(displayed?.blocks ?? [], directory),
    [displayed, directory],
  );
  const viewDefault = useMemo(
    () => pathTarget(displayed?.default_escalation_path, directory, displayed?.default_ask_agent_owner),
    [displayed, directory],
  );
  const jsonDraft = useMemo(() => {
    if (!editing || mode !== "json") return null;
    try {
      const document = parseDocument(jsonText);
      return { blocks: draftsFromBlocks(document.blocks, directory), target: pathTarget(document.default_escalation_path, directory, document.default_ask_agent_owner) };
    } catch {
      return null;
    }
  }, [editing, mode, jsonText, directory]);
  const currentDefault = editing ? (jsonDraft ? jsonDraft.target : draftDefault) : viewDefault;

  /* What no default means here: the queue for the entry pipeline, and
   * the delegating pipeline's own default for any other. */
  const inheritedLabel = entry ? "Any authorized reviewer" : "The delegating pipeline's default";
  const describeTargetRoute = (target: ReviewerTarget | null) =>
    target?.type === "agent_owner"
      ? `Asks the agent's owner. Without one, it goes to ${target.id ? target.name : "the pipeline default"}.`
      : target
        ? ""
        : `Uses pipeline default: ${currentDefault?.type === "agent_owner" ? agentOwnerLabel(currentDefault.name) : (currentDefault?.name ?? inheritedLabel)}.`;
  const defaultRoute = editing && currentDefault ? (
    <div className="flex flex-wrap items-center gap-1 text-micro text-ink-muted">
      <TargetBadge target={currentDefault} />
    </div>
  ) : undefined;
  /* Every condition the saved pipeline tests, branches' and delegates'
   * included. */
  const savedConditions = useMemo(() => allConditions(viewBlocks), [viewBlocks]);
  /* Fields to pick from: the fixed ones, then what the agents have
   * actually sent, then what saved conditions compare, then whatever the
   * current sample holds. */
  const suggestions = useMemo(() => [...new Set([
    ...offered.argumentPaths,
    ...conditionFieldPaths(savedConditions),
    ...suggestPaths(sampleText),
  ])].sort((a, b) => Number(FIXED_FIELD_PATHS.has(b)) - Number(FIXED_FIELD_PATHS.has(a))), [offered.argumentPaths, savedConditions, sampleText]);
  const valueSuggestions = useMemo(() => mergeValueSuggestions(
    offered.valueSuggestions,
    sampleValueSuggestions(sampleText),
    conditionValueSuggestions(savedConditions),
  ), [offered.valueSuggestions, sampleText, savedConditions]);
  const railBlocks = editing ? (jsonDraft?.blocks ?? draft) : viewBlocks;
  const reachability = pipelineReachability(railBlocks);

  /* A sample handed over by the overview's Test request replays as soon
   * as the pipeline is on screen. The ref keeps StrictMode's doubled
   * effect from running it twice. */
  const handedOff = useRef(false);
  useEffect(() => {
    if (handedOff.current) return;
    const text = takeTestSample(pipelineKey);
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
    if (count === 0 && routeKeyOf(draftDefault) !== routeKeyOf(baselineDefault)) {
      count = 1;
    }
    if (count === 0 && draftName.trim() !== baselineName.trim()) count = 1;
    if (
      count === 0 &&
      mode === "json" &&
      jsonText !== JSON.stringify(documentFromDrafts(draft, draftDefault), null, 2)
    ) {
      count = 1;
    }
    return count;
  }, [editing, draft, baseline, draftDefault, baselineDefault, draftName, baselineName, mode, jsonText]);

  /* Editing starts from the latest revision, which is also the baseline
   * the unsaved count is measured against. Starting from an earlier
   * revision keeps that baseline: the draft is that revision's blocks,
   * and the count says how far it stands from the latest. */
  function startEdit(from?: RequestApprovalPipelineRevision) {
    const revision = latest ?? displayed;
    const baselineDrafts = draftsFromBlocks(revision?.blocks ?? [], directory);
    const baselineTarget = pathTarget(revision?.default_escalation_path, directory, revision?.default_ask_agent_owner);
    const origin = from ?? revision;
    setDraft(from ? draftsFromBlocks(origin?.blocks ?? [], directory) : baselineDrafts);
    setBaseline(baselineDrafts);
    setDraftDefault(from ? pathTarget(origin?.default_escalation_path, directory, origin?.default_ask_agent_owner) : baselineTarget);
    setBaselineDefault(baselineTarget);
    setDraftName(revision?.name ?? pipelineName);
    setBaselineName(revision?.name ?? pipelineName);
    setMode("blocks");
    setJsonError("");
    setExpandedKey(null);
    void requestPipelineSuggestions();
    setEditing(true);
  }

  /* The list holds only summaries, so the revision's blocks are fetched
   * before the editor opens on them. */
  async function startEditFrom(summary: RequestApprovalPipelineRevisionSummary) {
    try {
      const revision = await api.requestApprovalPipelineRevision(pipelineKey, summary.revision);
      startEdit(revision);
      toast.add({
        title: `Editing from revision ${summary.revision}`,
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
    setDraftName(baselineName);
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
        nextDefault = pathTarget(document.default_escalation_path, directory, document.default_ask_agent_owner);
      } catch (reason) {
        setJsonError(reason instanceof Error ? reason.message : "The document is not valid JSON.");
        return;
      }
    }
    if (!draftName.trim()) {
      toast.add({ type: "deny", title: "The revision is not ready", description: "The pipeline needs a name." });
      return;
    }
    const problem = validateDrafts(next, { pipelineKey });
    if (problem) {
      toast.add({ type: "deny", title: "The revision is not ready", description: problem });
      return;
    }
    setSaving(true);
    try {
      const revision = await api.createRequestApprovalPipelineRevision(
        pipelineKey,
        { name: draftName.trim(), ...documentFromDrafts(next, nextDefault) },
      );
      await revisionActivation.offer({
        revision: revision.revision,
        activeRevision: activeRevision?.revision ?? null,
        canActivate,
        activationDescription: "Once active, new requests use this pipeline revision.",
        activate: () => api.activateRequestApprovalPipelineRevision(pipelineKey, revision.revision, activeRevision.revision),
      });
      setEditing(false);
      await router.invalidate();
    } catch (reason) {
      if (reason instanceof RequestError && reason.code === "request_approval_pipeline_not_found") {
        toast.add({
          type: "deny",
          title: "The revision could not be created",
          description: "A delegate block names a pipeline that does not exist or is archived. Choose another pipeline.",
        });
        return;
      }
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

  /* One preview runs the whole effective pipeline: this document, and the
   * active revision of every pipeline its delegates hand to. The rail
   * draws only this one, so what ran inside a delegated pipeline is
   * folded onto the delegate that handed the request there. */
  async function runTestWith(text: string): Promise<string | null> {
    try {
      const sample = JSON.parse(text) as ReturnType<typeof defaultSample>;
      const blocks = editing ? draft : viewBlocks;
      const preview = await api.previewRequestApprovalPipeline(
        pipelineKey,
        documentFromDrafts(blocks, currentDefault),
        sample,
      );
      setTestOpen(false);
      const delegateIds = new Set(walkDrafts(blocks).flatMap(({ block }) => (block.kind === "delegate" ? [block.id] : [])));
      setTest(foldDelegations(runFromPreview(preview), pipelineKey, delegateIds));
      setCursor(0);
      return null;
    } catch (reason) {
      return reason instanceof Error ? reason.message : "The test could not be run.";
    }
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
      setDraftDefault(pathTarget(document.default_escalation_path, directory, document.default_ask_agent_owner));
      setJsonError("");
      setMode("blocks");
    } catch (reason) {
      setJsonError(reason instanceof Error ? reason.message : "The document is not valid JSON.");
    }
  }

  async function activate(revision: RequestApprovalPipelineRevisionSummary) {
    if (activeNumber === null) return;
    try {
      await api.activateRequestApprovalPipelineRevision(pipelineKey, revision.revision, activeNumber);
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
        "Someone else changed the active revision. Review the latest history below before trying again.",
      );
      await router.invalidate();
      return;
    }
    if (reason instanceof RequestError) {
      const refused = activationRefusal(reason);
      if (refused) {
        toast.add({ type: "deny", title: "Activation refused", description: refused });
        return;
      }
    }
    toast.add({
      type: "deny",
      title: "The pipeline could not be changed",
      description: reason instanceof Error ? reason.message : undefined,
    });
  }

  async function archive() {
    try {
      await api.archiveRequestApprovalPipeline(pipelineKey);
      setArchiving(false);
      toast.add({
        title: "Pipeline archived",
      });
      await navigate({ to: "/pipelines" });
    } catch (reason) {
      if (reason instanceof RequestError && reason.code === "request_approval_pipeline_in_use") {
        /* Another administrator activated a revision delegating here
         * after the page loaded; reload so the dialog lists it. */
        await router.invalidate();
        toast.add({
          type: "deny",
          title: "Archive refused",
          description: "Another pipeline now delegates to this one.",
        });
        return;
      }
      toast.add({
        type: "deny",
        title: "Archive failed",
        description: reason instanceof Error ? reason.message : undefined,
      });
    }
  }

  async function openRevision(summary: RequestApprovalPipelineRevisionSummary) {
    const request = ++revisionRequest.current;
    try {
      const revision = summary.revision === activeNumber && activeRevision
        ? activeRevision
        : await api.requestApprovalPipelineRevision(pipelineKey, summary.revision);
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

  /* The rail's state: viewed or edited, under this pipeline's key. */
  const rail: RailState = {
    mode: editing ? "edit" : "view",
    section: pipelineKey,
    reachability,
    directory,
    endpoints: endpointOptions,
    pipelines,
    onOpenPipeline: (key) => void navigate(pipelineLink(key)),
    tools: offered.toolSchemas,
    suggestions,
    valueSuggestions,
    valueDetails: offered.valueDetails,
    defaultTarget: currentDefault,
    describeRoute: describeTargetRoute,
    expandedKey,
    onExpandedKeyChange: setExpandedKey,
    run: test,
    cursor,
    availability,
    onManageEndpoints: () => window.open("/webhooks", "_blank", "noopener,noreferrer"),
    highlight: highlightedBlock ? { id: highlightedBlock, ref: blockRef } : undefined,
    editor: editing ? railEditor.editor : undefined,
  };

  /* Who hands requests to this pipeline, one chip per delegating
   * pipeline. */
  const delegatingKeys = useMemo(
    () => [...new Set(uses.map((use) => use.pipeline_key))],
    [uses],
  );

  if (missing) {
    return (
      <Page width="wide">
        <PageHeader back={<PageBackLink to="/pipelines">Approval pipelines</PageBackLink>}>
          <h1 className="text-title-lg text-ink">
            <span className="font-mono text-[19px]">{pipelineKey}</span>
          </h1>
        </PageHeader>
        <Callout tone="warn" icon={<AlertIcon width={15} height={15} />} title="No pipeline has this key">
          Check the key, or open a pipeline from the overview.
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
              Block {positionLabel(path)} “{block.name || block.id}” cannot run.
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
      <RailProvider value={rail}>
        <div className="flex flex-col">
          <PipelineRail
            blocks={railBlocks}
            lastLabel="Otherwise"
            emptyText={entry
              ? "No blocks yet. Every request goes to the default reviewers below."
              : "No blocks in this pipeline yet."}
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
                {editing ? draftName.trim() || pipelineName : displayed?.name ?? pipelineName}
              </h1>
              <Badge>
                <span className="font-mono text-[12px]">{pipelineKey}</span>
              </Badge>
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
              {entry
                ? "All requests go through this pipeline first."
                : "Requests reach this pipeline from delegate blocks in other pipelines."}
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
            {editable || (!entry && !archived && !editing && canActivate) ? (
              <Menu
                actions={[
                  ...(editable
                    ? [{
                        id: "test",
                        label: "Test request",
                        action: () => {
                          void requestPipelineSuggestions();
                          setTestOpen(true);
                        },
                      }]
                    : []),
                  ...(!entry && !archived && !editing && canActivate
                    ? [{
                        id: "archive",
                        label: "Archive pipeline",
                        danger: true,
                        action: () => setArchiving(true),
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
          You can view its revisions, but not activate them.
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

      {editing ? (
        <TextField
          label="Pipeline name"
          value={draftName}
          onChange={(event) => setDraftName(event.target.value)}
          className="max-w-[380px]"
          info="Renaming creates a new revision."
        />
      ) : null}

      <div className="flex min-w-0 flex-col">
        <PipelineEntryCard
          title={entry ? "Approval requests start here" : "Requests arrive from other pipelines"}
          description={entry ? undefined : delegatingKeys.length ? (
            <span className="flex flex-wrap items-center gap-x-1.5 gap-y-1">
              <span>Handed here by</span>
              {delegatingKeys.map((key) => (
                <ReferenceChip
                  key={key}
                  reference={{ kind: "pipeline", pipelineKey: key, name: pipelines.find((option) => option.key === key)?.name ?? (key === ENTRY_PIPELINE_KEY ? "Entry pipeline" : undefined) }}
                  link={pipelineLink(key)}
                  inline
                />
              ))}
            </span>
          ) : "Add a delegate block to another pipeline to send requests here."}
        />
        <PipelineConnector label="Check the blocks in order" />

        {blocksArea}

        {!reachability.stoppedBy && (editing || railBlocks.length === 0) ? <PipelineConnector /> : null}
        {reachability.stoppedBy ? (
          <UnreachableNotice block={reachability.stoppedBy} />
        ) : null}
        <div
          className={cn(
            "rounded-lg transition-shadow duration-200",
            settled && test && test.reasonCode === "end_of_pipeline" && "ring-2 ring-warn",
          )}
        >
          <PipelineTerminusCard
            target={currentDefault}
            directory={editing ? directory : undefined}
            onTargetChange={editing ? setDraftDefault : undefined}
            fallbackLabel={inheritedLabel}
            route={defaultRoute}
          />
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

      {archiving && uses.length > 0 ? (
        <Dialog open onOpenChange={(open) => !open && setArchiving(false)}>
          <DialogContent>
            <DialogTitle>This pipeline is in use</DialogTitle>
            <DialogDescription>
              {uses.length === 1
                ? "Remove the delegate block from the pipeline below, then archive this one."
                : "Remove the delegate blocks from the pipelines below, then archive this one."}
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
          title={`Archive ${pipelineName}?`}
          description="This can't be undone. The pipeline's key can't be reused."
          confirmLabel="Archive pipeline"
          typeToConfirm={pipelineName}
          onConfirm={archive}
        />
      ) : null}

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
              Restore what it depends on, or create a revision that doesn't need it.
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
          description="New requests will use this revision immediately."
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

/* Why the server refused to activate a revision, as a sentence for the
 * toast; null for an error that is not an activation refusal. */
export function activationRefusal(reason: RequestError): string | null {
  const keys = (name: string) => (reason.details?.[name] as string[] | undefined) ?? [];
  const list = (items: string[], singular: string, plural: string) =>
    `${items.length === 1 ? singular : plural} ${items.join(", ")} ${items.length === 1 ? "has" : "have"}`;
  switch (reason.code) {
    case "escalation_path_inactive": {
      const paths = keys("escalation_paths");
      return `${list(paths, "Escalation path", "Escalation paths")} no active revision. Activate one, then try again.`;
    }
    case "webhook_endpoint_inactive": {
      const endpoints = keys("webhook_endpoints");
      return `${list(endpoints, "Webhook endpoint", "Webhook endpoints")} no active revision. Activate one, then try again.`;
    }
    case "request_approval_pipeline_inactive": {
      const pipelines = keys("pipelines");
      return `${list(pipelines, "Pipeline", "Pipelines")} no active revision or ${pipelines.length === 1 ? "is" : "are"} archived.`;
    }
    case "request_approval_pipeline_delegation_cycle": {
      const pipelines = keys("pipelines");
      return `Activating it would make pipelines delegate in a circle${pipelines.length ? ` (${pipelines.join(" → ")})` : ""}. Remove one of the delegate blocks.`;
    }
    case "request_approval_pipeline_delegation_too_deep": {
      const maximum = reason.details?.maximum;
      return `Pipelines would delegate more than ${typeof maximum === "number" ? maximum : "the allowed number of"} levels deep. Flatten the chain.`;
    }
    case "request_approval_pipeline_too_large":
      return "The pipelines together would hold too many blocks. Remove blocks or delegate less.";
    default:
      return null;
  }
}

/* What blocks activating a revision: the paths, endpoints, pipelines,
 * and block runtimes that are no longer in place. Healthy dependencies
 * are not listed; the dialog is for what to restore. */
function RevisionDependencies({
  references,
  pathNames,
}: {
  references: PipelineReferences;
  pathNames: Map<string, string>;
}) {
  const paths = references.escalation_paths.filter((path) => !path.active);
  const endpoints = references.webhook_endpoints.filter((endpoint) => !endpoint.active);
  const pipelines = references.pipelines.filter((pipeline) => !pipeline.active);
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
      {pipelines.map((pipeline) => (
        <li key={`pipeline-${pipeline.pipeline_key}`} className="flex items-center gap-2 text-body text-ink-secondary">
          <Badge tone="deny">{pipeline.exists ? "Archived" : "Missing"}</Badge>
          <span>
            Pipeline{" "}
            {pipeline.exists ? (
              <ReferenceChip
                reference={{ kind: "pipeline", pipelineKey: pipeline.pipeline_key, name: pipeline.name }}
                link={pipelineLink(pipeline.pipeline_key)}
                className="-my-1"
              />
            ) : (
              <span className="font-mono text-[12px]">{pipeline.pipeline_key}</span>
            )}
            {describeReferrers(pipeline.block_keys, false)}
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
  default_ask_agent_owner?: boolean;
} {
  const parsed = JSON.parse(text) as { blocks?: unknown; default_escalation_path?: unknown; default_ask_agent_owner?: unknown };
  if (!parsed || typeof parsed !== "object" || !Array.isArray(parsed.blocks)) {
    throw new Error("The pipeline document must be an object with a blocks array.");
  }
  checkDocumentBlocks(parsed.blocks, [], new Map());
  const defaultPath = parsed.default_escalation_path;
  if (defaultPath !== undefined && typeof defaultPath !== "string") {
    throw new Error("default_escalation_path must be an escalation path key.");
  }
  const askOwner = parsed.default_ask_agent_owner;
  if (askOwner !== undefined && typeof askOwner !== "boolean") {
    throw new Error("default_ask_agent_owner must be true or false.");
  }
  return {
    blocks: parsed.blocks as RequestApprovalBlockDefinition[],
    default_escalation_path: defaultPath,
    ...(askOwner ? { default_ask_agent_owner: true } : {}),
  };
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
        outcome?: string;
        endpoint_key?: unknown;
        allowed_outcomes?: unknown;
        blocks?: unknown;
        pipeline_key?: unknown;
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
      case "outcome":
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
      case "branch":
        if (!block.config?.when) {
          throw new Error(`${label} is missing config.when.`);
        }
        if (!Array.isArray(block.config.blocks)) {
          throw new Error(`${label} is missing config.blocks, the blocks the branch runs.`);
        }
        checkDocumentBlocks(block.config.blocks, [...path, index + 1], keys);
        break;
      case "delegate":
        if (!block.config?.when) {
          throw new Error(`${label} is missing config.when.`);
        }
        if (typeof block.config.pipeline_key !== "string" || block.config.pipeline_key === "") {
          throw new Error(`${label} is missing config.pipeline_key, the pipeline matching requests continue in.`);
        }
        if (block.config.pipeline_key === ENTRY_PIPELINE_KEY) {
          throw new Error(`${label} cannot hand requests to the entry pipeline.`);
        }
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

/* The types as a sentence lists them: "branch", "delegate", or "outcome". */
function listTypes(types: string[]): string {
  const quoted = types.map((type) => `"${type}"`);
  return `${quoted.slice(0, -1).join(", ")}, or ${quoted[quoted.length - 1]}`;
}
