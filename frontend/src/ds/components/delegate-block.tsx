import type { ComponentProps } from "react";
import type { ToolSchema } from "@/ds/lib/tool-schema";
import { BlockCard, blockMenuActions, type BlockMenuHandlers, type BlockPosition } from "./block-card";
import { BlockSummaryCard, SummaryField } from "./block-summary";
import { Button } from "./button";
import {
  ConditionBuilder,
  ConditionSummary,
  ConditionTree,
  summarizeCondition,
  type ConditionNode,
  type ConditionValueDetails,
  type ConditionValueSuggestions,
} from "./condition-builder";
import { InfoLabel } from "./info-label";
import { Referenced } from "./reference";
import { Badge } from "./badge";
import { Select, SelectContent, SelectItem, SelectTrigger } from "./select";
import { TextField } from "./text-field";
import { ArrowUpRightIcon, BranchIcon } from "@/ds/icons";

/*
 * A delegate block of an approval pipeline: a branch whose body is
 * another pipeline. Requests its condition matches continue in the
 * named pipeline's active revision; requests that do not match skip it
 * and continue down the rail. The condition belongs to this pipeline,
 * the way an ingress belongs to the team that routes traffic; what
 * happens after the match belongs to the target pipeline's owners. The
 * block carries no reason and no escalation path: when the target runs
 * out of blocks, the request goes to a human with the target's default
 * path, else this pipeline's, and the pipeline stops there.
 */

export interface DelegateBlockDraft {
  id: string;
  name: string;
  enabled: boolean;
  when: ConditionNode;
  /* The key of the pipeline matching requests continue in. */
  pipelineKey: string;
}

/* What the picker needs to know about a pipeline: its key, its name,
 * and whether a revision is active. */
export interface PipelineOption {
  key: string;
  name: string;
  active: boolean;
}

/* Where the delegate's fields are explained. */
export const DELEGATE_DOCS = {
  when: {
    href: "https://withhuman.ai/docs/web-app/delegate-blocks#when-a-delegate-runs",
    label: "Writing delegate conditions",
  },
  target: {
    href: "https://withhuman.ai/docs/web-app/delegate-blocks#the-target-pipeline",
    label: "The target pipeline",
  },
} as const;

export function findPipeline(pipelines: PipelineOption[], key: string): PipelineOption | undefined {
  return pipelines.find((pipeline) => pipeline.key === key);
}

/* The one-line summary: the kind, then the condition as a sentence. */
export function summarizeDelegate(block: Pick<DelegateBlockDraft, "when">): string {
  return `Delegate · ${summarizeCondition(block.when)}`;
}

/* The target as the subtitle names it: its name when known, else its
 * key, else nothing chosen yet. */
function targetLabel(block: DelegateBlockDraft, pipeline: PipelineOption | undefined): string {
  if (pipeline) return pipeline.active ? pipeline.name : `${pipeline.name} (archived)`;
  return block.pipelineKey || "No pipeline selected";
}

export interface DelegateBlockCardProps extends BlockMenuHandlers {
  block: DelegateBlockDraft;
  /* The pipelines the block may hand to: every live one the author may
   * read, except the pipeline being edited and the entry pipeline. */
  pipelines: PipelineOption[];
  position: BlockPosition;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  onChange: (next: DelegateBlockDraft) => void;
  /* Opens the chosen pipeline's page. */
  onOpenPipeline?: (pipelineKey: string) => void;
  /* Spread onto the grip handle; without it the handle is not rendered. */
  handleProps?: ComponentProps<"div">;
  dragging?: boolean;
  tools?: ToolSchema[];
  suggestions?: string[];
  valueSuggestions?: ConditionValueSuggestions;
  valueDetails?: ConditionValueDetails;
  /* The conditions of the branches this block sits in, which narrow the
   * condition builder's field hints. */
  scope?: ConditionNode[];
}

