import { Fragment, useCallback, useMemo, useState, type HTMLAttributes } from "react";
import { useLinkProps, useNavigate, useSearch } from "@tanstack/react-router";
import { Avatar } from "@/ds/components/avatar";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { EmptyState } from "@/ds/components/empty-state";
import { List, ListCell, ListRowContent, type ListRowProps } from "@/ds/components/list";
import { LoadingDots } from "@/ds/components/loading-dots";
import { SelectionBar } from "@/ds/components/selection-bar";
import { SidebarReopenControl } from "@/ds/components/sidebar-reopen";
import { useToast } from "@/ds/components/toast";
import {
  Dropdown,
  DropdownContent,
  DropdownGroup,
  DropdownGroupLabel,
  DropdownItem,
  DropdownTrigger,
} from "@/ds/components/dropdown";
import { ToggleGroup, ToggleGroupItem } from "@/ds/components/toggle-group";
import { Tooltip } from "@/ds/components/tooltip";
import { Page } from "@/ds/components/page";
import { CheckIcon, ChevronDownIcon, ClockIcon, DashIcon, InboxIcon, ProgressIcon, XIcon } from "@/ds/icons";
import { cn } from "@/ds/lib/cn";
import { focusRing } from "@/ds/lib/focus";
import { useMediaQuery } from "@/ds/lib/use-media-query";
import { api } from "../api";
import type { ApprovalRequest, ApprovalStatus } from "../types";
import { formatRelative } from "./format";
import { renderTitle } from "./tool-presentation";
import { useTitle } from "./use-title";
import { refreshQueue, useQueue } from "./queue-store";
import {
  queueScopeOf,
  queueSearch,
  type QueueFilter,
  type QueueScope,
  type QueueSearch,
} from "./queue-search";
import { STATUS_LABEL, STATUS_TONE } from "./approval-status";
import { RequestDetail } from "./request-detail";

const STATUS_ICON_TEXT: Record<ApprovalStatus, string> = {
  pending: "text-warn",
  approved: "text-approve",
  denied: "text-deny",
  expired: "text-ink-faint",
  cancelled: "text-ink-faint",
};

/* The icon carries the tone, property-pill style; hovering names the
 * status. */
function StatusIcon({ status }: { status: ApprovalStatus }) {
  const className = STATUS_ICON_TEXT[status];
  let icon = <ClockIcon width={14} height={14} className={className} />;
  if (status === "pending") {
    icon = <ProgressIcon width={14} height={14} className={className} />;
  } else if (status === "approved") {
    icon = <CheckIcon width={14} height={14} className={className} />;
  } else if (status === "denied") {
    icon = <XIcon width={14} height={14} className={className} />;
  } else if (status === "cancelled") {
    icon = <DashIcon width={14} height={14} className={className} />;
  }
  return (
    <Tooltip content={STATUS_LABEL[status]}>
      <span className="flex items-center">{icon}</span>
    </Tooltip>
  );
}

type Filter = QueueFilter;

/* One view of the queue: which layer, and which statuses within it. */
interface View {
  scope: QueueScope;
  filter: Filter;
}

function requestDestination(requestId: string, view: View) {
  return {
    to: "/queue/$requestId" as const,
    params: { requestId },
    search: queueSearch(view.scope, view.filter),
  };
}

/* Borrow only the router's intent handlers. The list still owns opening,
 * selection and keyboard navigation; preloading follows ordinary links. */
function QueueRow({
  requestId,
  view,
  ...props
}: ListRowProps & { requestId: string; view: View }) {
  const rowEvents: HTMLAttributes<HTMLElement> = props;
  const intent: HTMLAttributes<HTMLElement> = useLinkProps({
    ...requestDestination(requestId, view),
    onMouseEnter: rowEvents.onMouseEnter,
    onMouseLeave: rowEvents.onMouseLeave,
    onFocus: rowEvents.onFocus,
    onBlur: rowEvents.onBlur,
    onTouchStart: rowEvents.onTouchStart,
  });
  return (
    <li
      {...props}
      onMouseEnter={intent.onMouseEnter}
      onMouseLeave={intent.onMouseLeave}
      onFocus={intent.onFocus}
      onBlur={intent.onBlur}
      onTouchStart={intent.onTouchStart}
    />
  );
}

/* The list's age cell: the tooltip carries the request's exact
 * timeline, one line per event. */
