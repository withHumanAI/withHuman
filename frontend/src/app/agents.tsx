import { useMemo, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Card } from "@/ds/components/card";
import { EmptyState } from "@/ds/components/empty-state";
import {
  HoverCard,
  HoverCardBody,
  HoverCardMeta,
  HoverCardTitle,
} from "@/ds/components/hover-card";
import { Select, SelectContent, SelectItem, SelectTrigger } from "@/ds/components/select";
import { Table, type TableColumn } from "@/ds/components/table";
import { TextField } from "@/ds/components/text-field";
import { Page } from "@/ds/components/page";
import { PageHeader } from "@/ds/components/page-header";
import { AgentIcon } from "@/ds/icons";
import type { AgentSummary } from "../types";
import {
  agentConnection,
  type AgentConnection,
  CONNECTION_LABEL,
  CONNECTION_TONE,
} from "./agent-state";
import { useAccess } from "./authorization";
import { extensionLink, useAuditLinks } from "./extensions";
import { formatRelative, plural } from "./format";
import { RequestTrend } from "./request-trend";
import { useTitle } from "./use-title";

/* What the agents route loader delivers. */
export interface AgentsPage {
  agents: AgentSummary[];
  error: string;
}

/* The status filter: the agents still in service (the default, every
 * connection state but archived), one connection state, or everything
 * archived agents included. */
type StatusFilter = AgentConnection | "current" | "all";

const STATUS_FILTERS: StatusFilter[] = [
  "current",
  "active",
  "no_live_credential",
  "disabled",
  "archived",
  "all",
];

const STATUS_FILTER_LABEL: Record<StatusFilter, string> = {
  current: "Current agents",
  all: "All agents",
  ...CONNECTION_LABEL,
};

function matchesFilter(connection: AgentConnection, filter: StatusFilter): boolean {
  if (filter === "all") return true;
  if (filter === "current") return connection !== "archived";
  return connection === filter;
}

/* The agent inventory: every identity that can open requests, with its
 * runtime instances and credential state. Readable with agent.read;
 * connecting a new one needs agent.write, and the per-agent actions
 * live on the agent page. */
