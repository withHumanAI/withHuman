package approvalpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/approvalblock"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
)

// ManagementService owns the administrative control plane for request
// approval pipelines. It compiles documents before every write that could
// make them durable or active.
type ManagementService struct {
	repository application.RequestApprovalPipelineRepository
	compiled   *CompiledPipelineState
	paths      application.EscalationPathResolver
	pipelines  application.PipelineKeyResolver
	clock      application.Clock
}

// ManagementOption supplies an optional collaborator.
type ManagementOption func(*ManagementService)

// WithEscalationPathResolver checks that every escalation path a document
// names (block paths and the pipeline default) exists before the document
// becomes durable, and reports each path's state in References. Without
// it path keys are accepted unchecked at creation; the repository still
// refuses activating a revision whose path has no active revision.
func WithEscalationPathResolver(resolver application.EscalationPathResolver) ManagementOption {
	return func(service *ManagementService) { service.paths = resolver }
}

// WithPipelineKeyResolver checks that every pipeline a document delegates
// to exists before the document becomes durable, and reports each
// target's state in References. Without it delegate targets are accepted
// unchecked at creation; the repository still refuses activating a
// revision whose target has no active revision, and the executor still
// hands a request to a person when a target cannot be resolved.
func WithPipelineKeyResolver(resolver application.PipelineKeyResolver) ManagementOption {
	return func(service *ManagementService) { service.pipelines = resolver }
}

