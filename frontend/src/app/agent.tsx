import { AgentToolsPanel } from "./agent-tools";
import type { AgentCatalogData } from "./tool-catalog";
import { useState, type FormEvent, type ReactNode } from "react";
import {
  Link,
  useNavigate,
  useRouter,
  useRouterState,
} from "@tanstack/react-router";
import { Tab, TabPanel, Tabs, TabsList } from "@/ds/components/tabs";
import { AgentListFilters, AgentRequests } from "./agent-lists";
import { Pagination } from "@/ds/components/pagination";
import { AGENT_PAGE_SIZE, type AgentSearch } from "./agent-search";
import { Badge } from "@/ds/components/badge";
import { ReferenceChip } from "./reference-chip";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Card } from "@/ds/components/card";
import {
  HoverCard,
  HoverCardBody,
  HoverCardMeta,
  HoverCardTitle,
} from "@/ds/components/hover-card";
import { CodeBlock } from "@/ds/components/code-block";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import { NameDialog } from "./teams";
import { EmptyState } from "@/ds/components/empty-state";
import { IconButton } from "@/ds/components/icon-button";
import { Menu, MenuContent, MenuTrigger } from "@/ds/components/menu";
import { Table, type TableColumn } from "@/ds/components/table";
import { TextField } from "@/ds/components/text-field";
import { useToast } from "@/ds/components/toast";
import { Page } from "@/ds/components/page";
import { PageHeader, PageBackLink } from "@/ds/components/page-header";
import { focusRing } from "@/ds/lib/focus";
import {
  AgentIcon,
  ShieldIcon,
  ChevronRightIcon,
  EllipsisIcon,
  LockIcon,
  LogIcon,
} from "@/ds/icons";
import { api } from "../api";
import type {
  AgentCredentialState,
  AgentDetail,
  AgentInstanceDetail,
  AgentProvisioner,
  ApprovalRequest,
} from "../types";
import {
  CONNECTION_LABEL,
  CONNECTION_TONE,
  credentialLive,
  credentialSummary,
  detailConnection,
} from "./agent-state";
import { useAccess } from "./authorization";
import { extensionLink, useAgentTabs, useAuditLinks } from "./extensions";
import { formatAbsolute, formatRelative, formatUntil } from "./format";
import { RequestTrend } from "./request-trend";
import { useTitle } from "./use-title";

/* What the agent route loader delivers. A null agent with no error is
 * one that does not exist here. */
export interface AgentPage {
  /* What the edition tab the page opened on loaded, in that tab's own
   * shape; absent on core's tabs. */
  tabData?: unknown;
  catalogue?: AgentCatalogData;
  agent: AgentDetail | null;
  requests: ApprovalRequest[];
  requestsError: string;
  hasMoreRequests: boolean;
  search: AgentSearch;
  error: string;
}

/* One agent: its instances with their credential state, its provisioner
 * tokens, the policy that decides its requests, and what it has asked
 * for recently. Reading needs agent.read; the actions check agent.write,
 * agent.credential.issue, and agent.credential.revoke against the
 * agent itself (its slug), so a grant scoped to one agent opens only
 * that agent. */
