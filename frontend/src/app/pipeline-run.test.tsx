// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import type { RequestApprovalPipelineRevision, StoredRequestApprovalBlock } from "../types";
import { RecordedPipelineRun, type RunSection } from "./pipeline-run";
import { stepKey, type TestRun, type TestStep } from "./pipeline-replay";

beforeEach(() => vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function stored(block: object): StoredRequestApprovalBlock {
  return {
    snapshot_id: "s",
    config_version: 1,
    content_hash: "h",
    timeout_ms: 50,
    max_attempts: 1,
    enabled: true,
    ...block,
  } as unknown as StoredRequestApprovalBlock;
}

function revision(pipelineKey: string, name: string, blocks: StoredRequestApprovalBlock[]): RequestApprovalPipelineRevision {
  return {
    id: `${pipelineKey}-1`,
    pipeline_key: pipelineKey,
    name,
    revision: 1,
    is_active: true,
    block_count: blocks.length,
    created_by_actor_id: "a",
    created_at: "2026-10-26T10:00:00Z",
    blocks,
  };
}

const delegate = stored({
  id: "to-refunds",
  name: "Refund requests",
  type: "delegate",
  config: { when: { all: [{ field: "/request/tool", operator: "equals", value: "refund" }] }, pipeline_key: "refunds" },
});
const denyRest = stored({ id: "deny-rest", name: "Deny the rest", type: "outcome", config: { outcome: "deny" } });
const approveSmall = stored({ id: "small", name: "Approve small refunds", type: "outcome", config: { outcome: "approve" } });

function step(section: string, blockId: string, outcome: TestStep["outcome"]): TestStep {
  return { key: stepKey(section, blockId), blockId, section, outcome, endOfBranch: false, metadata: {} };
}

const run: TestRun = {
  steps: [step("entry", "to-refunds", "enter"), step("refunds", "small", "approve")],
  outcome: "approve",
  reasonCode: "block_outcome",
  terminalKey: stepKey("refunds", "small"),
};

describe("a recorded run", () => {
  it("draws the pipeline a delegate entered inside its well", () => {
    const sections: RunSection[] = [
      { pipelineKey: "entry", revision: 1, document: revision("entry", "Entry", [delegate, denyRest]) },
      { pipelineKey: "refunds", revision: 1, document: revision("refunds", "Refunds", [approveSmall]) },
    ];
    render(<RecordedPipelineRun sections={sections} run={run} />);
    const well = screen.getByRole("group", { name: "Blocks in “Refunds”" });
    const decided = within(well).getByText("Approve small refunds").closest("[data-block-card]") as HTMLElement;
    expect(decided.className).toContain("ring-approve");
    /* The block after the delegate never ran. */
    const skipped = screen.getByText("Deny the rest").closest("[data-block-row]") as HTMLElement;
    expect(skipped.className).toContain("opacity-40");
  });

  it("says so in place of a pipeline the reader cannot view", () => {
    const sections: RunSection[] = [
      { pipelineKey: "entry", revision: 1, document: revision("entry", "Entry", [delegate, denyRest]) },
      { pipelineKey: "refunds", name: "Refunds", revision: 1, document: null },
    ];
    render(<RecordedPipelineRun sections={sections} run={run} />);
    const well = screen.getByRole("group", { name: "Blocks in “Refunds”" });
    expect(within(well).getByText("You can't view this pipeline.")).toBeTruthy();
    expect(screen.queryByText("Approve small refunds")).toBeNull();
  });
});
