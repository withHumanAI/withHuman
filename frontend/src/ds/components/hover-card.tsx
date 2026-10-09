import type { ComponentProps, ReactNode } from "react";
import { PreviewCard } from "@base-ui/react/preview-card";
import { cn } from "@/ds/lib/cn";
import { Spinner } from "./spinner";

/*
 * A card that opens on hover or focus, for a trigger that stands for
 * something with more to say than a tooltip's one line: a name whose
 * details, state, or failure the reader may want without leaving the
 * row. It is the raised overlay surface the menus use, with a card's
 * inset, and it is read only: anything to act on belongs in a dialog
 * or on the page. A Tooltip names a control; a HoverCard explains a
 * thing.
 *
 * The trigger is a span the card owns, wrapped around whatever it is
 * given, so the child stays presentational: Base UI attaches its hover
 * listeners to the trigger node, and a child that changed element
 * once handed interaction props (a Badge turns into a button when it
 * receives onClick) would drop them after the first open.
 */
export interface HoverCardProps
  extends Omit<ComponentProps<typeof PreviewCard.Root>, "children"> {
  content: ReactNode;
  side?: "top" | "bottom" | "left" | "right";
  align?: "start" | "center" | "end";
  /* Milliseconds of hover before it opens; focus opens at once. */
  delay?: number;
  className?: string;
  /* The trigger's own class, on the span around the children. */
  triggerClassName?: string;
  /* The trigger span takes a tab stop so a keyboard reaches the card.
   * Pass false when the child is itself focusable (a button, a link):
   * focus inside the span still opens the card, and the reader is not
   * asked to tab through the same thing twice. */
  focusable?: boolean;
  children: ReactNode;
}

export function HoverCard({
  content,
  side = "bottom",
  align = "start",
  delay = 300,
  className,
  triggerClassName,
  focusable = true,
  children,
  ...rootProps
}: HoverCardProps) {
  return (
    <PreviewCard.Root {...rootProps}>
      <PreviewCard.Trigger
        render={<span />}
        tabIndex={focusable ? 0 : undefined}
        delay={delay}
        closeDelay={100}
        className={cn(
          /* The trigger reads as something to hover: the hand, and the
           * same rounded fill a clickable Badge shows. */
          "inline-flex cursor-pointer rounded-full transition-colors duration-100",
          "hover:bg-fill-hover data-popup-open:bg-fill-hover",
          "outline-none focus-visible:ring-2 focus-visible:ring-accent",
          triggerClassName,
        )}
      >
        {children}
      </PreviewCard.Trigger>
      <PreviewCard.Portal>
        <PreviewCard.Positioner side={side} align={align} sideOffset={6} collisionPadding={12}>
          <PreviewCard.Popup render={<HoverCardSurface />} className={className}>
            {content}
          </PreviewCard.Popup>
        </PreviewCard.Positioner>
      </PreviewCard.Portal>
    </PreviewCard.Root>
  );
}

/* Shared by reference previews and interactive info cards. A card taller
 * than the room the positioner leaves on screen stops at that height and
 * scrolls, so a long pipeline preview never runs off the page. */
export function HoverCardSurface({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn(
        /* ds-root re-establishes the baseline inside the portal. */
        "ds-root w-[320px] max-w-[calc(100vw-24px)] rounded-md bg-surface-raised p-3.5 outline-none",
        "max-h-(--available-height) overflow-y-auto overscroll-contain",
        "text-body text-ink shadow-overlay ring-hairline",
        "transition-[opacity,translate] duration-100 ease-standard",
        "data-starting-style:opacity-0 data-starting-style:translate-y-[2px]",
        "data-ending-style:opacity-0",
        className,
      )}
      {...props}
    />
  );
}

/* The card's heading line: a name, and an aside beside it. */
export function HoverCardTitle({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn("flex items-center justify-between gap-3 text-body font-medium text-ink", className)}
      {...props}
    />
  );
}

/* A muted line under the title, for where the thing comes from. */
export function HoverCardMeta({ className, ...props }: ComponentProps<"p">) {
  return <p className={cn("mt-0.5 text-label font-normal text-ink-muted", className)} {...props} />;
}

/* The body, separated from the head by a hairline. */
export function HoverCardBody({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn("mt-2 border-t-hairline border-edge pt-2 text-label font-normal text-ink-secondary", className)}
      {...props}
    />
  );
}

/* A short run of labelled facts inside the body: the term muted on the
 * left, the value in ink on the right, one line each. Tighter than a
 * DescriptionList and without its copy affordance, since the card is
 * for reading at a glance. */
export function HoverCardFacts({ className, ...props }: ComponentProps<"dl">) {
  return <dl className={cn("flex flex-col gap-1.5", className)} {...props} />;
}

export function HoverCardFact({
  term,
  mono = false,
  className,
  children,
}: {
  term: string;
  /* Machine values (keys, types, urls) render in Roboto Mono. */
  mono?: boolean;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("flex items-start justify-between gap-4", className)}>
      <dt className="shrink-0 text-label leading-5 font-normal text-ink-muted">{term}</dt>
      <dd
        className={cn(
          "flex min-w-0 flex-wrap items-center justify-end gap-1 text-right leading-5",
          mono ? "font-mono text-[12px] text-ink-secondary" : "text-label text-ink",
        )}
      >
        {children}
      </dd>
    </div>
  );
}

/* One muted line where facts would go: the card is still loading, the
 * thing is gone, or the reader may not see it. */
export function HoverCardNote({
  pending = false,
  className,
  children,
}: {
  /* Leads with a spinner while the facts are on their way. */
  pending?: boolean;
  className?: string;
  children: ReactNode;
}) {
  return (
    <p
      className={cn(
        "flex items-center gap-2 text-label font-normal text-ink-muted",
        className,
      )}
    >
      {pending ? <Spinner size={12} /> : null}
      {children}
    </p>
  );
}
