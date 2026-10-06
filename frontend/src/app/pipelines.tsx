import { useMemo, useState } from "react";
import { Link, useNavigate, useRouter } from "@tanstack/react-router";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Card } from "@/ds/components/card";
import { IconButton } from "@/ds/components/icon-button";
import { Menu, MenuContent, MenuTrigger } from "@/ds/components/menu";
import {
  PipelineConnector,
  PipelineEntryCard,
  PipelineTerminusCard,
} from "@/ds/components/pipeline-flow";
import { Referenced } from "@/ds/components/reference";
import { ReferenceChip } from "./reference-chip";
import type { ReviewerTarget } from "@/ds/components/reviewer-target";
import { Table, type TableColumn } from "@/ds/components/table";
import { TextField } from "@/ds/components/text-field";
import { ToggleGroup, ToggleGroupItem } from "@/ds/components/toggle-group";
import { Page } from "@/ds/components/page";
import { useToast } from "@/ds/components/toast";
import { BranchIcon, CornerDownRightIcon, EllipsisIcon } from "@/ds/icons";
import { api } from "../api";
import { useAccess } from "./authorization";
import { formatRelative } from "./format";
import {
  defaultSample,
  definitionsFromStored,
  handOffTestSample,
  asksAgentOwner,
  loadPathDirectory,
  pathKeyOf,
  pathTarget,
  routeKeyOf,
} from "./pipeline-blocks";
import { pipelineLink } from "./pipeline-link";
import { TestRequestDialog } from "./test-request-dialog";
import {
  ENTRY_PIPELINE_KEY,
  type PipelineDelegation,
  type RequestApprovalPipelineRevision,
  type RequestApprovalPipelineSummary,
} from "../types";
import { useTitle } from "./use-title";
import { useRevisionActivation } from "./use-revision-activation";

/*
 * The approval pipelines overview, drawn as the pipeline every request
 * experiences: entry, the entry pipeline (which every request runs),
 * one labeled fan-out into the table of pipelines its delegate blocks
 * hand to, and the human terminus.
 */

/* What the pipelines route loader delivers. A failed load is reported
 * by the screen rather than thrown, so an expired session reaches the
 * sign-in gate, not the router's error boundary. */
export interface PipelinesPage {
  pipelines: RequestApprovalPipelineSummary[];
  /* The entry pipeline's active revision, for the default reviewers
   * shortcut; null when the viewer may not read it. */
  entryRevision: RequestApprovalPipelineRevision | null;
  /* The organization's escalation paths, the choices for the default. */
  directory: ReviewerTarget[];
  defaultReviewers: ReviewerTarget | null;
  error: string;
}

/* The table's filter: the pipelines still in service (the default),
 * the archived ones, or both. */
type PipelineFilter = "all" | "active" | "archived";

export async function loadPipelinesPage(): Promise<PipelinesPage> {
  try {
    const response = await api.requestApprovalPipelines("all");
    const pipelines = response.request_approval_pipelines;
    const entry = pipelines.find((pipeline) => pipeline.pipeline_key === ENTRY_PIPELINE_KEY);
    /* Read the active revision to resolve the default reviewers, and the
     * paths so the terminus card can offer them. */
    const [entryRevision, directory] = await Promise.all([
      entry?.active_revision
        ? api.requestApprovalPipelineRevision(ENTRY_PIPELINE_KEY, entry.active_revision).catch(() => null)
        : Promise.resolve(null),
      loadPathDirectory(),
    ]);
    return {
      pipelines,
      entryRevision,
      directory,
      defaultReviewers: pathTarget(entryRevision?.default_escalation_path, directory, entryRevision?.default_ask_agent_owner),
      error: "",
    };
  } catch (reason) {
    return {
      pipelines: [],
      entryRevision: null,
      directory: [],
      defaultReviewers: null,
      error: reason instanceof Error ? reason.message : "Unable to load approval pipelines.",
    };
  }
}

