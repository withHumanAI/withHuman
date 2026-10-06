import { ToggleGroup, ToggleGroupItem } from "./toggle-group";
import { Tooltip } from "./tooltip";
import { CheckIcon, PersonIcon, XIcon } from "@/ds/icons";

/*
 * Picks what an outcome block decides: approve, deny, or hand the
 * request to a human. A domain skin over ToggleGroup; the pressed
 * segment carries the decision color so the block's consequence is
 * visible at a glance.
 */

export type BlockOutcome = "approve" | "deny" | "human";

export const OUTCOME_HELP: Record<BlockOutcome | "next", string> = {
  approve: "Approve the request automatically.",
  deny: "Deny the request automatically.",
  human: "Ask a human to review the request.",
  next: "Pass the request to the next block.",
};

const SEGMENTS: {
  value: BlockOutcome;
  label: string;
  icon: typeof CheckIcon;
  pressed: string;
}[] = [
  {
    value: "approve",
    label: "Approve",
    icon: CheckIcon,
    pressed: "data-pressed:bg-approve-subtle data-pressed:text-approve",
  },
  {
    value: "deny",
    label: "Deny",
    icon: XIcon,
    pressed: "data-pressed:bg-deny-subtle data-pressed:text-deny",
  },
  {
    value: "human",
    label: "Ask a human",
    icon: PersonIcon,
    pressed: "data-pressed:bg-warn-subtle data-pressed:text-warn",
  },
];

export interface OutcomeSelectProps {
  value: BlockOutcome;
  onValueChange: (next: BlockOutcome) => void;
  disabled?: boolean;
  "aria-label"?: string;
  className?: string;
}

export function OutcomeSelect({
  value,
  onValueChange,
  disabled,
  "aria-label": ariaLabel = "Outcome",
  className,
}: OutcomeSelectProps) {
  return (
    <ToggleGroup
      aria-label={ariaLabel}
      disabled={disabled}
      value={value}
      onValueChange={(next) => onValueChange(next as BlockOutcome)}
      className={className}
    >
      {SEGMENTS.map(({ value: segment, label, icon: Icon, pressed }) => (
        <Tooltip key={segment} content={OUTCOME_HELP[segment]}>
          <ToggleGroupItem
            value={segment}
            className={`px-2 data-pressed:font-medium data-pressed:shadow-none ${pressed}`}
          >
            <Icon width={13} height={13} />
            {label}
          </ToggleGroupItem>
        </Tooltip>
      ))}
    </ToggleGroup>
  );
}
