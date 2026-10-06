import { useMemo, useState } from "react";
import { Link, type LinkProps, useNavigate, useRouter } from "@tanstack/react-router";
import { Badge, type Tone } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { CodeBlock } from "@/ds/components/code-block";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import { EmptyState } from "@/ds/components/empty-state";
import { HoverCard, HoverCardBody, HoverCardMeta, HoverCardTitle } from "@/ds/components/hover-card";
import { List, ListRowContent } from "@/ds/components/list";
import { Referenced } from "@/ds/components/reference";
import { SettingsPage } from "@/ds/components/settings-page";
import { SettingsSection } from "@/ds/components/settings-section";
import { Table, type TableColumn } from "@/ds/components/table";
import { Tab, TabPanel, Tabs, TabsList } from "@/ds/components/tabs";
import { TextField } from "@/ds/components/text-field";
import { useToast } from "@/ds/components/toast";
import { ToggleGroup, ToggleGroupItem } from "@/ds/components/toggle-group";
import { AlertIcon, LockIcon } from "@/ds/icons";
import { api } from "../api";
import type { APIKey, MyAPIKeys } from "../types";
import { formatElapsed } from "./format";
import { useSession } from "./principal";
import { useRuntimeConfig } from "./use-runtime-config";
import { useTitle } from "./use-title";

/* The management MCP endpoint lives on the product server itself, so
 * its address is the deployment's public origin plus the protocol path
 * the server registers (internal/httpapi/mcp.go). */
const MCP_PATH = "/api/mcp/v1";

/*
 * Shared inventory UI for separate personal and organization settings routes.
 * The route fixes the credential kind and fetches only that inventory.
 * Personal keys follow their member; organization keys carry their own grants.
 */

/* Personal routes load mine and, for administrators, the personal-key
 * inventory. Organization routes only load the organization-key inventory.
 * A null inventory plus an error represents a failed or unauthorized read. */
export interface APIKeysPage {
  mine: MyAPIKeys | null;
  error: string;
  organization: APIKey[] | null;
  organizationError: string;
}

/* Where the inventory's affordances lead, from the route that fixes
 * the kind: the create page, and, for a kind whose keys have a page of
 * their own (an organization key, with its grants and policies), each
 * key's page. Personal keys have none, so their rows stay put. */
export interface APIKeyLinks {
  create: LinkProps["to"];
  key?: (key: APIKey) => Pick<LinkProps, "to" | "params">;
}

const STATUS_TONE: Record<APIKey["status"], Tone> = {
  active: "approve",
  expired: "neutral",
  revoked: "neutral",
};

const STATUS_LABEL: Record<APIKey["status"], string> = {
  active: "Active",
  expired: "Expired",
  revoked: "Revoked",
};

type StatusFilter = "all" | APIKey["status"];

const NONE: ReadonlySet<string> = new Set();

/* Live keys first, then the ones that ran out, then the revoked, newest
 * first within each: what still works is what the page is for. */
const STATUS_RANK: Record<APIKey["status"], number> = { active: 0, expired: 1, revoked: 2 };

function sortKeys(keys: APIKey[]): APIKey[] {
  return [...keys].sort(
    (left, right) =>
      STATUS_RANK[left.status] - STATUS_RANK[right.status] ||
      right.created_at.localeCompare(left.created_at),
  );
}

/* The search matches the name, the prefix (so a value pasted from a
 * config finds its row), a listed permission, and, on the organization's
 * list, who minted the key. */
function filterKeys(keys: APIKey[], query: string, status: StatusFilter): APIKey[] {
  const needle = query.trim().toLowerCase();
  return sortKeys(keys).filter((key) => {
    if (status !== "all" && key.status !== status) return false;
    if (!needle) return true;
    return (
      key.name.toLowerCase().includes(needle) ||
      key.prefix.toLowerCase().includes(needle) ||
      (key.permissions ?? []).some((permission) => permission.includes(needle)) ||
      key.grants.some((grant) => `${grant.permission} ${grant.scope_kind} ${grant.scope_id ?? ""}`.toLowerCase().includes(needle)) ||
      (key.created_by?.display_name ?? "").toLowerCase().includes(needle) ||
      (key.created_by?.email ?? "").toLowerCase().includes(needle)
    );
  });
}

/* "12 Oct 2026": the date a key stops working, without the time. */
export function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    day: "numeric",
    month: "short",
    year: "numeric",
  }).format(new Date(value));
}

