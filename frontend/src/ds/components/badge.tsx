import type { ComponentProps, ComponentType, ReactNode, SVGProps } from "react";
import { cn } from "@/ds/lib/cn";
import { focusRing } from "@/ds/lib/focus";
import {
  AlertCircleIcon,
  CheckCircleIcon,
  CircleIcon,
  ProgressIcon,
  XCircleIcon,
} from "@/ds/icons";

export type Tone = "neutral" | "accent" | "approve" | "deny" | "warn";

/* Only the icon carries the tone; the label stays ink. */
const ICON_TONES: Record<Tone, string> = {
  neutral: "text-ink-muted",
  accent: "text-accent",
  approve: "text-approve",
  deny: "text-deny",
  warn: "text-warn",
};

/* One status glyph per tone, so the shape says the state and the color
 * only reinforces it: a hollow circle for a state that ended or never
 * started, a filling ring for something under way, a check, a cross, and
 * an exclamation for the verdicts and warnings. */
const STATUS_ICONS: Record<Tone, ComponentType<SVGProps<SVGSVGElement>>> = {
  neutral: CircleIcon,
  accent: ProgressIcon,
  approve: CheckCircleIcon,
  deny: XCircleIcon,
  warn: AlertCircleIcon,
};

/* Internal anatomy shared with toast and timeline: the toned status icon
 * exists only inside a component that also carries a label. It is not a
 * public affordance. */
export function ToneIcon({
  tone,
  size = 14,
  className,
}: {
  tone: Tone;
  size?: number;
  className?: string;
}) {
  const Icon = STATUS_ICONS[tone];
  return <Icon width={size} height={size} className={cn(ICON_TONES[tone], className)} />;
}

export interface BadgeProps extends ComponentProps<"span"> {
  tone?: Tone;
  /* A mention in a sentence: inherit its type metrics and keep the
   * label on the text baseline, with no pill height or side padding. */
  inline?: boolean;
  /* Leading 14-16px icon. The default is the tone's status icon, for
   * status badges where the tone is the state; property badges (a team,
   * a role, a source) should pass their own icon so a status glyph never
   * appears without meaning. false drops the leading slot altogether, for
   * a value that is neither a state nor a thing with an icon (a
   * transport, a kind). */
  icon?: ReactNode | false;
  /* A quiet fill instead of the transparent rest, so a value-only
   * badge still reads as a pill inside running text. */
  fill?: boolean;
}

/*
 * Property pill in the Linear mold: transparent at rest, colored icon,
 * neutral label. With an onClick it becomes a button and gains the
 * rounded hover fill. With fill it carries the fill at rest, which is
 * how a bare value (no icon, no dot) keeps its edges.
 *
 * Status is always a labeled Badge, never a bare status dot: a dot asks
 * the reader to memorize a color code, a badge says the word. There is
 * deliberately no standalone dot component.
 */
export function Badge({
  tone = "neutral",
  inline = false,
  icon,
  fill = false,
  className,
  children,
  onClick,
  ...props
}: BadgeProps) {
  const content = (
    <>
      {icon === false ? null : (
        <span className={cn(
          /* align-middle centers on the lowercase letters; the nudge
           * centers the icon on the capitals instead. */
          inline ? "relative top-[-0.08em] mr-1 inline-flex items-center align-middle" : "flex items-center",
          ICON_TONES[tone],
        )}>
          {icon ?? <ToneIcon tone={tone} />}
        </span>
      )}
      {children}
    </>
  );
  const shared = cn(
    inline
      ? "inline rounded-sm text-[length:inherit] leading-[inherit] font-medium"
      : "inline-flex h-6 items-center gap-1.5 rounded-full px-2 text-label",
    "whitespace-nowrap text-ink select-none",
    fill && "bg-fill-hover",
    className,
  );

  if (onClick) {
    return (
      <button
        type="button"
        onClick={onClick}
        className={cn(
          shared,
          "cursor-pointer transition-colors duration-100",
          "hover:bg-fill-hover active:bg-fill-active",
          focusRing,
        )}
        {...(props as ComponentProps<"button">)}
      >
        {content}
      </button>
    );
  }
  return (
    <span className={shared} {...props}>
      {content}
    </span>
  );
}
