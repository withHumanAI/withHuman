import {
  createContext,
  useContext,
  useRef,
  useState,
  type Dispatch,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
  type RefObject,
  type SetStateAction,
} from "react";
import type { ToolSchema } from "@/ds/lib/tool-schema";
import { cn } from "@/ds/lib/cn";
import { Badge } from "@/ds/components/badge";
import { BranchBlockCard, BranchBlockSummaryCard } from "@/ds/components/branch-block";
import { DelegateBlockCard, DelegateBlockSummaryCard, type PipelineOption } from "@/ds/components/delegate-block";
import type { ConditionNode, ConditionValueDetails, ConditionValueSuggestions } from "@/ds/components/condition-builder";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import { OutcomeBlockCard, OutcomeBlockSummaryCard } from "@/ds/components/outcome-block";
import {
  PipelineAddSlot,
  PipelineBranchEndCard,
  PipelineBranchWell,
  PipelineConnector,
  PipelineDelegateExits,
  PipelineExits,
  type PipelineAddChoice,
} from "@/ds/components/pipeline-flow";
import { Referenced } from "@/ds/components/reference";
import { agentOwnerLabel, type ReviewerTarget } from "@/ds/components/reviewer-target";
import { UnknownBlockCard, UnknownBlockSummaryCard } from "@/ds/components/unknown-block";
import {
  WebhookBlockCard,
  WebhookBlockSummaryCard,
  webhookExits,
  type WebhookEndpointOption,
} from "@/ds/components/webhook-block";
import {
  AlertIcon,
  ArrowUpRightIcon,
  ChevronDownIcon,
  CornerDownRightIcon,
  SettingsIcon,
} from "@/ds/icons";
import { blockErrorLabel } from "./block-errors";
import { blockKind, extensions, requireBlockKind } from "./extensions";
import { duplicateDraft, newDraftBlock, type BlockDraft, type BranchDraft, type DelegateDraft } from "./pipeline-blocks";
import {
  statusAt,
  stepAt,
  stepKey,
  takenExit,
  type RailSection,
  type TestOutcome,
  type TestRun,
  type TestStatus,
  type TestStep,
} from "./pipeline-replay";
import {
  appendToRail,
  countDrafts,
  insertAfter,
  moveIntoBranchAbove,
  moveOutOfBranch,
  positionLabel,
  removeDraft,
  reorderRail,
  unwrapBranch,
  updateDraft,
  type Reachability,
} from "./pipeline-tree";

/*
 * One pipeline drawn down its rail, in viewing and editing alike: each
 * block's card, what a test replay found there, and its exits. A
 * branch's blocks sit in an indented well under the branch, drawn by
 * the same rail one level down, and end in the branch's end card when
 * a request can get that far. A
 * delegate is one card and an exit naming the pipeline matching requests
 * continue in; that pipeline's blocks belong to its owners and are not
 * drawn here, except where a recorded run unfolds the pipeline a
 * request entered (delegateContents). The screen's state
 * (mode, section, replay, edits) reaches every level through
 * RailContext rather than props.
 */

export interface RailDragSource {
  /* The rail the drag started in: a branch's id, or null for the
   * section's own rail. A drag never leaves it. */
  parentId: string | null;
  index: number;
}

export interface RailDrag {
  dragging: RailDragSource | null;
  start: (source: RailDragSource & { container: HTMLElement | null; event: ReactPointerEvent }) => void;
}

/* What editing adds to the rail: every change goes through update as a
 * function of the current tree. */
export interface RailEditor {
  update: (change: (drafts: BlockDraft[]) => BlockDraft[]) => void;
  drag: RailDrag;
  /* Asks before a branch that holds blocks is removed. */
  confirmRemove: (branch: BranchDraft) => void;
}