export function PipelinesScreen({ page }: { page: PipelinesPage }) {
  const navigate = useNavigate();
  const router = useRouter();
  const { can, scopeOf } = useAccess();
  /* Whether the viewer reads every pipeline. The server narrows the list
   * the same way, and the entry pipeline is out of a scoped reader's
   * reach unless their grant names it. */
  const readsAll = scopeOf("pipeline.read") === "all";
  const entryResource = { kind: "pipeline", id: ENTRY_PIPELINE_KEY } as const;
  const canReadEntry = can("pipeline.read", entryResource);
  const canCreate = can("pipeline.write");
  const toast = useToast();
  const revisionActivation = useRevisionActivation();
  const { pipelines, entryRevision, directory, defaultReviewers, error } = page;
  const [filter, setFilter] = useState("");
  const [status, setStatus] = useState<PipelineFilter>("active");
  useTitle("Approval pipelines");

  const entry = pipelines.find((pipeline) => pipeline.pipeline_key === ENTRY_PIPELINE_KEY);
  const others = useMemo(
    () => pipelines.filter((pipeline) => pipeline.pipeline_key !== ENTRY_PIPELINE_KEY),
    [pipelines],
  );
  const archivedCount = others.filter((pipeline) => pipeline.archived_at).length;
  const rows = useMemo(() => {
    const query = filter.trim().toLowerCase();
    return others.filter((pipeline) => {
      if (status === "active" && pipeline.archived_at) return false;
      if (status === "archived" && !pipeline.archived_at) return false;
      return (
        pipeline.pipeline_key.toLowerCase().includes(query) ||
        pipeline.name.toLowerCase().includes(query)
      );
    });
  }, [others, filter, status]);

  /* Testing from the overview: the run opens the entry pipeline, which
   * replays the whole order, delegated pipelines included, on its rail. */
  const canTest = can("pipeline.write", entryResource);
  const [testOpen, setTestOpen] = useState(false);
  const [sampleText, setSampleText] = useState(() => JSON.stringify(defaultSample(), null, 2));
  async function runTest(): Promise<string | null> {
    handOffTestSample(ENTRY_PIPELINE_KEY, sampleText);
    setTestOpen(false);
    await navigate(pipelineLink(ENTRY_PIPELINE_KEY));
    return null;
  }

  /* The shortcut saves an entry pipeline revision, then uses the same
   * activation choice as the full pipeline editor. */
  const canEditDefault = entryRevision !== null && can("pipeline.write", entryResource);
  const [editingDefault, setEditingDefault] = useState(false);
  const [draftDefault, setDraftDefault] = useState<ReviewerTarget | null>(null);
  const [savingDefault, setSavingDefault] = useState(false);
  function startEditDefault() {
    setDraftDefault(defaultReviewers);
    setEditingDefault(true);
  }
  function cancelDefault() {
    setEditingDefault(false);
  }
  async function saveDefault() {
    if (!entryRevision) return;
    if (
      routeKeyOf(draftDefault) ===
      routeKeyOf(pathTarget(entryRevision.default_escalation_path, [], entryRevision.default_ask_agent_owner))
    ) {
      setEditingDefault(false);
      return;
    }
    setSavingDefault(true);
    try {
      /* The blocks go back exactly as stored; only the default changes. */
      const key = pathKeyOf(draftDefault);
      const revision = await api.createRequestApprovalPipelineRevision(ENTRY_PIPELINE_KEY, {
        blocks: definitionsFromStored(entryRevision.blocks),
        ...(key ? { default_escalation_path: key } : {}),
        ...(asksAgentOwner(draftDefault) ? { default_ask_agent_owner: true } : {}),
      });
      await revisionActivation.offer({
        revision: revision.revision,
        activeRevision: entryRevision.revision,
        canActivate: can("pipeline.activate", entryResource),
        activationDescription: "Once active, new requests use these default reviewers.",
        activate: () => api.activateRequestApprovalPipelineRevision(ENTRY_PIPELINE_KEY, revision.revision, entryRevision.revision),
      });
      setEditingDefault(false);
      await router.invalidate();
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "The default reviewers could not be saved",
        description: reason instanceof Error ? reason.message : undefined,
      });
    } finally {
      setSavingDefault(false);
    }
  }

  const columns = useMemo<TableColumn<RequestApprovalPipelineSummary>[]>(
    () => [
      {
        id: "pipeline",
        label: "Pipeline",
        grow: true,
        cell: (pipeline) => (
          <span
            className="flex min-w-0 flex-col items-start gap-0.5"
            onClick={(event) => event.stopPropagation()}
            onKeyDown={(event) => event.stopPropagation()}
          >
            <ReferenceChip
              reference={{ kind: "pipeline", pipelineKey: pipeline.pipeline_key, name: pipeline.name }}
              link={pipelineLink(pipeline.pipeline_key)}
            />
            <span className="pl-2 font-mono text-[12px] text-ink-muted">{pipeline.pipeline_key}</span>
          </span>
        ),
      },
      {
        id: "referenced_by",
        label: "Delegated from",
        info: "The delegate blocks that send requests to this pipeline.",
        docsLinks: [{ href: "https://withhuman.ai/docs/web-app/approval-pipelines", label: "Delegating to a pipeline" }],
        width: 300,
        cell: (pipeline) => <DelegatedFrom pipeline={pipeline} />,
      },
      {
        id: "status",
        label: "Status",
        info: "The version used for new requests, or when this pipeline was archived.",
        docsLinks: [{ href: "https://withhuman.ai/docs/web-app/approval-pipelines#drafts-and-activation", label: "Activating a pipeline" }],
        width: 170,
        cell: (pipeline) =>
          pipeline.archived_at ? (
            <Badge tone="warn">Archived {new Date(pipeline.archived_at).toLocaleDateString()}</Badge>
          ) : (
            <Badge tone="approve">Active revision {pipeline.active_revision}</Badge>
          ),
      },
      {
        id: "revisions",
        label: "Revisions",
        info: "Saved versions of this pipeline.",
        docsLinks: [{ href: "https://withhuman.ai/docs/web-app/approval-pipelines#revision-history-and-rollback", label: "Revision history and rollback" }],
        width: 84,
        cell: (pipeline) => pipeline.revision_count,
      },
      {
        id: "updated",
        label: "Updated",
        info: "When changes to this pipeline were last saved.",
        docsLinks: [{ href: "https://withhuman.ai/docs/web-app/approval-pipelines#drafts-and-activation", label: "Saving pipeline changes" }],
        width: 110,
        cell: (pipeline) => (
          <span className="text-ink-muted">
            {formatRelative(pipeline.latest_created_at)} ago
          </span>
        ),
      },
    ],
    [],
  );

  return (
    <Page width="wide">
      {revisionActivation.dialog}
      <div className="flex flex-col items-start justify-between gap-4 sm:flex-row sm:gap-6">
        <div className="min-w-0">
          <h1 className="text-title-lg text-ink">Approval pipelines</h1>
          <p className="mt-1.5 max-w-[620px] text-body text-ink-muted">
            Approval pipelines let you decide what to do with approval requests automatically.{" "}
            {readsAll
              ? "All requests go through the entry pipeline first."
              : "You see the pipelines you manage."}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2 self-end sm:self-auto">
          {canTest ? (
            <Button variant="ghost" onClick={() => setTestOpen(true)}>
              Test request
            </Button>
          ) : null}
          {canCreate ? (
            <Button variant="primary" onClick={() => void navigate({ to: "/pipelines/new" })}>
              New pipeline
            </Button>
          ) : null}
        </div>
      </div>

      {error ? (
        <Callout tone="deny" title="Pipelines could not be loaded">
          <div className="flex items-center justify-between gap-4">
            <span>{error}</span>
            <Button size="sm" onClick={() => void router.invalidate()}>
              Try again
            </Button>
          </div>
        </Callout>
      ) : null}

      <div className="flex flex-col">
        <PipelineEntryCard />
        <PipelineConnector label={canReadEntry ? "First, check the entry pipeline" : "Check the pipelines you manage"} />

        {canReadEntry && entry ? (
        <Card
          role="button"
          tabIndex={0}
          onClick={() => void navigate(pipelineLink(ENTRY_PIPELINE_KEY))}
          onKeyDown={(event) => {
            if (event.key === "Enter" || event.key === " ") {
              event.preventDefault();
              void navigate(pipelineLink(ENTRY_PIPELINE_KEY));
            }
          }}
          className="flex cursor-pointer items-center gap-3 px-4 py-2.5 transition-colors duration-100 hover:bg-fill-hover"
        >
          <BranchIcon width={14} height={14} className="shrink-0 text-ink-muted" />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
              <h3 className="text-label font-medium text-ink" onClick={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()}>
                <Referenced reference={{ kind: "pipeline", pipelineKey: ENTRY_PIPELINE_KEY, name: entry.name }} inline focusable={false}>
                  <Link {...pipelineLink(ENTRY_PIPELINE_KEY)}>{entry.name}</Link>
                </Referenced>
              </h3>
              <Badge tone="approve">Active revision {entry.active_revision}</Badge>
            </div>
            <p className="mt-0.5 text-micro text-ink-muted">
              All requests go through these rules first.
            </p>
          </div>
          <span
            className="ml-auto flex items-center"
            onClick={(event) => event.stopPropagation()}
            onKeyDown={(event) => event.stopPropagation()}
          >
            <Menu
              actions={[
                {
                  id: "view",
                  group: "open",
                  label: "View pipeline",
                  action: () => void navigate(pipelineLink(ENTRY_PIPELINE_KEY)),
                },
                ...(canTest
                  ? [{ id: "test", label: "Test request", action: () => setTestOpen(true) }]
                  : []),
              ]}
            >
              <MenuTrigger
                render={
                  <IconButton aria-label="Actions for the entry pipeline" size="sm">
                    <EllipsisIcon width={15} height={15} />
                  </IconButton>
                }
              />
              <MenuContent align="end" />
            </Menu>
          </span>
        </Card>
        ) : null}

        {canReadEntry && entry ? (
          <PipelineConnector label="If a delegate block matches">
            <span className="text-micro text-ink-muted">
              continue in the pipeline it names
            </span>
          </PipelineConnector>
        ) : null}

        <Card className="overflow-x-auto">
          <div className="flex flex-wrap items-center gap-3 border-b-hairline border-edge px-4 py-3">
            <TextField
              aria-label="Filter pipelines"
              placeholder="Filter pipelines"
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
              className="w-[220px]"
            />
            <span className="text-micro text-ink-muted">
              {rows.length} of {others.length} pipelines
            </span>
            <span className="ml-auto flex items-center gap-3">
              {archivedCount > 0 ? (
                <ToggleGroup
                  aria-label="Filter pipelines"
                  value={status}
                  onValueChange={(value) => setStatus(value as PipelineFilter)}
                >
                  <ToggleGroupItem value="all">All</ToggleGroupItem>
                  <ToggleGroupItem value="active">Active</ToggleGroupItem>
                  <ToggleGroupItem value="archived">Archived ({archivedCount})</ToggleGroupItem>
                </ToggleGroup>
              ) : null}
            </span>
          </div>
          {rows.length ? (
            <Table
              aria-label="Delegated pipelines"
              items={rows}
              getId={(pipeline) => pipeline.pipeline_key}
              selected={new Set()}
              onSelectedChange={() => undefined}
              selectable={false}
              openOnClick
              openLabel="View pipeline"
              onOpen={(pipeline) => void navigate(pipelineLink(pipeline.pipeline_key))}
              columns={columns}
              rowLabel={(pipeline) => pipeline.name}
              className="min-w-[920px]"
            />
          ) : (
            <p className="px-4 py-6 text-body text-ink-muted">
              {others.length === 0 && !readsAll ? (
                "No pipelines you manage yet."
              ) : others.length === 0 ? (
                canCreate
                  ? "No other pipelines yet. Create one to give a team its own rules."
                  : "No other pipelines yet."
              ) : status === "archived" ? (
                "No archived pipeline matches."
              ) : status === "active" && rows.length === 0 && !filter.trim() ? (
                "All other pipelines are archived."
              ) : (
                <>
                  No pipeline matches{" "}
                  <span className="font-mono text-[12px] text-ink-secondary">{filter.trim()}</span>.
                </>
              )}
            </p>
          )}
        </Card>

        <PipelineConnector label="If no decision is made" />
        <PipelineTerminusCard
          target={editingDefault ? draftDefault : defaultReviewers}
          directory={editingDefault ? directory : undefined}
          onTargetChange={editingDefault ? setDraftDefault : undefined}
          onSave={editingDefault ? () => void saveDefault() : undefined}
          onCancel={editingDefault ? cancelDefault : undefined}
          saving={savingDefault}
          actions={
            canEditDefault && !editingDefault
              ? [{ id: "edit", label: "Change default reviewers", action: startEditDefault }]
              : undefined
          }
        />
      </div>

      <TestRequestDialog
        open={testOpen}
        onOpenChange={setTestOpen}
        sampleText={sampleText}
        onSampleTextChange={setSampleText}
        onRun={runTest}
        description="See what would happen to a sample request, starting in the entry pipeline."
      />
    </Page>
  );
}