export function AgentScreen({ page }: { page: AgentPage }) {
  const toast = useToast();
  const navigate = useNavigate();
  const router = useRouter();
  const { can } = useAccess();
  const auditLinkFor = useAuditLinks();
  const editionTabs = useAgentTabs();
  const { agent, requests, error, search, requestsError, hasMoreRequests } =
    page;
  const auditLink = agent ? auditLinkFor({ actor: agent.slug }) : null;
  const canReadAudit = auditLink?.allowed ?? false;
  const busy = useRouterState({
    select: (state) => state.status === "pending",
  });
  const tab = search.tab ?? "overview";
  const pageIndex = search.page ?? 0;
  const changeSearch = (next: AgentSearch) =>
    void navigate({
      to: "/agents/$agentSlug",
      params: { agentSlug: agent!.slug },
      search: next,
    });
  const [issuingProvisioner, setIssuingProvisioner] = useState(false);
  /* Renaming edits the display name in place of the heading; the slug
   * never changes. */
  const [renaming, setRenaming] = useState(false);
  const [draftName, setDraftName] = useState("");
  const [savingName, setSavingName] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const [confirmingStatus, setConfirmingStatus] = useState(false);
  const [archiving, setArchiving] = useState(false);
  const [revokingCredential, setRevokingCredential] =
    useState<AgentInstanceDetail | null>(null);
  const [renamingInstance, setRenamingInstance] =
    useState<AgentInstanceDetail | null>(null);
  const [revokingProvisioner, setRevokingProvisioner] =
    useState<AgentProvisioner | null>(null);
  /* Secrets shown once, kept only in memory for this visit. */
  const [provisionerToken, setProvisionerToken] = useState("");
  useTitle(agent ? agent.name : "Agent");

  const resource = agent
    ? ({ kind: "agent", id: agent.slug } as const)
    : null;
  const canWrite = resource !== null && can("agent.write", resource);
  const canIssue = resource !== null && can("agent.credential.issue", resource);
  const canRevoke =
    resource !== null && can("agent.credential.revoke", resource);
  /* A personal agent's instances belong to people, so there is no fleet
   * to mint for: no provisioner tokens, no provisioner tab. */
  const personal = agent?.kind === "personal";

  async function act(action: () => Promise<unknown>, failure: string) {
    try {
      await action();
      await router.invalidate();
      return true;
    } catch (reason) {
      toast.add({
        type: "deny",
        title: failure,
        description: reason instanceof Error ? reason.message : undefined,
      });
      return false;
    }
  }

  async function issueProvisioner() {
    if (!agent) return;
    setIssuingProvisioner(true);
    try {
      const result = await api.createAgentProvisioner(agent.slug);
      setProvisionerToken(result.credential.token);
      await navigate({
        to: "/agents/$agentSlug",
        params: { agentSlug: agent.slug },
        search: { tab: "provisioners" },
      });
      await router.invalidate();
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "Could not issue the token",
        description: reason instanceof Error ? reason.message : undefined,
      });
    } finally {
      setIssuingProvisioner(false);
    }
  }

  function startRename() {
    if (!agent) return;
    setDraftName(agent.name);
    setRenaming(true);
  }

  async function rename(event: FormEvent) {
    event.preventDefault();
    if (!agent || savingName) return;
    const next = draftName.trim();
    if (!next || next === agent.name) {
      setRenaming(false);
      return;
    }
    setSavingName(true);
    const ok = await act(() => api.renameAgent(agent.slug, next), "Rename failed");
    setSavingName(false);
    if (ok) {
      setRenaming(false);
      toast.add({
        type: "approve",
        title: "Agent renamed",
        description: `Reviewers now see ${next} on its requests.`,
      });
    }
  }

  /* Restoring brings the agent back to active. Credentials revoked at
   * archive stay revoked, so instances enroll again. */
  async function restore() {
    if (!agent || restoring) return;
    setRestoring(true);
    const ok = await act(() => api.restoreAgent(agent.slug), "Restore failed");
    setRestoring(false);
    if (ok) {
      toast.add({
        type: "approve",
        title: "Agent restored",
        description: "Instances need a new credential to call in.",
      });
    }
  }

  if (!agent) {
    return (
      <Page>
        <PageHeader back={<PageBackLink to="/agents">Agents</PageBackLink>} />
        {error ? (
          <Callout tone="warn">{error}</Callout>
        ) : (
          <EmptyState
            icon={<AgentIcon width={18} height={18} />}
            title="Agent not found"
            description="It may have been created in another organization, or the link is stale."
            action={
              <Button onClick={() => void navigate({ to: "/agents" })}>
                Back to agents
              </Button>
            }
          />
        )}
      </Page>
    );
  }

  const connection = detailConnection(agent);
  const disabled = agent.status === "disabled";
  const archived = agent.status === "archived";
  /* Once archived, nothing about the agent can change. */
  const frozen = archived;
  const provisionerColumns: TableColumn<AgentProvisioner>[] = [
    {
      id: "token",
      label: "Token",
      info: "Token identifier. The full secret is shown only when issued.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/provisioner-tokens#instance-registration", label: "Automatic instance registration" }],
      grow: true,
      cell: (provisioner) => (
        <span className="font-mono text-[12px] text-ink">
          whp_…{provisioner.id.slice(-4)}
        </span>
      ),
    },
    {
      id: "status",
      label: "Status",
      width: 120,
      cell: (provisioner) => {
        if (provisioner.revoked_at) {
          return (
            <Badge title={`Revoked ${formatAbsolute(provisioner.revoked_at)}`}>
              Revoked
            </Badge>
          );
        }
        return new Date(provisioner.expires_at).getTime() <= Date.now() ? (
          <Badge tone="warn">Expired</Badge>
        ) : (
          <Badge tone="approve">Active</Badge>
        );
      },
    },
    {
      id: "issued",
      label: "Issued",
      width: 100,
      cell: (provisioner) => (
        <time
          dateTime={provisioner.issued_at}
          title={formatAbsolute(provisioner.issued_at)}
        >
          {formatRelative(provisioner.issued_at)} ago
        </time>
      ),
    },
    {
      id: "used",
      label: "Last used",
      info: "The last time this token registered a new instance.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/provisioner-tokens#instances-and-token-activity", label: "Automatic instance registration" }],
      width: 100,
      cell: (provisioner) =>
        provisioner.last_used_at ? (
          <time
            dateTime={provisioner.last_used_at}
            title={formatAbsolute(provisioner.last_used_at)}
          >
            {formatRelative(provisioner.last_used_at)} ago
          </time>
        ) : (
          <span className="text-ink-muted">Never</span>
        ),
    },
    {
      id: "expires",
      label: "Expires",
      width: 120,
      cell: (provisioner) => (
        <time
          dateTime={provisioner.expires_at}
          title={formatAbsolute(provisioner.expires_at)}
        >
          {new Date(provisioner.expires_at).getTime() <= Date.now()
            ? `${formatRelative(provisioner.expires_at)} ago`
            : `In ${formatUntil(provisioner.expires_at)}`}
        </time>
      ),
    },
  ];

  const provisionerActions = (provisioner: AgentProvisioner) => [
    {
      id: "revoke",
      label: "Revoke token",
      danger: true,
      disabled: !canRevoke || frozen || !!provisioner.revoked_at,
      action: () => setRevokingProvisioner(provisioner),
    },
  ];

  const instanceColumns: TableColumn<AgentInstanceDetail>[] = [
    {
      id: "instance",
      label: "Instance",
      info: "One running copy of this agent, e.g. a specific agent running on CI, a container, a laptop, etc.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/agents#instance-connections", label: "Connecting agent instances" }],
      grow: true,
      cell: (instance) => (
        <p className="truncate text-body font-medium text-ink">
          {instance.name}
          {instance.status === "disabled" ? (
            <span className="ml-2 text-label font-normal text-ink-faint">
              disabled
            </span>
          ) : null}
        </p>
      ),
    },
    ...(personal
      ? [
          {
            id: "owner",
            label: "Owner",
            info: "The member this instance belongs to. It acts on their behalf and is disabled when they leave the organization.",
            width: 160,
            cell: (instance: AgentInstanceDetail) =>
              instance.owner_display_name ? (
                <ReferenceChip
                  reference={{
                    kind: "person",
                    id: instance.membership_id,
                    name: instance.owner_display_name,
                  }}
                />
              ) : (
                <span className="text-body text-ink-faint">Unknown</span>
              ),
          } satisfies TableColumn<AgentInstanceDetail>,
        ]
      : []),
    {
      id: "seen",
      label: "Last seen",
      info: "The last time this instance made a request to withHuman.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/agents#connection-status-and-requests", label: "Connection status and requests" }],
      width: 90,
      cell: (instance) => (
        <span className="text-body text-ink">
          {formatRelative(instance.last_seen_at)}
        </span>
      ),
    },
    {
      id: "credential",
      label: "Credential",
      info: "Whether the instance's newest credential still lets it in. Hover a state for when it expires.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/agents#credential-revocation", label: "Managing instance credentials" }],
      width: 130,
      cell: (instance) => <CredentialState instance={instance} />,
    },
    {
      id: "requests",
      label: "Requests, 7d",
      info: "Requests made in the last 7 days by the instance.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/agents#connection-status-and-requests", label: "Connection status and requests" }],
      width: 130,
      cell: (instance) => (
        <RequestTrend
          label={instance.name}
          total={instance.recent_request_count}
          trend={instance.request_trend}
        />
      ),
    },
  ];

  const instanceActions = (instance: AgentInstanceDetail) => [
    {
      id: "rename",
      label: "Rename instance",
      disabled: !canWrite || frozen,
      action: () => setRenamingInstance(instance),
    },
    {
      id: "status",
      label:
        instance.status === "disabled" ? "Enable instance" : "Disable instance",
      disabled: !canWrite || frozen,
      action: () =>
        void act(
          () =>
            api.setAgentInstanceStatus(
              instance.id,
              instance.status === "disabled" ? "active" : "disabled",
            ),
          "Status change failed",
        ),
    },
    {
      id: "revoke",
      label: "Revoke credential",
      danger: true,
      disabled:
        !canRevoke ||
        !instance.credentials.some((credential) => !credential.revoked_at),
      action: () => setRevokingCredential(instance),
    },
  ];

  return (
    <Page width="wide">
      <PageHeader back={<PageBackLink to="/agents">Agents</PageBackLink>}>
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="flex min-w-0 flex-col gap-2.5">
            {renaming ? (
              <form
                className="flex flex-wrap items-center gap-2"
                onSubmit={(event) => void rename(event)}
              >
                <TextField
                  aria-label="Agent name"
                  value={draftName}
                  onChange={(event) => setDraftName(event.target.value)}
                  maxLength={120}
                  disabled={savingName}
                  required
                  autoFocus
                  className="w-[280px]"
                />
                <Button
                  type="submit"
                  variant="primary"
                  size="sm"
                  loading={savingName}
                  disabled={!draftName.trim()}
                >
                  Save name
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={savingName}
                  onClick={() => setRenaming(false)}
                >
                  Cancel
                </Button>
              </form>
            ) : (
              <h1 className="truncate text-title-lg text-ink">{agent.name}</h1>
            )}
            <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
              <Badge className="px-0" tone={CONNECTION_TONE[connection]}>
                {CONNECTION_LABEL[connection]}
              </Badge>
              {archived && canWrite ? (
                <Button
                  size="sm"
                  variant="ghost"
                  loading={restoring}
                  onClick={() => void restore()}
                >
                  Restore agent
                </Button>
              ) : null}
              <Badge
                className="px-0"
                icon={<AgentIcon width={14} height={14} />}
              >
                <span className="font-mono text-[12px]">{agent.slug}</span>
              </Badge>
              <Badge tone="neutral">
                {personal ? "Personal agent" : "Service agent"}
              </Badge>
              <div className="flex flex-wrap items-center gap-x-1 text-label font-normal text-ink-muted">
                <span>
                  Created {formatRelative(agent.created_at)} ago
                  {agent.created_by_display_name ? " by" : ""}
                </span>
                {agent.created_by_actor_type === "api_key" ? <span className="font-mono text-label text-ink-muted">{agent.created_by_display_name} (organization key)</span> : agent.created_by_display_name ? (
                  <ReferenceChip
                    reference={{
                      kind: "person",
                      id: agent.created_by_actor_id,
                      name: agent.created_by_display_name,
                    }}
                  />
                ) : null}
              </div>
            </div>
          </div>
          {canWrite || canIssue || canReadAudit ? (
            <div className="flex shrink-0 items-center gap-2">
              {canIssue ? (
                <Button
                  variant="primary"
                  onClick={() =>
                    void navigate({
                      to: "/agents/$agentSlug/instances/new",
                      params: { agentSlug: agent.slug },
                    })
                  }
                  disabled={disabled || frozen}
                >
                  New instance
                </Button>
              ) : null}
              {canWrite || canIssue || canReadAudit ? (
                <Menu
                  actions={[
                    ...(canIssue && !personal
                      ? [
                          {
                            id: "provisioner",
                            label: "Issue provisioner token",
                            disabled: disabled || frozen || issuingProvisioner,
                            action: () => void issueProvisioner(),
                          },
                        ]
                      : []),
                    ...(canReadAudit
                      ? [
                          {
                            id: "audit",
                            label: auditLink?.label ?? "",
                            action: () => {
                              if (auditLink) void navigate(extensionLink(auditLink));
                            },
                          },
                        ]
                      : []),
                    ...(canWrite && !frozen
                      ? [
                          {
                            id: "rename",
                            group: "edit",
                            label: "Rename agent",
                            disabled: renaming,
                            action: startRename,
                          },
                          {
                            id: "status",
                            group: "status",
                            label: disabled ? "Enable agent" : "Disable agent",
                            danger: !disabled,
                            action: () => setConfirmingStatus(true),
                          },
                          {
                            id: "archive",
                            group: "status",
                            label: "Archive agent",
                            danger: true,
                            action: () => setArchiving(true),
                          },
                        ]
                      : []),
                  ]}
                >
                  <MenuTrigger
                    render={
                      <IconButton
                        aria-label={`Manage ${agent.name}`}
                        variant="neutral"
                      >
                        <EllipsisIcon width={15} height={15} />
                      </IconButton>
                    }
                  />
                  <MenuContent align="end" />
                </Menu>
              ) : null}
            </div>
          ) : null}
        </div>
      </PageHeader>

      {archived ? (
        <Callout
          tone="warn"
          title={`Archived ${agent.archived_at ? formatAbsolute(agent.archived_at) : ""}`.trim()}
        >
          Every credential was revoked and nothing can be enrolled under this
          agent while it stays archived. Its instances and requests stay here
          for the record, its name is free for a new agent, and its slug stays
          reserved. Restore it to bring the agent back.
          <p className="mt-2">
            Connected machines will block gated tool calls until this is run on
            each one, with its runtime such as claude-code or codex:
          </p>
          <CodeBlock className="mt-2" code="withhuman agent eject <runtime>" />
        </Callout>
      ) : disabled ? (
        <Callout tone="warn" title="This agent is disabled">
          Its credentials are kept but none of them resolve. Enable it again to
          let every instance back in without re-enrolling.
        </Callout>
      ) : null}

      {provisionerToken && tab !== "provisioners" ? (
        <Callout tone="warn" title="A provisioner token is ready to copy">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => changeSearch({ tab: "provisioners" })}
          >
            Open Provisioner tokens
          </Button>
        </Callout>
      ) : null}
      <Tabs
        value={tab}
        onValueChange={(value) =>
          changeSearch({
            tab: value === "overview" ? undefined : String(value),
          })
        }
      >
        <TabsList aria-label="Agent sections" className="gap-6 overflow-x-auto overflow-y-hidden">
          <Tab value="overview" className="px-0">
            Overview
          </Tab>
          {personal ? null : (
            <Tab value="provisioners" className="px-0">
              Provisioner tokens
            </Tab>
          )}
          <Tab value="instances" className="px-0">
            Instances{" "}
            <span className="text-micro text-ink-muted">
              {agent.instance_count.toLocaleString()}
            </span>
          </Tab>
          <Tab value="requests" className="px-0">
            Approval requests{" "}
            <span className="text-micro text-ink-muted">
              {agent.request_count.toLocaleString()}
            </span>
          </Tab>
          {editionTabs.map((entry) => (
            <Tab key={entry.id} value={entry.id} className="px-0">
              {entry.label}
            </Tab>
          ))}
          <Tab value="tools" className="px-0">Tool Catalogue</Tab>
        </TabsList>
        <TabPanel value="tools" className="space-y-4">
          {page.catalogue ? <AgentToolsPanel key={search.q ?? ""} agentSlug={agent.slug} data={page.catalogue} search={search} onSearch={changeSearch} /> : null}
        </TabPanel>
        {editionTabs.map((entry) => {
          const Content = entry.component;
          return (
            <TabPanel key={entry.id} value={entry.id} className="space-y-4">
              {/* The loader delivers a tab's data only when the page
               * opened on that tab. */}
              {tab === entry.id ? (
                <Content agent={agent} data={page.tabData} search={search} />
              ) : null}
            </TabPanel>
          );
        })}
        <TabPanel value="overview" className="space-y-3">
          <Card>
            <Link
              to="/agents/$agentSlug"
              params={{ agentSlug: agent.slug }}
              search={{ tab: "requests" }}
              className={`flex items-center gap-3 rounded-lg px-5 py-5 transition-colors hover:bg-fill-hover ${focusRing}`}
            >
              <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-fill-hover text-ink-muted">
                <LogIcon width={18} height={18} />
              </span>
              <div className="min-w-0 flex-1">
                <h3 className="text-body font-medium text-ink">
                  Approval requests
                </h3>
                <p className="text-label font-normal text-ink-muted">
                  {agent.recent_request_count.toLocaleString()} in the last 7
                  days. Browse all requests and their outcomes.
                </p>
              </div>
              <span className="text-title-lg tabular-nums text-ink">
                {agent.request_count.toLocaleString()}
              </span>
              <ChevronRightIcon
                width={14}
                height={14}
                className="shrink-0 text-ink-muted"
              />
            </Link>
          </Card>
          {(
            [
              {
                title: "Instances",
                count: agent.instance_count,
                description: `${agent.live_instance_count.toLocaleString()} with a live credential. View runtimes and manage their access.`,
                icon: <AgentIcon width={18} height={18} />,
                search: { tab: "instances" },
              },
              {
                title: "Provisioner tokens",
                count: agent.provisioner_count,
                description:
                  "Find tokens, check expiry dates, and revoke access to instance registration.",
                icon: <LockIcon width={18} height={18} />,
                search: { tab: "provisioners" },
              },
            ] as const
          )
            .filter((item) => !personal || item.search.tab !== "provisioners")
            .map((item) => (
            <Card key={item.title}>
              <Link
                to="/agents/$agentSlug"
                params={{ agentSlug: agent.slug }}
                search={item.search}
                className={`flex items-center gap-3 rounded-lg px-5 py-5 transition-colors hover:bg-fill-hover ${focusRing}`}
              >
                <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-fill-hover text-ink-muted">
                  {item.icon}
                </span>
                <div className="min-w-0 flex-1">
                  <h3 className="text-body font-medium text-ink">
                    {item.title}
                  </h3>
                  <p className="text-label font-normal text-ink-muted">
                    {item.description}
                  </p>
                </div>
                <span className="text-title-lg tabular-nums text-ink">
                  {item.count.toLocaleString()}
                </span>
                <ChevronRightIcon
                  width={14}
                  height={14}
                  className="shrink-0 text-ink-muted"
                />
              </Link>
            </Card>
          ))}
        </TabPanel>
        <TabPanel value="provisioners" className="space-y-4">
          <AgentListFilters
            search={search}
            onChange={changeSearch}
            busy={busy}
          />
          <Card className="overflow-hidden">
            {provisionerToken ? (
              <div className="flex flex-col gap-2 border-b-hairline border-edge px-5 py-4">
                <Callout tone="warn" title="Copy this token now">
                  withHuman stores only its hash; it will not be shown again.
                </Callout>
                <CodeBlock label="provisioner token" code={provisionerToken} />
              </div>
            ) : null}
            {agent.provisioners.length === 0 ? (
              <EmptyState
                title={
                  agent.provisioner_count === 0
                    ? "No provisioner tokens issued"
                    : "No matching tokens"
                }
                description={
                  agent.provisioner_count === 0
                    ? "Issue a provisioner token from the agent menu to let your platform register instances."
                    : "Try another identifier or clear the filters."
                }
              />
            ) : (
              <div className="overflow-x-auto">
                <Table
                  aria-label="Provisioner tokens"
                  items={agent.provisioners}
                  getId={(provisioner) => provisioner.id}
                  selected={new Set()}
                  onSelectedChange={() => undefined}
                  selectable={false}
                  columns={provisionerColumns}
                  rowLabel={(provisioner) =>
                    `token whp_…${provisioner.id.slice(-4)}`
                  }
                  rowActions={provisionerActions}
                  className="min-w-[720px]"
                />
              </div>
            )}
            <Pagination
              pageSize={AGENT_PAGE_SIZE}
              page={pageIndex}
              count={agent.provisioners.length}
              total={agent.matching_provisioner_count}
              hasMore={
                (pageIndex + 1) * AGENT_PAGE_SIZE <
                agent.matching_provisioner_count
              }
              busy={busy}
              onChange={(next) =>
                changeSearch({ ...search, page: next || undefined })
              }
            />
          </Card>
        </TabPanel>
        <TabPanel value="instances" className="space-y-4">
          <section className="space-y-4" aria-label="Instances">
            <AgentListFilters
              search={search}
              onChange={changeSearch}
              busy={busy}
            />
            <Card>
              <div className="overflow-x-auto">
                {agent.instances.length === 0 ? (
                  <EmptyState
                    title={
                      agent.instance_count === 0
                        ? "No instances yet"
                        : "No matching instances"
                    }
                    description={
                      agent.instance_count === 0
                        ? "An instance appears when a runtime enrolls through the CLI, is registered here, or is minted with a provisioner token."
                        : "Try another search or clear the filters."
                    }
                  />
                ) : (
                  <Table
                    aria-label="Agent instances"
                    items={agent.instances}
                    getId={(instance) => instance.id}
                    selected={new Set()}
                    onSelectedChange={() => undefined}
                    selectable={false}
                    columns={instanceColumns}
                    rowLabel={(instance) => instance.name}
                    rowActions={instanceActions}
                    className="min-w-[720px]"
                  />
                )}
              </div>
              <Pagination
                pageSize={AGENT_PAGE_SIZE}
                page={pageIndex}
                count={agent.instances.length}
                total={agent.matching_instance_count}
                hasMore={
                  (pageIndex + 1) * AGENT_PAGE_SIZE <
                  agent.matching_instance_count
                }
                busy={busy}
                onChange={(next) =>
                  changeSearch({ ...search, page: next || undefined })
                }
              />
            </Card>
          </section>
        </TabPanel>
        <TabPanel value="requests" className="space-y-4">
          <AgentListFilters
            search={search}
            onChange={changeSearch}
            busy={busy}
          />
          {requestsError ? (
            <Callout tone="warn" title="Could not load requests">
              <p>{requestsError}</p>
              <Button
                size="sm"
                disabled={busy}
                onClick={() => void router.invalidate()}
              >
                Retry
              </Button>
            </Callout>
          ) : (
            <Card>
              <AgentRequests
                requests={requests}
                filtered={!!(search.q || search.status || pageIndex)}
                onOpen={(request) =>
                  void navigate({
                    to: "/queue/$requestId",
                    params: { requestId: request.id },
                  })
                }
              />
              <Pagination
                pageSize={AGENT_PAGE_SIZE}
                page={pageIndex}
                count={requests.length}
                hasMore={hasMoreRequests}
                busy={busy}
                onChange={(next) =>
                  changeSearch({ ...search, page: next || undefined })
                }
              />
            </Card>
          )}
        </TabPanel>
      </Tabs>

      {canWrite || canIssue || canRevoke ? (
        <>
          <ConfirmDialog
            open={archiving}
            onOpenChange={setArchiving}
            title={`Archive ${agent.name}?`}
            description="All credentials and provisioner tokens are revoked for good, even if you restore the agent. Connected machines will block gated tool calls until this is run on each one, with its runtime such as claude-code or codex:"
            confirmLabel="Archive agent"
            typeToConfirm={agent.name}
            onConfirm={async () => {
              const ok = await act(
                () => api.archiveAgent(agent.slug),
                "Archive failed",
              );
              if (ok) {
                toast.add({
                  title: "Agent archived",
                  description:
                    "Connected machines need the hook removed. The command stays on this page.",
                });
                setArchiving(false);
              }
            }}
          >
            <CodeBlock
              label="On each connected machine"
              code="withhuman agent eject <runtime>"
            />
          </ConfirmDialog>
          <ConfirmDialog
            open={confirmingStatus}
            onOpenChange={setConfirmingStatus}
            title={
              disabled ? `Enable ${agent.name}?` : `Disable ${agent.name}?`
            }
            description={
              disabled
                ? "Every instance with a live credential can open requests again. Nothing needs to re-enroll."
                : "Every instance stops resolving immediately, credentials included. Enabling the agent later lets them back in without re-enrolling."
            }
            confirmLabel={disabled ? "Enable agent" : "Disable agent"}
            tone={disabled ? "approve" : "danger"}
            onConfirm={async () => {
              const ok = await act(
                () =>
                  api.setAgentStatus(
                    agent.slug,
                    disabled ? "active" : "disabled",
                  ),
                "Status change failed",
              );
              if (ok) setConfirmingStatus(false);
            }}
          />
          {renamingInstance ? (
            <NameDialog
              open
              title={`Rename ${renamingInstance.name}`}
              description="The name must be unique within the agent. Requests already made keep the old name."
              confirmLabel="Rename instance"
              initialName={renamingInstance.name}
              onOpenChange={(open) => !open && setRenamingInstance(null)}
              onSubmit={async (name) => {
                const ok = await act(
                  () => api.renameAgentInstance(renamingInstance.id, name),
                  "Rename failed",
                );
                if (ok) setRenamingInstance(null);
              }}
            />
          ) : null}
          {revokingCredential ? (
            <ConfirmDialog
              open
              onOpenChange={(open) => !open && setRevokingCredential(null)}
              title={`Revoke the credential for ${revokingCredential.name}?`}
              description="The instance loses access immediately and cannot get it back: it has to enroll again for a new credential. Other instances are untouched."
              confirmLabel="Revoke credential"
              onConfirm={async () => {
                const credential = revokingCredential.credentials.find(
                  (candidate) => !candidate.revoked_at,
                );
                if (!credential) {
                  setRevokingCredential(null);
                  return;
                }
                const ok = await act(
                  () => api.revokeAgentCredential(credential.id),
                  "Revocation failed",
                );
                if (ok) setRevokingCredential(null);
              }}
            />
          ) : null}
          {revokingProvisioner ? (
            <ConfirmDialog
              open
              onOpenChange={(open) => !open && setRevokingProvisioner(null)}
              title="Revoke this provisioner token?"
              description="The platform holding it can no longer mint instances. Instances it already minted keep their own credentials."
              confirmLabel="Revoke token"
              onConfirm={async () => {
                const ok = await act(
                  () => api.revokeAgentProvisioner(revokingProvisioner.id),
                  "Revocation failed",
                );
                if (ok) setRevokingProvisioner(null);
              }}
            />
          ) : null}
        </>
      ) : null}
    </Page>
  );
}

