import type { ReactNode } from "react";
import { cn } from "@/ds/lib/cn";
import { Badge } from "./badge";
import { Avatar } from "./avatar";
import { Button } from "./button";
import { CardDescription, CardHeader, CardTitle } from "./card";
import { EmptyState } from "./empty-state";
import type { InfoCardProps } from "./info-card";
import { InfoLabel } from "./info-label";
import type { MenuAction } from "./menu";
import { Referenced } from "./reference";
import { Table, type TableColumn } from "./table";

/*
 * The history of a revisioned resource: pipelines, escalation paths and
 * webhook endpoints all keep immutable, numbered revisions with one
 * active, so they share one table. The screen supplies what a revision
 * is (a name, a destination, a block count) and what activating does;
 * the table owns the columns, the active mark, the row menu, and
 * paging, so no two histories can drift apart.
 *
 * Every row carries the table's menu: View, then Activate, Roll back,
 * and Create new revision from this. An entry the reader cannot take
 * right now (the active revision, a newer one under Roll back, a
 * missing permission) stays in place faded rather than vanishing.
 *
 * Paging is by cursor from the server: the screen passes the rows it
 * has and how many exist in all, and the table asks for the next page
 * with Load more. Newest first, always.
 */
export interface RevisionRow {
  id: string;
  revision: number;
  is_active: boolean;
  created_at: string;
  created_by_actor_id: string;
  /* Present when the revision was archived: readable history that can
   * never be activated again, though a new revision may start from it. */
  archived_at?: string;
}

export interface RevisionHistoryProps<Item extends RevisionRow> {
  /* The card header: the title, one line under it, and the active
   * revision named at the right so the reader never scans for it. */
  title?: string;
  /* What a revision is here, behind an info mark on the title. */
  info?: ReactNode;
  docsLinks?: InfoCardProps["docsLinks"];
  description?: ReactNode;
  items: Item[];
  /* How many revisions the key has across every page. */
  totalCount: number;
  /* Optional resource-specific column, omitted for a compact history. */
  summaryLabel?: string;
  summary?: (item: Item) => ReactNode;
  /* Who created a revision, when the screen can name people; the
   * membership id stays available as a tooltip either way. */
  authorName?: (membershipId: string) => string | undefined;
  /* The revision requests use now. Activating an earlier one is a
   * roll back, and the button says so. */
  activeRevision: number | null;
  /* Opens the revision read-only: a plain click on the row, or Enter.
   * There is no button for it; the row is the affordance. */
  onOpen?: (item: Item) => void;
  /* Absent when the reader cannot activate (permission, archived); the
   * Activate and Roll back entries then stay in the menu, faded. */
  onActivate?: (item: Item) => void;
  /* The revision whose activation is in flight; every other row's
   * Activate and Roll back wait for it. */
  activating?: number | null;
  /* Loads this revision's content into the editor as a fresh draft, so
   * the next save creates a new revision from it. Absent when the
   * reader cannot write (permission, archived); the entry stays in the
   * menu, faded. */
  onCreateFrom?: (item: Item) => void;
  /* Extra entries a resource needs in the row menu (a webhook's Test
   * delivery), placed in their own group above the revision actions. */
  menuActions?: (item: Item) => MenuAction[];
  hasMore: boolean;
  onLoadMore?: () => void;
  loadingMore?: boolean;
  /* What the table says with no revisions at all. */
  emptyTitle?: string;
  emptyDescription?: string;
  className?: string;
  "aria-label": string;
}

/* Column widths are fixed once here so every history lines up the same
 * way; the summary column, or Created when it is absent, is elastic. The budget fits the narrow
 * page measure, where the escalation path and webhook editors live, with
 * room for the summary to read. */
const REVISION_WIDTH = 92;
const STATUS_WIDTH = 72;
const CREATED_WIDTH = 148;

/* One date format for every history; the exact second is not what a
 * reader compares revisions by. */
const CREATED_FORMAT: Intl.DateTimeFormatOptions = { dateStyle: "medium", timeStyle: "short" };

