// Package blockruntime is the execution contract of a request approval
// block: what a block sees, what it answers, and how an edition registers
// a block kind's runtime on the pipeline. Core's own kinds and an
// edition's (the hosted AI judge) compile through the same contract, so
// the pipeline executor, the preview, and the audit trail treat them
// alike. The grammar side of a kind (its configuration and limits) is
// approvalblock.RegisterKind; this package is the runtime side.
package blockruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/approvalblock"
	"github.com/withHumanAI/withHuman/domain"
)

// Outcome is what a block decided for the request.
type Outcome string

const (
	Next    Outcome = "next"
	Approve Outcome = "approve"
	Deny    Outcome = "deny"
	Human   Outcome = "human"
	// Enter is evidence, never a pipeline outcome: a branch whose
	// condition matched, so the executor runs the branch's blocks next.
	Enter Outcome = "enter"
)

// ErrorCode is the safe reason a block handed a request to a human. The
// audit trail keeps the code alone; the underlying error goes to the
// operator log.
type ErrorCode string

const (
	InvalidInput  ErrorCode = "invalid_input"
	Evaluation    ErrorCode = "evaluation_error"
	CostLimit     ErrorCode = "cost_limit_exceeded"
	ResourceLimit ErrorCode = "resource_limit_exceeded"
	Timeout       ErrorCode = "timeout"
	InvalidOutput ErrorCode = "invalid_block_output"
	Internal      ErrorCode = "internal_error"
	// Provider is an external model or service failure the attempt
	// budget did not absorb.
	Provider ErrorCode = "provider_error"
	// Webhook delivery failures, by the endpoint's status or transport error.
	EndpointUnreachable ErrorCode = "endpoint_unreachable"
	EndpointError       ErrorCode = "endpoint_error"
	EndpointRejected    ErrorCode = "endpoint_rejected"
	OutcomeNotAllowed   ErrorCode = "outcome_not_allowed"
	EndpointUnavailable ErrorCode = "endpoint_unavailable"
)

// Block is the side-effect-free execution contract shared by block
// runtimes. Evaluate may return an error for internal diagnostics; the
// pipeline's execution boundary turns every error, panic, or ambiguous
// result into human review.
type Block interface {
	Evaluate(context.Context, Input) (Result, error)
}

// Input is what a block sees of one evaluation.
type Input struct {
	// OrganizationID scopes usage metering and endpoint lookups; blocks
	// never use it to read request state.
	OrganizationID domain.ID
	// EvaluationID correlates a delivery with the pipeline run; empty in
	// preview.
	EvaluationID domain.ID
	// Preview marks a management-time evaluation so external calls can be
	// flagged as such and never bind to a request.
	Preview bool
	// PipelineKey is the pipeline the block being evaluated belongs to,
	// so a block's external side effects can be keyed apart from a block
	// of the same key in another pipeline of the same run.
	PipelineKey      string
	Request          Request
	Agent            Agent
	PreviousMetadata map[string]json.RawMessage
	Budget           *Budget
}

// Request is the canonical request as blocks see it. Tool and Arguments
// are the ground truth; AgentReasoning and Context are untrusted or
// unmodeled and only reach block types that opt into them.
type Request struct {
	// ID, CreatedAt, and DeadlineAt identify the request to external
	// services; all three are empty in preview.
	ID   domain.ID
	Tool string
	// Server is the MCP server that defines the tool; empty for built-ins.
	Server         string
	Arguments      json.RawMessage
	AgentReasoning string
	Context        json.RawMessage
	CreatedAt      time.Time
	DeadlineAt     time.Time
}

// Agent is the requesting agent as blocks see it.
type Agent struct {
	Slug string
	Name string
}

// Result is a block's answer.
type Result struct {
	Outcome   Outcome
	Reason    string
	Metadata  map[string]json.RawMessage
	Evidence  Evidence
	ErrorCode ErrorCode
	// Attempts is how many provider calls the block made; zero means one.
	Attempts int
}

// Evidence is the safe record of how a block reached its answer.
type Evidence struct {
	Cost  uint64
	Trace []NodeTrace
	// Delivery is present for webhook blocks: what was called and what
	// came back, never the bodies or the secret.
	Delivery *DeliveryEvidence
}

