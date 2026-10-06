import { Link } from "@tanstack/react-router";
import {
  Dropdown,
  DropdownContent,
  DropdownGroup,
  DropdownGroupLabel,
  DropdownLinkItem,
  DropdownTrigger,
} from "@/ds/components/dropdown";
import { IconButton } from "@/ds/components/icon-button";
import { Tooltip } from "@/ds/components/tooltip";
import { BellIcon } from "@/ds/icons";
import { extensionLink, type TodoItem } from "./extensions";

/* The bell in the sidebar header: what the person should do, from the
 * edition's to-do sources. A mark on the bell says the list is not
 * empty; it clears when the last item is done, never by opening it. */
export function TodoBell({ items }: { items: TodoItem[] }) {
  const label = items.length === 0 ? "To do" : `To do, ${count(items.length)}`;
  return (
    <Dropdown>
      <Tooltip content="To do">
        <DropdownTrigger
          render={
            <IconButton aria-label={label} size="sm" attention={items.length > 0}>
              <BellIcon width={15} height={15} />
            </IconButton>
          }
        />
      </Tooltip>
      <DropdownContent align="start" className="w-[300px]">
        <DropdownGroup>
          <DropdownGroupLabel>To do</DropdownGroupLabel>
          {items.length === 0 ? (
            <p className="px-2 pt-0.5 pb-1.5 text-body text-ink-muted">You're all set.</p>
          ) : (
            items.map((item) => (
              <DropdownLinkItem
                key={item.key}
                render={<Link {...extensionLink({ to: item.to })} />}
                description={item.description}
              >
                {item.title}
              </DropdownLinkItem>
            ))
          )}
        </DropdownGroup>
      </DropdownContent>
    </Dropdown>
  );
}

function count(n: number): string {
  return n === 1 ? "1 thing" : `${n} things`;
}