export interface RailState {
  mode: "view" | "edit";
  /* The key of the pipeline this rail draws. */
  section: RailSection;
  reachability: Reachability;
  directory: ReviewerTarget[];
  endpoints: WebhookEndpointOption[];
  /* The pipelines a delegate may hand to, for its picker and labels. */
  pipelines?: PipelineOption[];
  onOpenPipeline?: (pipelineKey: string) => void;
  tools?: ToolSchema[];
  suggestions?: string[];
  valueSuggestions?: ConditionValueSuggestions;
  valueDetails?: ConditionValueDetails;
  /* Who a human exit that names no path asks: the pipeline default, else
   * the organization's. */
  defaultTarget: ReviewerTarget | null;
  describeRoute: (target: ReviewerTarget | null) => string;
  /* The one expanded card, as `${section}/${block id}`. */
  expandedKey: string | null;
  onExpandedKeyChange: (key: string | null) => void;
  /* The test replay and how far it has got. */
  run: TestRun | null;
  cursor: number;
  /* Whether replayed steps show the evidence under their cards: a
   * kind's own and a webhook's delivery. Failures always show. */
  evidence?: boolean;
  /* What an entered delegate draws in a well under its card, in place
   * of its exit: the pipeline it handed the request to, for a recorded
   * run. Without it, or when it draws nothing, the exit names the
   * pipeline and its blocks are not drawn. */
  delegateContents?: (delegate: DelegateDraft) => ReactNode;
  /* The block a review link points at, anywhere in the tree. */
  highlight?: { id: string; ref: RefObject<HTMLDivElement | null> };
  /* Present while editing. */
  editor?: RailEditor;
  /* Which of the block kinds that depend on deployment configuration
   * (webhook, and every kind the edition adds) this deployment can run,
   * by type. A kind not listed is offered. */
  availability?: Record<string, boolean>;
  onManageEndpoints?: () => void;
}

const RailContext = createContext<RailState | null>(null);

export function RailProvider({ value, children }: { value: RailState; children: ReactNode }) {
  return <RailContext.Provider value={value}>{children}</RailContext.Provider>;
}

function useRail(): RailState {
  const rail = useContext(RailContext);
  if (!rail) throw new Error("PipelineRail needs a RailProvider.");
  return rail;
}

/* The rail's describeRoute for a pipeline whose default is
 * defaultTarget: what a human exit that names a route, or none, asks.
 * inheritedLabel is what no default means there. */
export function routeDescriber(defaultTarget: ReviewerTarget | null, inheritedLabel: string) {
  return (target: ReviewerTarget | null) =>
    target?.type === "agent_owner"
      ? `Asks the agent's owner. Without one, it goes to ${target.id ? target.name : "the pipeline default"}.`
      : target
        ? ""
        : `Uses pipeline default: ${defaultTarget?.type === "agent_owner" ? agentOwnerLabel(defaultTarget.name) : (defaultTarget?.name ?? inheritedLabel)}.`;
}

export function testRing(status: TestStatus, outcome: TestOutcome | undefined): string {
  if (status === "evaluating") return "ring-2 ring-accent";
  if (status !== "terminal") return "";
  if (outcome === "approve") return "ring-2 ring-approve";
  if (outcome === "deny") return "ring-2 ring-deny";
  return "ring-2 ring-warn";
}

/* The kinds of block an add slot offers, at any depth: core's own, then
 * the kinds the edition registered, then Branch, which holds any of
 * them, and Delegate, which hands to another pipeline. A kind this
 * deployment cannot run stays listed, disabled, so the choice explains
 * itself. */
export function addChoices(availability: Record<string, boolean> = {}): PipelineAddChoice[] {
  const enabled = (type: string) => availability[type] ?? true;
  return [
    {
      id: "outcome",
      label: "Outcome block",
      icon: <ChevronDownIcon width={14} height={14} />,
      info: "Approves, denies, or asks a human about every request that reaches it.",
    },
    {
      id: "webhook",
      label: enabled("webhook") ? "Webhook block" : "Webhook block (unavailable)",
      icon: <SettingsIcon width={14} height={14} />,
      disabled: !enabled("webhook"),
    },
    ...extensions.blockKinds.map((kind): PipelineAddChoice => ({
      id: kind.type,
      label: enabled(kind.type) ? kind.label : `${kind.label} (unavailable)`,
      icon: kind.icon,
      info: enabled(kind.type) ? kind.description : kind.unavailable,
      disabled: !enabled(kind.type),
    })),
    {
      id: "branch",
      label: "Branch",
      icon: <CornerDownRightIcon width={14} height={14} />,
      info: "Runs its own blocks only for requests that match its conditions.",
    },
    {
      id: "delegate",
      label: "Delegate to pipeline",
      icon: <ArrowUpRightIcon width={14} height={14} />,
      info: "Sends requests that match its conditions to another pipeline.",
    },
  ];
}