// DeliveryEvidence is the safe record of one webhook block's delivery.
// Reason is the endpoint's own explanation, bounded, recorded because the
// reviewer needs it; it never reaches the decision note.
type DeliveryEvidence struct {
	EndpointKey      string `json:"endpoint_key"`
	EndpointRevision int64  `json:"endpoint_revision,omitempty"`
	EndpointHost     string `json:"endpoint_host"`
	HTTPStatus       int    `json:"http_status,omitempty"`
	DurationMS       int64  `json:"duration_ms"`
	Reason           string `json:"reason,omitempty"`
}

// NodeTrace intentionally contains no observed or configured values.
type NodeTrace struct {
	NodeID  string `json:"node_id"`
	Matched bool   `json:"matched"`
	Error   bool   `json:"error"`
}

// Budget is shared by every block evaluating one request. It enforces
// request-wide per-kind limits without exposing pipeline state to a block.
type Budget struct {
	mu     sync.Mutex
	counts map[approvalblock.BlockType]uint32
}

// Consume counts one more block of the kind against its per-request
// limit; the limit exceeded is an error the block reports as a resource
// limit. A nil budget (a bare evaluation in a test) counts nothing.
func (budget *Budget) Consume(kind approvalblock.BlockType, limit uint32) error {
	if budget == nil {
		return nil
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.counts == nil {
		budget.counts = make(map[approvalblock.BlockType]uint32)
	}
	budget.counts[kind]++
	if budget.counts[kind] > limit {
		return fmt.Errorf("%s block count exceeded", kind)
	}
	return nil
}

// executionError carries the safe code of a failed evaluation alongside
// the diagnostic error.
type executionError struct {
	code ErrorCode
	err  error
}

func (failure *executionError) Error() string { return string(failure.code) }
func (failure *executionError) Unwrap() error { return failure.err }

// ExecutionError is how a block reports a failure with its safe code: the
// execution boundary records the code and hands the request to a human.
func ExecutionError(code ErrorCode, err error) error {
	return &executionError{code: code, err: err}
}

// ErrorCodeOf reads the safe code a block reported, if the error carries
// one.
func ErrorCodeOf(err error) (ErrorCode, bool) {
	var failure *executionError
	if errors.As(err, &failure) {
		return failure.code, true
	}
	return "", false
}

// MaximumEditionAsynchronousBudget bounds what an edition's asynchronous
// kinds may take, together, in one request's resumed segment: the worst
// case of every such block at its maximum count, per-attempt timeout,
// and attempts. The request workflow's resume activity is budgeted from
// this constant, so a kind whose limits exceed it would be abandoned
// mid-run rather than finish.
const MaximumEditionAsynchronousBudget = 16 * time.Minute

// Runtime is one block kind's execution, registered on the pipeline's
// block registry by the composition root through
// runtime.ServerOptions.ApprovalBlocks and WorkerOptions.ApprovalBlocks.
type Runtime struct {
	Type approvalblock.BlockType
	// ConfigVersion stamps stored snapshots; a process that cannot
	// interpret a stored version refuses the revision rather than guessing.
	ConfigVersion int
	// Fingerprint covers the runtime's semantics, limits, and
	// dependencies; it joins the compiled cache key so replicas never mix
	// meanings.
	Fingerprint string
	// Asynchronous runtimes take seconds or longer and call external
	// services, so the request path suspends in front of them and a
	// worker runs them.
	Asynchronous bool
	// Capability gates the kind on the organization's plan; empty means
	// every plan may use it.
	Capability application.Capability
	// ErrorCodes are the kind's own safe codes beyond the shared ones.
	ErrorCodes []ErrorCode
	// Compile turns a validated definition into its executor. A
	// dependency the deployment lacks is reported as *Unconfigured so the
	// document is refused for what it is, not as invalid.
	Compile func(approvalblock.Definition) (Block, error)
	// Problem, when set, reports what stands in the way of running the
	// definition right now: the problem code a revision's references list
	// carries, and the refusal creating or activating the revision
	// answers with. Both empty means the block can run.
	Problem func(approvalblock.Definition) (code string, refusal error)
	// Grammar is the kind's section of the pipeline_blocks reference the
	// management MCP server serves, in Markdown.
	Grammar string
}

// Unconfigured means the deployment lacks what the kind needs to run at
// all (no model catalog, no signing key). The API answers 501 with the
// code; the revision cannot be created or activated until it is.
type Unconfigured struct {
	Code string
}

func (u *Unconfigured) Error() string { return u.Code }

// Refused means the definition names a dependency this deployment does
// not offer (a model the catalog dropped). The API answers 400 with the
// code.
type Refused struct {
	Code    string
	Message string
}

func (r *Refused) Error() string {
	if r.Message != "" {
		return r.Message
	}
	return r.Code
}
