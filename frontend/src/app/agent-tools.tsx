import { useState } from "react";
import { useRouter } from "@tanstack/react-router";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Card } from "@/ds/components/card";
import { CodeBlock } from "@/ds/components/code-block";
import { EmptyState } from "@/ds/components/empty-state";
import { TextField } from "@/ds/components/text-field";
import { Select, SelectTrigger, SelectContent, SelectItem } from "@/ds/components/select";
import { Table, type TableColumn } from "@/ds/components/table";
import { schemaFields, type SchemaField } from "@/ds/lib/tool-schema";
import type { ToolCatalogEntry } from "../types";
import { api } from "../api";
import { externalToolSources, toolSourceLabel, toolSources, type AgentCatalogData } from "./tool-catalog";
import { Pagination } from "@/ds/components/pagination";
import { AGENT_PAGE_SIZE, type AgentSearch } from "./agent-search";
import { formatAbsolute, formatRelative } from "./format";
import { AgentToolServers } from "./agent-tool-servers";

const ALL_SOURCES = "All sources";
const statusOptions = ["All statuses", "available", "unavailable", "stale"];

/* One label as it is, or "A and B" when an edition adds more than one. */
function listLabels(labels: string[]): string {
  if (labels.length <= 1) return labels.join("");
  return `${labels.slice(0, -1).join(", ")} and ${labels[labels.length - 1]}`;
}