func NewManagementService(repository application.RequestApprovalPipelineRepository, compiled *CompiledPipelineState, clock application.Clock, options ...ManagementOption) *ManagementService {
	service := &ManagementService{repository: repository, compiled: compiled, clock: clock}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

type PipelineRevision struct {
	Revision domain.RequestApprovalPipelineRevision
	Blocks   []PipelineBlock
}

// PipelineBlock is one stored block. A branch's definition carries its
// blocks as definitions, and Blocks carries them with their own snapshot
// metadata.
type PipelineBlock struct {
	SnapshotID    domain.ID
	ConfigVersion int
	ContentHash   string
	Definition    approvalblock.Definition
	Blocks        []PipelineBlock
}

type PreviewSample struct {
	Request BlockRequest
	Agent   BlockAgent
	// PreviousMetadata seeds what earlier blocks returned, keyed
	// "<pipeline_key>/<block_key>", exactly as the executor namespaces it.
	PreviousMetadata map[string]json.RawMessage
}

type PreviewResult struct {
	Outcome          BlockOutcome
	ReasonCode       string
	TerminalBlockKey string
	Blocks           []PreviewBlockResult
	// EscalationPath is the path a human outcome would be handed to: the
	// terminal block's, else the default of the pipeline the request was
	// in, else the defaults of the pipelines that delegated into it.
	// EscalationPathReason names which, and EscalationPathPipelineKey the
	// pipeline that supplied it; no_escalation means the queue decides.
	EscalationPath            string
	EscalationPathReason      string
	EscalationPathPipelineKey string
	// AskAgentOwner reports that the route asks the agent's owner, with
	// EscalationPath as the fallback. A preview has no instance, so it
	// names the route, never a person.
	AskAgentOwner bool
}

// Route is where a human outcome goes: an escalation path, or the agent's
// owner with that path as the fallback for a request without one. The
// zero value names no path and inherits the pipeline default.
type Route struct {
	EscalationPath string
	AskAgentOwner  bool
}

// PreviewBlockResult is one step of a preview, in execution order. A branch
// or delegate appears when its condition is evaluated (BlockEnter or
// BlockNext) and, if nothing inside it decided, once more at its end with
// EndOfBranch set. PipelineKey names the pipeline the block belongs to: the
// previewed document's key, or a delegated pipeline's.
type PreviewBlockResult struct {
	BlockKey    string
	PipelineKey string
	Position    int
	Outcome     BlockOutcome
	Reason      string
	Metadata    map[string]json.RawMessage
	Evidence    BlockEvidence
	ErrorCode   BlockErrorCode
	EndOfBranch bool
}

// List is the pipelines index, narrowed to the pipelines the caller holds
// pipeline.read at. Status leaves archived pipelines out unless it says
// otherwise.
func (service *ManagementService) List(ctx context.Context, principal domain.Principal, status application.PipelineStatus, limit int) ([]domain.RequestApprovalPipelineSummary, error) {
	if err := requirePipelineMember(principal); err != nil {
		return nil, err
	}
	scope := authorization.Scope(principal, domain.PermissionPipelineRead)
	if scope.Empty() {
		return nil, authorization.Denied{Permission: domain.PermissionPipelineRead, Resource: domain.OrganizationResource()}
	}
	return service.repository.ListRequestApprovalPipelineSummaries(ctx, application.ListRequestApprovalPipelineSummariesInput{
		OrganizationID: principal.OrganizationID,
		Scope:          scope,
		Status:         status,
		Limit:          limit,
	})
}

func (service *ManagementService) GetActive(ctx context.Context, principal domain.Principal, pipelineKey string) (PipelineRevision, error) {
	if err := authorizePipelineManagement(principal, domain.PermissionPipelineRead, pipelineKey); err != nil {
		return PipelineRevision{}, err
	}
	revision, err := service.repository.GetActiveRequestApprovalPipelineRevision(ctx, application.GetActiveRequestApprovalPipelineRevisionInput{
		OrganizationID: principal.OrganizationID,
		PipelineKey:    pipelineKey,
	})
	if err != nil {
		return PipelineRevision{}, err
	}
	return service.loadRevisionBlocks(ctx, revision)
}

// ListRevisions returns one page of the pipeline's history, newest first;
// cursor is the previous page's NextCursor, empty for the first page.
func (service *ManagementService) ListRevisions(ctx context.Context, principal domain.Principal, pipelineKey string, limit int, cursor string) (application.RevisionPage[domain.RequestApprovalPipelineRevision], error) {
	var page application.RevisionPage[domain.RequestApprovalPipelineRevision]
	if err := authorizePipelineManagement(principal, domain.PermissionPipelineRead, pipelineKey); err != nil {
		return page, err
	}
	return service.repository.ListRequestApprovalPipelineRevisions(ctx, application.ListRequestApprovalPipelineRevisionsInput{
		OrganizationID: principal.OrganizationID,
		PipelineKey:    pipelineKey,
		Limit:          limit,
		Cursor:         cursor,
	})
}

func (service *ManagementService) GetRevision(ctx context.Context, principal domain.Principal, pipelineKey string, revision int64) (PipelineRevision, error) {
	if err := authorizePipelineManagement(principal, domain.PermissionPipelineRead, pipelineKey); err != nil {
		return PipelineRevision{}, err
	}
	stored, err := service.repository.GetRequestApprovalPipelineRevision(ctx, application.GetRequestApprovalPipelineRevisionInput{
		OrganizationID: principal.OrganizationID,
		PipelineKey:    pipelineKey,
		Revision:       revision,
	})
	if err != nil {
		return PipelineRevision{}, err
	}
	return service.loadRevisionBlocks(ctx, stored)
}

// Uses lists the active pipeline revisions whose delegate blocks name the
// pipeline: what archiving it would have to refuse.
func (service *ManagementService) Uses(ctx context.Context, principal domain.Principal, pipelineKey string) ([]domain.PipelineUse, error) {
	if err := authorizePipelineManagement(principal, domain.PermissionPipelineRead, pipelineKey); err != nil {
		return nil, err
	}
	return service.repository.ListRequestApprovalPipelineUses(ctx, application.ListRequestApprovalPipelineUsesInput{
		OrganizationID: principal.OrganizationID, PipelineKey: pipelineKey,
	})
}

// Create brings a pipeline into being with its first revision, active at
// once. Creating a pipeline is an organization-wide act: the caller holds
// pipeline.write at the organization, since no narrower scope exists yet.
func (service *ManagementService) Create(ctx context.Context, principal domain.Principal, pipelineKey, name string, definitions []approvalblock.Definition, defaultRoute Route) (PipelineRevision, error) {
	if err := requirePipelineMember(principal); err != nil {
		return PipelineRevision{}, err
	}
	if err := authorization.Check(principal, domain.PermissionPipelineWrite, domain.OrganizationResource()); err != nil {
		return PipelineRevision{}, err
	}
	if err := validatePipelineKey(pipelineKey); err != nil {
		return PipelineRevision{}, err
	}
	if pipelineKey == domain.EntryPipelineKey {
		return PipelineRevision{}, domain.ErrRequestApprovalPipelineEntry
	}
	blocks, err := service.prepareDocument(ctx, principal.OrganizationID, pipelineKey, definitions, defaultRoute)
	if err != nil {
		return PipelineRevision{}, err
	}
	revision, err := service.repository.CreateRequestApprovalPipeline(ctx, application.CreateRequestApprovalPipelineInput{
		ID:                    domain.ID(uuid.NewString()),
		OrganizationID:        principal.OrganizationID,
		PipelineKey:           pipelineKey,
		Name:                  name,
		Blocks:                blocks,
		DefaultEscalationPath: defaultRoute.EscalationPath,
		DefaultAskAgentOwner:  defaultRoute.AskAgentOwner,
		CreatedByActorID:      principal.ManagementActorID(),
		Now:                   service.clock.Now(),
	})
	if err != nil {
		return PipelineRevision{}, err
	}
	return service.loadRevisionBlocks(ctx, revision)
}

// CreateRevision appends an inactive revision to a live pipeline. An empty
// name keeps the active revision's.
func (service *ManagementService) CreateRevision(ctx context.Context, principal domain.Principal, pipelineKey, name string, definitions []approvalblock.Definition, defaultRoute Route) (PipelineRevision, error) {
	if err := authorizePipelineManagement(principal, domain.PermissionPipelineWrite, pipelineKey); err != nil {
		return PipelineRevision{}, err
	}
	if strings.TrimSpace(name) == "" {
		active, err := service.repository.GetActiveRequestApprovalPipelineRevision(ctx, application.GetActiveRequestApprovalPipelineRevisionInput{
			OrganizationID: principal.OrganizationID, PipelineKey: pipelineKey,
		})
		if err != nil {
			return PipelineRevision{}, err
		}
		name = active.Name
	}
	blocks, err := service.prepareDocument(ctx, principal.OrganizationID, pipelineKey, definitions, defaultRoute)
	if err != nil {
		return PipelineRevision{}, err
	}
	revision, err := service.repository.CreateRequestApprovalPipelineRevision(ctx, application.CreateRequestApprovalPipelineRevisionInput{
		ID:                    domain.ID(uuid.NewString()),
		OrganizationID:        principal.OrganizationID,
		PipelineKey:           pipelineKey,
		Name:                  name,
		Blocks:                blocks,
		DefaultEscalationPath: defaultRoute.EscalationPath,
		DefaultAskAgentOwner:  defaultRoute.AskAgentOwner,
		CreatedByActorID:      principal.ManagementActorID(),
		Now:                   service.clock.Now(),
	})
	if err != nil {
		return PipelineRevision{}, err
	}
	return service.loadRevisionBlocks(ctx, revision)
}

// prepareDocument compiles a document and checks everything it references
// exists, then turns it into snapshot inputs.
func (service *ManagementService) prepareDocument(ctx context.Context, organizationID domain.ID, pipelineKey string, definitions []approvalblock.Definition, defaultRoute Route) ([]application.RequestApprovalBlockSnapshotInput, error) {
	if err := approvalblock.ValidateRoute(defaultRoute.EscalationPath, defaultRoute.AskAgentOwner); err != nil {
		return nil, invalidPipelineDocument("default route is invalid", err)
	}
	if _, err := service.compiled.registry.compileDefinitions(definitions); err != nil {
		return nil, err
	}
	all := flattenDefinitions(definitions)
	if err := service.requireBlockDependencies(ctx, organizationID, all); err != nil {
		return nil, err
	}
	if err := service.requireEscalationPaths(ctx, organizationID, all, defaultRoute.EscalationPath); err != nil {
		return nil, err
	}
	if err := service.requireDelegateTargets(ctx, organizationID, pipelineKey, all); err != nil {
		return nil, err
	}
	return service.compiled.registry.snapshotInputs(definitions)
}

func (service *ManagementService) Activate(ctx context.Context, principal domain.Principal, pipelineKey string, revision int64, expectedActiveRevision int64) (PipelineRevision, error) {
	if err := authorizePipelineManagement(principal, domain.PermissionPipelineActivate, pipelineKey); err != nil {
		return PipelineRevision{}, err
	}
	target, err := service.GetRevision(ctx, principal, pipelineKey, revision)
	if err != nil {
		return PipelineRevision{}, err
	}
	if err := service.compiled.Refresh(ctx, target.Revision); err != nil {
		return PipelineRevision{}, err
	}
	if err := service.requireBlockDependencies(ctx, principal.OrganizationID, flattenDefinitions(pipelineDefinitions(target.Blocks))); err != nil {
		return PipelineRevision{}, err
	}
	// Escalation paths, webhook endpoints, and delegated pipelines are
	// checked by the repository inside the activating transaction, under
	// the same locks an archive takes, so the answer cannot go stale
	// before the switch; the delegation graph is checked there too.
	activated, err := service.repository.ActivateRequestApprovalPipelineRevision(ctx, application.ActivateRequestApprovalPipelineRevisionInput{
		OrganizationID:         principal.OrganizationID,
		PipelineKey:            pipelineKey,
		Revision:               revision,
		ExpectedActiveRevision: expectedActiveRevision,
		ActorID:                principal.ManagementActorID(),
		Now:                    service.clock.Now(),
	})
	if err != nil {
		return PipelineRevision{}, err
	}
	target.Revision = activated
	return target, nil
}

// Archive ends a pipeline: every revision is stamped and the active one
// deactivated. The entry pipeline and a pipeline an active revision
// delegates to refuse.
func (service *ManagementService) Archive(ctx context.Context, principal domain.Principal, pipelineKey string) ([]domain.RequestApprovalPipelineRevision, error) {
	if err := authorizePipelineManagement(principal, domain.PermissionPipelineActivate, pipelineKey); err != nil {
		return nil, err
	}
	return service.repository.ArchiveRequestApprovalPipeline(ctx, application.ArchiveRequestApprovalPipelineInput{
		OrganizationID: principal.OrganizationID, PipelineKey: pipelineKey,
		ActorID: principal.ManagementActorID(), Now: service.clock.Now(),
	})
}

// Preview executes a transient document for pipelineKey against a sample
// without persisting anything. Delegate blocks expand to their targets'
// current active revisions, so the preview walks what a request would; a
// skipped delegate calls nothing.
func (service *ManagementService) Preview(ctx context.Context, principal domain.Principal, pipelineKey string, definitions []approvalblock.Definition, defaultRoute Route, sample PreviewSample) (PreviewResult, error) {
	if err := authorizePipelineManagement(principal, domain.PermissionPipelineWrite, pipelineKey); err != nil {
		return PreviewResult{}, err
	}
	if err := validatePreviewSample(sample); err != nil {
		return PreviewResult{}, err
	}
	compiled, err := service.compiled.registry.compileDefinitions(definitions)
	if err != nil {
		return PreviewResult{}, err
	}
	if err := service.requireBlockDependencies(ctx, principal.OrganizationID, flattenDefinitions(definitions)); err != nil {
		return PreviewResult{}, err
	}
	if err := approvalblock.ValidateRoute(defaultRoute.EscalationPath, defaultRoute.AskAgentOwner); err != nil {
		return PreviewResult{}, invalidPipelineDocument("default route is invalid", err)
	}
	root := &compiledPipeline{
		Revision:  domain.RequestApprovalPipelineRevision{OrganizationID: principal.OrganizationID, PipelineKey: pipelineKey, DefaultEscalationPath: defaultRoute.EscalationPath, DefaultAskAgentOwner: defaultRoute.AskAgentOwner},
		Blocks:    compiled,
		Snapshots: previewSnapshots(definitions),
		Delegates: delegateKeys(compiled),
	}
	effective, err := service.compiled.Effective(ctx, root, func(ctx context.Context, key string) (domain.RequestApprovalPipelineRevision, error) {
		return service.repository.GetActiveRequestApprovalPipelineRevision(ctx, application.GetActiveRequestApprovalPipelineRevisionInput{
			OrganizationID: principal.OrganizationID, PipelineKey: key,
		})
	})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return PreviewResult{}, fmt.Errorf("%w or has no active revision", domain.ErrRequestApprovalPipelineNotFound)
		}
		if ctx.Err() != nil {
			return PreviewResult{}, ctx.Err()
		}
		return PreviewResult{}, invalidPipelineDocument("effective pipeline cannot be built", err)
	}

	result := PreviewResult{Outcome: BlockHuman, ReasonCode: "end_of_pipeline", Blocks: make([]PreviewBlockResult, 0, len(effective.Blocks))}
	previous := make(map[string]json.RawMessage, len(sample.PreviousMetadata))
	for key, value := range sample.PreviousMetadata {
		previous[key] = value
	}
	input := RequestApprovalBlockInput{OrganizationID: principal.OrganizationID, Preview: true, Request: sample.Request, Agent: sample.Agent, PreviousMetadata: previous, Budget: &BlockExecutionBudget{}}
	var terminal terminalBlock
	route, routeFrom := Route{}, 0
	cursor := newBranchCursor(effective.Blocks, 0)