/* The index a pointer at clientY drops at among a rail's own rows. Only
 * the rail's direct rows count, and each is measured at its own card, so
 * a branch's tall well neither joins the list nor skews its midpoint. */
export function dropIndex(container: HTMLElement, clientY: number): number | null {
  const rows = [...container.querySelectorAll<HTMLElement>(":scope > [data-block-row]")];
  if (!rows.length) return null;
  let target = 0;
  for (const row of rows) {
    const card = row.querySelector<HTMLElement>(":scope > [data-block-card]") ?? row;
    const rect = card.getBoundingClientRect();
    if (clientY > rect.top + rect.height / 2) target += 1;
  }
  return Math.min(target, rows.length - 1);
}

/* Pointer-based drag reordering within one rail. */
function useRailDrag(
  update: RailEditor["update"],
  onStart: () => void,
): RailDrag {
  const [dragging, setDragging] = useState<RailDragSource | null>(null);
  const state = useRef<(RailDragSource & { container: HTMLElement }) | null>(null);

  function start({ parentId, index, container, event }: RailDragSource & { container: HTMLElement | null; event: ReactPointerEvent }) {
    if (event.button !== 0 || !container) return;
    event.preventDefault();
    state.current = { parentId, index, container };
    setDragging({ parentId, index });
    onStart();

    function onMove(move: PointerEvent) {
      const current = state.current;
      if (!current) return;
      const target = dropIndex(current.container, move.clientY);
      if (target === null || target === current.index) return;
      const from = current.index;
      update((drafts) => reorderRail(drafts, current.parentId, from, target));
      current.index = target;
      setDragging({ parentId: current.parentId, index: target });
    }

    function onUp() {
      state.current = null;
      setDragging(null);
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      window.removeEventListener("pointercancel", onUp);
      /* The click that follows pointerup would land on the card header
       * and toggle it; swallow exactly that one. */
      const squash = (click: MouseEvent) => {
        click.stopPropagation();
        click.preventDefault();
      };
      window.addEventListener("click", squash, { capture: true, once: true });
      window.setTimeout(() => window.removeEventListener("click", squash, { capture: true }), 0);
    }

    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointercancel", onUp);
  }

  return { dragging, start };
}

/* The editing half of the rail's state for a draft held in a screen's
 * state, and the dialog that confirms removing a branch with blocks. */
export function useRailEditor(
  setDraft: Dispatch<SetStateAction<BlockDraft[]>>,
  onDragStart: () => void,
): { editor: RailEditor; dialog: ReactNode } {
  const [removal, setRemoval] = useState<{ branch: BranchDraft; open: boolean } | null>(null);
  const drag = useRailDrag(setDraft, onDragStart);
  const count = removal ? countDrafts(removal.branch.blocks) : 0;
  const dialog = (
    <ConfirmDialog
      open={removal?.open ?? false}
      onOpenChange={(open) => {
        if (!open) setRemoval((current) => (current ? { ...current, open: false } : null));
      }}
      title={removal
        ? `Remove “${removal.branch.name || "Untitled branch"}” and its ${count} ${count === 1 ? "block" : "blocks"}?`
        : ""}
      description="The branch and its blocks are removed from this draft."
      confirmLabel="Remove branch"
      onConfirm={() => {
        if (removal) setDraft((drafts) => removeDraft(drafts, removal.branch.id));
      }}
    />
  );
  return {
    editor: { update: setDraft, drag, confirmRemove: (branch) => setRemoval({ branch, open: true }) },
    dialog,
  };
}

export function UnreachableNotice({ block, children }: { block: BlockDraft; children?: ReactNode }) {
  return (
    <div className="my-3 flex flex-wrap items-center gap-x-2 gap-y-1 text-micro text-ink-muted">
      <Badge tone="warn" icon={<AlertIcon width={12} height={12} />}>Unreachable</Badge>
      <span>Requests stop at “{block.name || "Untitled block"}”, an earlier Outcome block. {children}</span>
    </div>
  );
}

/* What a replayed step of a registered kind shows under its card: the
 * kind's own evidence, read next to the exit it took. */
function KindEvidenceRow({ step, block }: { step?: TestStep; block: BlockDraft }) {
  if (!step || block.kind !== "edition") return null;
  const Evidence = blockKind(block.type)?.evidence;
  return Evidence ? <Evidence step={step} block={block} /> : null;
}

