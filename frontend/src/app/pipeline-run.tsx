import { useRef, useState, type RefObject } from "react";
import { cn } from "@/ds/lib/cn";
import type { PipelineOption } from "@/ds/components/delegate-block";
import { PipelineBranchEndCard, PipelineTerminusCard } from "@/ds/components/pipeline-flow";
import type { ReviewerTarget } from "@/ds/components/reviewer-target";
import type { RequestApprovalPipelineRevision } from "../types";
import { draftsFromBlocks, pathTarget, type DelegateDraft } from "./pipeline-blocks";
import { PipelineRail, RailProvider, routeDescriber, testRing, type RailState } from "./pipeline-rail";
import { statusAt, stepKey, type TestRun } from "./pipeline-replay";
import { pipelineReachability } from "./pipeline-tree";

/*
 * A run the pipeline made for a real request, drawn down the rails of
 * the revisions that ran: every block passed, decided, or never
 * reached. A delegate the request entered unfolds the pipeline it
 * handed the request to in a well under its card, so the whole path
 * reads top to bottom. A pipeline the reader may not read is a line in
 * its place. Steps show their outcome alone, never their evidence.
 */

export interface RunSection {
  pipelineKey: string;
  name?: string;
  revision: number;
  /* Null when the reader may not read the revision. */
  document: RequestApprovalPipelineRevision | null;
}

export function RecordedPipelineRun({
  sections,
  run,
  directory = [],
  highlight,
}: {
  /* Every pipeline the run entered, the one it started in first. */
  sections: RunSection[];
  run: TestRun;
  /* The escalation paths, for naming the reviewers a human exit asks. */
  directory?: ReviewerTarget[];
  /* One block picked out, by its pipeline and block key. */
  highlight?: { pipelineKey: string; blockId: string };
}) {
  const [expandedKey, setExpandedKey] = useState<string | null>(null);
  const highlightRef = useRef<HTMLDivElement | null>(null);
  const root = sections[0];
  if (!root) return null;
  const pipelines: PipelineOption[] = sections.map((section) => ({
    key: section.pipelineKey,
    name: section.document?.name ?? section.name ?? section.pipelineKey,
    active: true,
  }));
  const context: RunContext = {
    sections,
    run,
    directory,
    pipelines,
    expandedKey,
    onExpandedKeyChange: setExpandedKey,
    highlight: highlight ? { ...highlight, ref: highlightRef } : undefined,
  };
  if (!root.document) return <HiddenSection />;
  const defaultTarget = sectionDefault(root, directory, null);
  const reached = run.reasonCode === "end_of_pipeline";
  return (
    <div className="flex flex-col">
      <SectionRail context={context} section={root} defaultTarget={defaultTarget} inheritedLabel="Any authorized reviewer" />
      <div
        className={cn(
          "rounded-lg transition-[box-shadow,opacity] duration-200",
          reached ? "ring-2 ring-warn" : "opacity-40",
        )}
      >
        <PipelineTerminusCard target={defaultTarget} />
      </div>
    </div>
  );
}

interface RunContext {
  sections: RunSection[];
  run: TestRun;
  directory: ReviewerTarget[];
  pipelines: PipelineOption[];
  expandedKey: string | null;
  onExpandedKeyChange: (key: string | null) => void;
  highlight?: { pipelineKey: string; blockId: string; ref: RefObject<HTMLDivElement | null> };
}

/* A pipeline's default, else the one it inherits from the pipeline
 * that delegated to it. */
function sectionDefault(section: RunSection, directory: ReviewerTarget[], inherited: ReviewerTarget | null) {
  const document = section.document;
  return pathTarget(document?.default_escalation_path, directory, document?.default_ask_agent_owner) ?? inherited;
}

function HiddenSection() {
  return <p className="py-1 text-body text-ink-muted">You can't view this pipeline.</p>;
}

/* One pipeline's rail, under its own key, with every delegate the
 * request entered unfolded inside it. */
function SectionRail({
  context,
  section,
  defaultTarget,
  inheritedLabel,
}: {
  context: RunContext;
  section: RunSection;
  defaultTarget: ReviewerTarget | null;
  inheritedLabel: string;
}) {
  const { run, directory } = context;
  const blocks = draftsFromBlocks(section.document?.blocks ?? [], directory);
  const reachability = pipelineReachability(blocks);
  const delegateContents = (delegate: DelegateDraft) => {
    const target = context.sections.find((candidate) => candidate.pipelineKey === delegate.pipelineKey);
    if (!target) return null;
    if (!target.document) return <HiddenSection />;
    const targetDefault = sectionDefault(target, directory, defaultTarget);
    const targetBlocks = draftsFromBlocks(target.document.blocks, directory);
    const endStatus = statusAt(run, run.steps.length, stepKey(section.pipelineKey, delegate.id, true));
    return (
      <>
        <SectionRail
          context={context}
          section={target}
          defaultTarget={targetDefault}
          inheritedLabel="The delegating pipeline's default"
        />
        {pipelineReachability(targetBlocks).stoppedBy ? null : (
          <div
            className={cn(
              "rounded-lg transition-[box-shadow,opacity] duration-200",
              testRing(endStatus, run.outcome),
              endStatus === "unreached" && "opacity-40",
            )}
          >
            <PipelineBranchEndCard
              branchName={target.document.name}
              defaultTarget={targetDefault}
            />
          </div>
        )}
      </>
    );
  };
  const rail: RailState = {
    mode: "view",
    section: section.pipelineKey,
    reachability,
    directory,
    endpoints: [],
    pipelines: context.pipelines,
    defaultTarget,
    describeRoute: routeDescriber(defaultTarget, inheritedLabel),
    expandedKey: context.expandedKey,
    onExpandedKeyChange: context.onExpandedKeyChange,
    run,
    cursor: run.steps.length,
    evidence: false,
    delegateContents,
    highlight: context.highlight?.pipelineKey === section.pipelineKey
      ? { id: context.highlight.blockId, ref: context.highlight.ref }
      : undefined,
  };
  return (
    <RailProvider value={rail}>
      <PipelineRail blocks={blocks} lastLabel="Otherwise" emptyText="No blocks in this pipeline." />
    </RailProvider>
  );
}