walk:
	for {
		step, index := cursor.next()
		if step == stepDone {
			break
		}
		block, snapshot := effective.Blocks[index], effective.Snapshots[index]
		if step == stepBranchEnd {
			// Nothing inside the entered branch or delegated pipeline
			// decided: a human is asked, and nothing after it runs.
			result.Blocks = append(result.Blocks, PreviewBlockResult{
				BlockKey: block.Key, PipelineKey: effective.keyAt(index), Position: index, Outcome: BlockHuman, Reason: block.Reason, EndOfBranch: true,
			})
			result.Outcome, result.ReasonCode, result.TerminalBlockKey = BlockHuman, "end_of_branch", block.Key
			terminal = effective.terminalAt(snapshot, index)
			if block.Delegate {
				routeFrom = effective.target(index)
			} else {
				route, routeFrom = block.route(), effective.Owner[index]
			}
			break
		}
		input.PipelineKey = effective.keyAt(index)
		var evaluated RequestApprovalBlockResult
		if block.container() {
			evaluated = executeBranchCondition(ctx, block.Executor, input)
		} else {
			evaluated = ExecuteRequestApprovalBlock(ctx, block.Executor, input)
		}
		result.Blocks = append(result.Blocks, PreviewBlockResult{
			BlockKey: block.Key, PipelineKey: effective.keyAt(index), Position: index, Outcome: evaluated.Outcome,
			Reason: evaluated.Reason, Metadata: evaluated.Metadata, Evidence: evaluated.Evidence,
			ErrorCode: evaluated.ErrorCode,
		})
		switch {
		case block.container() && evaluated.Outcome == BlockEnter:
			cursor.enter()
			continue
		case block.container() && evaluated.Outcome == BlockNext:
			cursor.skip()
			continue
		case evaluated.Outcome == BlockNext:
			if len(evaluated.Metadata) > 0 {
				encoded, marshalErr := json.Marshal(evaluated.Metadata)
				if marshalErr != nil {
					return PreviewResult{}, marshalErr
				}
				input.PreviousMetadata[effective.keyAt(index)+"/"+block.Key] = encoded
			}
			cursor.advance()
			continue
		}
		result.Outcome = evaluated.Outcome
		result.ReasonCode = completionReason(evaluated)
		result.TerminalBlockKey = block.Key
		terminal = effective.terminalAt(snapshot, index)
		routeFrom = effective.Owner[index]
		if !block.container() {
			// A branch or delegate whose condition failed keeps the
			// default: a branch's own path is for its end only.
			route = block.route()
		}
		break walk
	}
	if result.Outcome != BlockHuman {
		return result, nil
	}
	resolved, section := routeSection(effective, terminal, route, routeFrom)
	result.EscalationPath, result.EscalationPathReason, result.AskAgentOwner = resolved.PathKey, resolved.Reason, resolved.AskAgentOwner
	if section >= 0 {
		result.EscalationPathPipelineKey = effective.Sections[section].Key
	}
	return result, nil
}

