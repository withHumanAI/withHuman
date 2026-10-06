import type { ComponentProps } from "react";
import { BlockCard, type BlockMenuHandlers, type BlockPosition } from "./block-card";
import { BlockSummaryCard, SummaryField, SummaryText } from "./block-summary";
import { Callout } from "./callout";
import { TargetBadge, type ReviewerTarget } from "./reviewer-target";

/*
 * The face of a block whose kind this bundle does not know: a kind
 * another edition adds, met in a stored revision. Both faces show what
 * every block has (its name, type, reason and reviewers) and say that
 * this deployment cannot edit it. The editing card keeps the block
 * menu, so the block can still be moved or removed from a draft; its
 * settings stay exactly as stored.
 */

export interface UnknownBlockDraftFields {
  id: string;
  name: string;
  enabled: boolean;
  /* The block type the revision stored. */
  type: string;
  reason: string;
  target?: ReviewerTarget | null;
}

function summarizeUnknown(block: UnknownBlockDraftFields): string {
  return `${block.type} · not available in this edition`;
}

function UnknownBlockDetails({
  block,
  describeRoute,
}: {
  block: UnknownBlockDraftFields;
  describeRoute?: (target: ReviewerTarget | null) => string;
}) {
  const route = describeRoute?.(block.target ?? null);
  return (
    <>
      <Callout tone="warn">
        This deployment can't edit "{block.type}" blocks.
      </Callout>
      <SummaryField label="Reason">
        <SummaryText empty="No reason stored.">{block.reason}</SummaryText>
      </SummaryField>
      <SummaryField label="Escalation path">
        <TargetBadge target={block.target ?? null} fallbackLabel="Pipeline default" className="self-start" />
        {route ? <p className="text-micro text-ink-muted">{route}</p> : null}
      </SummaryField>
    </>
  );
}

export interface UnknownBlockCardProps extends BlockMenuHandlers {
  block: UnknownBlockDraftFields;
  position: BlockPosition;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  handleProps?: ComponentProps<"div">;
  dragging?: boolean;
  describeRoute?: (target: ReviewerTarget | null) => string;
}

export function UnknownBlockCard({
  block,
  position,
  expanded,
  onExpandedChange,
  handleProps,
  dragging = false,
  describeRoute,
  ...menu
}: UnknownBlockCardProps) {
  return (
    <BlockCard
      {...menu}
      position={position}
      title={block.name || "Untitled block"}
      subtitle={summarizeUnknown(block)}
      enabled={block.enabled}
      expanded={expanded}
      onExpandedChange={onExpandedChange}
      handleProps={handleProps}
      dragging={dragging}
    >
      <UnknownBlockDetails block={block} describeRoute={describeRoute} />
    </BlockCard>
  );
}

export function UnknownBlockSummaryCard({
  block,
  position,
  expanded,
  onExpandedChange,
  describeRoute,
}: {
  block: UnknownBlockDraftFields;
  position: BlockPosition;
  expanded?: boolean;
  onExpandedChange?: (expanded: boolean) => void;
  describeRoute?: (target: ReviewerTarget | null) => string;
}) {
  return (
    <BlockSummaryCard
      position={position}
      title={block.name || "Untitled block"}
      subtitle={summarizeUnknown(block)}
      enabled={block.enabled}
      expanded={expanded}
      onExpandedChange={onExpandedChange}
    >
      <UnknownBlockDetails block={block} describeRoute={describeRoute} />
    </BlockSummaryCard>
  );
}