export function APIKeysScreen({
  page,
  canIssue,
  canReadAll,
  canRevokeAny,
  kind,
  links,
}: {
  page: APIKeysPage;
  canIssue: boolean;
  kind: APIKey["kind"];
  canReadAll: boolean;
  canRevokeAny: boolean;
  links: APIKeyLinks;
}) {
  const organizationView = kind === "organization";
  const title = organizationView ? "Organization API keys" : "Personal API keys";
  useTitle(title);
  const router = useRouter();
  const navigate = useNavigate();
  const toast = useToast();
  const session = useSession();
  const held = useMemo(
    () => new Set(session.principal.permissions),
    [session.principal.permissions],
  );
  const { mine, error, organization, organizationError } = page;
  /* Registry descriptions for the hover card's rows, from the bearable
   * vocabulary the list answer carries; a permission the viewer cannot
   * bear (someone else's, in the Everyone view) shows its key alone. */
  const descriptions = useMemo(
    () =>
      new Map(
        (mine?.bearable_permissions ?? []).map((permission) => [permission.key, permission.description]),
      ),
    [mine],
  );
  const allowed = mine?.personal_api_keys_allowed ?? false;
  const canCreate = canIssue && (organizationView || (allowed && mine !== null));

  const [revoking, setRevoking] = useState<{ key: APIKey; own: boolean } | null>(null);
  const [query, setQuery] = useState("");
  /* Live keys are what the page is for, so that is the opening view;
   * expired and revoked ones are a click away. */
  const [status, setStatus] = useState<StatusFilter>("active");

  // Personal-key oversight remains separate from the organization-key inventory.
  const [scope, setScope] = useState<"mine" | "everyone">("mine");
  const everyone = !organizationView && scope === "everyone" && canReadAll;
  const ownKeys = mine?.keys ?? [];
  const organizationKeys = organization ?? [];
  const source = organizationView || everyone ? organizationKeys : ownKeys;
  const visible = useMemo(() => filterKeys(source, query, status), [source, query, status]);
  const countOf = (status: APIKey["status"]) =>
    source.filter((key) => key.status === status).length;
  const searching = query.trim() !== "";

  async function revoke(target: { key: APIKey; own: boolean }) {
    try {
      if (target.own) await api.revokeMyAPIKey(target.key.id);
      else await api.revokeAPIKey(target.key.id);
      toast.add({ type: "approve", title: `Revoked ${target.key.name}` });
      await router.invalidate();
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "Could not revoke the key",
        description: reason instanceof Error ? reason.message : undefined,
      });
    }
  }

  const createButton = (
    <Button variant="primary" onClick={() => void navigate({ to: links.create })}>
      {organizationView ? "Create organization key" : "Create personal key"}
    </Button>
  );

  /* The section's title stays put; what the filters left is said in
   * its description, so the card never re-titles itself. */
  const whose = organizationView ? "Organization-owned API keys" : everyone ? "Every member's API keys" : "Your personal API keys";
  const shown =
    source.length > 0 && visible.length < source.length
      ? `${whose}, showing ${visible.length} of ${source.length}.`
      : `${whose}.`;

  return (
    <SettingsPage
      title={title}
      width={organizationView || everyone ? "default" : "narrow"}
      description={organizationView ? "Give shared integrations their own scoped access." : "Let your scripts act with your permissions."}
      action={canCreate ? createButton : undefined}
    >
      {error ? (
        <Callout tone="warn" title="Unable to load your API keys">
          {error}
        </Callout>
      ) : null}

      {!organizationView && mine && !allowed ? (
        <Callout tone="warn" title="Personal API keys are turned off">
          Calls with existing keys are refused until an administrator turns them back on.
        </Callout>
      ) : null}

      {/* The filter bar belongs to the table under it, closer than the
          page stacks its sections. */}
      <div className="flex flex-col gap-3">
        {source.length > 0 || (!organizationView && canReadAll && organizationKeys.length > 0) ? (
          <div className="flex items-center gap-3">
            <div className="min-w-0 flex-1">
              <TextField
                aria-label="Search keys by name, prefix, or permission"
                placeholder="Search keys by name, prefix, or permission"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
            </div>
            <div className="flex shrink-0 items-center gap-2">
              {!organizationView && canReadAll ? (
                <ToggleGroup
                  aria-label="Whose keys to show"
                  value={scope}
                  onValueChange={(value) => setScope(value as "mine" | "everyone")}
                >
                  <ToggleGroupItem value="mine">Your keys</ToggleGroupItem>
                  <ToggleGroupItem value="everyone">All members</ToggleGroupItem>
                </ToggleGroup>
              ) : null}
              <ToggleGroup
                aria-label="Filter keys by status"
                value={status}
                onValueChange={(value) => setStatus(value as StatusFilter)}
              >
                <ToggleGroupItem value="all">All</ToggleGroupItem>
                <ToggleGroupItem value="active">Active{countOf("active") > 0 ? ` (${countOf("active")})` : ""}</ToggleGroupItem>
                <ToggleGroupItem value="expired">Expired{countOf("expired") > 0 ? ` (${countOf("expired")})` : ""}</ToggleGroupItem>
                <ToggleGroupItem value="revoked">Revoked{countOf("revoked") > 0 ? ` (${countOf("revoked")})` : ""}</ToggleGroupItem>
              </ToggleGroup>
            </div>
          </div>
        ) : null}

        {(organizationView || everyone) && organizationError ? (
          <Callout tone="warn" title="Unable to load the organization's API keys">
            {organizationError}
          </Callout>
        ) : null}

        {(organizationView ? organization !== null : mine !== null) ? (
          <SettingsSection title="Keys" description={shown} inset="none">
            {source.length === 0 ? (
              organizationView || everyone ? (
                <EmptyState
                  icon={<LockIcon width={18} height={18} />}
                  title={organizationView ? "No organization API keys yet" : "No personal API keys in the organization yet"}
                  description={organizationView ? "Create an organization key for a shared integration." : "Nobody has created a personal key."}
                />
              ) : (
                <EmptyState
                  icon={<LockIcon width={18} height={18} />}
                  title="No personal API keys yet"
                  description="Create a key to let a script act as you."
                />
              )
            ) : visible.length === 0 ? (
              <EmptyState
                title={!searching && status === "active" ? "No active keys" : "No keys match"}
                description={
                  !searching && status === "active"
                    ? "Every key here has expired or was revoked. Choose All to see them."
                    : "Try a different search or status filter."
                }
              />
            ) : (
              <div className="overflow-x-auto">
                <KeyTable
                  label={organizationView ? "Organization API keys" : everyone ? "Every personal API key in the organization" : "Your API keys"}
                  keys={visible}
                  held={held}
                  descriptions={descriptions}
                  viewerMembershipId={session.principal.membership_id ?? ""}
                  showCreator={organizationView || everyone}
                  showStatus={true}
                  canRevokeOthers={canRevokeAny}
                  keyLink={links.key}
                  onRevoke={(key, own) => setRevoking({ key, own })}
                />
              </div>
            )}
          </SettingsSection>
        ) : null}
      </div>

      <McpServerSection />

      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(open) => {
          if (!open) setRevoking(null);
        }}
        title={revoking ? `Revoke ${revoking.key.name}?` : "Revoke key?"}
        description={
          revoking?.own
            ? "Scripts using this key stop working immediately. This can't be undone."
            : "Scripts the owner runs with this key stop working immediately. This can't be undone."
        }
        confirmLabel="Revoke key"
        onConfirm={() => (revoking ? revoke(revoking) : undefined)}
      />
    </SettingsPage>
  );
}