// previewSnapshots stands in for stored snapshots when a transient
// document is executed: only the fields the walk reads.
func previewSnapshots(definitions []approvalblock.Definition) []domain.RequestApprovalBlock {
	all := flattenDefinitions(definitions)
	snapshots := make([]domain.RequestApprovalBlock, len(all))
	for index, definition := range all {
		snapshots[index] = domain.RequestApprovalBlock{BlockKey: definition.ID, Name: definition.Name, BlockType: string(definition.Type)}
	}
	return snapshots
}

// requireDelegateTargets checks that every pipeline the document delegates
// to exists and is live, and that none is the document's own pipeline. A
// revision may be created against a target whose graph position would
// refuse activation; the repository checks the graph when activating.
func (service *ManagementService) requireDelegateTargets(ctx context.Context, organizationID domain.ID, pipelineKey string, definitions []approvalblock.Definition) error {
	keys := delegateTargetKeys(definitions)
	if len(keys) == 0 {
		return nil
	}
	if slices.Contains(keys, pipelineKey) {
		return invalidPipelineDocument("a pipeline cannot delegate to itself", nil)
	}
	if service.pipelines == nil {
		return nil
	}
	resolved, err := service.pipelines.ResolvePipelineKeys(ctx, application.ResolvePipelineKeysInput{
		OrganizationID: organizationID, PipelineKeys: keys,
	})
	if err != nil {
		return err
	}
	if len(resolved.Missing) > 0 {
		return fmt.Errorf("%w: %s", domain.ErrRequestApprovalPipelineNotFound, strings.Join(resolved.Missing, ", "))
	}
	return nil
}

