import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { extensions, registerExtensions } from "./extensions";
import type { AgentSummary, ApprovalRequest, ToolCatalogEntry } from "../types";
import { argumentPathSuggestions, conditionFieldPaths, conditionValueSuggestions, loadPipelineSuggestions, mergeValueSuggestions, sampleValueSuggestions } from "./pipeline-suggestions";
import { sampleFromRequest, suggestPaths } from "./pipeline-blocks";

afterEach(() => {
  vi.restoreAllMocks();
  extensions.serverDirectory = null;
  extensions.toolSources.length = 0;
});

describe("condition value suggestions", () => {
  it("matches escaped field paths, flattens array choices, and preserves numeric types", () => {
    const sample = JSON.stringify({ request: { arguments: { "a/b~c": ["verified", "priority", "verified"], amount: 900, nested: { empty: null, enabled: true } } } });
    const suggestions = sampleValueSuggestions(sample);
    expect(suggestions).toEqual({ "/request/arguments/a~1b~0c": ["verified", "priority"], "/request/arguments/amount": [900] });
    expect(suggestPaths(sample)).toContain("/request/arguments/a~1b~0c");
    expect(sampleValueSuggestions("{")).toEqual({});
    expect(sampleValueSuggestions("null")).toEqual({});
  });

  it("retains saved choices from nested conditions and merges sources without duplicates", () => {
    const saved = conditionValueSuggestions([{ id: "group", any: [
      { id: "tool", field: "/request/tool", value: ["Bash", "Read"] },
      { id: "nested", all: [{ id: "amount", field: "/request/arguments/amount", value: 900 }] },
    ] }]);
    expect(mergeValueSuggestions(saved, { "/request/tool": ["Read", "Write"] })).toEqual({
      "/request/tool": ["Bash", "Read", "Write"], "/request/arguments/amount": [900],
    });
  });

  const history: Record<string, Partial<ApprovalRequest>[]> = {
    support: [
      { agent_slug: "support", agent_name: "Support agent", tool: "refund", arguments: { currency: "GBP", order: { id: "o-2" } }, created_at: "2026-09-15T10:00:00Z", agent_reasoning: "Duplicate charge." },
      { agent_slug: "support", agent_name: "Support agent", tool: "refund", arguments: { currency: "EUR", note: "late" }, created_at: "2026-09-14T10:00:00Z" },
    ],
    release: [
      { agent_slug: "release", agent_name: "Release agent", tool: "deploy", arguments: { environment: "production" }, created_at: "2026-09-13T10:00:00Z" },
    ],
  };

  function mockDirectory() {
    vi.spyOn(api, "agents").mockResolvedValue({ agents: [
      { slug: "support", name: "Support agent", last_seen_at: "2026-09-15T10:00:00Z" },
      { slug: "release", name: "Release agent", last_seen_at: "2026-09-13T10:00:00Z" },
    ] as AgentSummary[] });
    vi.spyOn(api, "agentApprovals").mockImplementation(async (agentSlug) => ({ requests: (history[agentSlug] ?? []) as ApprovalRequest[] }));
    vi.spyOn(api, "approvals").mockResolvedValue({ requests: [] });
    vi.spyOn(api, "toolCatalog").mockResolvedValue({ tools: [{ id: "merge", name: "merge", agent_slug: "support", runtime: "claude-code", source: "mcp", server: "github", available: true, stale: false, discovery_context: "project", schema_fingerprint: "schema", last_seen_at: "2026-09-22T09:00:00Z", last_attempt_at: "2026-09-22T09:00:00Z", input_schema: { type: "object", properties: { branch: { type: "string" } } } }] as ToolCatalogEntry[], sources: [], total: 1, next_offset: null });
  }

  it("uses scoped catalogues and histories with actual approval-request names", async () => {
    mockDirectory();
    const page = await loadPipelineSuggestions("support");
    expect(page.sourceAgents.map((agent) => agent.slug)).toEqual(["support"]);
    expect(vi.mocked(api.agentApprovals).mock.calls.map(([agentSlug]) => agentSlug)).toEqual(["support"]);
    expect(api.approvals).not.toHaveBeenCalled();
    expect(page.toolSchemas.find((tool) => tool.name === "merge")?.inputSchema).toEqual({ type: "object", properties: { branch: { type: "string" } } });
    expect(page.valueSuggestions).toEqual({
      "/agent/slug": ["support"], "/agent/name": ["Support agent"],
      "/request/tool": ["merge", "refund"],
      "/request/server": ["github"],
      "/request/arguments/currency": ["GBP", "EUR"],
      "/request/arguments/order/id": ["o-2"],
      "/request/arguments/note": ["late"],
    });
    const organization = await loadPipelineSuggestions("");
    expect(organization.valueSuggestions["/agent/slug"]).toEqual(["support", "release"]);
    expect(organization.valueSuggestions["/request/tool"]).toContain("deploy");
    expect(organization.sourceAgents).toEqual([]);
  });

  it("learns argument fields from the histories, most common first, and seeds the sample from the newest request", async () => {
    mockDirectory();
    const page = await loadPipelineSuggestions("");
    expect(page.argumentPaths).toEqual([
      "/request/arguments/currency",
      "/request/arguments/order",
      "/request/arguments/order/id",
      "/request/arguments/note",
      "/request/arguments/environment",
    ]);
    expect(page.sample).toEqual({
      request: { tool: "refund", arguments: { currency: "GBP", order: { id: "o-2" } }, agent_reasoning: "Duplicate charge." },
      agent: { slug: "support", name: "Support agent" },
    });
    const scoped = await loadPipelineSuggestions("release");
    expect(scoped.argumentPaths).toEqual(["/request/arguments/environment"]);
    expect(scoped.sample?.agent).toEqual({ slug: "release", name: "Release agent" });
  });

  it("falls back to the review queue without agents and leaves the sample to the placeholder without requests", async () => {
    mockDirectory();
    vi.mocked(api.agents).mockRejectedValue(new Error("Forbidden"));
    vi.mocked(api.approvals).mockResolvedValue({ requests: history.release as ApprovalRequest[] });
    const page = await loadPipelineSuggestions("");
    expect(api.agentApprovals).not.toHaveBeenCalled();
    expect(page.argumentPaths).toEqual(["/request/arguments/environment"]);
    expect(page.sample?.agent).toEqual({ slug: "release", name: "Release agent" });

    vi.mocked(api.approvals).mockResolvedValue({ requests: [] });
    const empty = await loadPipelineSuggestions("");
    expect(empty.argumentPaths).toEqual([]);
    expect(empty.sample).toBeNull();
  });

  it("keeps available choices when optional directories are forbidden or unavailable", async () => {
    mockDirectory();
    vi.mocked(api.toolCatalog).mockRejectedValue(new Error("Forbidden"));
    const page = await loadPipelineSuggestions("support");
    expect(page.valueSuggestions["/request/tool"]).toEqual(["refund"]);
    expect(page.valueSuggestions["/agent/name"]).toEqual(["Support agent"]);
  });

  it("offers the edition's registered servers first, named by their source, and catalogue sources without tools", async () => {
    mockDirectory();
    vi.mocked(api.toolCatalog).mockResolvedValue({
      tools: [],
      sources: [{ id: "src", agent_slug: "support", runtime: "claude-code", discovery_context: "project", source: "mcp", server: "linear", last_attempt_at: "2026-09-22T09:00:00Z", last_success_at: "2026-09-22T09:00:00Z" }],
      total: 0,
      next_offset: null,
    });
    registerExtensions({
      toolSources: [{ source: "gateway", label: "Gateway", external: true }],
      serverDirectory: async () => [{ server: "github-prod", name: "GitHub production", source: "gateway" }],
    });
    const page = await loadPipelineSuggestions("support");
    expect(page.valueSuggestions["/request/server"]).toEqual(["github-prod", "linear"]);
    expect(page.valueDetails).toEqual({ "/request/server": { "github-prod": "Gateway: GitHub production" } });
  });

  it("keeps the observed servers when the edition's directory refuses the reader", async () => {
    mockDirectory();
    registerExtensions({ serverDirectory: () => Promise.reject(new Error("Forbidden")) });
    const page = await loadPipelineSuggestions("support");
    expect(page.valueSuggestions["/request/server"]).toEqual(["github"]);
    expect(page.valueDetails).toEqual({ "/request/server": {} });
  });
});