/* Personal keys follow their owner's permissions. Organization keys have
 * independent policies; shared inventories show their creator separately.
 * With keyLink, a key's name and its grants open its page. */
function KeyTable({
  label,
  keys,
  held,
  descriptions,
  viewerMembershipId,
  showCreator,
  showStatus,
  canRevokeOthers,
  keyLink,
  onRevoke,
}: {
  label: string;
  keys: APIKey[];
  held: ReadonlySet<string>;
  descriptions: ReadonlyMap<string, string>;
  viewerMembershipId: string;
  showCreator: boolean;
  showStatus: boolean;
  canRevokeOthers: boolean;
  keyLink?: APIKeyLinks["key"];
  onRevoke: (key: APIKey, own: boolean) => void;
}) {
  const isOwn = (key: APIKey) => key.kind === "personal" && key.membership_id === viewerMembershipId;
  const owner = (key: APIKey) =>
    key.created_by?.display_name || key.created_by?.email || "Unknown";
  const columns: TableColumn<APIKey>[] = [
    {
      id: "key",
      label: "Key",
      grow: true,
      cell: (key) => (
        <div className="min-w-0">
          <p className="truncate text-body font-medium text-ink">{keyLink ? <Link {...keyLink(key)}>{key.name}</Link> : key.name}</p>
          <p className="truncate font-mono text-label text-ink-muted">{key.prefix}</p>
          {key.oauth_client ? <p className="mt-1"><Badge tone="neutral">Connected agent</Badge></p> : null}
        </div>
      ),
    },
    ...(showCreator
      ? [{
          id: "created_by",
          label: "Created by",
          width: 144,
          cell: (key: APIKey) => (
            <Referenced
              reference={{ kind: "person", id: key.created_by?.membership_id, name: owner(key) }}
              triggerClassName="min-w-0 max-w-full"
            >
              <span className="block truncate text-label text-ink-secondary">{owner(key)}</span>
            </Referenced>
          ),
        } satisfies TableColumn<APIKey>]
      : []),
    {
      id: "permissions",
      label: "Permissions",
      info: "Personal keys act with their creator's permissions. Organization keys have their own.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/api-keys#key-types", label: "Personal and organization keys" }],
      width: 150,
      cell: (key) => (
        <PermissionsCell apiKey={key} held={held} descriptions={descriptions} own={isOwn(key)} link={keyLink?.(key)} />
      ),
    },
    ...(!showStatus
      ? []
      : [
          {
            id: "status",
            label: "Status",
            width: 96,
            cell: (key: APIKey) => (
              <><Badge tone={STATUS_TONE[key.status]}>{STATUS_LABEL[key.status]}</Badge>
              {key.blocked_reason ? <Badge tone="warn">{key.blocked_reason === "organization_inactive" ? "Organization inactive" : "Disabled by policy"}</Badge> : null}</>
            ),
          } satisfies TableColumn<APIKey>,
        ]),
    {
      id: "expires",
      label: "Expires",
      info: "Never means the key only stops working when it is revoked.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/api-keys#secrets-and-expiry", label: "Secrets and expiry" }],
      width: 104,
      cell: (key) => (
        <span className="text-label text-ink-muted">
          {key.expires_at ? formatDate(key.expires_at) : "Never"}
        </span>
      ),
    },
    {
      id: "last_used",
      label: "Last used",
      info: "The last authenticated call made with this key.",
      docsLinks: [{ href: "https://withhuman.ai/docs/web-app/api-keys#api-access", label: "API access" }],
      width: 96,
      cell: (key) => (
        <span className="text-label text-ink-muted">
          {key.last_used_at ? `${formatElapsed(key.last_used_at)} ago` : "Never"}
        </span>
      ),
    },
  ];

  return (
    <Table
      aria-label={label}
      items={keys}
      getId={(key) => key.id}
      selected={new Set()}
      onSelectedChange={() => undefined}
      selectable={false}
      columns={columns}
      rowLabel={(key) => key.name}
      rowActions={(key) => [
        {
          id: "revoke",
          label: "Revoke",
          danger: true,
          disabled: key.status !== "active" || !(isOwn(key) || canRevokeOthers),
          action: () => onRevoke(key, isOwn(key)),
        },
      ]}
      className={showCreator ? "min-w-[860px]" : "min-w-[600px]"}
    />
  );
}