// delegateTargetKeys lists the distinct pipeline keys the delegate blocks
// name, in block order.
func delegateTargetKeys(definitions []approvalblock.Definition) []string {
	keys := make([]string, 0)
	for _, definition := range definitions {
		if definition.Type != approvalblock.TypeDelegate || definition.Delegate == nil {
			continue
		}
		if !slices.Contains(keys, definition.Delegate.PipelineKey) {
			keys = append(keys, definition.Delegate.PipelineKey)
		}
	}
	return keys
}

func validOptionalEscalationPath(key string) error {
	if key != "" && !approvalblock.ValidEscalationPathKey(key) {
		return invalidPipelineDocument("default_escalation_path must match ^[a-z0-9][a-z0-9._-]{0,62}$", nil)
	}
	return nil
}

// requireEscalationPaths checks that every escalation path the document
// names exists. A pipeline revision may be created against a path that is
// not active yet; the repository refuses activating it until the path is.
func (service *ManagementService) requireEscalationPaths(ctx context.Context, organizationID domain.ID, definitions []approvalblock.Definition, defaultEscalationPath string) error {
	if err := validOptionalEscalationPath(defaultEscalationPath); err != nil {
		return err
	}
	if service.paths == nil {
		return nil
	}
	keys := escalationPathKeys(definitions, defaultEscalationPath)
	if len(keys) == 0 {
		return nil
	}
	resolved, err := service.paths.ResolveEscalationPaths(ctx, application.ResolveEscalationPathsInput{
		OrganizationID: organizationID, PathKeys: keys,
	})
	if err != nil {
		return err
	}
	if len(resolved.Missing) > 0 {
		return invalidPipelineDocument("escalation path not found: "+strings.Join(resolved.Missing, ", "), nil)
	}
	return nil
}