export function DelegateBlockCard({
  block,
  pipelines,
  position,
  expanded,
  onExpandedChange,
  onChange,
  onOpenPipeline,
  handleProps,
  dragging = false,
  tools,
  suggestions,
  valueSuggestions,
  valueDetails,
  scope,
  ...menu
}: DelegateBlockCardProps) {
  const actions = blockMenuActions({ ...menu, noun: "block" });
  const pipeline = findPipeline(pipelines, block.pipelineKey);
  return (
    <BlockCard
      {...menu}
      actions={actions}
      position={position}
      title={block.name || "Untitled delegate"}
      subtitle={<>Delegate · <ConditionSummary node={block.when} /> · {targetLabel(block, pipeline)}</>}
      enabled={block.enabled}
      expanded={expanded}
      onExpandedChange={onExpandedChange}
      handleProps={handleProps}
      dragging={dragging}
    >
      {/* Reads in evaluation order: name, who enters, where they go. */}
      <TextField
        label="Name"
        value={block.name}
        onChange={(event) => onChange({ ...block, name: event.target.value })}
        className="max-w-[380px]"
      />

      <div className="flex flex-col gap-1.5">
        <InfoLabel
          className="text-label text-ink-secondary"
          info="Requests that match continue in the other pipeline."
          docsLinks={[DELEGATE_DOCS.when]}
        >
          When
        </InfoLabel>
        <ConditionBuilder
          tools={tools}
          value={block.when}
          onChange={(when) => onChange({ ...block, when })}
          suggestions={suggestions}
          valueSuggestions={valueSuggestions}
          valueDetails={valueDetails}
          scope={scope}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <InfoLabel
          className="text-label text-ink-secondary"
          info="Matching requests continue in this pipeline."
          docsLinks={[DELEGATE_DOCS.target]}
        >
          Continue in pipeline
        </InfoLabel>
        <div className="flex flex-wrap items-center gap-2">
          <Select
            items={pipelines}
            itemToStringLabel={(item) => (item as PipelineOption).name}
            value={pipeline ?? null}
            onValueChange={(next) => {
              if (next) onChange({ ...block, pipelineKey: (next as PipelineOption).key });
            }}
          >
            <SelectTrigger
              aria-label="Continue in pipeline"
              className="w-72"
              placeholder={pipelines.length ? "Choose a pipeline" : "No other pipelines yet"}
            />
            <SelectContent<PipelineOption>>
              {(item) => (
                <SelectItem key={item.key} value={item}>
                  <span className="flex items-baseline gap-2">
                    <span>{item.active ? item.name : `${item.name} (archived)`}</span>
                    <span className="font-mono text-[11px] text-ink-muted">{item.key}</span>
                  </span>
                </SelectItem>
              )}
            </SelectContent>
          </Select>
          {onOpenPipeline && block.pipelineKey ? (
            <Button variant="ghost" size="sm" onClick={() => onOpenPipeline(block.pipelineKey)}>
              Open pipeline
              <ArrowUpRightIcon width={13} height={13} />
            </Button>
          ) : null}
        </div>
        {(pipeline && !pipeline.active) || (!pipeline && block.pipelineKey) ? (
          <p role="alert" className="text-micro text-deny">
            {pipeline
              ? "This pipeline is archived. Choose another before activating this revision."
              : "This pipeline is not one you can see, or no longer exists. Choose another one."}
          </p>
        ) : null}
      </div>
    </BlockCard>
  );
}

/* The read-only face of a delegate: who enters, and the pipeline they
 * continue in. That pipeline's blocks are its own page's business. */
export function DelegateBlockSummaryCard({
  block,
  pipelines,
  position,
  onNavigate,
  expanded,
  onExpandedChange,
}: {
  block: DelegateBlockDraft;
  pipelines: PipelineOption[];
  position: BlockPosition;
  /* Takes the user to the pipeline's own editor. */
  onNavigate?: () => void;
  expanded?: boolean;
  /* Present when the card opens to its details. */
  onExpandedChange?: (expanded: boolean) => void;
}) {
  const pipeline = findPipeline(pipelines, block.pipelineKey);
  return (
    <BlockSummaryCard
      position={position}
      title={block.name || "Untitled delegate"}
      subtitle={<>Delegate · <ConditionSummary node={block.when} /> · {targetLabel(block, pipeline)}</>}
      enabled={block.enabled}
      onNavigate={onNavigate}
      expanded={expanded}
      onExpandedChange={onExpandedChange}
    >
      <SummaryField label="When">
        <ConditionTree node={block.when} />
      </SummaryField>

      <SummaryField label="Continue in pipeline">
        <span className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-body text-ink">
          <Referenced reference={{ kind: "pipeline", pipelineKey: block.pipelineKey, name: pipeline?.name }}>
            <Badge className="gap-1" icon={<BranchIcon width={13} height={13} />}>
              {pipeline?.name ?? <span className="font-mono text-[12px]">{block.pipelineKey}</span>}
            </Badge>
          </Referenced>
          {pipeline && !pipeline.active ? <Badge tone="warn">Archived</Badge> : null}
        </span>
        <p className="text-micro text-ink-muted">
          Matching requests continue in this pipeline.
        </p>
      </SummaryField>
    </BlockSummaryCard>
  );
}