function PermissionsCell({
  apiKey,
  held,
  descriptions,
  own,
  link,
}: {
  apiKey: APIKey;
  held: ReadonlySet<string>;
  descriptions: ReadonlyMap<string, string>;
  own: boolean;
  /* The key's own page, where its grants are read and edited. */
  link?: Pick<LinkProps, "to" | "params">;
}) {
  if (apiKey.kind === "organization") {
    const grants = `${apiKey.grants.length} scoped grants`;
    return link ? (
      <Link {...link} className="text-label text-ink-secondary hover:text-ink">{grants}</Link>
    ) : (
      <span className="text-label text-ink-secondary">{grants}</span>
    );
  }
  if (apiKey.permissions === null) {
    return (
      <span className="text-label text-ink-muted">
        {own ? "Everything you can do" : "Everything the owner can do"}
      </span>
    );
  }
  const count = apiKey.permissions.length;
  const lost = own ? apiKey.permissions.filter((permission) => !held.has(permission)) : [];
  const narrowed = lost.length > 0;
  return (
    <HoverCard
      triggerClassName="rounded-sm"
      content={
        <>
          <HoverCardTitle>
            <span>
              {count} {count === 1 ? "permission" : "permissions"}
            </span>
            {narrowed ? (
              <Badge tone="neutral" icon={<AlertIcon width={14} height={14} />}>
                Narrowed
              </Badge>
            ) : (
              <Badge tone="neutral">Scoped</Badge>
            )}
          </HoverCardTitle>
          <HoverCardMeta>
            {narrowed
              ? `The key can no longer use ${lost.length === 1 ? "one of these permissions" : `${lost.length} of these permissions`}.`
              : own
                ? "The key can use each of these only while you still hold it."
                : "The key can use each of these only while its owner still holds it."}
          </HoverCardMeta>
          <HoverCardBody>
            <div className="-mx-2 max-h-[240px] overflow-y-auto">
              <List
                items={apiKey.permissions}
                getId={(permission) => permission}
                selected={NONE}
                onSelectedChange={() => undefined}
                selectable={false}
                openButton={false}
                aria-label={`${apiKey.name} permissions`}
              >
                {(permission) => (
                  <ListRowContent
                    title={<span className="font-mono text-[12px]">{permission}</span>}
                    description={
                      own && !held.has(permission)
                        ? "No longer yours"
                        : descriptions.get(permission)
                    }
                  />
                )}
              </List>
            </div>
          </HoverCardBody>
        </>
      }
    >
      <span className="flex flex-wrap items-center gap-1.5">
        <span className="text-label text-ink-muted underline decoration-edge-strong decoration-dotted underline-offset-2">
          {count} {count === 1 ? "permission" : "permissions"}
        </span>
        {narrowed ? (
          <Badge tone="neutral" icon={<AlertIcon width={14} height={14} />}>
            Narrowed
          </Badge>
        ) : null}
      </span>
    </HoverCard>
  );
}

