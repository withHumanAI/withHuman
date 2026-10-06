import type { ComponentProps } from "react";
import { BlockCard, type BlockMenuHandlers, type BlockPosition } from "./block-card";
import { BlockSummaryCard, SummaryField, SummaryText } from "./block-summary";
import { InfoLabel } from "./info-label";
import { OutcomeSelect, type BlockOutcome } from "./outcome-select";
import { OUTCOME_COPY, OUTCOME_ICONS } from "./pipeline-flow";
import { TargetBadge, TargetSelect, type ReviewerTarget } from "./reviewer-target";
import { TextField } from "./text-field";

/*
 * An outcome block decides every request that reaches it: approve,
 * deny, or ask a human, and the pipeline stops there. It has no
 * condition of its own; "if X, approve" is a branch on X holding one,
 * so the branch decides who arrives and the block what happens to them.
 */

export interface OutcomeBlockDraft {
  id: string;
  name: string;
  enabled: boolean;
  outcome: BlockOutcome;
  reason: string;
  /* Who is asked when the outcome is human. null or absent inherits
   * the pipeline default from the terminus. */
  target?: ReviewerTarget | null;
}

/* The subtitle both faces share. */
const OUTCOME_SUBTITLE = "Outcome · Every request that reaches it";

const OUTCOME_DOCS = {
  href: "https://withhuman.ai/docs/web-app/approval-pipelines#outcome-blocks",
} as const;

/* Move up, Move down and the moves across a branch's edge are the
 * keyboard-reachable fallbacks for the grip, shown in the block menu. */
export interface OutcomeBlockCardProps extends BlockMenuHandlers {
  block: OutcomeBlockDraft;
  position: BlockPosition;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  onChange: (next: OutcomeBlockDraft) => void;
  /* Spread onto the grip handle; the parent wires its drag behavior.
   * Without it the handle is not rendered. */
  handleProps?: ComponentProps<"div">;
  /* True while this card is the one being dragged. */
  dragging?: boolean;
  /* Teams and people a human outcome may name. Without it the block
   * offers no reviewer choice and a human outcome uses the default. */
  directory?: ReviewerTarget[];
  /* The resolved reviewer destination, included in the field help. */
  describeRoute?: (target: ReviewerTarget | null) => string;
}

export function OutcomeBlockCard({
  block,
  position,
  expanded,
  onExpandedChange,
  onChange,
  handleProps,
  dragging = false,
  directory,
  describeRoute,
  ...menu
}: OutcomeBlockCardProps) {
  return (
    <BlockCard
      {...menu}
      position={position}
      title={block.name || "Untitled block"}
      subtitle={OUTCOME_SUBTITLE}
      enabled={block.enabled}
      expanded={expanded}
      onExpandedChange={onExpandedChange}
      handleProps={handleProps}
      dragging={dragging}
    >
      {/* Reads in evaluation order: name, outcome, reviewers, and
        * reason. */}
      <TextField
        label="Name"
        value={block.name}
        onChange={(event) => onChange({ ...block, name: event.target.value })}
        className="max-w-[380px]"
      />

      <p className="text-body text-ink-secondary">Decides every request that reaches this block.</p>

      <div className="flex flex-col gap-1.5">
        <InfoLabel
          className="text-label text-ink-secondary"
          info="Choose what happens to every request that reaches this block."
          docsLinks={[{ ...OUTCOME_DOCS, label: "Choosing outcomes" }]}
        >
          Outcome
        </InfoLabel>
        <OutcomeSelect
          aria-label="Outcome"
          className="self-start"
          value={block.outcome}
          onValueChange={(outcome) => onChange({ ...block, outcome })}
        />
      </div>

      {/* Only a human outcome has a "who". The row appears under the
       * outcome so the sentence reads: ask a human, these ones. */}
      {block.outcome === "human" && directory ? (
        <div className="flex flex-col gap-1.5">
          <InfoLabel
            className="text-label text-ink-secondary"
            info={["Choose who gets notified to approve these requests.", describeRoute?.(block.target ?? null)].filter(Boolean).join(" ")}
            docsLinks={[{ href: "https://withhuman.ai/docs/web-app/approval-pipelines#human-review", label: "Routing requests to reviewers" }]}
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

      <TextField
        label="Reason"
        info="The explanation the agent receives when this block decides the request."
        docsLinks={[{ ...OUTCOME_DOCS, label: "Decision reasons" }]}
        value={block.reason}
        onChange={(event) => onChange({ ...block, reason: event.target.value })}
        placeholder="Small priority refunds are pre-authorized."
      />
    </BlockCard>
  );
}

/* The read-only face of an outcome block, sharing the editor's fields. */
export function OutcomeBlockSummaryCard({
  block,
  position,
  onNavigate,
  expanded,
  onExpandedChange,
  describeRoute,
}: {
  block: OutcomeBlockDraft;
  position: BlockPosition;
  /* Takes the user to the scope's own editor. */
  onNavigate?: () => void;
  expanded?: boolean;
  /* Present when the card opens to its details. */
  onExpandedChange?: (expanded: boolean) => void;
  /* The resolved reviewer destination, shown under the path. */
  describeRoute?: (target: ReviewerTarget | null) => string;
}) {
  const OutcomeIcon = OUTCOME_ICONS[block.outcome];
  const route = block.outcome === "human" ? describeRoute?.(block.target ?? null) : undefined;
  return (
    <BlockSummaryCard
      position={position}
      title={block.name || "Untitled block"}
      subtitle={OUTCOME_SUBTITLE}
      enabled={block.enabled}
      onNavigate={onNavigate}
      expanded={expanded}
      onExpandedChange={onExpandedChange}
    >
      <SummaryField label="Outcome">
        <span className="flex items-center gap-1.5 text-body text-ink">
          <OutcomeIcon width={13} height={13} className="shrink-0 text-ink-muted" />
          {OUTCOME_COPY[block.outcome]}
        </span>
      </SummaryField>

      {block.outcome === "human" ? (
        <SummaryField label="Escalation path">
          <TargetBadge target={block.target ?? null} fallbackLabel="Pipeline default" className="self-start" />
          {route ? <p className="text-micro text-ink-muted">{route}</p> : null}
        </SummaryField>
      ) : null}

      <SummaryField label="Reason">
        <SummaryText empty="No reason given.">{block.reason}</SummaryText>
      </SummaryField>
    </BlockSummaryCard>
  );
}