describe("argument field learning", () => {
  it("counts a field once per request, escapes pointer tokens, and treats arrays as values", () => {
    expect(argumentPathSuggestions([
      { arguments: { "a/b": 1, tags: ["x", "y"], meta: { meta: 2 } } },
      { arguments: { meta: { meta: 3 } } },
      { arguments: "not an object" },
    ])).toEqual(["/request/arguments/meta", "/request/arguments/meta/meta", "/request/arguments/a~1b", "/request/arguments/tags"]);
  });

  it("collects the fields saved conditions compare, nested groups and array predicates included", () => {
    expect(conditionFieldPaths([{ id: "g", all: [
      { id: "a", field: "/request/tool" },
      { id: "b", any: [{ id: "c", field: "/request/arguments/amount" }, { id: "d", some: { field: "/request/arguments/items", where: { id: "e" } } }] },
      { id: "f" },
    ] }])).toEqual(["/request/tool", "/request/arguments/amount", "/request/arguments/items"]);
  });

  it("builds a sample from a request, dropping context that is not an object", () => {
    expect(sampleFromRequest({ tool: "deploy", arguments: ["odd"], agent_slug: "release", agent_name: "Release", context: "n/a" })).toEqual({
      request: { tool: "deploy", arguments: {} },
      agent: { slug: "release", name: "Release" },
    });
  });
});