/* Where a coding assistant points its MCP client to manage withHuman's
 * own settings. This is the management MCP server, not how an agent
 * joins withHuman to have its actions approved: that lives on the
 * Agents page. Connecting is an OAuth flow that lands a personal key,
 * named after the assistant, in the list above. */
function McpServerSection() {
  const { config } = useRuntimeConfig();
  const origin = config?.public_origin || (typeof window === "undefined" ? "" : window.location.origin);
  const endpoint = `${origin}${MCP_PATH}`;
  const clientConfig = JSON.stringify({ mcpServers: { withhuman: { url: endpoint } } }, null, 2);
  return (
    <SettingsSection
      title="withHuman MCP server"
      inset="none"
      description="Let a coding assistant set up approval pipelines, escalation paths, teams and webhook endpoints for you."
    >
      <div className="flex flex-col gap-3 px-4 py-3">
        <CodeBlock label="MCP endpoint" code={endpoint} />
        <Tabs defaultValue="claude-code">
          <TabsList aria-label="MCP clients">
            <Tab value="claude-code">Claude Code</Tab>
            <Tab value="codex">Codex</Tab>
            <Tab value="cursor">Cursor</Tab>
            <Tab value="other">Other clients</Tab>
          </TabsList>
          <TabPanel value="claude-code" className="flex flex-col gap-2">
            <CodeBlock code={`claude mcp add --transport http withhuman ${endpoint}`} wrap />
            <p className="text-micro text-ink-faint">
              Claude Code opens the sign-in in your browser the first time it uses a withHuman
              tool.
            </p>
          </TabPanel>
          <TabPanel value="codex" className="flex flex-col gap-2">
            <CodeBlock code={`codex mcp add withhuman --url ${endpoint}`} wrap />
            <p className="text-micro text-ink-faint">
              Codex opens the sign-in when you add the server.
            </p>
          </TabPanel>
          <TabPanel value="cursor" className="flex flex-col gap-2">
            <CodeBlock label="~/.cursor/mcp.json" code={clientConfig} />
            <p className="text-micro text-ink-faint">
              Add this, then turn the server on under MCP in Cursor's settings. Cursor opens the
              sign-in in your browser.
            </p>
          </TabPanel>
          <TabPanel value="other" className="flex flex-col gap-2">
            <CodeBlock label="MCP server settings" code={clientConfig} />
            <p className="text-micro text-ink-faint">
              Add a server named withhuman with the address above. The client opens the sign-in in
              your browser the first time it reaches withHuman.
            </p>
          </TabPanel>
        </Tabs>
        <p className="text-micro text-ink-faint">
          Full setup for each client is in the{" "}
          <a
            href="https://withhuman.ai/docs/web-app/mcp-server"
            target="_blank"
            rel="noopener noreferrer"
            className="text-ink-muted underline-offset-[3px] hover:underline"
          >
            withHuman MCP server guide
          </a>
          .
        </p>
      </div>
    </SettingsSection>
  );
}