// escalationPathKeys lists the distinct path keys a document names, block
// paths first in block order, then the default.
func escalationPathKeys(definitions []approvalblock.Definition, defaultEscalationPath string) []string {
	seen := make(map[string]struct{})
	keys := make([]string, 0)
	add := func(key string) {
		if key == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for _, definition := range definitions {
		add(definition.EscalationPath())
	}
	add(defaultEscalationPath)
	return keys
}

// RevisionReferences is the live state of everything one immutable
// revision points at outside itself, so an administrator sees what a
// rollback needs before attempting it. Activatable is false while any
// entry would refuse activation.
type RevisionReferences struct {
	EscalationPaths  []EscalationPathReference
	WebhookEndpoints []WebhookEndpointReference
	Pipelines        []PipelineReference
	Blocks           []BlockReference
	Activatable      bool
}

// PipelineReference is one pipeline the revision's delegate blocks hand
// requests to. Exists is false when the pipeline has no live revision
// (never created, or archived); Active when a revision is active, in
// which case Name is that revision's name.
type PipelineReference struct {
	PipelineKey string
	Name        string
	BlockKeys   []string
	Exists      bool
	Active      bool
}

// EscalationPathReference is one path the revision names. BlockKeys are
// the blocks naming it; Default reports the revision names it as its
// default. Exists is false when no revision of the path exists; Active
// when a revision is active.
type EscalationPathReference struct {
	PathKey   string
	BlockKeys []string
	Default   bool
	Exists    bool
	Active    bool
}

// WebhookEndpointReference is one endpoint key the revision's webhook
// blocks post to. Exists is false when no revision of the key exists;
// Active when a revision is active, in which case Name is that
// revision's name.
type WebhookEndpointReference struct {
	EndpointKey string
	Name        string
	BlockKeys   []string
	Exists      bool
	Active      bool
}

// BlockReference is a block the deployment or plan cannot run: Problem is
// the error code activation would answer with.
type BlockReference struct {
	BlockKey string
	Type     approvalblock.BlockType
	Problem  string
}

const (
	blockProblemPlan                = "plan"
	blockProblemWebhookUnconfigured = "webhook_unconfigured"
)

// References reports what one revision depends on and whether each
// dependency is in place right now.
func (service *ManagementService) References(ctx context.Context, principal domain.Principal, pipelineKey string, revision int64) (RevisionReferences, error) {
	target, err := service.GetRevision(ctx, principal, pipelineKey, revision)
	if err != nil {
		return RevisionReferences{}, err
	}
	definitions := flattenDefinitions(pipelineDefinitions(target.Blocks))
	references := RevisionReferences{
		EscalationPaths:  make([]EscalationPathReference, 0),
		WebhookEndpoints: make([]WebhookEndpointReference, 0),
		Pipelines:        make([]PipelineReference, 0),
		Blocks:           make([]BlockReference, 0),
		Activatable:      true,
	}
	if err := service.referencedEscalationPaths(ctx, principal.OrganizationID, definitions, target.Revision.DefaultEscalationPath, &references); err != nil {
		return RevisionReferences{}, err
	}
	if err := service.referencedWebhookEndpoints(ctx, principal.OrganizationID, definitions, &references); err != nil {
		return RevisionReferences{}, err
	}
	if err := service.referencedPipelines(ctx, principal.OrganizationID, definitions, &references); err != nil {
		return RevisionReferences{}, err
	}
	if err := service.referencedBlockProblems(ctx, principal.OrganizationID, definitions, &references); err != nil {
		return RevisionReferences{}, err
	}
	return references, nil
}

func (service *ManagementService) referencedEscalationPaths(ctx context.Context, organizationID domain.ID, definitions []approvalblock.Definition, defaultEscalationPath string, references *RevisionReferences) error {
	keys := escalationPathKeys(definitions, defaultEscalationPath)
	if len(keys) == 0 {
		return nil
	}
	missing, inactive := map[string]struct{}{}, map[string]struct{}{}
	if service.paths != nil {
		resolved, err := service.paths.ResolveEscalationPaths(ctx, application.ResolveEscalationPathsInput{
			OrganizationID: organizationID, PathKeys: keys,
		})
		if err != nil {
			return err
		}
		for _, key := range resolved.Missing {
			missing[key] = struct{}{}
		}
		for _, key := range resolved.Inactive {
			inactive[key] = struct{}{}
		}
	}
	for _, key := range keys {
		reference := EscalationPathReference{PathKey: key, BlockKeys: make([]string, 0), Default: key == defaultEscalationPath}
		for _, definition := range definitions {
			if definition.EscalationPath() == key {
				reference.BlockKeys = append(reference.BlockKeys, definition.ID)
			}
		}
		_, isMissing := missing[key]
		_, isInactive := inactive[key]
		reference.Exists = !isMissing
		reference.Active = !isMissing && !isInactive
		if !reference.Active {
			references.Activatable = false
		}
		references.EscalationPaths = append(references.EscalationPaths, reference)
	}
	return nil
}

func (service *ManagementService) referencedWebhookEndpoints(ctx context.Context, organizationID domain.ID, definitions []approvalblock.Definition, references *RevisionReferences) error {
	runtime := service.compiled.registry.webhooks
	if runtime == nil || runtime.Endpoints == nil {
		// The block problem below already says the runtime is absent.
		return nil
	}
	keys := webhookEndpointKeys(definitions)
	if len(keys) == 0 {
		return nil
	}
	resolved, err := runtime.Endpoints.ResolveWebhookEndpoints(ctx, application.ResolveWebhookEndpointsInput{
		OrganizationID: organizationID, EndpointKeys: keys,
	})
	if err != nil {
		return err
	}
	missing, inactive := map[string]struct{}{}, map[string]struct{}{}
	for _, key := range resolved.Missing {
		missing[key] = struct{}{}
	}
	for _, key := range resolved.Inactive {
		inactive[key] = struct{}{}
	}
	for _, key := range keys {
		reference := WebhookEndpointReference{EndpointKey: key, BlockKeys: make([]string, 0)}
		for _, definition := range definitions {
			if definition.Type == approvalblock.TypeWebhook && definition.Webhook != nil && definition.Webhook.EndpointKey == key {
				reference.BlockKeys = append(reference.BlockKeys, definition.ID)
			}
		}
		_, isMissing := missing[key]
		_, isInactive := inactive[key]
		reference.Exists = !isMissing
		reference.Active = !isMissing && !isInactive
		if reference.Active {
			active, err := runtime.Endpoints.GetActiveWebhookEndpointRevision(ctx, application.GetActiveWebhookEndpointRevisionInput{
				OrganizationID: organizationID, EndpointKey: key,
			})
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return err
			}
			reference.Name = active.Name
		} else {
			references.Activatable = false
		}
		references.WebhookEndpoints = append(references.WebhookEndpoints, reference)
	}
	return nil
}