/* The credential state as a word, with the dates behind it on hover:
 * when it expires or expired, when it was issued, last used, and
 * revoked. An instance that never held a credential has nothing to
 * explain, so its badge stands alone. */
function CredentialState({ instance }: { instance: AgentInstanceDetail }) {
  const summary = credentialSummary(instance);
  const credential: AgentCredentialState | undefined = instance.credentials[0];
  const badge = <Badge tone={summary.tone}>{summary.label}</Badge>;
  if (!credential) return badge;
  const live = credentialLive(credential);
  const expired = !credential.revoked_at && !live;
  return (
    <HoverCard
      content={
        <>
          <HoverCardTitle>
            <span>Newest credential</span>
            {badge}
          </HoverCardTitle>
          <HoverCardMeta>
            {credential.revoked_at
              ? "Revoked by hand. The instance has to enroll again for a new one."
              : expired
                ? "Past its expiry. The instance has to enroll again for a new one."
                : `Lets the instance in for another ${formatUntil(credential.expires_at)}.`}
          </HoverCardMeta>
          <HoverCardBody>
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
              <dt className="text-ink-muted">
                {expired ? "Expired" : "Expires"}
              </dt>
              <dd className="text-ink">
                {formatAbsolute(credential.expires_at)}
                <span className="text-ink-muted">
                  {expired
                    ? ` (${formatRelative(credential.expires_at)} ago)`
                    : ` (in ${formatUntil(credential.expires_at)})`}
                </span>
              </dd>
              <dt className="text-ink-muted">Issued</dt>
              <dd className="text-ink">
                {formatAbsolute(credential.issued_at)}
              </dd>
              <dt className="text-ink-muted">Last used</dt>
              <dd className="text-ink">
                {credential.last_used_at
                  ? formatAbsolute(credential.last_used_at)
                  : "Never"}
              </dd>
              {credential.revoked_at ? (
                <>
                  <dt className="text-ink-muted">Revoked</dt>
                  <dd className="text-ink">
                    {formatAbsolute(credential.revoked_at)}
                  </dd>
                </>
              ) : null}
            </dl>
          </HoverCardBody>
        </>
      }
    >
      {badge}
    </HoverCard>
  );
}