/* A replayed webhook step's delivery, in machine mono under the card:
 * host, status, latency, and the endpoint's own reason. */
function DeliveryEvidenceRow({ step, block, endpoints }: {
  step?: TestStep;
  block: BlockDraft;
  endpoints: WebhookEndpointOption[];
}) {
  if (!step?.delivery) return null;
  const { endpointHost, httpStatus, durationMs, reason } = step.delivery;
  const endpoint = block.kind === "webhook" ? endpoints.find((item) => item.key === block.endpointKey) : undefined;
  const parts = [httpStatus ? `HTTP ${httpStatus}` : "no response", `${durationMs} ms`];
  if (step.errorCode) parts.push(step.errorCode);
  return (
    <div className="pt-1 pl-8">
      <p className="font-mono text-[11px] text-ink-muted">
        {block.kind === "webhook" && block.endpointKey ? (
          <Referenced reference={{ kind: "webhook_endpoint", key: block.endpointKey, name: endpoint?.name ?? block.endpointKey }} inline>
            {endpointHost || endpoint?.name || block.endpointKey}
          </Referenced>
        ) : endpointHost || "endpoint"}
        {" · "}{parts.join(" · ")}
        {reason ? <span className="text-ink-secondary">{` · "${reason}"`}</span> : null}
      </p>
    </div>
  );
}

/* A replayed step that failed instead of answering: the code and what
 * it means, so its human exit is not mistaken for a match. A webhook's
 * delivery row, when shown, already carries its code. */
function ErrorEvidenceRow({ step, block, deliveryShown }: { step?: TestStep; block: BlockDraft; deliveryShown: boolean }) {
  if (!step?.errorCode || (deliveryShown && block.kind === "webhook" && step.delivery)) return null;
  return (
    <div className="pt-1 pl-8">
      <p className="font-mono text-[11px] text-warn">
        Failed · {step.errorCode}
        <span className="text-ink-secondary">{` · ${blockErrorLabel(step.errorCode)}, so the request goes to a human`}</span>
      </p>
    </div>
  );
}

/* The exit fork under a block. A branch draws its own: the well. */
function BlockExits({
  block,
  step,
  continueLabel,
  defaultTarget,
}: {
  block: BlockDraft;
  step?: TestStep;
  continueLabel?: ReactNode;
  defaultTarget?: ReviewerTarget | null;
}) {
  const rail = useRail();
  const matchedKey = takenExit(step, block);
  switch (block.kind) {
    case "outcome":
      return <PipelineExits exits={[{ key: "match", label: "Every request", outcome: block.outcome, target: block.target }]} matchedKey={matchedKey} defaultTarget={defaultTarget} />;
    case "edition":
      return <PipelineExits {...requireBlockKind(block.type).exits(block)} matchedKey={matchedKey} defaultTarget={defaultTarget} />;
    case "unknown":
      /* Its exits are its own business; the rail runs on past it. */
      return <PipelineExits exits={[]} continueLabel={continueLabel ?? "Continue"} />;
    case "webhook":
      return <PipelineExits {...webhookExits(block)} matchedKey={matchedKey} defaultTarget={defaultTarget} />;
    case "delegate": {
      const pipeline = rail.pipelines?.find((option) => option.key === block.pipelineKey);
      const terminal = statusAt(rail.run, rail.cursor, stepKey(rail.section, block.id)) === "terminal";
      const target = block.pipelineKey ? (
        <Referenced reference={{ kind: "pipeline", pipelineKey: block.pipelineKey, name: pipeline?.name }} inline>
          {pipeline?.name ?? <span className="font-mono text-[11px]">{block.pipelineKey}</span>}
        </Referenced>
      ) : "a pipeline";
      const contents = matchedKey === "enter" ? rail.delegateContents?.(block) : undefined;
      if (contents) {
        return (
          <PipelineBranchWell
            name={pipeline?.name ?? block.pipelineKey}
            taken="enter"
            label={<>If conditions are met, continue in {target}</>}
            continueLabel={continueLabel ?? "Otherwise, check the next block"}
          >
            {contents}
          </PipelineBranchWell>
        );
      }
      return (
        <PipelineDelegateExits
          pipeline={target}
          taken={matchedKey === "enter" ? "enter" : matchedKey === "continue" ? "skip" : undefined}
          result={matchedKey === "enter" && terminal ? rail.run?.outcome : undefined}
          continueLabel={continueLabel}
        />
      );
    }
    case "branch":
      return null;
  }
}

