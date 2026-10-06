import { useState } from "react";
import { cn } from "@/ds/lib/cn";
import { Avatar } from "./avatar";
import { Badge } from "./badge";
import { HoverCard, HoverCardTitle, HoverCardBody } from "./hover-card";
import { Referenced, type Reference } from "./reference";
import { Select, SelectContent, SelectItem, SelectTrigger } from "./select";
import {
  BroadcastIcon,
  ChevronRightIcon,
  InboxIcon,
  PeopleIcon,
  PersonIcon,
  ProgressIcon,
} from "@/ds/icons";

/*
 * Who gets asked when a pipeline exit says "Ask a human". A target is a
 * person or a team; a team may carry its own escalation policy, so
 * pointing at a team is pointing at that policy. A block that names no
 * target inherits the pipeline's default, which lives on the terminus
 * card.
 */

/* A pipeline's human exit names an escalation path, or the agent's owner
 * with a path as the fallback; a path's levels name people and teams; a
 * team escalation policy's levels name members or everyone on the team
 * (broadcast). One chip and one picker serve all five. */
export type ReviewerTargetType = "user" | "team" | "escalation_path" | "agent_owner" | "broadcast";

export interface ReviewerTarget {
  type: ReviewerTargetType;
  /* A membership or team id, or an escalation path key. An agent_owner
   * target carries its fallback path's key and name. A broadcast
   * carries the id of the team it notifies, so option keys stay unique
   * beside the team's members. */
  id: string;
  name: string;
  /* One muted phrase after the name in the picker, for example a path
   * without an active revision. */
  detail?: string;
}

/* The picker also offers "no explicit target", and in a route picker the
 * agent's owner. */
type TargetOption =
  | ReviewerTarget
  | { type: "default"; id: "default"; name: string }
  | { type: "owner"; id: "owner"; name: string };

const OWNER_OPTION = { type: "owner" as const, id: "owner" as const, name: "The agent's owner" };

/* An agent-owner route as its chip says it: the fallback path when it
 * names one; without one the pipeline default is the fallback. */
export function agentOwnerLabel(fallbackName: string): string {
  return fallbackName ? `Agent owner, else ${fallbackName}` : "Agent owner";
}

function optionKey(option: TargetOption): string {
  return `${option.type}:${option.id}`;
}

/* The reference a target's chip explains on hover. A broadcast names
 * everyone on a team, so it opens the team. */
function referenceOf(target: ReviewerTarget): Reference {
  switch (target.type) {
    case "user":
      return { kind: "person", id: target.id, name: target.name };
    case "team":
    case "broadcast":
      return { kind: "team", id: target.id, name: target.name };
    case "escalation_path":
    case "agent_owner":
      return { kind: "escalation_path", key: target.id, name: target.name };
  }
}

/*
 * The property chip for a target: a person shows their avatar, a team
 * its people icon. Without a target the chip names what it falls back
 * to (the pipeline default, or any authorized reviewer), in muted ink
 * so an inherited value never reads as a chosen one. A named target's
 * chip opens its reference card on hover.
 */
export function TargetBadge({
  target,
  fallbackLabel,
  className,
  onClick,
  expanded,
}: {
  target: ReviewerTarget | null | undefined;
  fallbackLabel?: string;
  className?: string;
  /* Makes the chip a button, with a chevron that turns when expanded:
   * the escalation tree uses it to open a team's path inline. */
  onClick?: () => void;
  expanded?: boolean;
}) {
  /* An expandable chip's card closes on the click that opens the well
   * beneath it, so the card never sits over what the reader asked for. */
  const [cardOpen, setCardOpen] = useState(false);
  const chip = (
    <TargetChip
      target={target}
      fallbackLabel={fallbackLabel}
      className={className}
      onClick={
        onClick
          ? () => {
              setCardOpen(false);
              onClick();
            }
          : undefined
      }
      expanded={expanded}
    />
  );
  if (!target && fallbackLabel && !["Pipeline default", "Any authorized reviewer"].includes(fallbackLabel)) return chip;
  if (target?.type === "agent_owner") return (
    <HoverCard content={<>
      <HoverCardTitle>The agent's owner</HoverCardTitle>
      <HoverCardBody>
        The agent's owner decides the request. If the agent has no owner, it goes to{" "}
        {target.id ? target.name : "the pipeline default"} instead.
      </HoverCardBody>
    </>}>
      {chip}
    </HoverCard>
  );
  if (!target) return (
    <HoverCard content={<>
      <HoverCardTitle>{fallbackLabel ?? "Pipeline default"}</HoverCardTitle>
      <HoverCardBody>
        {fallbackLabel === "Any authorized reviewer"
          ? "Anyone with permission can decide the request."
          : "Uses the pipeline's default escalation path."}
      </HoverCardBody>
    </>}>
      {chip}
    </HoverCard>
  );
  return (
    <Referenced
      reference={referenceOf(target)}
      focusable={!onClick}
      open={cardOpen}
      onOpenChange={setCardOpen}
    >
      {chip}
    </Referenced>
  );
}