function AgeCell({ request }: { request: ApprovalRequest }) {
  const decision = request.decision;
  return (
    <Tooltip
      content={
        <span className="flex flex-col gap-0.5">
          <span>
            Requested: {new Date(request.created_at).toLocaleString()}
          </span>
          {decision ? (
            <span>
              {STATUS_LABEL[decision.status]}:{" "}
              {new Date(decision.decided_at).toLocaleString()}
            </span>
          ) : null}
        </span>
      }
    >
      <span>{formatRelative(request.created_at)}</span>
    </Tooltip>
  );
}

/* Rows lead with the verified tool call, named by its presentation when
 * one exists (the tool itself then moves to the second line); the agent
 * identity sits underneath. */
function rowText(request: ApprovalRequest) {
  const title = renderTitle(request.presentation?.title, request.arguments);
  return {
    title: title ? (
      <span>
        {title.map((segment, index) =>
          segment.kind === "value" ? (
            <span key={index} className="font-mono text-[12px] text-ink-secondary">
              {segment.text}
            </span>
          ) : (
            <Fragment key={index}>{segment.text}</Fragment>
          ),
        )}
      </span>
    ) : (
      <span className="font-mono text-[12px]">{request.tool}</span>
    ),
    description: (
      <>
        {title ? (
          <>
            <span className="font-mono text-[11px]">{request.tool}</span>
            <span className="text-ink-faint"> · </span>
          </>
        ) : null}
        {request.agent_name}
        <span className="text-ink-faint"> · </span>
        {request.agent_instance_name}
        {request.requester_display_name ? (
          <>
            <span className="text-ink-faint"> · </span>
            for {request.requester_display_name}
          </>
        ) : null}
      </>
    ),
  };
}

/* The layers as the page names them: the heading is the layer, so each
 * label must read as a title on its own and as a choice in the menu. */
const LAYERS: Array<{ value: QueueScope; label: string; description: string }> = [
  { value: "notified", label: "Sent to you", description: "Requests you were notified about" },
  { value: "decidable", label: "Can review", description: "Everything you can review" },
  { value: "visible", label: "All visible", description: "All requests you can see" },
];

/* The heading doubles as the layer switcher, in the way a mail client's
 * heading names the folder: one row holds the title and the status
 * control, and the menu explains the layers where they are chosen. */
function LayerHeading({
  scope,
  onChange,
  size,
}: {
  scope: QueueScope;
  onChange: (scope: QueueScope) => void;
  size: "title" | "title-lg";
}) {
  const current = LAYERS.find((layer) => layer.value === scope) ?? LAYERS[0];
  return (
    <Dropdown>
      <DropdownTrigger
        aria-label="Queue layer"
        className={cn(
          "-ml-1.5 flex min-w-0 cursor-pointer items-center gap-1 rounded-sm px-1.5 py-0.5",
          "text-left text-ink select-none",
          "transition-colors duration-100 hover:bg-fill-hover data-popup-open:bg-fill-active",
          size === "title" ? "text-title" : "text-title-lg",
          focusRing,
        )}
      >
        <h1 className="truncate">{current.label}</h1>
        <ChevronDownIcon
          width={size === "title" ? 14 : 16}
          height={size === "title" ? 14 : 16}
          className="shrink-0 text-ink-faint"
        />
      </DropdownTrigger>
      <DropdownContent align="start" className="w-[264px]">
        <DropdownGroup>
          <DropdownGroupLabel>Show</DropdownGroupLabel>
          {LAYERS.map((layer) => (
            <DropdownItem
              key={layer.value}
              className="h-auto items-start py-1.5"
              onClick={() => onChange(layer.value)}
            >
              <span className="flex items-start gap-2">
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="text-body text-ink">{layer.label}</span>
                  <span className="text-micro leading-[16px] font-normal whitespace-normal text-ink-muted">
                    {layer.description}
                  </span>
                </span>
                <CheckIcon
                  width={14}
                  height={14}
                  aria-hidden={layer.value !== scope}
                  className={cn("mt-0.5 shrink-0 text-ink", layer.value !== scope && "invisible")}
                />
              </span>
            </DropdownItem>
          ))}
        </DropdownGroup>
      </DropdownContent>
    </Dropdown>
  );
}

/* What an empty list says, by layer while showing pending requests, and
 * once for every other status filter. */
const EMPTY_COPY: Record<QueueScope | "resolved", { title: string; description: string }> = {
  notified: {
    title: "Nothing waiting for you",
    description: "Requests sent to you will appear here.",
  },
  decidable: {
    title: "Nothing to review",
    description: "Requests you can review will appear here.",
  },
  visible: {
    title: "Nothing pending",
    description: "Pending requests you can see will appear here.",
  },
  resolved: {
    title: "No requests here",
    description: "Requests will show up here as their status changes.",
  },
};

