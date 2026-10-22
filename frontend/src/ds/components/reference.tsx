import { createContext, useContext, type ReactNode } from "react";
import { cn } from "@/ds/lib/cn";
import { HoverCard, type HoverCardProps } from "./hover-card";

/*
 * A reference is a mention of one of the platform's named things from
 * somewhere else: the escalation path a pipeline exit hands to, the
 * webhook endpoint a block posts to, the person behind an avatar. Every
 * mention explains itself the same way, in a HoverCard the app fills:
 * the design system knows what can be referenced and where a mention
 * sits, and the app, which owns the data, decides what the card says
 * and whether the reader may see it at all.
 *
 * Wrap the mention in Referenced. When nothing provides a card (the
 * styleguide, a reader without the permission) the children render
 * exactly as given, so a reference is never worse than plain text.
 */
export type Reference =
  /* A membership id when the mention carries one; a display name is
   * enough for the app to find a current member. */
  | { kind: "person"; id?: string; name: string }
  | { kind: "team"; id: string; name: string }
  /* A team's escalation policy, as distinct from the team itself. */
  | { kind: "team_escalation_policy"; teamId: string; teamName: string }
  | { kind: "escalation_path"; key: string; name: string }
  /* A pipeline by key; "entry" is the one every request enters. The
   * name, when the mention knows it, is the chip's label. */
  | { kind: "pipeline"; pipelineKey: string; name?: string; revision?: number }
  | { kind: "webhook_endpoint"; key: string; name: string }
  /* An agent by its slug when the mention carries one; a display name
   * is enough for the app to find a live agent that is called that.
   * The instance, when the mention is of one request, names which of
   * the agent's instances made it. */
  | { kind: "agent"; slug?: string; name: string; instance?: string }
  /* A tool by the name its server defines, with that server when the
   * mention knows it: a hook's MCP server alias or the slug of a server an
   * edition registers. */
  | { kind: "tool"; name: string; server?: string }
  /* An approval request by id: the mention shows a short id or a tool
   * name, and the card reads the request itself when it opens. */
  | { kind: "request"; id: string }
  /* A block of the pipeline that ran, as the review recorded it: the
   * card draws that pipeline at the revision that ran, with this block
   * picked out. The pipeline may have changed since, so the mention
   * carries what the block said rather than reading it back. */
  | {
      kind: "pipeline_block";
      key: string;
      name: string;
      /* The block type as the review recorded it: one of core's kinds, or
       * a kind the edition adds. */
      type: string;
      outcome: "approve" | "deny" | "human";
      /* Set when the request ran out of this branch without a decision:
       * the card picks out the branch's end, not its condition. */
      endOfBranch?: boolean;
      /* The sentence the block's author wrote for the reviewer. */
      reason?: string;
      /* A webhook endpoint's own explanation of its answer. */
      endpointReason?: string;
      /* Set when the block failed instead of answering, which sent the
       * request to a human. */
      errorCode?: string;
      /* The pipeline that held the block, at the revision that ran. */
      pipeline?: { pipelineKey: string; revision: number };
    };

export type ReferenceKind = Reference["kind"];

/* What the card says about a reference, or null to leave the mention
 * plain. Called on render, so it must be cheap: return an element that
 * loads what it needs once it mounts inside the open card. */
export type ReferenceCardRenderer = (reference: Reference) => ReactNode | null;

const ReferenceCardsContext = createContext<ReferenceCardRenderer | null>(null);

export function ReferenceCardsProvider({
  render,
  children,
}: {
  render: ReferenceCardRenderer;
  children: ReactNode;
}) {
  return <ReferenceCardsContext.Provider value={render}>{children}</ReferenceCardsContext.Provider>;
}

/* The card for a reference, or null when nobody provides one. For a
 * component that has its own fallback when there is no card (Avatar
 * falls back to a name tooltip). */
export function useReferenceCard(reference: Reference): ReactNode | null {
  const render = useContext(ReferenceCardsContext);
  return render ? render(reference) : null;
}

export interface ReferencedProps extends Omit<HoverCardProps, "content" | "children"> {
  reference: Reference;
  /* A mention inside running text: the trigger wraps with the line and
   * its hover bubble hugs the words without moving them. */
  inline?: boolean;
  children: ReactNode;
}

/* Cards that draw a structure rather than list facts get more room. */
const CARD_WIDTH: Partial<Record<ReferenceKind, string>> = {
  escalation_path: "w-[400px]",
  team_escalation_policy: "w-[400px]",
  pipeline: "w-[400px]",
  pipeline_block: "w-[400px]",
  /* Arguments and reasoning wrap; a little more width keeps them
   * readable without the card turning into a page. */
  request: "w-[360px]",
};

/* A mention that opens its reference's card on hover or focus. The
 * default trigger suits a chip or an avatar; inline suits words. */
export function Referenced({
  reference,
  inline = false,
  children,
  className,
  triggerClassName,
  ...hoverCardProps
}: ReferencedProps) {
  const content = useReferenceCard(reference);
  if (content === null) return <>{children}</>;
  return (
    <HoverCard
      content={content}
      className={cn(CARD_WIDTH[reference.kind], className)}
      triggerClassName={cn(inline && "-mx-1 inline rounded-sm px-1", triggerClassName)}
      {...hoverCardProps}
    >
      {children}
    </HoverCard>
  );
}
