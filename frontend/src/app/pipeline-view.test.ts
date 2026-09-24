import { afterEach, describe, expect, it, vi } from "vitest";
import { api, RequestError } from "../api";
import type { RequestApprovalPipelineRevision } from "../types";
import { loadPipelineView } from "./pipeline-view";
import { pipelineLink, pipelineSearch } from "./pipeline-link";

function revision(number: number, agentSlug = ""): RequestApprovalPipelineRevision {
  return {
    id: `revision-${number}`,
    scope: agentSlug ? "agent" : "organization",
    agent_slug: agentSlug || undefined,
    revision: number,
    is_active: number === 3,
    block_count: 0,
    blocks: [],
    created_by_actor_id: "author",
    created_at: "2026-09-14T10:00:00Z",
  };
}

afterEach(() => vi.restoreAllMocks());

describe("pipeline review links", () => {
  it("preserves the pipeline scope, historical revision, and exact block key", () => {
    const search = pipelineSearch({ revision: "2", block: "production-review" });
    expect(pipelineLink("", search)).toEqual({ to: "/pipelines/organization", search: { revision: 2, block: "production-review" } });
    expect(pipelineLink("release", search)).toEqual({
      to: "/pipelines/agents/$agentSlug",
      params: { agentSlug: "release" },
      search: { revision: 2, block: "production-review" },
    });
  });

  it("rejects invalid revision numbers and non-text block keys", () => {
    for (const revision of [0, -1, "2.5", "abc", true, [], Infinity]) {
      expect(pipelineSearch({ revision, block: {} })).toEqual({});
    }
    expect(pipelineSearch({ edit: "true", block: " " })).toEqual({ edit: true });
  });
});

describe("loading a pipeline from a review link", () => {
  function mockPage() {
    vi.spyOn(api, "requestApprovalPipelineRevisions").mockImplementation(async (agentSlug) => ({
      revisions: [revision(3, agentSlug)],
      total_count: 3,
    }));
    vi.spyOn(api, "activeRequestApprovalPipeline").mockImplementation(async (agentSlug) => revision(3, agentSlug));
    vi.spyOn(api, "escalationPaths").mockResolvedValue({ escalation_paths: [] });
    vi.spyOn(api, "runtimeConfig").mockRejectedValue(new Error("No optional block configuration"));
    vi.spyOn(api, "agents").mockResolvedValue({ agents: [] });
    vi.spyOn(api, "approvals").mockResolvedValue({ requests: [] });
  }

  it.each(["", "release"])("loads the pinned revision separately from the active revision for scope %s", async (agentSlug) => {
    mockPage();
    const readRevision = vi.spyOn(api, "requestApprovalPipelineRevision").mockResolvedValue(revision(2, agentSlug));
    const page = await loadPipelineView(agentSlug, 2);
    expect(readRevision).toHaveBeenCalledWith(agentSlug, 2);
    expect(page.linkedRevision?.revision).toBe(2);
    expect(page.displayed?.revision).toBe(3);
    expect(page.error).toBe("");
  });

  it("reports a missing pinned revision instead of silently showing today's pipeline", async () => {
    mockPage();
    vi.spyOn(api, "requestApprovalPipelineRevision").mockRejectedValue(new RequestError("Not found", 404, "not_found"));
    const page = await loadPipelineView("", 2);
    expect(page.linkedRevision).toBeNull();
    expect(page.displayed).toBeNull();
    expect(page.missing).toBe(false);
    expect(page.error).toBe("Revision 2 could not be found.");
  });
});