interface RailLevel {
  /* The branch whose blocks this rail draws; null for the section's own. */
  parent: BranchDraft | null;
  /* The positions of the enclosing branches, from the top rail down. */
  path: number[];
  /* 0 for the section's own rail, 1 inside a branch, and so on. */
  depth: number;
  /* The conditions of the enclosing branches, for field hints. */
  scope: ConditionNode[];
  /* The label under the rail's last block, where the rail runs out. */
  lastLabel: ReactNode;
  /* An enclosing row is already faded as unreached; nothing inside it
   * fades again. */
  dimmed: boolean;
}

/*
 * One rail of blocks, recursively: the section's own, or a branch's
 * inside its well. In editing it ends in its own add slot.
 */
export function PipelineRail({
  blocks,
  parent = null,
  path = [],
  depth = 0,
  scope = [],
  lastLabel,
  emptyText,
  dimmed = false,
}: {
  blocks: BlockDraft[];
  parent?: BranchDraft | null;
  path?: number[];
  depth?: number;
  scope?: ConditionNode[];
  lastLabel: ReactNode;
  /* Shown instead of an empty rail while viewing. */
  emptyText?: string;
  dimmed?: boolean;
}) {
  const rail = useRail();
  const listRef = useRef<HTMLDivElement | null>(null);
  const editor = rail.mode === "edit" ? rail.editor : undefined;
  const parentId = parent?.id ?? null;
  const level: RailLevel = { parent, path, depth, scope, lastLabel, dimmed };
  if (!blocks.length && !editor) {
    return emptyText ? (
      <p className="rounded-lg border-hairline border-dashed border-edge px-4 py-3 text-body text-ink-muted">
        {emptyText}
      </p>
    ) : null;
  }
  const dragging = editor?.drag.dragging;
  return (
    <div
      ref={listRef}
      className={dragging && dragging.parentId === parentId ? "cursor-grabbing select-none" : undefined}
    >
      {blocks.map((block, index) => (
        <RailRow key={block.id} block={block} index={index} siblings={blocks} level={level} listRef={listRef} />
      ))}
      {editor ? (
        <PipelineAddSlot
          label={parent
            ? blocks.length ? "Add block to this branch" : "Add the first block to this branch"
            : blocks.length ? "Add block" : "Add the first block"}
          choices={addChoices(rail.availability)}
          onChoose={(type) => {
            const block = newDraftBlock(type);
            editor.update((drafts) => appendToRail(drafts, parentId, block));
            rail.onExpandedKeyChange(`${rail.section}/${block.id}`);
          }}
        />
      ) : null}
    </div>
  );
}

function RailRow({
  block,
  index,
  siblings,
  level,
  listRef,
}: {
  block: BlockDraft;
  index: number;
  siblings: BlockDraft[];
  level: RailLevel;
  listRef: RefObject<HTMLDivElement | null>;
}) {
  const rail = useRail();
  const key = stepKey(rail.section, block.id);
  const status = statusAt(rail.run, rail.cursor, key);
  const step = stepAt(rail.run, rail.cursor, key);
  const blockedBy = rail.reachability.blockedBy.get(block.id);
  const here = [...level.path, index + 1];
  const last = index === siblings.length - 1;
  const highlighted = rail.mode === "view" && rail.highlight?.id === block.id;
  const editing = rail.mode === "edit" && rail.editor !== undefined;
  /* A rail that runs into an Outcome block has no continue row under its
   * last block; the add slot needs the room. */
  const railStopped = level.parent
    ? rail.reachability.endBlockedBy.has(level.parent.id)
    : rail.reachability.stoppedBy !== undefined;
  return (
    <div
      data-block-row
      className={cn(
        "flex flex-col transition-opacity duration-200",
        editing && last && railStopped && "pb-7",
        status === "unreached" && !level.dimmed && "opacity-40",
      )}
    >
      {blockedBy ? <UnreachableNotice block={blockedBy} /> : null}
      <div
        data-block-card
        ref={highlighted ? rail.highlight?.ref : undefined}
        tabIndex={highlighted ? -1 : undefined}
        aria-label={highlighted ? `Referenced block: ${block.name || block.id}` : undefined}
        className={cn(
          "scroll-mt-8 rounded-lg outline-none transition-shadow duration-200",
          highlighted && "ring-2 ring-accent",
          testRing(status, rail.run?.outcome),
        )}
      >
        <RailCard
          block={block}
          index={index}
          siblings={siblings}
          position={positionLabel(here)}
          level={level}
          listRef={listRef}
        />
      </div>
      {rail.evidence === false ? null : (
        <>
          <KindEvidenceRow step={step} block={block} />
          <DeliveryEvidenceRow step={step} block={block} endpoints={rail.endpoints} />
        </>
      )}
      <ErrorEvidenceRow step={step} block={block} deliveryShown={rail.evidence !== false} />
      {block.kind === "branch" ? (
        <BranchContents
          branch={block}
          step={step}
          blocked={blockedBy !== undefined}
          path={here}
          level={level}
          continueLabel={last ? level.lastLabel : undefined}
          dimmed={level.dimmed || status === "unreached"}
        />
      ) : !blockedBy ? (
        <BlockExits
          block={block}
          step={step}
          continueLabel={last ? level.lastLabel : undefined}
          defaultTarget={rail.defaultTarget}
        />
      ) : null}
    </div>
  );
}