export function AgentsScreen({
  page,
  canCreate,
}: {
  page: AgentsPage;
  canCreate: boolean;
}) {
  useTitle("Agents");
  const navigate = useNavigate();
  /* The server already filters the list to the agents the viewer may
   * read; an empty answer for a scoped reader means "none of yours",
   * not "none at all". */
  const { scopeOf, holdsAnywhere } = useAccess();
  const scopedRead = scopeOf("agent.read") !== "all";
  const auditLinkFor = useAuditLinks();
  const { agents, error } = page;
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState<StatusFilter>("current");

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return agents.filter((agent) => {
      if (!matchesFilter(agentConnection(agent), status)) return false;
      if (!needle) return true;
      return (
        agent.name.toLowerCase().includes(needle) ||
        agent.slug.toLowerCase().includes(needle)
      );
    });
  }, [agents, status, query]);

  const columns: TableColumn<AgentSummary>[] = [
    {
      id: "agent",
      label: "Agent",
      grow: true,
      cell: (agent) => (
        <div className="min-w-0">
          <p className="truncate text-body font-medium text-ink">
            {agent.name}
            {agent.kind === "personal" ? (
              <span className="ml-2 text-label font-normal text-ink-faint">
                personal
              </span>
            ) : null}
          </p>
          <p className="truncate font-mono text-[12px] text-ink-muted">{agent.slug}</p>
        </div>
      ),
    },
    {
      id: "instances",
      label: "Instances",
      info: "How many instances of the agent have credentials.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/agents#instance-connections", label: "Connecting agent instances" }],
      width: 100,
      cell: (agent) => <InstanceCount agent={agent} />,
    },
    {
      id: "seen",
      label: "Last seen",
      info: "The last time a request was made from an agent instance.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/agents#connection-status-and-requests", label: "Connection status and requests" }],
      width: 90,
      cell: (agent) =>
        agent.last_seen_at ? (
          <span className="text-body text-ink">{formatRelative(agent.last_seen_at)}</span>
        ) : (
          <span className="text-body text-ink-faint">Never</span>
        ),
    },
    {
      id: "requests",
      label: "Requests, 7d",
      info: "The number of requests made in the last 7 days by instances of this agent.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/agents#connection-status-and-requests", label: "Connection status and requests" }],
      width: 130,
      cell: (agent) => (
        <RequestTrend
          label={agent.name}
          total={agent.recent_request_count}
          trend={agent.request_trend}
          pending={agent.pending_request_count}
        />
      ),
    },
    {
      id: "status",
      label: "Status",
      info: "State of the agent. Active, no live credential, disabled or archived.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/agents#enabled-and-disabled-access", label: "Agent access and status" }],
      width: 150,
      cell: (agent) => {
        const connection = agentConnection(agent);
        return <Badge tone={CONNECTION_TONE[connection]}>{CONNECTION_LABEL[connection]}</Badge>;
      },
    },
  ];

  return (
    <Page>
      <PageHeader>
        <div className="flex items-start justify-between gap-4">
          <div>
            <h1 className="text-title-lg text-ink">Agents</h1>
            <p className="mt-1.5 max-w-[560px] text-body text-ink-muted">
              Add new agents or look into the ones you already have.
            </p>
          </div>
          {canCreate ? (
            <Button variant="primary" onClick={() => void navigate({ to: "/connect" })}>
              Add New Agent
            </Button>
          ) : null}
        </div>
      </PageHeader>

      {error ? (
        <Callout tone="warn">{error}</Callout>
      ) : agents.length === 0 ? (
        <Card>
          {scopedRead ? (
            <EmptyState
              icon={<AgentIcon width={18} height={18} />}
              title="No agents you can see yet"
              description={
                canCreate
                  ? "Agents you add will appear here."
                  : "Agents you can see will appear here."
              }
              action={
                canCreate ? (
                  <Button variant="primary" onClick={() => void navigate({ to: "/connect" })}>
                    Add New Agent
                  </Button>
                ) : undefined
              }
            />
          ) : (
            <EmptyState
              icon={<AgentIcon width={18} height={18} />}
              title="No agents added yet"
              description={
                canCreate
                  ? "Add a new agent"
                  : "Agents an administrator adds will appear here."
              }
              action={
                canCreate ? (
                  <Button variant="primary" onClick={() => void navigate({ to: "/connect" })}>
                    Add New Agent
                  </Button>
                ) : undefined
              }
            />
          )}
        </Card>
      ) : (
        <>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <TextField
              aria-label="Search agents"
              placeholder="Search agents"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              className="w-[280px]"
            />
            <div className="flex items-center gap-4">
              <span className="text-label text-ink-muted">
                Showing {visible.length} of {agents.length}
              </span>
              <Select
                items={STATUS_FILTERS}
                itemToStringLabel={(item) => STATUS_FILTER_LABEL[item as StatusFilter]}
                value={status}
                onValueChange={(next) => setStatus((next as StatusFilter | null) ?? "all")}
              >
                <SelectTrigger aria-label="Status" />
                <SelectContent<StatusFilter>>
                  {(item) => (
                    <SelectItem key={item} value={item}>
                      {STATUS_FILTER_LABEL[item]}
                    </SelectItem>
                  )}
                </SelectContent>
              </Select>
            </div>
          </div>

          <Card className="overflow-x-auto">
            {visible.length === 0 ? (
              <EmptyState
                title="No agents match"
                description={
                  status === "all"
                    ? "Try a different search."
                    : status === "archived"
                      ? "No archived agents match."
                      : status === "current" && !query.trim()
                        ? "All agents are archived. Filter by Archived to see them."
                        : "Try a different search or status filter."
                }
              />
            ) : (
              <Table
                aria-label="Agents"
                items={visible}
                getId={(agent) => agent.slug}
                selected={new Set()}
                onSelectedChange={() => undefined}
                selectable={false}
                openOnClick
                onOpen={(agent) =>
                  void navigate({ to: "/agents/$agentSlug", params: { agentSlug: agent.slug } })
                }
                columns={columns}
                rowLabel={(agent) => agent.name}
                rowActions={(agent) => {
                  const audit = auditLinkFor({ actor: agent.slug });
                  return audit
                    ? [
                        {
                          id: "audit",
                          label: audit.label,
                          disabled: !audit.allowed,
                          action: () => void navigate(extensionLink(audit)),
                        },
                      ]
                    : [];
                }}
                className="min-w-[760px]"
              />
            )}
          </Card>
        </>
      )}
    </Page>
  );
}

/* The instance count, with the breakdown on hover: how many hold a
 * live credential. An agent with no instances has nothing to break
 * down, so it shows a plain zero. */
function InstanceCount({ agent }: { agent: AgentSummary }) {
  if (agent.instance_count === 0) {
    return <span className="text-body text-ink-faint">0</span>;
  }
  const live = agent.live_instance_count;
  const idle = agent.instance_count - live;
  return (
    <HoverCard
      triggerClassName="rounded-sm"
      content={
        <>
          <HoverCardTitle>
            <span>{plural(agent.instance_count, "instance")}</span>
            <Badge tone={live > 0 ? "approve" : "warn"}>{live} live</Badge>
          </HoverCardTitle>
          <HoverCardMeta>
            Live instances hold a valid credential.
          </HoverCardMeta>
          <HoverCardBody>
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
              <dt className="text-ink-muted">Live</dt>
              <dd className="text-ink">{live}</dd>
              <dt className="text-ink-muted">Without a live credential</dt>
              <dd className="text-ink">{idle}</dd>
            </dl>
          </HoverCardBody>
        </>
      }
    >
      <span className="text-body text-ink underline decoration-edge-strong decoration-dotted underline-offset-4">
        {agent.instance_count}
      </span>
    </HoverCard>
  );
}