func (service *ManagementService) referencedPipelines(ctx context.Context, organizationID domain.ID, definitions []approvalblock.Definition, references *RevisionReferences) error {
	keys := delegateTargetKeys(definitions)
	if len(keys) == 0 {
		return nil
	}
	missing, inactive := map[string]struct{}{}, map[string]struct{}{}
	if service.pipelines != nil {
		resolved, err := service.pipelines.ResolvePipelineKeys(ctx, application.ResolvePipelineKeysInput{
			OrganizationID: organizationID, PipelineKeys: keys,
		})
		if err != nil {
			return err
		}
		for _, key := range resolved.Missing {
			missing[key] = struct{}{}
		}
		for _, key := range resolved.Inactive {
			inactive[key] = struct{}{}
		}
	}
	for _, key := range keys {
		reference := PipelineReference{PipelineKey: key, BlockKeys: make([]string, 0)}
		for _, definition := range definitions {
			if definition.Type == approvalblock.TypeDelegate && definition.Delegate != nil && definition.Delegate.PipelineKey == key {
				reference.BlockKeys = append(reference.BlockKeys, definition.ID)
			}
		}
		_, isMissing := missing[key]
		_, isInactive := inactive[key]
		reference.Exists = !isMissing
		reference.Active = !isMissing && !isInactive
		if reference.Active {
			active, err := service.repository.GetActiveRequestApprovalPipelineRevision(ctx, application.GetActiveRequestApprovalPipelineRevisionInput{
				OrganizationID: organizationID, PipelineKey: key,
			})
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return err
			}
			reference.Name = active.Name
		} else {
			references.Activatable = false
		}
		references.Pipelines = append(references.Pipelines, reference)
	}
	return nil
}

