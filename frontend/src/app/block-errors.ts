import { extensions } from "./extensions";

/* What a block's error code means to a reader. A failed block never
 * decides: the request goes to a human, and the reader must see that
 * the block broke rather than take the human exit for a match. The
 * codes are the pipeline's BlockErrorCode values; a kind an edition
 * adds labels the codes only it reports through its registry entry. */
const BLOCK_ERROR_LABEL: Record<string, string> = {
  invalid_input: "the request could not be read",
  evaluation_error: "a condition could not be evaluated",
  cost_limit_exceeded: "the conditions cost too much to evaluate",
  resource_limit_exceeded: "the conditions exceeded a resource limit",
  timeout: "the block timed out",
  invalid_block_output: "the block gave an invalid answer",
  internal_error: "the block failed internally",
  provider_error: "the model provider failed",
  endpoint_unreachable: "the endpoint could not be reached",
  endpoint_error: "the endpoint failed",
  endpoint_rejected: "the endpoint rejected the delivery",
  outcome_not_allowed: "the endpoint's outcome is not allowed",
  endpoint_unavailable: "the endpoint is unavailable",
};

/* Why a revision's block cannot run right now, from
 * PipelineReferences.blocks: core's codes, or a kind's own. */
const BLOCK_PROBLEM_LABEL: Record<string, string> = {
  plan: "not available on this plan",
  webhook_unconfigured: "webhook delivery is not configured",
};

/* Core's label, else the first registered kind's, else the code as words. */
function labelled(code: string, own: Record<string, string>, table: "errorLabels" | "problemLabels"): string {
  const fromKind = extensions.blockKinds.map((kind) => kind[table][code]).find((label) => label !== undefined);
  return own[code] ?? fromKind ?? code.replace(/_/g, " ");
}

export function blockErrorLabel(code: string): string {
  return labelled(code, BLOCK_ERROR_LABEL, "errorLabels");
}

export function blockProblemLabel(code: string): string {
  return labelled(code, BLOCK_PROBLEM_LABEL, "problemLabels");
}
