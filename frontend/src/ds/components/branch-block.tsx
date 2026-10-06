import type { ComponentProps } from "react";
import type { ToolSchema } from "@/ds/lib/tool-schema";
import { BlockCard, blockMenuActions, type BlockMenuHandlers, type BlockPosition } from "./block-card";
import { BlockSummaryCard, SummaryField, SummaryText } from "./block-summary";
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
import { TargetBadge, TargetSelect, type ReviewerTarget } from "./reviewer-target";
import { TextField } from "./text-field";
import { PersonIcon, XIcon } from "@/ds/icons";

/*
 * A branch of an approval pipeline: a block that runs its own ordered
 * blocks only for requests its condition matches. A request that does
 * not match skips every block inside and continues down the rail.
 * Inside, blocks run as they do anywhere: approve, deny or ask a human
 * ends the whole pipeline, and continue moves to the branch's next
 * block. When no block in the branch decides, the request goes to a
 * human with the branch's reason and escalation path, and the pipeline
 * stops there. Branches nest; the rail draws the blocks inside, so the
 * card itself edits only the branch's own fields.
 */

export interface BranchBlockDraft<Child = unknown> {
  id: string;
  name: string;
  enabled: boolean;
  when: ConditionNode;
  /* The branch's own blocks, in order. The app decides what a block is. */
  blocks: Child[];
  /* The explanation recorded when no block inside decides. Optional:
   * a branch whose blocks end in an Outcome block never needs one. */
  reason: string;
  /* Who is asked when no block inside decides. null or absent uses the
   * pipeline default. Blocks inside never inherit it. */
  target?: ReviewerTarget | null;
}

/* Where the branch's fields are explained. */
export const BRANCH_DOCS = {
  when: {
    href: "https://withhuman.ai/docs/web-app/branch-blocks#when-a-branch-runs",
    label: "Writing branch conditions",
  },
  end: {
    href: "https://withhuman.ai/docs/web-app/branch-blocks#when-nothing-decides",
    label: "When nothing decides",
  },
} as const;

/* The one-line summary: the kind, then the condition as a sentence. */
export function summarizeBranch(block: Pick<BranchBlockDraft, "when">): string {
  return `Branch · ${summarizeCondition(block.when)}`;
}

export interface BranchBlockCardProps<Child = unknown> extends BlockMenuHandlers {
  block: BranchBlockDraft<Child>;
  position: BlockPosition;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  onChange: (next: BranchBlockDraft<Child>) => void;
  /* Removes the branch and puts its blocks where it stood. The menu
   * offers it only while the branch holds blocks. */
  onUnwrap?: () => void;
  /* Spread onto the grip handle; without it the handle is not rendered. */
  handleProps?: ComponentProps<"div">;
  dragging?: boolean;
  tools?: ToolSchema[];
  suggestions?: string[];
  valueSuggestions?: ConditionValueSuggestions;
  valueDetails?: ConditionValueDetails;
  /* The conditions of the branches this one sits in, which narrow the
   * condition builder's field hints. */
  scope?: ConditionNode[];
  /* The escalation paths the branch's end may name. Without it the card
   * offers no choice and the end uses the pipeline default. */
  directory?: ReviewerTarget[];
  /* The resolved reviewer destination, included in the field help. */
  describeRoute?: (target: ReviewerTarget | null) => string;
  /* False when a block inside decides every request that gets that far,
   * so the branch never runs out and its end's fields mean nothing. */
  endReachable?: boolean;
}