/* Who hands requests to a pipeline: each delegating pipeline once, with
 * how many of its delegate blocks name this one, and a link per block
 * that opens that pipeline with the block picked out. */
function DelegatedFrom({ pipeline }: { pipeline: RequestApprovalPipelineSummary }) {
  const groups = useMemo(() => {
    const byPipeline = new Map<string, PipelineDelegation[]>();
    for (const delegation of pipeline.referenced_by) {
      const blocks = byPipeline.get(delegation.pipeline_key) ?? [];
      blocks.push(delegation);
      byPipeline.set(delegation.pipeline_key, blocks);
    }
    return [...byPipeline.values()];
  }, [pipeline.referenced_by]);
  if (pipeline.archived_at) {
    return <span className="text-ink-muted">None</span>;
  }
  if (groups.length === 0) {
    return <span className="text-micro text-ink-muted">No delegate blocks send requests here</span>;
  }
  return (
    <span
      className="flex min-w-0 flex-col items-start gap-1.5 py-1"
      onClick={(event) => event.stopPropagation()}
      onKeyDown={(event) => event.stopPropagation()}
    >
      {groups.map((blocks) => {
        const from = blocks[0];
        return (
          <span key={from.pipeline_key} className="flex min-w-0 max-w-full flex-col items-start gap-0.5">
            <span className="flex min-w-0 max-w-full items-center gap-1.5">
              <ReferenceChip
                reference={{ kind: "pipeline", pipelineKey: from.pipeline_key, name: from.pipeline_name }}
                link={pipelineLink(from.pipeline_key)}
              />
              <span className="shrink-0 text-micro text-ink-muted">
                {blocks.length === 1 ? "1 delegate block" : `${blocks.length} delegate blocks`}
              </span>
            </span>
            {blocks.map((block) => (
              <Link
                key={block.block_key}
                {...pipelineLink(block.pipeline_key, { block: block.block_key })}
                aria-label={`Open the delegate block “${block.block_name || block.block_key}” in ${block.pipeline_name}`}
                className="ml-2 flex min-w-0 max-w-full items-center gap-1 rounded-sm text-micro text-ink-secondary hover:text-ink hover:underline"
                onClick={(event) => event.stopPropagation()}
              >
                <CornerDownRightIcon width={12} height={12} className="shrink-0 text-ink-muted" />
                <span className="truncate">{block.block_name || block.block_key}</span>
              </Link>
            ))}
          </span>
        );
      })}
    </span>
  );
}
