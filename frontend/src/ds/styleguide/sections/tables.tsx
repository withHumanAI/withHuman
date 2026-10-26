import { useMemo, useState } from "react";
import { Avatar } from "@/ds/components/avatar";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import type { MenuAction } from "@/ds/components/menu";
import { Pagination } from "@/ds/components/pagination";
import { Table, type TableColumn, type TableSort } from "@/ds/components/table";
import { useToast } from "@/ds/components/toast";
import { CopyIcon, TrashIcon } from "@/ds/icons";
import { Section, Demo, KeyboardHint } from "../section";

interface Member {
  id: string;
  name: string;
  email: string;
  role: string;
  scopes: string[];
  joined: string;
  joinedAt: number;
}

const MEMBERS: Member[] = [
  { id: "m1", name: "Ana Batista", email: "ana@acme.test", role: "Owner", scopes: ["production", "billing"], joined: "Jan 2026", joinedAt: 1 },
  { id: "m2", name: "Sam Okafor", email: "sam@acme.test", role: "Reviewer", scopes: ["production"], joined: "Mar 2026", joinedAt: 3 },
  { id: "m3", name: "Priya Nair", email: "priya@acme.test", role: "Reviewer", scopes: ["billing"], joined: "Apr 2026", joinedAt: 4 },
  { id: "m4", name: "Jonas Weber", email: "jonas@acme.test", role: "Member", scopes: [], joined: "Aug 2026", joinedAt: 8 },
];

interface TailEvent {
  id: string;
  type: string;
  actor: string;
}

const TAIL_TYPES = ["approval.created", "approval.decided", "routing.level_entered", "agent.updated"];
const TAIL_ACTORS = ["Ana Batista", "deploy-bot", "System", "Sam Okafor"];
const TAIL_SEED: TailEvent[] = [
  { id: "t3", type: "approval.decided", actor: "Sam Okafor" },
  { id: "t2", type: "approval.created", actor: "deploy-bot" },
  { id: "t1", type: "agent.updated", actor: "Ana Batista" },
];

const TAIL_COLUMNS: TableColumn<TailEvent>[] = [
  {
    id: "event",
    label: "Event",
    grow: true,
    cell: (event) => (
      <Badge tone="neutral">
        <span className="font-mono text-[11px]">{event.type}</span>
      </Badge>
    ),
  },
  {
    id: "actor",
    label: "Actor",
    width: 140,
    cell: (event) => <span className="text-body text-ink-secondary">{event.actor}</span>,
  },
];

