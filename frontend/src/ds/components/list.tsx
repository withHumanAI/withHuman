import {
  Fragment,
  useEffect,
  useRef,
  useState,
  type ComponentPropsWithRef,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { cn } from "@/ds/lib/cn";
import { Button } from "./button";
import { Checkbox } from "./checkbox";

/*
 * The row interaction engine, hand-rolled for the Linear feel:
 * - hover and keyboard share one highlight; arrows move it
 * - plain click only focuses a row: nothing happens by accident.
 *   Selection is deliberate (checkbox, x or Space, mod+click, shift
 *   ranges); opening is deliberate (View button, Enter, double-click)
 * - selection is a controlled Set of ids owned by the screen
 */
export interface ListProps<Item> {
  items: Item[];
  getId: (item: Item) => string;
  selected: ReadonlySet<string>;
  onSelectedChange: (next: Set<string>) => void;
  onOpen?: (item: Item) => void;
  /* Row content; use ListRowContent for the standard slots. */
  children: (item: Item) => ReactNode;
  /* Optional row component, including the row's content. Forward the
   * props and ref to an li and compose any added interaction handlers. */
  renderRow?: (item: Item, props: ListRowProps) => ReactNode;
  selectable?: boolean;
  /* Plain click opens the row (inspection into a pane is cheap and
   * reversible). Selection still runs through the checkbox, x, mod,
   * and shift. */
  openOnClick?: boolean;
  /* Hover-revealed View button. Defaults to showing only when clicks
   * do not open; pass true to keep it as an explicit affordance
   * alongside openOnClick. */
  openButton?: boolean;
  /* The row currently open in a detail pane; it keeps a persistent
   * marker distinct from selection. */
  activeId?: string | null;
  /* Inline expansion: the row whose panel is open, and what it shows.
   * Cheap, reversible inspection in place; the screen owns the state
   * and typically toggles it from onOpen. */
  expandedId?: string | null;
  renderExpanded?: (item: Item) => ReactNode;
  /* Horizontal inset of every row. "compact" is the bare list; "card"
   * matches a Card header's inset so a list inside a card lines up
   * with the title above it. */
  padding?: ListPadding;
  /* A live list's rows that just arrived. Each opens from zero height
   * over 160ms as it mounts, so the eye sees where the rows below went;
   * nothing glows. Pass it (even empty) only on a list that receives
   * live data, and leave out a batch too large to follow. */
  enteringIds?: ReadonlySet<string>;
  className?: string;
  "aria-label": string;
}

export type ListPadding = "compact" | "card";
export type ListRowProps = ComponentPropsWithRef<"li">;

export const LIST_PADDING: Record<ListPadding, string> = {
  compact: "px-2",
  card: "px-5",
};

interface Anchor {
  index: number;
  /* Selection snapshot when the anchor was set; shift ranges extend it. */
  base: Set<string>;
}

export function List<Item>({
  items,
  getId,
  selected,
  onSelectedChange,
  onOpen,
  children,
  renderRow,
  selectable = true,
  openOnClick = false,
  openButton = !openOnClick,
  activeId = null,
  expandedId = null,
  renderExpanded,
  padding = "compact",
  enteringIds,
  className,
  "aria-label": ariaLabel,
}: ListProps<Item>) {
  const inset = LIST_PADDING[padding];
  const [highlighted, setHighlighted] = useState<number | null>(null);
  const listRef = useRef<HTMLUListElement | null>(null);
  const rowRefs = useRef<(HTMLLIElement | null)[]>([]);
  const anchorRef = useRef<Anchor | null>(null);
  /* Expansion settles over 160ms (grid-rows 0fr to 1fr), motion as
   * feedback to the click, never at rest. Opening animates via
   * @starting-style on insertion; closing keeps the panel mounted while
   * the rows collapse, and a timer (not transitionend) unmounts it so
   * reduced motion and throttled tabs cannot strand an invisible panel. */
  const [panelRowId, setPanelRowId] = useState<string | null>(null);

  useEffect(() => {
    if (expandedId) {
      setPanelRowId(expandedId);
      return;
    }
    const timer = setTimeout(() => setPanelRowId(null), 200);
    return () => clearTimeout(timer);
  }, [expandedId]);

  const ids = items.map(getId);

  useEffect(() => {
    if (highlighted !== null && highlighted >= items.length) {
      setHighlighted(items.length ? items.length - 1 : null);
    }
  }, [items.length, highlighted]);

  function toggle(index: number) {
    const id = ids[index];
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    anchorRef.current = { index, base: next };
    onSelectedChange(next);
  }

  function rangeTo(index: number) {
    const anchor = anchorRef.current ?? { index, base: new Set(selected) };
    anchorRef.current = anchor;
    const [from, to] = [
      Math.min(anchor.index, index),
      Math.max(anchor.index, index),
    ];
    const next = new Set(anchor.base);
    for (let i = from; i <= to; i++) next.add(ids[i]);
    onSelectedChange(next);
  }

  function moveTo(index: number, extend: boolean) {
    setHighlighted(index);
    rowRefs.current[index]?.focus();
    if (extend && selectable) rangeTo(index);
  }

  function onRowKeyDown(event: KeyboardEvent, index: number, item: Item) {
    const mod = event.metaKey || event.ctrlKey;
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        if (index < items.length - 1) moveTo(index + 1, event.shiftKey);
        break;
      case "ArrowUp":
        event.preventDefault();
        if (index > 0) moveTo(index - 1, event.shiftKey);
        break;
      case "Home":
        event.preventDefault();
        moveTo(0, event.shiftKey);
        break;
      case "End":
        event.preventDefault();
        moveTo(items.length - 1, event.shiftKey);
        break;
      case "Enter":
        event.preventDefault();
        onOpen?.(item);
        break;
      case "x":
      case " ":
        if (!selectable || mod) break;
        event.preventDefault();
        toggle(index);
        break;
      case "a":
        if (!selectable || !mod) break;
        event.preventDefault();
        anchorRef.current = null;
        onSelectedChange(new Set(ids));
        break;
      case "Escape":
        if (!selectable || selected.size === 0) break;
        event.preventDefault();
        anchorRef.current = null;
        onSelectedChange(new Set());
        break;
    }
  }

  const anySelected = selectable && selected.size > 0;
  /* A row that can neither be opened nor selected is a plain row: no
   * hover highlight and no pointer, so nothing promises a click that
   * does nothing. Its buttons still work and the keyboard still walks it. */
  const interactive = selectable || onOpen !== undefined;

  return (
    <ul
      ref={listRef}
      role="listbox"
      aria-label={ariaLabel}
      aria-multiselectable={selectable || undefined}
      onMouseLeave={() => {
        /* A hover highlight leaves with the pointer; only KEYBOARD
         * focus keeps it (click-focus must not pin the row). */
        const active = document.activeElement;
        const keyboardFocusInside =
          !!active &&
          listRef.current?.contains(active) &&
          active.matches(":focus-visible");
        if (!keyboardFocusInside) setHighlighted(null);
      }}
      className={cn("flex flex-col", className)}
    >
      {items.map((item, index) => {
        const id = ids[index];
        const isSelected = selectable && selected.has(id);
        const isHighlighted = interactive && highlighted === index;
        const isExpanded = renderExpanded != null && panelRowId === id;
        const row = (
          <li
            key={id}
            ref={(el) => {
              rowRefs.current[index] = el;
            }}
            role="option"
            aria-selected={selectable ? isSelected : undefined}
            tabIndex={index === (highlighted ?? 0) ? 0 : -1}
            data-highlighted={isHighlighted || undefined}
            data-selected={isSelected || undefined}
            data-active={activeId === id || undefined}
            onMouseEnter={() => setHighlighted(index)}
            onFocus={() => setHighlighted(index)}
            onKeyDown={(event) => onRowKeyDown(event, index, item)}
            onMouseDown={(event) => {
              if (event.shiftKey) event.preventDefault();
            }}
            onClick={(event) => {
              if (selectable && event.shiftKey) {
                rangeTo(index);
                return;
              }
              if (selectable && (event.metaKey || event.ctrlKey)) {
                toggle(index);
                return;
              }
              if (openOnClick || !selectable) onOpen?.(item);
            }}
            onDoubleClick={() => onOpen?.(item)}
            className={cn(
              "relative flex flex-col",
              interactive && "cursor-pointer",
              "border-b-hairline border-edge outline-none select-none last:border-b-0",
              "transition-colors duration-100",
              "data-highlighted:bg-fill-hover data-selected:bg-fill-selected",
              "data-active:bg-fill-hover data-active:shadow-[inset_2px_0_0_var(--ds-accent)]",
              "focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-focus",
            )}
          >
            <RowEntrance live={enteringIds !== undefined} entering={enteringIds?.has(id) ?? false}>
              <div className={cn("flex min-h-9 items-center gap-1", inset)}>
                {selectable ? (
                  <span
                    onClick={(event) => event.stopPropagation()}
                    className={cn(
                      "flex w-[22px] shrink-0 items-center transition-opacity duration-100",
                      isHighlighted || isSelected || anySelected
                        ? "opacity-100"
                        : "opacity-0",
                    )}
                  >
                    <Checkbox
                      checked={isSelected}
                      onCheckedChange={() => toggle(index)}
                      aria-label="Select item"
                      tabIndex={-1}
                    />
                  </span>
                ) : null}
                <div className="flex min-w-0 flex-1 items-center">
                  {children(item)}
                </div>
                {onOpen && openButton ? (
                  <span
                    onClick={(event) => event.stopPropagation()}
                    onDoubleClick={(event) => event.stopPropagation()}
                    className={cn(
                      "ml-2 flex w-[52px] shrink-0 justify-end transition-opacity duration-100",
                      isHighlighted ? "opacity-100" : "opacity-0",
                    )}
                  >
                    <Button
                      size="sm"
                      variant="neutral"
                      tabIndex={-1}
                      onClick={() => onOpen(item)}
                    >
                      View
                    </Button>
                  </span>
                ) : null}
              </div>
              {isExpanded ? (
                <div
                  className={cn(
                    "grid transition-[grid-template-rows] duration-160 ease-standard",
                    "motion-reduce:transition-none",
                    expandedId === id
                      ? "grid-rows-[1fr] starting:grid-rows-[0fr]"
                      : "grid-rows-[0fr]",
                  )}
                >
                  <div className="overflow-hidden">
                    {/* The panel is content, not a click target: clicks
                     * inside must not collapse the row they describe. */}
                    <div
                      onClick={(event) => event.stopPropagation()}
                      onDoubleClick={(event) => event.stopPropagation()}
                      className={cn("cursor-default pb-3", inset)}
                    >
                      {renderExpanded(item)}
                    </div>
                  </div>
                </div>
              ) : null}
            </RowEntrance>
          </li>
        );
        return renderRow ? (
          <Fragment key={id}>{renderRow(item, row.props)}</Fragment>
        ) : row;
      })}
    </ul>
  );
}