// webhookEndpointKeys lists the endpoint keys the webhook blocks post to,
// in block order, deduplicated.
func webhookEndpointKeys(definitions []approvalblock.Definition) []string {
	keys := make([]string, 0)
	seen := make(map[string]struct{})
	for _, definition := range definitions {
		if definition.Type != approvalblock.TypeWebhook || definition.Webhook == nil {
			continue
		}
		key := definition.Webhook.EndpointKey
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func (service *ManagementService) referencedBlockProblems(ctx context.Context, organizationID domain.ID, definitions []approvalblock.Definition, references *RevisionReferences) error {
	registry := service.compiled.registry
	denied := make(map[application.Capability]bool)
	for _, definition := range definitions {
		problem, _ := registry.problem(definition)
		if capability, gated := registry.capability(definition.Type); problem == "" && gated && registry.entitlements != nil {
			refused, checked := denied[capability]
			if !checked {
				decision, err := registry.entitlements.CheckCapability(ctx, application.CheckCapabilityInput{OrganizationID: organizationID, Capability: capability})
				if err != nil {
					return err
				}
				refused = !decision.Allowed
				denied[capability] = refused
			}
			if refused {
				problem = blockProblemPlan
			}
		}
		if problem == "" {
			continue
		}
		references.Activatable = false
		references.Blocks = append(references.Blocks, BlockReference{BlockKey: definition.ID, Type: definition.Type, Problem: problem})
	}
	return nil
}
func (service *ManagementService) loadRevisionBlocks(ctx context.Context, revision domain.RequestApprovalPipelineRevision) (PipelineRevision, error) {
	stored, err := service.repository.GetRequestApprovalBlocks(ctx, application.GetRequestApprovalBlocksInput{
		OrganizationID: revision.OrganizationID,
		BlockIDs:       revision.BlockIDs,
	})
	if err != nil {
		return PipelineRevision{}, err
	}
	blocks, err := service.compiled.registry.nestSnapshots(stored)
	if err != nil {
		return PipelineRevision{}, err
	}
	return PipelineRevision{Revision: revision, Blocks: blocks}, nil
}

// requireBlockDependencies gates plan-limited block types on the
// organization's plan and checks that every judge model and webhook
// endpoint a block references is available. It runs after compilation, so
// an unconfigured runtime is reported first.
func (service *ManagementService) requireBlockDependencies(ctx context.Context, organizationID domain.ID, definitions []approvalblock.Definition) error {
	if err := service.requireCapabilities(ctx, organizationID, definitions); err != nil {
		return err
	}
	if err := service.requireRuntimeDependencies(definitions); err != nil {
		return err
	}
	return service.requireWebhookEndpoints(ctx, organizationID, definitions)
}

// requireRuntimeDependencies asks each block's runtime whether the
// deployment can run it right now: a kind naming a dependency the
// deployment does not offer (a judge model the catalog dropped) refuses
// the write, while a stored revision naming one still compiles and hands
// its requests to a person.
func (service *ManagementService) requireRuntimeDependencies(definitions []approvalblock.Definition) error {
	registry := service.compiled.registry
	for _, definition := range definitions {
		if _, refusal := registry.problem(definition); refusal != nil {
			return fmt.Errorf("block %q: %w", definition.ID, refusal)
		}
	}
	return nil
}

func (service *ManagementService) requireCapabilities(ctx context.Context, organizationID domain.ID, definitions []approvalblock.Definition) error {
	entitlements := service.compiled.registry.entitlements
	if entitlements == nil {
		return nil
	}
	checked := make(map[application.Capability]struct{})
	for _, definition := range definitions {
		capability, gated := service.compiled.registry.capability(definition.Type)
		if !gated {
			continue
		}
		if _, done := checked[capability]; done {
			continue
		}
		checked[capability] = struct{}{}
		decision, err := entitlements.CheckCapability(ctx, application.CheckCapabilityInput{
			OrganizationID: organizationID, Capability: capability,
		})
		if err != nil {
			return err
		}
		if !decision.Allowed {
			return fmt.Errorf("%s blocks are not available on this plan: %s: %w", definition.Type, decision.Reason, domain.ErrForbidden)
		}
	}
	return nil
}

// requireWebhookEndpoints rejects definitions that post to an endpoint
// key with no revision in the organization. Creation and preview only
// need the key to exist; activation requires it active, checked by the
// store inside the activating transaction.
func (service *ManagementService) requireWebhookEndpoints(ctx context.Context, organizationID domain.ID, definitions []approvalblock.Definition) error {
	runtime := service.compiled.registry.webhooks
	keys := webhookEndpointKeys(definitions)
	if len(keys) == 0 {
		return nil
	}
	if runtime == nil || runtime.Endpoints == nil {
		return domain.ErrWebhookUnconfigured
	}
	resolved, err := runtime.Endpoints.ResolveWebhookEndpoints(ctx, application.ResolveWebhookEndpointsInput{
		OrganizationID: organizationID, EndpointKeys: keys,
	})
	if err != nil {
		return err
	}
	if len(resolved.Missing) > 0 {
		return fmt.Errorf("%w: %s", domain.ErrWebhookEndpointNotFound, strings.Join(resolved.Missing, ", "))
	}
	return nil
}

func firstDuplicateConditionID(condition approvalblock.Condition, seen map[string]struct{}) string {
	if _, exists := seen[condition.ID]; exists {
		return condition.ID
	}
	seen[condition.ID] = struct{}{}
	for _, child := range condition.All {
		if duplicate := firstDuplicateConditionID(child, seen); duplicate != "" {
			return duplicate
		}
	}
	for _, child := range condition.Any {
		if duplicate := firstDuplicateConditionID(child, seen); duplicate != "" {
			return duplicate
		}
	}
	if condition.Some != nil {
		return firstDuplicateConditionID(condition.Some.Where, seen)
	}
	if condition.Every != nil {
		return firstDuplicateConditionID(condition.Every.Where, seen)
	}
	return ""
}

func validatePreviewSample(sample PreviewSample) error {
	if strings.TrimSpace(sample.Request.Tool) == "" {
		return invalidPipelineDocument("sample request tool is required", nil)
	}
	if strings.TrimSpace(sample.Agent.Name) == "" {
		return invalidPipelineDocument("sample agent name is required", nil)
	}
	// Every pipeline applies to every agent, so a preview accepts any
	// valid sample agent slug.
	if !domain.AgentSlug(sample.Agent.Slug).Valid() {
		return invalidPipelineDocument("sample agent slug must match ^[a-z0-9][a-z0-9._-]{0,62}$", nil)
	}
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(sample.Request.Arguments, &arguments); err != nil || arguments == nil {
		return invalidPipelineDocument("sample request arguments must be a JSON object", err)
	}
	for key, value := range sample.PreviousMetadata {
		pipelineKey, blockKey, found := strings.Cut(key, "/")
		if !found || !domain.ValidPipelineKey(pipelineKey) || blockKey == "" {
			return invalidPipelineDocument("sample previous metadata keys must be <pipeline_key>/<block_key>", nil)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(value, &object); err != nil || object == nil {
			return invalidPipelineDocument("sample previous metadata values must be JSON objects", err)
		}
	}
	return nil
}

// requirePipelineMember is the shared precondition: a person with a
// membership in the organization.
func requirePipelineMember(principal domain.Principal) error {
	if !domain.ManagementPrincipal(principal) {
		return domain.ErrForbidden
	}
	return nil
}

// authorizePipelineManagement admits a member holding the permission on
// the pipeline: at the pipeline's own scope, or the organization.
func authorizePipelineManagement(principal domain.Principal, permission domain.Permission, pipelineKey string) error {
	if err := requirePipelineMember(principal); err != nil {
		return err
	}
	if err := validatePipelineKey(pipelineKey); err != nil {
		return err
	}
	return authorization.Check(principal, permission, domain.PipelineResource(pipelineKey))
}

func validatePipelineKey(pipelineKey string) error {
	if !domain.ValidPipelineKey(pipelineKey) {
		return invalidPipelineDocument("pipeline key must match ^[a-z0-9][a-z0-9._-]{0,62}$", nil)
	}
	return nil
}

func invalidPipelineDocument(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%s: %w", message, domain.ErrInvalid)
	}
	return fmt.Errorf("%s: %v: %w", message, cause, domain.ErrInvalid)
}