export function TablesSection() {
  const toast = useToast();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [sort, setSort] = useState<TableSort | null>({ column: "name", direction: "asc" });
  const [removing, setRemoving] = useState<Member | null>(null);
  const [removed, setRemoved] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<string | null>(null);
  const [page, setPage] = useState(0);
  const [tail, setTail] = useState<{ events: TailEvent[]; entering: Set<string> }>({
    events: TAIL_SEED,
    entering: new Set(),
  });
  /* A live tail inserts each batch at the top and marks it entering; the
   * next batch replaces the mark, so only the newest rows ever slide. */
  const arrive = (count: number) =>
    setTail((current) => {
      const batch = Array.from({ length: count }, (_, index) => {
        const sequence = current.events.length + index + 1;
        return { id: `t${sequence}`, type: TAIL_TYPES[sequence % TAIL_TYPES.length], actor: TAIL_ACTORS[sequence % TAIL_ACTORS.length] };
      }).reverse();
      return { events: [...batch, ...current.events], entering: new Set(batch.map((event) => event.id)) };
    });

  const members = useMemo(() => {
    const rows = MEMBERS.filter((member) => !removed.has(member.id));
    if (!sort) return rows;
    const factor = sort.direction === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => {
      if (sort.column === "joined") return (a.joinedAt - b.joinedAt) * factor;
      return a.name.localeCompare(b.name) * factor;
    });
  }, [sort, removed]);

  const columns: TableColumn<Member>[] = [
    {
      id: "name",
      label: "Member",
      grow: true,
      sortable: true,
      cell: (member) => (
        <span className="flex items-center gap-2.5">
          <Avatar name={member.name} size="sm" tooltip={false} />
          <span className="flex min-w-0 flex-col">
            <span className="truncate text-body text-ink">{member.name}</span>
            <span className="truncate text-label font-normal text-ink-muted">
              {member.email}
            </span>
          </span>
        </span>
      ),
    },
    {
      id: "role",
      label: "Role",
      width: 90,
      cell: (member) => (
        <span className="text-body text-ink-secondary">{member.role}</span>
      ),
    },
    {
      id: "scopes",
      label: "Scopes",
      info: "The environments this member can review requests in. A member with no scopes sees the queue but cannot decide.",
      width: 170,
      cell: (member) =>
        member.scopes.length ? (
          <span className="flex flex-wrap gap-1">
            {member.scopes.map((scope) => (
              <span
                key={scope}
                className="rounded-sm bg-fill-active px-1.5 py-0.5 font-mono text-[11px] text-ink-secondary"
              >
                {scope}
              </span>
            ))}
          </span>
        ) : (
          <span className="text-body text-ink-faint">None</span>
        ),
    },
    {
      id: "joined",
      label: "Joined",
      info: "When the invitation was accepted, not when it was sent.",
      width: 76,
      sortable: true,
      cell: (member) => (
        <span className="text-body text-ink-secondary">{member.joined}</span>
      ),
    },
  ];

  /* A suspended member is switched off, not gone: the row fades and the
   * status column says why at full contrast. */
  const suspendedColumns: TableColumn<Member>[] = [
    ...columns.slice(0, 2),
    {
      id: "status",
      label: "Status",
      width: 120,
      status: true,
      cell: (member) =>
        member.id === "m4" ? (
          <Badge tone="neutral">Suspended</Badge>
        ) : (
          <Badge tone="approve">Active</Badge>
        ),
    },
  ];

  /* The row menu is the table's own: View comes first whenever a row
   * opens, then these. Entries the reader cannot take stay faded. */
  const rowActions = (member: Member): MenuAction[] => [
    {
      id: "copy-email",
      label: "Copy email",
      icon: <CopyIcon />,
      action: () => {
        void navigator.clipboard.writeText(member.email);
        toast.add({ title: "Email copied" });
      },
    },
    {
      id: "remove",
      label: "Remove from organization",
      danger: true,
      disabled: member.role === "Owner",
      icon: <TrashIcon />,
      action: () => setRemoving(member),
    },
  ];

  return (
    <Section
      id="tables"
      title="Table"
      lede="Column headers over the List engine: the same highlight, selection, and keyboard contract, with controlled sorting so the order can live in the URL. Every row ends in the same dot-dot-dot menu: View first whenever a row opens, then the screen's rowActions, with entries the reader cannot take faded rather than hidden."
    >
      <Demo
        label="Members"
        hint="sort by header; a column with info explains itself on hover; the row menu opens the member first, then its actions (the owner cannot be removed); removal gets the confirm ceremony"
      >
        <div className="overflow-hidden rounded-lg border-hairline border-edge bg-surface shadow-sm">
          <Table
            items={members}
            columns={columns}
            getId={(member) => member.id}
            selected={selected}
            onSelectedChange={setSelected}
            sort={sort}
            onSort={setSort}
            onOpen={(member) => toast.add({ title: `Opened ${member.name} (demo)` })}
            rowLabel={(member) => member.name}
            rowActions={rowActions}
            aria-label="Members"
          />
        </div>
        <KeyboardHint>
          Arrows move, x or Space selects, shift extends, mod+A selects all.
        </KeyboardHint>
      </Demo>
      <Demo
        label="Row expansion"
        hint="click (or Enter) toggles an inline panel; the screen owns expandedId and renderExpanded; the menu's first entry names what opening does"
      >
        <div className="overflow-hidden rounded-lg border-hairline border-edge bg-surface shadow-sm">
          <Table
            items={MEMBERS.slice(0, 3)}
            columns={columns}
            getId={(member) => member.id}
            selected={new Set()}
            onSelectedChange={() => undefined}
            selectable={false}
            openLabel="View scopes"
            rowLabel={(member) => member.name}
            expandedId={expanded}
            onOpen={(member) =>
              setExpanded((current) => (current === member.id ? null : member.id))
            }
            renderExpanded={(member) => (
              <div className="rounded-md border-hairline border-edge bg-canvas px-3 py-2">
                <p className="text-micro tracking-[0.06em] text-ink-faint uppercase">
                  Scopes
                </p>
                <p className="pt-1 text-body text-ink-secondary">
                  {member.scopes.length ? member.scopes.join(", ") : "None assigned."}
                </p>
              </div>
            )}
            aria-label="Members with expansion"
          />
        </div>
      </Demo>
      <Demo
        label="Muted rows"
        hint="rowMuted fades a row that is switched off; the column marked status and the row menu keep full contrast, so the badge explains the fade"
      >
        <div className="overflow-hidden rounded-lg border-hairline border-edge bg-surface shadow-sm">
          <Table
            items={MEMBERS.slice(1)}
            columns={suspendedColumns}
            getId={(member) => member.id}
            selected={new Set()}
            onSelectedChange={() => undefined}
            selectable={false}
            rowLabel={(member) => member.name}
            rowActions={rowActions}
            rowMuted={(member) => member.id === "m4"}
            aria-label="Members with a suspended row"
          />
        </div>
      </Demo>
      <Demo
        label="Pagination"
        hint="the footer of a paged table: the rows on screen, the total when known, Previous and Next; the screen owns the page index and the page size, whether the rows come from the server or from memory"
      >
        <div className="overflow-hidden rounded-lg border-hairline border-edge bg-surface shadow-sm">
          <Table
            items={MEMBERS.slice(page * 2, page * 2 + 2)}
            columns={columns}
            getId={(member) => member.id}
            selected={new Set()}
            onSelectedChange={() => undefined}
            selectable={false}
            aria-label="Paged members"
          />
          <Pagination
            page={page}
            pageSize={2}
            count={MEMBERS.slice(page * 2, page * 2 + 2).length}
            total={MEMBERS.length}
            hasMore={(page + 1) * 2 < MEMBERS.length}
            onChange={setPage}
          />
        </div>
      </Demo>
      <Demo
        label="Live tail"
        hint="enteringIds opens a batch of arriving rows from the top in one 160ms slide; a screen leaves out a batch too large to follow, and reduced motion inserts instantly"
      >
        <div className="flex flex-col gap-3">
          <div className="flex gap-2">
            <Button size="sm" onClick={() => arrive(1)}>
              Add one event
            </Button>
            <Button size="sm" onClick={() => arrive(3)}>
              Add three events
            </Button>
          </div>
          <div className="overflow-hidden rounded-lg border-hairline border-edge bg-surface shadow-sm">
            <Table
              items={tail.events.slice(0, 6)}
              columns={TAIL_COLUMNS}
              getId={(event) => event.id}
              selected={new Set()}
              onSelectedChange={() => undefined}
              selectable={false}
              enteringIds={tail.entering}
              aria-label="Live events"
            />
          </div>
        </div>
      </Demo>
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => {
          if (!open) setRemoving(null);
        }}
        title={`Remove ${removing?.name ?? "this member"}?`}
        description="They lose access immediately. Their past decisions stay in the audit record."
        confirmLabel="Remove member"
        onConfirm={() => {
          if (!removing) return;
          setRemoved((current) => new Set(current).add(removing.id));
          toast.add({ type: "deny", title: `${removing.name} removed (demo)` });
        }}
      />
    </Section>
  );
}