export function AgentToolsPanel({ agentSlug, data, search, onSearch }: { agentSlug: string; data: AgentCatalogData; search: AgentSearch; onSearch: (search: AgentSearch) => void }) {
  const router = useRouter();
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState("");
  const [query, setQuery] = useState(search.q ?? "");
  /* The edition's centrally discovered sources, whose tools an agent is
   * granted rather than reports, and which a refresh rediscovers. */
  const external = externalToolSources();
  const externalLabel = listLabels(external.map((entry) => entry.label));
  const isExternal = (source: string) => external.some((entry) => entry.source === source);
  /* The filter offers every source this bundle knows, plus the one the
   * URL names when no bundle knows it, shown as it is. */
  const knownSources = toolSources().map((entry) => entry.source);
  const sourceOptions = [ALL_SOURCES, ...knownSources, ...(search.source && !knownSources.includes(search.source) ? [search.source] : [])];
  const sourceOptionLabel = (option: string) => (option === ALL_SOURCES ? option : toolSourceLabel(option));
  function change(next: Partial<AgentSearch>) { onSearch({ ...search, tab: "tools", page: undefined, tool: undefined, ...next }); }
  async function refresh() {
    setRefreshing(true); setRefreshError("");
    try { await api.toolCatalog({ agent_slug: agentSlug, refresh: true, limit: 1 }); await router.invalidate(); }
    catch { setRefreshError(`${externalLabel} tools could not be refreshed.`); }
    finally { setRefreshing(false); }
  }
  const columns: TableColumn<ToolCatalogEntry>[] = [
    {
      id: "tool", grow: true, label: "Tool",
      info: "The tool name sent in approval requests.",
      cell: (tool) => <><span className="block font-mono text-[12px]">{tool.name}</span><span className="line-clamp-2 text-micro text-ink-muted">{tool.description || tool.display_name || "No description supplied"}</span></>,
    },
    {
      id: "source", width: 160, label: "Source",
      info: "The runtime or server that supplied this tool.",
      cell: (tool) => <><span className="block text-body">{tool.server || tool.runtime}</span><span className="text-micro text-ink-muted">{tool.instance_name || toolSourceLabel(tool.source)}</span></>,
    },
    {
      id: "availability", width: 150, label: "Availability",
      info: <>
        <p><strong>Reported</strong> means a local instance discovered the tool.</p>
        {external.length > 0 ? <p><strong>Granted</strong> means it is available through the agent's {externalLabel} access.</p> : null}
        <p><strong>Unavailable</strong> means it was removed or access ended.</p>
        <p><strong>Stale</strong> means the last refresh failed.</p>
      </>,
      cell: (tool) => <div className="flex flex-wrap gap-1"><Badge tone={tool.available ? "neutral" : "warn"}>{tool.available ? isExternal(tool.source) ? "Granted" : "Reported" : "Unavailable"}</Badge>{tool.stale ? <Badge tone="warn">Stale</Badge> : null}</div>,
    },
    {
      id: "seen", width: 150, label: "Last discovered",
      info: "When this tool was last discovered.",
      cell: (tool) => <span title={formatAbsolute(tool.last_seen_at)} className="text-micro text-ink-muted">{formatRelative(tool.last_seen_at)}</span>,
    },
  ];
  const selected = data.selected;
  return <>
    <p className="text-body text-ink-muted">Tools this agent's instances reported{external.length > 0 ? ` or can reach through ${externalLabel}` : ""}.</p>
    {data.error ? <Callout tone="warn" title="Catalogue unavailable">{data.error}</Callout> : null}
    <AgentToolServers agentSlug={agentSlug} servers={data.servers} refreshing={refreshing} refreshError={refreshError} onRefresh={() => void refresh()} onShowTools={(server) => { setQuery(server.name); change({ q: server.name, source: server.source }); }} />
    <form className="flex flex-wrap gap-2" onSubmit={(event) => { event.preventDefault(); change({ q: query || undefined }); }}>
      <TextField aria-label="Search tools" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search tools" className="min-w-48 flex-1" />
      <Button type="submit" variant="neutral">Search</Button>
      <Select items={sourceOptions} value={search.source ?? ALL_SOURCES} itemToStringLabel={(item) => sourceOptionLabel(item as string)} onValueChange={(value) => change({ source: typeof value === "string" && value !== ALL_SOURCES && sourceOptions.includes(value) ? value : undefined })}><SelectTrigger aria-label="Tool source" /><SelectContent<string>>{(value) => <SelectItem value={value}>{sourceOptionLabel(value)}</SelectItem>}</SelectContent></Select>
      <Select items={statusOptions} value={search.status ?? statusOptions[0]} onValueChange={(value) => change({ status: value !== statusOptions[0] ? value as AgentSearch["status"] : undefined })}><SelectTrigger aria-label="Tool availability" /><SelectContent<string>>{(value) => <SelectItem value={value}>{value}</SelectItem>}</SelectContent></Select>
    </form>
    {!selected ? (
      <Card className="overflow-hidden">
        {data.tools.length === 0 ? (
          <EmptyState
            title="No matching tools"
            description="Try another search or clear the filters."
          />
        ) : (
          <div className="overflow-x-auto">
            <Table<ToolCatalogEntry>
              aria-label="Agent tools"
              selectable={false}
              selected={new Set()}
              onSelectedChange={() => {}}
              onOpen={(tool) => change({ tool: tool.id, page: search.page })}
              columns={columns}
              items={data.tools}
              getId={(tool) => tool.id}
              rowLabel={(tool) => tool.name}
              className="min-w-[720px]"
            />
          </div>
        )}
        <Pagination
          pageSize={AGENT_PAGE_SIZE}
          page={search.page ?? 0}
          count={data.tools.length}
          total={data.total}
          hasMore={data.next_offset !== null}
          busy={refreshing}
          onChange={(page) => change({ page: page || undefined })}
        />
      </Card>
    ) : null}
    {selected ? <Card className="space-y-4 p-5">
      <div className="flex items-center justify-between"><h3 className="font-mono text-body">{selected.name}</h3><Button size="sm" variant="ghost" onClick={() => change({ tool: undefined, page: search.page })}>Back to tools</Button></div>
      <p className="text-body text-ink-muted">{selected.description || "No description supplied."}</p>
      <p className="text-micro text-ink-muted">{selected.server || selected.runtime} · {selected.instance_name || toolSourceLabel(selected.source)} · Last discovered {formatAbsolute(selected.last_seen_at)}</p>
      {selected.input_schema ? <>
        <Table<SchemaField> aria-label="Tool arguments" selectable={false} selected={new Set()} onSelectedChange={() => {}} columns={[
          { id: "path", width: 200, label: "Argument", info: "An input field declared by the tool.", cell: (field) => <span className="font-mono text-[12px]">{field.path.replace("/request/arguments/", "").split("/").map((part) => part.replace(/~1/g, "/").replace(/~0/g, "~")).join(".")}</span> },
          { id: "type", width: 150, label: "Type", info: "The value types the tool declares, and whether the field is required.", cell: (field) => <span>{field.types.join(" | ") || "See schema"}{field.required ? " · Required" : ""}</span> },
          { id: "description", grow: true, label: "Description", info: "The field's description and allowed values.", cell: (field) => <span>{field.description}{field.values.length ? ` Allowed values: ${field.values.map((value) => JSON.stringify(value)).join(", ")}` : ""}</span> },
        ]} items={schemaFields(selected.input_schema)} getId={(field) => field.path} />
        <details><summary className="cursor-pointer text-label">Full argument schema</summary><CodeBlock code={JSON.stringify(selected.input_schema, null, 2)} label="JSON Schema" /></details>
      </> : <p className="text-body text-ink-muted">Argument schema unavailable.</p>}
    </Card> : null}
    <section className="space-y-3" aria-labelledby="refresh-tools-heading">
      <h2 id="refresh-tools-heading" className="text-body font-medium text-ink">Refresh tools</h2>
      {external.length > 0 ? (
        <Card className="space-y-4 p-5">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="space-y-1">
              <h3 className="text-label">{externalLabel} tools</h3>
              <p className="text-body text-ink-muted">Discover the latest tools this agent can reach through {externalLabel}.</p>
            </div>
            <Button size="sm" variant="neutral" disabled={refreshing} onClick={() => void refresh()}>{refreshing ? "Refreshing…" : `Refresh ${externalLabel} tools`}</Button>
          </div>
          {refreshError ? <Callout tone="warn" title="Refresh unavailable">{refreshError}</Callout> : null}
        </Card>
      ) : null}
      <Card className="space-y-3 p-5">
        <h3 className="text-label">Local tools</h3>
        <p className="text-body text-ink-muted">Run this on the instance's machine, from the project folder. Replace &lt;runtime&gt; with the runtime, such as claude-code or codex.</p>
        <CodeBlock code={`withhuman agent refresh-tools <runtime> --agent ${agentSlug}`} />
      </Card>
    </section>
  </>;
}