export function RevisionHistory<Item extends RevisionRow>({
  title = "Revisions",
  info,
  docsLinks,
  description,
  items,
  totalCount,
  summaryLabel = "Summary",
  summary,
  authorName,
  activeRevision,
  onOpen,
  onActivate,
  activating = null,
  onCreateFrom,
  menuActions,
  hasMore,
  onLoadMore,
  loadingMore = false,
  emptyTitle = "No revisions yet",
  emptyDescription = "Revisions you save will appear here.",
  className,
  "aria-label": ariaLabel,
}: RevisionHistoryProps<Item>) {
  const columns: TableColumn<Item>[] = [
    {
      id: "revision",
      label: "Revision",
      width: REVISION_WIDTH,
      cell: (item) => <span className="font-medium text-ink">Revision {item.revision}</span>,
    },
    {
      id: "status",
      label: "Status",
      width: STATUS_WIDTH,
      cell: (item) =>
        item.is_active ? (
          <Badge tone="approve">Active</Badge>
        ) : item.archived_at ? (
          <span className="text-ink-muted" title={new Date(item.archived_at).toLocaleString()}>
            Archived
          </span>
        ) : (
          <span className="text-ink-muted">Inactive</span>
        ),
    },
    ...(summary
      ? [{
          id: "summary",
          label: summaryLabel,
          grow: true,
          cell: (item: Item) => <span className="block min-w-0 truncate">{summary(item)}</span>,
        }]
      : []),
    {
      id: "created",
      label: "Created",
      width: summary ? CREATED_WIDTH : undefined,
      grow: !summary,
      cell: (item) => {
        const author = authorName?.(item.created_by_actor_id);
        return (
          <span className="flex min-w-0 flex-col" title={item.created_by_actor_id}>
            <span className="truncate text-ink-secondary">
              {new Date(item.created_at).toLocaleString(undefined, CREATED_FORMAT)}
            </span>
            {author ? (
              <span className="-ml-2 flex">
                <Referenced reference={{ kind: "person", id: item.created_by_actor_id, name: author }}>
                  <Badge className="gap-1 pl-0.5" icon={<Avatar name={author} size="sm" tooltip={false} />}>
                    {author}
                  </Badge>
                </Referenced>
              </span>
            ) : null}
          </span>
        );
      },
    },
  ];

  /* Activating an earlier revision than the active one is a roll back;
   * a later one (or any, with nothing active) is a plain activation.
   * Exactly one of the two entries ever applies, and neither to an
   * archived revision. */
  const rowActions = (item: Item): MenuAction[] => {
    const earlier = activeRevision !== null && item.revision < activeRevision;
    const busy = activating !== null;
    const inFlight = activating === item.revision;
    const frozen = item.archived_at !== undefined;
    return [
      ...(menuActions?.(item) ?? []).map((entry) => ({ ...entry, group: entry.group ?? "resource" })),
      {
        id: "activate",
        group: "revision",
        label: inFlight && !earlier ? "Activating" : "Activate",
        disabled: !onActivate || item.is_active || earlier || busy || frozen,
        action: () => onActivate?.(item),
      },
      {
        id: "roll-back",
        group: "revision",
        label: inFlight && earlier ? "Rolling back" : "Roll back",
        disabled: !onActivate || item.is_active || !earlier || busy || frozen,
        action: () => onActivate?.(item),
      },
      {
        id: "create-from",
        group: "draft",
        label: "Create new revision from this",
        disabled: !onCreateFrom,
        action: () => onCreateFrom?.(item),
      },
    ];
  };

  const shown = items.length;
  const count =
    hasMore || shown < totalCount
      ? `${shown} of ${totalCount} revisions`
      : `${totalCount} ${totalCount === 1 ? "revision" : "revisions"}`;

  return (
    <div className={cn("flex flex-col", className)}>
      <CardHeader>
        <div className="min-w-0">
          <CardTitle>{info ? <InfoLabel info={info} docsLinks={docsLinks}>{title}</InfoLabel> : title}</CardTitle>
          {description ? <CardDescription>{description}</CardDescription> : null}
        </div>
        <span className="shrink-0 pt-0.5 text-label text-ink-muted">
          {activeRevision !== null ? `Active revision ${activeRevision}` : "No active revision"}
        </span>
      </CardHeader>
      {shown ? (
        <div className="overflow-x-auto">
          <Table
            aria-label={ariaLabel}
            items={items}
            getId={(item) => item.id}
            selected={new Set()}
            onSelectedChange={() => undefined}
            selectable={false}
            openOnClick={onOpen !== undefined}
            onOpen={onOpen}
            padding="card"
            columns={columns}
            rowActions={rowActions}
            rowLabel={(item) => `revision ${item.revision}`}
            rowBusy={(item) => activating === item.revision}
            className="min-w-[560px]"
          />
        </div>
      ) : (
        <EmptyState title={emptyTitle} description={emptyDescription} className="py-10" />
      )}
      {shown ? (
        <div className="flex min-h-10 items-center justify-between gap-3 border-t-hairline border-edge px-5 py-1.5">
          <span className="text-label text-ink-muted">{count}</span>
          {hasMore ? (
            <Button
              variant="ghost"
              size="sm"
              loading={loadingMore}
              onClick={onLoadMore}
            >
              Load more
            </Button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
