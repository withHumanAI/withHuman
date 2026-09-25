import { sampleValueSuggestions } from "./request-suggestions";
export { sampleValueSuggestions } from "./request-suggestions";
import { catalogSchemas, loadToolCatalog, toolSourceLabel } from "./tool-catalog";
import type { ToolSchema } from "@/ds/lib/tool-schema";
import type { ConditionNode, ConditionValueDetails, ConditionValueSuggestions } from "@/ds/components/condition-builder";
import { api } from "../api";
import { extensions, type ServerDirectoryEntry } from "./extensions";
import type { AgentSummary, ApprovalRequest, RequestApprovalPipelinePreviewSample } from "../types";
import { argumentPaths, sampleFromRequest } from "./pipeline-blocks";

export function mergeValueSuggestions(...sources: ConditionValueSuggestions[]): ConditionValueSuggestions {
  const result: ConditionValueSuggestions = {};
  for (const source of sources) {
    for (const [path, values] of Object.entries(source)) {
      result[path] = [...new Set([...(result[path] ?? []), ...values])];
    }
  }
  return result;
}

export function conditionValueSuggestions(nodes: ConditionNode[]): ConditionValueSuggestions {
  return mergeValueSuggestions(...nodes.map((node) => {
    if (node.all || node.any) return conditionValueSuggestions(node.all ?? node.any ?? []);
    if (!node.field) return {};
    const values = Array.isArray(node.value) ? node.value : [node.value];
    return { [node.field]: values.filter((value): value is string | number => (typeof value === "string" && value !== "") || typeof value === "number") };
  }));
}

/* The fields saved conditions already compare, in tree order. */
export function conditionFieldPaths(nodes: ConditionNode[]): string[] {
  return nodes.flatMap((node) => {
    if (node.all || node.any) return conditionFieldPaths(node.all ?? node.any ?? []);
    const field = node.field ?? node.some?.field ?? node.every?.field;
    return field ? [field] : [];
  });
}

/* Every argument key the requests carried, most common first; ties keep
 * the order of the newest request that has them. */
export function argumentPathSuggestions(requests: Pick<ApprovalRequest, "arguments">[]): string[] {
  const counts = new Map<string, number>();
  for (const request of requests) {
    for (const path of new Set(argumentPaths(request.arguments))) {
      counts.set(path, (counts.get(path) ?? 0) + 1);
    }
  }
  return [...counts.entries()].sort((a, b) => b[1] - a[1]).map(([path]) => path);
}

/* An agent's own listing is its full history, automatic decisions
 * included, where the queue holds only what reached a person. The
 * fan-out is bounded to the agents heard from most recently, each
 * contributing its newest rows. */
const HISTORY_AGENTS = 25;
const HISTORY_ROWS = 50;

async function loadRecentRequests(agents: AgentSummary[]): Promise<ApprovalRequest[]> {
  /* Suggestions read the whole visible queue, not the reader's own layer:
   * a tool is worth a rule whoever was told about it. */
  if (agents.length === 0) return api.approvals("visible").then((page) => page.requests, () => []);
  const sources = [...agents]
    .sort((a, b) => (b.last_seen_at ?? "").localeCompare(a.last_seen_at ?? ""))
    .slice(0, HISTORY_AGENTS);
  const pages = await Promise.all(sources.map((agent) =>
    api.agentApprovals(agent.slug, { limit: HISTORY_ROWS, offset: 0 }).then((page) => page.requests, () => []),
  ));
  return pages.flat().sort((a, b) => b.created_at.localeCompare(a.created_at));
}

/* The edition's centrally registered servers, when it has any; a
 * refused or failed read is an empty directory. */
function loadServerDirectory(): Promise<ServerDirectoryEntry[]> {
  if (!extensions.serverDirectory) return Promise.resolve([]);
  return extensions.serverDirectory().then((entries) => entries, () => []);
}

/* Optional, permission-scoped reads belong to the route loader. Failure of
 * one directory must not prevent editing or discard the other sources. */
export async function loadPipelineSuggestions(agentSlug: string): Promise<{
  /* The agent whose pipeline this is, when the inventory lists it; empty
   * for the organization scope. */
  sourceAgents: AgentSummary[];
  toolSchemas: ToolSchema[];
  valueSuggestions: ConditionValueSuggestions;
  /* What the editor can say under a suggested server: its name and
   * source, for the edition's registered ones. */
  valueDetails: ConditionValueDetails;
  /* Argument fields the scope's agents have actually sent. */
  argumentPaths: string[];
  /* The newest real request as the test sample, when the scope has one. */
  sample: RequestApprovalPipelinePreviewSample | null;
}> {
  const [inventory, catalogue, directory] = await Promise.all([
    api.agents().then((page) => page.agents, () => []),
    loadToolCatalog(agentSlug),
    loadServerDirectory(),
  ]);
  const agents = inventory.filter((agent) => !agentSlug || agent.slug === agentSlug);
  const requests = (await loadRecentRequests(agents)).filter((request) => !agentSlug || request.agent_slug === agentSlug);
  const tools = [
    ...catalogue.tools.map((tool) => tool.name),
    ...requests.map((request) => request.tool),
  ];
  /* Registered servers first: a rule is usually written before the
   * first call through one. A catalogue source names its server even
   * when discovery found no tools there. */
  const servers = [
    ...directory.map((entry) => entry.server),
    ...catalogue.sources.map((source) => source.server ?? ""),
    ...catalogue.tools.map((tool) => tool.server ?? ""),
    ...requests.map((request) => request.server ?? ""),
  ].filter(Boolean);
  const newest = requests[0];
  return {
    sourceAgents: agentSlug ? agents : [],
    toolSchemas: catalogSchemas(catalogue.tools, requests),
    valueSuggestions: mergeValueSuggestions({
      "/request/tool": tools,
      "/request/server": servers,
      "/agent/slug": agentSlug ? [agentSlug] : agents.map((agent) => agent.slug),
      "/agent/name": agents.map((agent) => agent.name),
    }, ...requests.map((request) => sampleValueSuggestions(JSON.stringify({ request: { arguments: request.arguments, context: request.context } })))),
    valueDetails: {
      "/request/server": Object.fromEntries(directory.map((entry) => [entry.server, `${toolSourceLabel(entry.source)}: ${entry.name}`])),
    },
    argumentPaths: argumentPathSuggestions(requests),
    sample: newest ? sampleFromRequest(newest) : null,
  };
}