export function BranchBlockCard<Child>({
  block,
  position,
  expanded,
  onExpandedChange,
  onChange,
  onUnwrap,
  handleProps,
  dragging = false,
  tools,
  suggestions,
  valueSuggestions,
  valueDetails,
  scope,
  directory,
  describeRoute,
  endReachable = true,
  ...menu
}: BranchBlockCardProps<Child>) {
  const actions = blockMenuActions({
    ...menu,
    noun: "branch",
    extra:
      block.blocks.length > 0 && onUnwrap
        ? [{
            id: "unwrap",
            label: "Remove branch, keep its blocks",
            group: "danger",
            icon: <XIcon width={14} height={14} />,
            action: onUnwrap,
          }]
        : [],
  });
  return (
    <BlockCard
      {...menu}
      actions={actions}
      position={position}
      title={block.name || "Untitled branch"}
      subtitle={<>Branch · <ConditionSummary node={block.when} /></>}
      enabled={block.enabled}
      expanded={expanded}
      onExpandedChange={onExpandedChange}
      handleProps={handleProps}
      dragging={dragging}
    >
      {/* Reads in evaluation order: name, who enters, then what happens
        * when nothing inside decides. */}
      <TextField
        label="Name"
        value={block.name}
        onChange={(event) => onChange({ ...block, name: event.target.value })}
        className="max-w-[380px]"
      />

      <div className="flex flex-col gap-1.5">
        <InfoLabel
          className="text-label text-ink-secondary"
          info="Choose which requests run the blocks in this branch."
          docsLinks={[BRANCH_DOCS.when]}
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

      {endReachable ? (
        <p className="text-body text-ink-secondary">
          If no block in this branch decides, the request goes to a human and the pipeline stops.
        </p>
      ) : null}

      {endReachable && directory ? (
        <div className="flex flex-col gap-1.5">
          <InfoLabel
            className="text-label text-ink-secondary"
            info={[
              "Choose who reviews a request when no block in this branch decides.",
              describeRoute?.(block.target ?? null),
            ].filter(Boolean).join(" ")}
            docsLinks={[BRANCH_DOCS.end]}
          >
            Escalation path
          </InfoLabel>
          <TargetSelect
            directory={directory}
            value={block.target ?? null}
            onChange={(target) => onChange({ ...block, target })}
            fallbackLabel="Pipeline default"
          />
        </div>
      ) : null}

      {endReachable ? (
        <TextField
          label="Reason (optional)"
          info="The explanation reviewers see when no block in this branch decides."
          docsLinks={[BRANCH_DOCS.end]}
          value={block.reason}
          onChange={(event) => onChange({ ...block, reason: event.target.value })}
          placeholder="Refunds need a person when no check decides."
        />
      ) : null}
    </BlockCard>
  );
}

/* The read-only face of a branch: who enters, what happens when no
 * block inside decides, and why. The rail draws the blocks inside. */
export function BranchBlockSummaryCard({
  block,
  position,
  onNavigate,
  expanded,
  onExpandedChange,
  describeRoute,
  endReachable = true,
}: {
  block: BranchBlockDraft;
  position: BlockPosition;
  /* Takes the user to the scope's own editor. */
  onNavigate?: () => void;
  expanded?: boolean;
  /* Present when the card opens to its details. */
  onExpandedChange?: (expanded: boolean) => void;
  /* The resolved reviewer destination, shown under the path. */
  describeRoute?: (target: ReviewerTarget | null) => string;
  /* False when the branch never runs out; its end is then left out. */
  endReachable?: boolean;
}) {
  const route = describeRoute?.(block.target ?? null);
  return (
    <BlockSummaryCard
      position={position}
      title={block.name || "Untitled branch"}
      subtitle={<>Branch · <ConditionSummary node={block.when} /></>}
      enabled={block.enabled}
      onNavigate={onNavigate}
      expanded={expanded}
      onExpandedChange={onExpandedChange}
    >
      <SummaryField label="When">
        <ConditionTree node={block.when} />
      </SummaryField>

      {endReachable ? (
        <>
          <SummaryField label="If no block in this branch decides">
            <span className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-body text-ink">
              <PersonIcon width={13} height={13} className="shrink-0 text-ink-muted" />
              Ask a human, and the pipeline stops
            </span>
            <TargetBadge target={block.target ?? null} fallbackLabel="Pipeline default" className="self-start" />
            {route ? <p className="text-micro text-ink-muted">{route}</p> : null}
          </SummaryField>

          <SummaryField label="Reason">
            <SummaryText empty="No reason given.">{block.reason}</SummaryText>
          </SummaryField>
        </>
      ) : null}
    </BlockSummaryCard>
  );
}
