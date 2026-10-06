import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Card, CardDescription, CardHeader, CardTitle } from "@/ds/components/card";
import { CodeBlock } from "@/ds/components/code-block";
import { Table, type TableColumn } from "@/ds/components/table";
import { extensionLink, extensions } from "./extensions";
import { formatAbsolute, formatRelative } from "./format";
import { toolSourceLabel, type CatalogServer } from "./tool-catalog";

/* One label as it is, or "A and B" for more. */
function listNames(names: string[]): string {
  if (names.length <= 1) return names.join("");
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

/* Where a server's tools come from, in the reader's words. */
function origin(server: CatalogServer): string {
  if (server.external) return `Through ${toolSourceLabel(server.source)}`;
  const kind = server.source === "builtin" ? "Built-in tools" : "";
  const where = server.instances.length ? `On ${listNames(server.instances)}` : "";
  return [kind, where].filter(Boolean).join(" · ");
}

/*
 * The servers an agent's tools come from, one row each however many
 * instances and projects report them. A server that could not list its
 * tools sorts first and opens into the reason and the fix; the rest stay
 * folded behind one line until asked for.
 */
export function AgentToolServers({ agentSlug, servers, refreshing, refreshError, onRefresh, onShowTools }: {
  agentSlug: string;
  servers: CatalogServer[];
  refreshing: boolean;
  refreshError: string;
  onRefresh: () => void;
  onShowTools: (server: CatalogServer) => void;
}) {
  const navigate = useNavigate();
  const failing = servers.filter((server) => server.failures.length > 0);
  const [showAll, setShowAll] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  if (servers.length === 0) return null;
  const visible = showAll ? servers : failing;
  const summary = failing.length
    ? `${failing.length} of ${servers.length} ${servers.length === 1 ? "server" : "servers"} couldn't list ${failing.length === 1 ? "its" : "their"} tools.`
    : `${servers.length === 1 ? "The server" : `All ${servers.length} servers`} listed ${servers.length === 1 ? "its" : "their"} tools.`;
  const columns: TableColumn<CatalogServer>[] = [
    {
      id: "server", grow: true, label: "Server",
      cell: (server) => <><span className="block font-mono text-[12px]">{server.name}</span><span className="text-micro text-ink-muted">{origin(server)}</span></>,
    },
    { id: "tools", width: 80, label: "Tools", cell: (server) => <span className="text-body">{server.tools || "None"}</span> },
    {
      id: "status", width: 170, label: "Status",
      info: <>
        <p><strong>Listed</strong> means the last attempt to list its tools worked.</p>
        <p><strong>Couldn't list tools</strong> means the last attempt failed on at least one instance.</p>
      </>,
      cell: (server) => <Badge tone={server.failures.length ? "warn" : "neutral"}>{server.failures.length ? "Couldn't list tools" : "Listed"}</Badge>,
    },
    {
      id: "listed", width: 130, label: "Last listed",
      cell: (server) => server.lastSuccessAt
        ? <span title={formatAbsolute(server.lastSuccessAt)} className="text-micro text-ink-muted">{formatRelative(server.lastSuccessAt)} ago</span>
        : <span className="text-micro text-ink-muted">Never</span>,
    },
  ];
  return (
    <Card className="overflow-hidden">
      <CardHeader className="items-center">
        <div>
          <CardTitle>Servers</CardTitle>
          <CardDescription>{summary}</CardDescription>
        </div>
        {failing.length < servers.length ? (
          <Button size="sm" variant="ghost" onClick={() => setShowAll(!showAll)}>
            {showAll ? (failing.length ? "Show problems only" : "Hide servers") : `Show all ${servers.length}`}
          </Button>
        ) : null}
      </CardHeader>
      {visible.length ? (
        <div className="overflow-x-auto">
          <Table<CatalogServer>
            aria-label="Tool servers"
            selectable={false}
            selected={new Set()}
            onSelectedChange={() => {}}
            openLabel="Details"
            expandedId={expanded}
            onOpen={(server) => setExpanded((current) => (current === server.id ? null : server.id))}
            renderExpanded={(server) => <ServerDetail agentSlug={agentSlug} server={server} refreshing={refreshing} refreshError={refreshError} onRefresh={onRefresh} onShowTools={onShowTools} onOpenSettings={(path) => void navigate(extensionLink({ to: path }))} />}
            columns={columns}
            items={visible}
            getId={(server) => server.id}
            rowLabel={(server) => server.name}
            className="min-w-[640px]"
          />
        </div>
      ) : null}
    </Card>
  );
}

function ServerDetail({ agentSlug, server, refreshing, refreshError, onRefresh, onShowTools, onOpenSettings }: {
  agentSlug: string;
  server: CatalogServer;
  refreshing: boolean;
  refreshError: string;
  onRefresh: () => void;
  onShowTools: (server: CatalogServer) => void;
  onOpenSettings: (path: string) => void;
}) {
  const settingsPath = extensions.toolSources.find((entry) => entry.source === server.source)?.serverPath?.(server.name);
  const failedOn = [...new Set(server.failures.map((failure) => failure.instance).filter((name): name is string => !!name))];
  const runtimes = [...new Set(server.failures.map((failure) => failure.runtime))];
  const showTools = server.tools > 0 ? <Button size="sm" variant="ghost" onClick={() => onShowTools(server)}>Show its tools</Button> : null;
  if (!server.failures.length) {
    return (
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border-hairline border-edge bg-canvas px-3 py-2">
        <p className="text-body text-ink-secondary">{server.tools} {server.tools === 1 ? "tool" : "tools"} listed{server.lastSuccessAt ? ` ${formatRelative(server.lastSuccessAt)} ago` : ""}.</p>
        {showTools}
      </div>
    );
  }
  return (
    <div className="space-y-3 rounded-md border-hairline border-edge bg-canvas px-3 py-3">
      <ul className="space-y-1.5">
        {server.failures.map((failure) => (
          <li key={`${failure.instance ?? ""}:${failure.error}`} className="text-body text-ink-secondary">
            {failure.instance ? <span className="text-ink">On {failure.instance}: </span> : null}
            {failure.error}{" "}
            <span title={formatAbsolute(failure.lastAttemptAt)} className="text-ink-muted">Tried {formatRelative(failure.lastAttemptAt)} ago.</span>
          </li>
        ))}
      </ul>
      {server.external ? (
        <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="neutral" loading={refreshing} onClick={onRefresh}>Try again</Button>
          {settingsPath ? <Button size="sm" variant="ghost" onClick={() => onOpenSettings(settingsPath)}>Open server settings</Button> : null}
          {showTools}
          {refreshError ? <p className="basis-full text-body text-ink-muted">{refreshError}</p> : null}
        </div>
      ) : (
        <>
          <p className="text-body text-ink-muted">Once it's fixed, run this on {listNames(failedOn) || "the instance's machine"} from the project that uses the server:</p>
          <CodeBlock code={runtimes.map((runtime) => `withhuman agent refresh-tools ${runtime} --agent ${agentSlug}`).join("\n")} />
          {showTools ? <div>{showTools}</div> : null}
        </>
      )}
    </div>
  );
}