/* A live list's row body. An arriving row opens from 0fr to 1fr as it
 * mounts (@starting-style), the same settle as an expanding panel; a
 * row already on screen keeps the wrapper, so leaving the entering set
 * never remounts it. Lists without live data render the body bare. */
function RowEntrance({
  live,
  entering,
  children,
}: {
  live: boolean;
  entering: boolean;
  children: ReactNode;
}) {
  if (!live) return children;
  return (
    <div
      className={cn(
        "grid grid-rows-[1fr]",
        entering &&
          "transition-[grid-template-rows] duration-160 ease-out starting:grid-rows-[0fr] motion-reduce:transition-none",
      )}
    >
      <div className="min-h-0 overflow-hidden">{children}</div>
    </div>
  );
}

/* Fixed-width trailing cell so metadata lines up in columns across
 * rows; size each column once per screen. */
export function ListCell({
  width,
  align = "left",
  className,
  children,
}: {
  width: number;
  align?: "left" | "right";
  className?: string;
  children?: ReactNode;
}) {
  return (
    <span
      style={{ width }}
      className={cn(
        "shrink-0 truncate",
        align === "right" && "text-right",
        className,
      )}
    >
      {children}
    </span>
  );
}

/* Standard row layout: leading marks, one or two text lines, trailing
 * metadata. A title given both lines (titleLines 2, no description)
 * wraps over them and keeps the two-line height; a one-line title
 * centers, lining up with the leading and trailing marks. */
export function ListRowContent({
  leading,
  title,
  titleLines = 1,
  description,
  trailing,
}: {
  leading?: ReactNode;
  title: ReactNode;
  titleLines?: 1 | 2;
  description?: ReactNode;
  trailing?: ReactNode;
}) {
  return (
    <>
      {leading ? (
        <span className="mr-2.5 flex shrink-0 items-center gap-2">
          {leading}
        </span>
      ) : null}
      <span className="flex min-w-0 flex-1 flex-col py-1.5">
        {titleLines === 2 ? (
          <span className="flex min-h-10 items-center">
            <span className="line-clamp-2 text-body break-words text-ink">
              {title}
            </span>
          </span>
        ) : (
          <span className="truncate text-body text-ink">{title}</span>
        )}
        {description ? (
          <span className="truncate text-body text-ink-muted">
            {description}
          </span>
        ) : null}
      </span>
      {trailing ? (
        <span className="ml-3 flex shrink-0 items-center gap-2.5">
          {trailing}
        </span>
      ) : null}
    </>
  );
}