function TargetChip({
  target,
  fallbackLabel = "Pipeline default",
  className,
  onClick,
  expanded,
}: {
  target: ReviewerTarget | null | undefined;
  fallbackLabel?: string;
  className?: string;
  /* Makes the chip a button, with a chevron that turns when expanded:
   * the escalation tree uses it to open a team's path inline. */
  onClick?: () => void;
  expanded?: boolean;
}) {
  const chevron = onClick ? (
    <ChevronRightIcon
      width={12}
      height={12}
      className={cn(
        "-mr-0.5 text-ink-faint transition-transform duration-100",
        expanded && "rotate-90",
      )}
    />
  ) : null;
  const shared = onClick ? { onClick, "aria-expanded": expanded } : {};
  if (!target) {
    return (
      <Badge
        className={cn("text-ink-muted", className)}
        icon={<InboxIcon width={13} height={13} />}
        {...shared}
      >
        {fallbackLabel}
        {chevron}
      </Badge>
    );
  }
  /* Icon-led chips keep the pill's own inset; only the icon-to-label
   * gap is tighter. */
  const led = cn("gap-1", className);
  if (target.type === "team") {
    return (
      <Badge className={led} icon={<PeopleIcon width={13} height={13} />} {...shared}>
        {target.name}
        {chevron}
      </Badge>
    );
  }
  if (target.type === "escalation_path") {
    return (
      <Badge className={led} icon={<ProgressIcon width={13} height={13} />} {...shared}>
        {target.name}
        {chevron}
      </Badge>
    );
  }
  if (target.type === "agent_owner") {
    return (
      <Badge className={led} icon={<PersonIcon width={13} height={13} />} {...shared}>
        {agentOwnerLabel(target.name)}
        {chevron}
      </Badge>
    );
  }
  if (target.type === "broadcast") {
    return (
      <Badge className={led} icon={<BroadcastIcon width={13} height={13} />} {...shared}>
        {target.name}
        {chevron}
      </Badge>
    );
  }
  return (
    <Badge
      /* The round avatar sits inside the pill's curve, not after its
       * padding: 2px in, the same inset a stacked avatar ring takes. */
      className={cn("gap-1 pl-0.5", className)}
      icon={<Avatar name={target.name} size="sm" tooltip={false} />}
      {...shared}
    >
      {target.name}
      {chevron}
    </Badge>
  );
}

export interface TargetSelectProps {
  /* Teams and people the organization can route to. Teams list first:
   * they are the durable choice, people the exception. */
  directory: ReviewerTarget[];
  value: ReviewerTarget | null;
  onChange: (next: ReviewerTarget | null) => void;
  /* Label for the "no explicit target" choice. Omit to require one. */
  fallbackLabel?: string;
  className?: string;
  "aria-label"?: string;
}