export function QueueScreen({
  openID,
  preloaded,
}: {
  openID: string | null;
  /* The open request as the route loader fetched it, for requests the
   * snapshot does not carry. */
  preloaded: ApprovalRequest | null;
}) {
  const navigate = useNavigate();
  const search = useSearch({ strict: false }) as QueueSearch;
  const scope = queueScopeOf(search);
  const filter: Filter = search.filter ?? "pending";
  const view: View = { scope, filter };

  const { requests, loaded, liveError } = useQueue(scope);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkSaving, setBulkSaving] = useState(false);
  const toast = useToast();
  useTitle("Queue");

  /* Changing the view rewrites the URL in place; the open request, if
   * any, stays open even when the new view does not list it. */
  function setView(next: View) {
    if (openID) {
      void navigate({
        to: "/queue/$requestId",
        params: { requestId: openID },
        search: queueSearch(next.scope, next.filter),
        replace: true,
      });
      return;
    }
    void navigate({ to: "/queue", search: queueSearch(next.scope, next.filter), replace: true });
  }

  function openRequest(id: string) {
    void navigate(requestDestination(id, view));
  }

  function closeRequest() {
    void navigate({ to: "/queue", search: queueSearch(scope, filter) });
  }

  const visible = useMemo(() => {
    if (filter === "pending") {
      return requests.filter((request) => request.status === "pending");
    }
    if (filter === "resolved") {
      return requests.filter((request) => request.status !== "pending");
    }
    return requests;
  }, [requests, filter]);

  const selectedPending = useMemo(
    () =>
      requests.filter(
        (request) => selected.has(request.id) && request.status === "pending",
      ),
    [requests, selected],
  );

  async function decideSelected(status: "approved" | "denied") {
    setBulkSaving(true);
    let done = 0;
    let failed = 0;
    for (const request of selectedPending) {
      try {
        await api.decide(request.id, status, "");
        done += 1;
      } catch {
        failed += 1;
      }
    }
    setBulkSaving(false);
    setSelected(new Set());
    await refreshQueue(scope);
    if (done > 0) {
      toast.add({
        type: status === "approved" ? "approve" : "deny",
        title: `${done} ${done === 1 ? "request" : "requests"} ${status}`,
      });
    }
    if (failed > 0) {
      toast.add({
        type: "deny",
        title: `${failed} ${failed === 1 ? "decision" : "decisions"} failed`,
      });
    }
  }

  /* Automatic decisions never enter the queue and older requests fall
   * outside the snapshot, but the audit log links straight to both: fetch
   * by id whatever the snapshot does not carry. */
  const listed = openID
    ? requests.find((request) => request.id === openID)
    : undefined;
  const [fetched, setFetched] = useState<ApprovalRequest | null>(preloaded);
  const loadOpen = useCallback(() => {
    if (!openID) return Promise.resolve();
    return api
      .approval(openID)
      .then(setFetched)
      .catch(() => setFetched(null));
  }, [openID]);
  const open = listed ?? (fetched?.id === openID ? fetched : undefined);
  const refresh = () =>
    Promise.all([refreshQueue(scope), listed ? Promise.resolve() : loadOpen()]);

  /* After a decision the reviewer moves on: the other pending requests,
   * oldest first, and a way to open the next one. */
  const otherPending = useMemo(
    () => requests.filter((request) => request.status === "pending" && request.id !== openID),
    [requests, openID],
  );
  const openNext = otherPending.length > 0 ? () => openRequest(otherPending[0].id) : undefined;

  const split = useMediaQuery("(min-width: 1280px)");

  const setScope = (next: QueueScope) => setView({ scope: next, filter });

  const reconnecting = liveError ? (
    <Tooltip content="Reconnecting to live updates">
      <LoadingDots aria-label="Reconnecting to live updates" />
    </Tooltip>
  ) : null;

  const filterControl = (
    <ToggleGroup
      aria-label="Queue filter"
      value={filter}
      onValueChange={(value) => setView({ scope, filter: value as Filter })}
    >
      <ToggleGroupItem value="pending">Pending</ToggleGroupItem>
      <ToggleGroupItem value="resolved">Resolved</ToggleGroupItem>
      <ToggleGroupItem value="all">All</ToggleGroupItem>
    </ToggleGroup>
  );

  const empty = EMPTY_COPY[filter === "pending" ? scope : "resolved"];
  const emptyList = (
    <EmptyState icon={<InboxIcon />} title={empty.title} description={empty.description} />
  );

  const selectionBar = (
    <SelectionBar count={selected.size} onClear={() => setSelected(new Set())}>
      <Button
        size="sm"
        variant="danger"
        loading={bulkSaving}
        disabled={selectedPending.length === 0}
        onClick={() => void decideSelected("denied")}
      >
        Deny
      </Button>
      <Button
        size="sm"
        variant="approve"
        loading={bulkSaving}
        disabled={selectedPending.length === 0}
        onClick={() => void decideSelected("approved")}
      >
        Approve
      </Button>
    </SelectionBar>
  );

  if (split) {
    return (
      <div className="flex h-full">
        <section className="flex w-[400px] shrink-0 flex-col border-r-hairline border-edge">
          <div className="flex flex-col gap-3 px-4 py-[11px]">
            <div className="flex items-center gap-3">
              <SidebarReopenControl className="-ml-1 flex" claim />
              <div className="mr-auto flex min-w-0 items-center gap-2">
                <LayerHeading scope={scope} onChange={setScope} size="title" />
                {reconnecting}
              </div>
              {filterControl}
            </div>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto border-t-hairline border-edge">
            {!loaded ? null : visible.length === 0 ? (
              emptyList
            ) : (
              <List
                items={visible}
                getId={(request) => request.id}
                selected={selected}
                onSelectedChange={setSelected}
                onOpen={(request) => openRequest(request.id)}
                renderRow={(request, props) => (
                  <QueueRow {...props} requestId={request.id} view={view} />
                )}
                openOnClick
                activeId={openID}
                aria-label="Review queue"
              >
                {(request) => (
                  <ListRowContent
                    leading={
                      <Avatar
                        name={request.agent_name}
                        id={request.agent_slug}
                        kind="agent"
                        size="sm"
                      />
                    }
                    {...rowText(request)}
                    trailing={
                      <>
                        <StatusIcon status={request.status} />
                        <ListCell
                          width={30}
                          align="right"
                          className="font-mono text-micro font-normal text-ink-faint"
                        >
                          <AgeCell request={request} />
                        </ListCell>
                      </>
                    }
                  />
                )}
              </List>
            )}
          </div>
        </section>
        <section className="min-w-0 flex-1 overflow-y-auto">
          {open ? (
            <RequestDetail
              request={open}
              onDecided={refresh}
              nextPending={otherPending.length}
              onOpenNext={openNext}
            />
          ) : (
            <div className="flex h-full items-center justify-center">
              <EmptyState
                icon={<InboxIcon />}
                title="Select a request"
                description="Choose an item from the queue to review it here."
              />
            </div>
          )}
        </section>
        {selectionBar}
      </div>
    );
  }

  if (open) {
    return (
      <RequestDetail
        request={open}
        onBack={closeRequest}
        onDecided={refresh}
        nextPending={otherPending.length}
        onOpenNext={openNext}
      />
    );
  }

  return (
    <Page className="gap-5">
      <div className="flex items-center justify-between gap-4">
        <div className="flex min-w-0 items-center gap-2">
          <LayerHeading scope={scope} onChange={setScope} size="title-lg" />
          {reconnecting}
        </div>
        {filterControl}
      </div>

      <div className="overflow-hidden rounded-lg border-hairline border-edge bg-surface shadow-sm">
        {!loaded ? null : visible.length === 0 ? (
          emptyList
        ) : (
          <List
            items={visible}
            getId={(request) => request.id}
            selected={selected}
            onSelectedChange={setSelected}
            onOpen={(request) => openRequest(request.id)}
            renderRow={(request, props) => (
              <QueueRow {...props} requestId={request.id} view={view} />
            )}
            openOnClick
            openButton
            aria-label="Review queue"
          >
            {(request) => (
              <ListRowContent
                leading={
                  <Avatar
                    name={request.agent_name}
                    id={request.agent_slug}
                    kind="agent"
                    size="sm"
                  />
                }
                {...rowText(request)}
                trailing={
                  <>
                    <ListCell width={96}>
                      <Badge tone={STATUS_TONE[request.status]}>
                        {STATUS_LABEL[request.status]}
                      </Badge>
                    </ListCell>
                    <ListCell
                      width={30}
                      align="right"
                      className="font-mono text-micro font-normal text-ink-faint"
                    >
                      <AgeCell request={request} />
                    </ListCell>
                  </>
                }
              />
            )}
          </List>
        )}
      </div>

      {selectionBar}
    </Page>
  );
}
