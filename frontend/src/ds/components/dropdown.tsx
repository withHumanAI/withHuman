import type { ComponentProps, ReactNode } from "react";
import { Menu as BaseMenu } from "@base-ui/react/menu";
import { cn } from "@/ds/lib/cn";
import { Kbd } from "./kbd";

/*
 * Static menu: a fixed set of entries composed as JSX, for menus that
 * carry more than a list of actions (an identity header, a labelled
 * section, a segmented choice). Menu stays the choice for long action
 * lists that want filtering; this one never filters. Both draw the
 * same raised surface and the same item anatomy.
 */
export function Dropdown(props: ComponentProps<typeof BaseMenu.Root>) {
  return <BaseMenu.Root {...props} />;
}

export function DropdownTrigger(props: ComponentProps<typeof BaseMenu.Trigger>) {
  return <BaseMenu.Trigger {...props} />;
}

export function DropdownContent({
  className,
  side = "bottom",
  align = "start",
  sideOffset = 4,
  children,
  ...props
}: ComponentProps<typeof BaseMenu.Positioner> & { className?: string }) {
  return (
    <BaseMenu.Portal>
      <BaseMenu.Positioner side={side} align={align} sideOffset={sideOffset} {...props}>
        <BaseMenu.Popup
          className={cn(
            /* ds-root re-establishes the baseline inside the portal. */
            "ds-root min-w-[190px] rounded-md bg-surface-raised p-1 outline-none",
            "shadow-overlay ring-hairline",
            "[transform-origin:var(--transform-origin)]",
            "transition-[opacity,scale] duration-120 ease-out",
            "data-starting-style:scale-[0.98] data-starting-style:opacity-0",
            "data-ending-style:scale-[0.98] data-ending-style:opacity-0",
            className,
          )}
        >
          {children}
        </BaseMenu.Popup>
      </BaseMenu.Positioner>
    </BaseMenu.Portal>
  );
}

/* Groups are separated by a hairline; the group is also what a label
 * belongs to. */
export function DropdownGroup({
  className,
  ...props
}: ComponentProps<typeof BaseMenu.Group>) {
  return (
    <BaseMenu.Group
      className={cn(
        "not-last:mb-1 not-last:border-b-hairline not-last:border-edge not-last:pb-1",
        className,
      )}
      {...props}
    />
  );
}

/* A small uppercase heading over a group, in the sidebar's group-label
 * voice but tighter. */
export function DropdownGroupLabel({
  className,
  ...props
}: ComponentProps<typeof BaseMenu.GroupLabel>) {
  return (
    <BaseMenu.GroupLabel
      className={cn(
        "px-2 pt-1 pb-0.5 text-micro tracking-[0.08em] text-ink-faint uppercase select-none",
        className,
      )}
      {...props}
    />
  );
}

const itemClass = (danger: boolean | undefined, described: boolean) =>
  cn(
    "flex cursor-pointer gap-2 rounded-sm px-2",
    described ? "items-start py-1.5" : "h-7 items-center",
    "text-body outline-none select-none",
    "transition-colors duration-100",
    danger
      ? "text-deny data-highlighted:bg-deny-subtle"
      : "text-ink data-highlighted:bg-fill-hover",
    "data-disabled:pointer-events-none data-disabled:opacity-50",
  );

interface ItemContentProps {
  icon?: ReactNode;
  /* Shortcut hint, e.g. "mod+C". Display only. */
  keys?: string;
  danger?: boolean;
  /* A muted line under the label that wraps rather than truncates, for
   * an entry that has to say why it is there (the to-do bell). */
  description?: ReactNode;
  children: ReactNode;
}

function ItemContent({ icon, keys, danger, description, children }: ItemContentProps) {
  return (
    <>
      {icon ? (
        <span className={cn("flex", danger ? "text-deny" : "text-ink-muted", description && "mt-0.5")}>
          {icon}
        </span>
      ) : null}
      {description ? (
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="truncate">{children}</span>
          <span className="text-label text-ink-muted">{description}</span>
        </span>
      ) : (
        <span className="min-w-0 flex-1 truncate">{children}</span>
      )}
      {keys ? <Kbd keys={keys} /> : null}
    </>
  );
}

export type DropdownItemProps = Omit<ComponentProps<typeof BaseMenu.Item>, "children"> &
  ItemContentProps;

export function DropdownItem({
  icon,
  keys,
  danger,
  description,
  className,
  children,
  ...props
}: DropdownItemProps) {
  return (
    <BaseMenu.Item className={cn(itemClass(danger, description != null), className)} {...props}>
      <ItemContent icon={icon} keys={keys} danger={danger} description={description}>
        {children}
      </ItemContent>
    </BaseMenu.Item>
  );
}

/* A navigation entry: renders an anchor (pass the router Link through
 * `render`) so it opens in a new tab like any link. Base UI leaves a
 * link item open by default because a full navigation unloads the
 * page; the router navigates in place, so the menu closes itself. */
export type DropdownLinkItemProps = Omit<
  ComponentProps<typeof BaseMenu.LinkItem>,
  "children"
> &
  ItemContentProps;

export function DropdownLinkItem({
  icon,
  keys,
  danger,
  description,
  className,
  closeOnClick = true,
  children,
  ...props
}: DropdownLinkItemProps) {
  return (
    <BaseMenu.LinkItem
      closeOnClick={closeOnClick}
      className={cn(itemClass(danger, description != null), className)}
      {...props}
    >
      <ItemContent icon={icon} keys={keys} danger={danger} description={description}>
        {children}
      </ItemContent>
    </BaseMenu.LinkItem>
  );
}

/*
 * A single-select choice drawn as a segmented control inside the menu,
 * in ToggleGroup's clothes. The segments are menu radio items, so the
 * arrow keys reach them like any entry, and choosing one keeps the
 * menu open.
 */
export function DropdownRadioGroup({
  className,
  ...props
}: ComponentProps<typeof BaseMenu.RadioGroup>) {
  return (
    <BaseMenu.RadioGroup
      className={cn(
        "mx-1 mt-0.5 mb-1 flex items-center gap-0.5 rounded-md border-hairline border-edge bg-canvas p-0.5",
        className,
      )}
      {...props}
    />
  );
}

export function DropdownRadioItem({
  className,
  closeOnClick = false,
  ...props
}: ComponentProps<typeof BaseMenu.RadioItem>) {
  return (
    <BaseMenu.RadioItem
      closeOnClick={closeOnClick}
      className={cn(
        "flex h-6 flex-1 cursor-pointer items-center justify-center gap-1.5",
        "rounded-[5px] px-1.5 text-label whitespace-nowrap text-ink-muted outline-none select-none",
        "transition-colors duration-100",
        "data-highlighted:bg-fill-hover data-highlighted:text-ink-secondary",
        "data-checked:bg-control data-checked:text-ink data-checked:shadow-sm data-checked:ring-hairline",
        "data-checked:data-highlighted:bg-control",
        "data-disabled:pointer-events-none data-disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}