export function TargetSelect({
  directory,
  value,
  onChange,
  fallbackLabel,
  className,
  "aria-label": ariaLabel = "Reviewers",
}: TargetSelectProps) {
  const [pickerOpen, setPickerOpen] = useState(false);
  const [cardOpen, setCardOpen] = useState(false);
  const paths = directory.filter((entry) => entry.type === "escalation_path");
  const broadcasts = directory.filter((entry) => entry.type === "broadcast");
  const teams = directory.filter((entry) => entry.type === "team");
  const people = directory.filter((entry) => entry.type === "user");
  /* A route picker (a pipeline exit or default) lists paths only, or
   * nothing yet; it may instead ask the agent's owner, keeping a path as
   * the fallback. */
  const routePicker = paths.length === directory.length;
  const items: TargetOption[] = [
    ...(fallbackLabel ? [{ type: "default" as const, id: "default" as const, name: fallbackLabel }] : []),
    ...(routePicker ? [OWNER_OPTION] : []),
    ...paths,
    ...broadcasts,
    ...teams,
    ...people,
  ];
  const selected: TargetOption | null =
    value?.type === "agent_owner"
      ? OWNER_OPTION
      : value
        ? (items.find((item) => optionKey(item) === optionKey(value)) ?? value)
        : (items.find((item) => item.type === "default") ?? null);

  const picker = (
    <Select
      items={items}
      itemToStringLabel={(item) => (item as TargetOption).name}
      value={selected}
      onOpenChange={setPickerOpen}
      onValueChange={(next) => {
        const option = next as TargetOption | null;
        if (!option || option.type === "default") onChange(null);
        else if (option.type === "owner") {
          /* Keep the path already chosen as the fallback; without one the
           * pipeline default is the fallback. */
          const fallback = value?.type === "escalation_path" || value?.type === "agent_owner" ? value : null;
          onChange({ type: "agent_owner", id: fallback?.id ?? "", name: fallback?.name ?? "" });
        } else onChange(option);
      }}
    >
      {value && value.type !== "agent_owner" ? (
        <Referenced reference={referenceOf(value)} focusable={false}
          open={cardOpen && !pickerOpen} onOpenChange={setCardOpen}>
          <SelectTrigger aria-label={ariaLabel} className={cn("w-60", className)} placeholder="Choose" />
        </Referenced>
      ) : (
        <SelectTrigger aria-label={ariaLabel} className={cn("w-60", className)} placeholder="Choose" />
      )}
      <SelectContent<TargetOption> filterPlaceholder="Filter">
        {(item) => (
          <SelectItem key={optionKey(item)} value={item}>
            <span className="flex items-center gap-2">
              {item.type === "team" ? (
                <PeopleIcon width={13} height={13} className="text-ink-muted" />
              ) : item.type === "user" ? (
                <PersonIcon width={13} height={13} className="text-ink-muted" />
              ) : item.type === "escalation_path" ? (
                <ProgressIcon width={13} height={13} className="text-ink-muted" />
              ) : item.type === "broadcast" ? (
                <BroadcastIcon width={13} height={13} className="text-ink-muted" />
              ) : item.type === "owner" ? (
                <PersonIcon width={13} height={13} className="text-ink-muted" />
              ) : (
                <InboxIcon width={13} height={13} className="text-ink-muted" />
              )}
              {item.type === "default" ? (
                <span className="text-ink-muted">{item.name}</span>
              ) : item.type === "owner" ? (
                <span>{item.name}</span>
              ) : (
                <Referenced reference={referenceOf(item)} focusable={false} inline>{item.name}</Referenced>
              )}
              {item.type !== "default" && item.type !== "owner" && item.detail ? (
                <span className="text-micro font-normal text-ink-faint">{item.detail}</span>
              ) : null}
            </span>
          </SelectItem>
        )}
      </SelectContent>
    </Select>
  );
  if (value?.type !== "agent_owner") return picker;
  /* Asking the owner may name the path a request without one goes to;
   * without one the pipeline default is the fallback. */
  const inherited: TargetOption = { type: "default", id: "default", name: fallbackLabel ?? "Pipeline default" };
  const fallbacks: TargetOption[] = [inherited, ...paths];
  const fallback = value.id ? (paths.find((path) => path.id === value.id) ?? { ...value, type: "escalation_path" as const }) : inherited;
  return (
    <span className="inline-flex flex-wrap items-center gap-x-2 gap-y-1">
      {picker}
      <span className="text-micro text-ink-muted">If the agent has no owner</span>
      <Select
        items={fallbacks}
        itemToStringLabel={(item) => (item as TargetOption).name}
        value={fallback}
        onValueChange={(next) => {
          const option = next as TargetOption | null;
          if (!option || option.type === "default") onChange({ type: "agent_owner", id: "", name: "" });
          else onChange({ type: "agent_owner", id: option.id, name: option.name });
        }}
      >
        <SelectTrigger aria-label={`${ariaLabel} fallback`} className={cn("w-60", className)} placeholder="Choose" />
        <SelectContent<TargetOption> filterPlaceholder="Filter">
          {(item) => (
            <SelectItem key={optionKey(item)} value={item}>
              <span className="flex items-center gap-2">
                {item.type === "default" ? (
                  <>
                    <InboxIcon width={13} height={13} className="text-ink-muted" />
                    <span className="text-ink-muted">{item.name}</span>
                  </>
                ) : item.type === "escalation_path" ? (
                  <>
                    <ProgressIcon width={13} height={13} className="text-ink-muted" />
                    <Referenced reference={referenceOf(item)} focusable={false} inline>{item.name}</Referenced>
                    {item.detail ? <span className="text-micro font-normal text-ink-faint">{item.detail}</span> : null}
                  </>
                ) : null}
              </span>
            </SelectItem>
          )}
        </SelectContent>
      </Select>
    </span>
  );
}