/* The well under a branch: its blocks, their add slot while editing,
 * and the branch's end. An end no request can reach (an Outcome block
 * inside, or an earlier one, decides everything first) is not drawn at
 * all: a branch holding one Outcome block is how "if this, approve" is
 * written, so its rail simply ends at that block's exit. */
function BranchContents({
  branch,
  step,
  blocked,
  path,
  level,
  continueLabel,
  dimmed,
}: {
  branch: BranchDraft;
  step?: TestStep;
  blocked: boolean;
  path: number[];
  level: RailLevel;
  continueLabel?: ReactNode;
  dimmed: boolean;
}) {
  const rail = useRail();
  const exit = takenExit(step, branch);
  const endStatus = statusAt(rail.run, rail.cursor, stepKey(rail.section, branch.id, true));
  const endBlockedBy = rail.reachability.endBlockedBy.get(branch.id);
  const editing = rail.mode === "edit" && rail.editor !== undefined;
  return (
    <PipelineBranchWell
      name={branch.name}
      taken={exit === "enter" ? "enter" : exit === "continue" ? "skip" : undefined}
      blocked={blocked}
      continueLabel={continueLabel}
    >
      <PipelineRail
        blocks={branch.blocks}
        parent={branch}
        path={path}
        depth={level.depth + 1}
        scope={[...level.scope, branch.when]}
        lastLabel="Otherwise"
        emptyText="No blocks in this branch yet."
        dimmed={dimmed}
      />
      {endBlockedBy ? null : (
        <>
          {editing || branch.blocks.length === 0 ? <PipelineConnector /> : null}
          <div
            className={cn(
              "rounded-lg transition-[box-shadow,opacity] duration-200",
              testRing(endStatus, rail.run?.outcome),
              endStatus === "unreached" && !dimmed && "opacity-40",
            )}
          >
            <PipelineBranchEndCard
              branchName={branch.name}
              target={branch.target}
              defaultTarget={rail.defaultTarget}
              reason={branch.reason}
            />
          </div>
        </>
      )}
    </PipelineBranchWell>
  );
}

/* The block's card: its read-only face while viewing, its editor while
 * editing, with the menu's structural moves wired to the tree. */
function RailCard({
  block,
  index,
  siblings,
  position,
  level,
  listRef,
}: {
  block: BlockDraft;
  index: number;
  siblings: BlockDraft[];
  position: string;
  level: RailLevel;
  listRef: RefObject<HTMLDivElement | null>;
}) {
  const rail = useRail();
  const expandedKey = `${rail.section}/${block.id}`;
  const expansion = {
    position,
    expanded: rail.expandedKey === expandedKey,
    onExpandedChange: (open: boolean) => rail.onExpandedKeyChange(open ? expandedKey : null),
    describeRoute: rail.describeRoute,
  };
  const editor = rail.mode === "edit" ? rail.editor : undefined;
  if (!editor) {
    switch (block.kind) {
      case "outcome":
        return <OutcomeBlockSummaryCard {...expansion} block={block} />;
      case "webhook":
        return <WebhookBlockSummaryCard {...expansion} block={block} endpoints={rail.endpoints} />;
      case "branch":
        return <BranchBlockSummaryCard {...expansion} block={block} endReachable={!rail.reachability.endBlockedBy.has(block.id)} />;
      case "delegate":
        return <DelegateBlockSummaryCard {...expansion} block={block} pipelines={rail.pipelines ?? []} />;
      case "edition": {
        const SummaryCard = requireBlockKind(block.type).summaryCard;
        return <SummaryCard {...expansion} block={block} />;
      }
      case "unknown":
        return <UnknownBlockSummaryCard {...expansion} block={block} />;
    }
  }
  const parentId = level.parent?.id ?? null;
  const dragging = editor.drag.dragging;
  const shared = {
    ...expansion,
    dragging: dragging !== null && dragging.parentId === parentId && dragging.index === index,
    handleProps: {
      onPointerDown: (event: ReactPointerEvent) =>
        editor.drag.start({ parentId, index, container: listRef.current, event }),
    },
    onRemove: () =>
      block.kind === "branch" && block.blocks.length
        ? editor.confirmRemove(block)
        : editor.update((drafts) => removeDraft(drafts, block.id)),
    onDuplicate: () => editor.update((drafts) => insertAfter(drafts, block.id, duplicateDraft(block))),
    onMoveUp: index > 0
      ? () => editor.update((drafts) => reorderRail(drafts, parentId, index, index - 1))
      : undefined,
    onMoveDown: index < siblings.length - 1
      ? () => editor.update((drafts) => reorderRail(drafts, parentId, index, index + 1))
      : undefined,
    onMoveIntoBranch: index > 0 && siblings[index - 1].kind === "branch"
      ? () => editor.update((drafts) => moveIntoBranchAbove(drafts, block.id))
      : undefined,
    onMoveOutOfBranch: level.parent
      ? () => editor.update((drafts) => moveOutOfBranch(drafts, block.id))
      : undefined,
    directory: rail.directory,
  };
  const replace = (next: BlockDraft) => editor.update((drafts) => updateDraft(drafts, block.id, () => next));
  switch (block.kind) {
    case "outcome":
      return <OutcomeBlockCard {...shared} block={block} onChange={(next) => replace({ ...next, kind: "outcome" })} />;
    case "webhook":
      return (
        <WebhookBlockCard
          {...shared}
          block={block}
          endpoints={rail.endpoints}
          onManageEndpoints={rail.onManageEndpoints}
          onChange={(next) => replace({ ...next, kind: "webhook" })}
        />
      );
    case "edition": {
      const Editor = requireBlockKind(block.type).editor;
      return <Editor {...shared} block={block} onChange={replace} />;
    }
    case "unknown":
      return <UnknownBlockCard {...shared} block={block} />;
    case "delegate": {
      /* A delegate has no reviewer of its own to pick. */
      const { directory: _directory, ...delegateShared } = shared;
      return (
        <DelegateBlockCard
          {...delegateShared}
          block={block}
          pipelines={(rail.pipelines ?? []).filter((pipeline) => pipeline.key !== rail.section)}
          onChange={(next) => replace({ ...next, kind: "delegate" })}
          onOpenPipeline={rail.onOpenPipeline}
          tools={rail.tools}
          suggestions={rail.suggestions}
          valueSuggestions={rail.valueSuggestions}
          valueDetails={rail.valueDetails}
          scope={level.scope}
        />
      );
    }
    case "branch":
      return (
        <BranchBlockCard
          {...shared}
          block={block}
          /* The card edits the branch's own fields; its blocks come from
           * the tree as it is now, never from the card's copy. */
          onChange={(next) =>
            editor.update((drafts) =>
              updateDraft(drafts, block.id, (current) => ({
                ...next,
                kind: "branch",
                blocks: current.kind === "branch" ? current.blocks : [],
              })),
            )}
          onUnwrap={() => editor.update((drafts) => unwrapBranch(drafts, block.id))}
          endReachable={!rail.reachability.endBlockedBy.has(block.id)}
          tools={rail.tools}
          suggestions={rail.suggestions}
          valueSuggestions={rail.valueSuggestions}
          valueDetails={rail.valueDetails}
          scope={level.scope}
        />
      );
  }
}
