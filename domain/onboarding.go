package domain

import (
	"encoding/json"
	"time"
)

// OnboardingStatus is the phase of one guided setup between a person in
// the browser and the withhuman CLI on their machine. The two sides take
// turns: the CLI advances the odd phases (connect, detect, discover,
// enroll, install) and the person advances the even ones (choose agents,
// provision, decide the test request, finish). The person's own steps
// that never gate the CLI (choosing how withHuman reaches them, writing
// the rules with the assistant) are stamps on the session rather than
// statuses: channels_set_at must be set before provisioning and
// rules_set_at before the test. After installed the CLI is done; tested
// records the request the person decided, and finishing follows.
type OnboardingStatus string

const (
	OnboardingAwaitingCLI     OnboardingStatus = "awaiting_cli"
	OnboardingConnected       OnboardingStatus = "connected"
	OnboardingDetected        OnboardingStatus = "detected"
	OnboardingAgentsSelected  OnboardingStatus = "agents_selected"
	OnboardingToolsDiscovered OnboardingStatus = "tools_discovered"
	OnboardingProvisioning    OnboardingStatus = "provisioning"
	OnboardingEnrolled        OnboardingStatus = "enrolled"
	OnboardingInstalled       OnboardingStatus = "installed"
	OnboardingTested          OnboardingStatus = "tested"
	OnboardingCompleted       OnboardingStatus = "completed"
	OnboardingSkipped         OnboardingStatus = "skipped"
)

// OnboardingStatuses lists every status, in flow order.
var OnboardingStatuses = []OnboardingStatus{
	OnboardingAwaitingCLI, OnboardingConnected, OnboardingDetected, OnboardingAgentsSelected,
	OnboardingToolsDiscovered, OnboardingProvisioning, OnboardingEnrolled, OnboardingInstalled,
	OnboardingTested, OnboardingCompleted, OnboardingSkipped,
}

// Terminal reports whether no further transition is possible.
func (s OnboardingStatus) Terminal() bool {
	return s == OnboardingCompleted || s == OnboardingSkipped
}

// Valid reports whether s is a known status.
func (s OnboardingStatus) Valid() bool {
	for _, known := range OnboardingStatuses {
		if known == s {
			return true
		}
	}
	return false
}

// OnboardingPermissions is the authority a person needs to run the guided
// setup: it connects agents, writes a pipeline and an escalation path,
// and ends with the person deciding a request.
var OnboardingPermissions = []Permission{
	PermissionAgentWrite, PermissionAgentCredentialIssue,
	PermissionPipelineWrite, PermissionPipelineActivate,
	PermissionEscalationPathWrite, PermissionEscalationPathActivate,
	PermissionRequestRead, PermissionRequestDecide,
}

// OnboardingOutcome is how a person closes the flow.
type OnboardingOutcome string

const (
	OnboardingFinished OnboardingOutcome = "finished"
	OnboardingSkip     OnboardingOutcome = "skipped"
)

// OnboardingMachine describes the machine the CLI runs on, as the CLI
// reports it at exchange time.
type OnboardingMachine struct {
	Hostname   string `json:"hostname"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	CLIVersion string `json:"cli_version"`
	User       string `json:"user,omitempty"`
}

// OnboardingMCPServer is one MCP server an agent runtime is configured
// with, as found in the runtime's configuration files.
type OnboardingMCPServer struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Source    string `json:"source"`
}

// OnboardingDetection is the CLI's report on one runtime.
type OnboardingDetection struct {
	Runtime       string                `json:"runtime"`
	Installed     bool                  `json:"installed"`
	Version       string                `json:"version,omitempty"`
	BinaryPath    string                `json:"binary_path,omitempty"`
	ConfigPath    string                `json:"config_path,omitempty"`
	AlreadyHooked bool                  `json:"already_hooked"`
	MCPServers    []OnboardingMCPServer `json:"mcp_servers"`
}

// OnboardingToolAnnotations are the MCP tool annotations a server
// advertised. Pointers distinguish "false" from "not stated".
type OnboardingToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"read_only_hint,omitempty"`
	DestructiveHint *bool  `json:"destructive_hint,omitempty"`
	IdempotentHint  *bool  `json:"idempotent_hint,omitempty"`
	OpenWorldHint   *bool  `json:"open_world_hint,omitempty"`
}

// OnboardingReportedTool is one tool as the CLI discovered it, before the
// server has judged it.
type OnboardingReportedTool struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description,omitempty"`
	Annotations *OnboardingToolAnnotations `json:"annotations,omitempty"`
	InputSchema json.RawMessage            `json:"input_schema,omitempty"`
	DisplayName string                     `json:"display_name,omitempty"`
}

// OnboardingDiscoveryError records one MCP server that could not be
// listed. Discovery of the other servers continues.
type OnboardingDiscoveryError struct {
	Server  string `json:"server"`
	Message string `json:"message"`
}

// OnboardingReportedTools is the CLI's discovery report for one runtime.
type OnboardingReportedTools struct {
	Builtin []OnboardingReportedTool            `json:"builtin"`
	MCP     map[string][]OnboardingReportedTool `json:"mcp"`
	Errors  []OnboardingDiscoveryError          `json:"errors,omitempty"`
}

// OnboardingTool is a discovered tool as the session records it, with the
// server's suggestion attached.
type OnboardingTool struct {
	InputSchema json.RawMessage            `json:"input_schema,omitempty"`
	DisplayName string                     `json:"display_name,omitempty"`
	Name        string                     `json:"name"`
	Source      string                     `json:"source"`
	Server      string                     `json:"server,omitempty"`
	Description string                     `json:"description,omitempty"`
	Kind        string                     `json:"kind,omitempty"`
	Annotations *OnboardingToolAnnotations `json:"annotations,omitempty"`
	Suggested   bool                       `json:"suggested"`
	Reason      string                     `json:"reason"`
}

// OnboardingGeneratedPipeline records what provisioning wrote for one
// agent: the revision it activated and the one that was active
// before (the empty first revision the agent was born with, or an
// earlier onboarding's).
type OnboardingGeneratedPipeline struct {
	Revision               int64 `json:"revision"`
	ReplacedActiveRevision int64 `json:"replaced_active_revision"`
}

// OnboardingGenerated records every object provisioning created so the
// page can show them and link to them.
type OnboardingGenerated struct {
	EscalationPathKey      string                                 `json:"escalation_path_key"`
	EscalationPathName     string                                 `json:"escalation_path_name"`
	EscalationPathRevision int64                                  `json:"escalation_path_revision"`
	Pipelines              map[string]OnboardingGeneratedPipeline `json:"pipelines"`
}

// OnboardingEnrollment records the agent, instance, and credential id
// minted for one runtime. The raw credential is handed to the CLI once
// and never stored.
type OnboardingEnrollment struct {
	Runtime           string    `json:"runtime"`
	AgentSlug         AgentSlug `json:"agent_slug"`
	AgentName         string    `json:"agent_name"`
	AgentInstanceID   ID        `json:"agent_instance_id"`
	AgentInstanceName string    `json:"agent_instance_name"`
	CredentialID      ID        `json:"credential_id"`
}

// OnboardingFileChange is one edit the CLI made to a runtime's configuration.
type OnboardingFileChange struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

// OnboardingInstallResult is the CLI's report on wiring one runtime.
type OnboardingInstallResult struct {
	Runtime string                 `json:"runtime"`
	OK      bool                   `json:"ok"`
	Changes []OnboardingFileChange `json:"changes,omitempty"`
	Notes   []string               `json:"notes,omitempty"`
	Manual  bool                   `json:"manual,omitempty"`
	Message string                 `json:"message,omitempty"`
	Snippet string                 `json:"snippet,omitempty"`
}

// OnboardingTestCommand is the command the page shows to exercise one
// runtime's gated tool.
type OnboardingTestCommand struct {
	Tool    string `json:"tool"`
	Command string `json:"command"`
	Note    string `json:"note,omitempty"`
}

// OnboardingError is the last failure either side recorded.
type OnboardingError struct {
	Source  string `json:"source"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// OnboardingAssistantRole is who wrote one message of the setup
// assistant's conversation.
type OnboardingAssistantRole string

const (
	OnboardingAssistantUser      OnboardingAssistantRole = "user"
	OnboardingAssistantAssistant OnboardingAssistantRole = "assistant"
)

// OnboardingAssistantMessageStatus is where an assistant message is:
// running while the model still works, done once it answered, failed
// when the turn ended in an error the page shows in its place.
type OnboardingAssistantMessageStatus string

const (
	OnboardingAssistantRunning OnboardingAssistantMessageStatus = "running"
	OnboardingAssistantDone    OnboardingAssistantMessageStatus = "done"
	OnboardingAssistantFailed  OnboardingAssistantMessageStatus = "failed"
)

// OnboardingAssistantAction is one tool call the assistant made during
// a turn, as the page shows it: which tool, a one-line summary, and
// whether it succeeded. Inputs and results stay in the model context.
type OnboardingAssistantAction struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	OK      bool   `json:"ok"`
}

// OnboardingAssistantMessage is one message of the setup assistant's
// conversation as the page shows it. The person's messages carry text
// alone; the assistant's carry the actions it took, its answer, and a
// status, so a reload during a turn shows the work in progress.
type OnboardingAssistantMessage struct {
	Role    OnboardingAssistantRole          `json:"role"`
	Text    string                           `json:"text"`
	Actions []OnboardingAssistantAction      `json:"actions"`
	Status  OnboardingAssistantMessageStatus `json:"status"`
	Error   string                           `json:"error,omitempty"`
	At      time.Time                        `json:"at"`
}

// OnboardingSession is the shared record both sides read and write. It
// never carries the setup code, the CLI's bearer secret, or any agent
// credential; those exist only as hashes or in the one response that
// hands them over.
type OnboardingSession struct {
	ID               ID                                    `json:"id"`
	OrganizationID   ID                                    `json:"organization_id"`
	MembershipID     ID                                    `json:"membership_id"`
	Status           OnboardingStatus                      `json:"status"`
	Version          int64                                 `json:"version"`
	Machine          OnboardingMachine                     `json:"machine"`
	Detections       []OnboardingDetection                 `json:"detections"`
	SelectedRuntimes []string                              `json:"selected_runtimes"`
	Tools            map[string][]OnboardingTool           `json:"tools"`
	DiscoveryErrors  map[string][]OnboardingDiscoveryError `json:"discovery_errors"`
	Generated        OnboardingGenerated                   `json:"generated"`
	Enrollments      []OnboardingEnrollment                `json:"enrollments"`
	InstallResults   []OnboardingInstallResult             `json:"install_results"`
	TestCommands     map[string]OnboardingTestCommand      `json:"test_commands"`
	// The request the person decided in the test, and when; set with
	// tested. ChannelsSetAt is stamped when the person passed the
	// notification steps (provisioning needs it), RulesSetAt when they
	// left the rules step for the test (the test counts requests that
	// arrived after it).
	TestRequestID *ID        `json:"test_request_id"`
	TestedAt      *time.Time `json:"tested_at"`
	ChannelsSetAt *time.Time `json:"channels_set_at"`
	RulesSetAt    *time.Time `json:"rules_set_at"`
	// Assistant is the setup assistant's conversation as the page shows
	// it. AssistantContext is the model's own transcript of the same
	// conversation (tool calls and results included), kept out of the
	// wire representation because it is bulky and the model's alone.
	Assistant        []OnboardingAssistantMessage `json:"assistant"`
	AssistantContext json.RawMessage              `json:"-"`
	LastError        *OnboardingError             `json:"last_error"`
	CreatedAt        time.Time                    `json:"created_at"`
	UpdatedAt        time.Time                    `json:"updated_at"`
	ExpiresAt        time.Time                    `json:"expires_at"`
	CompletedAt      *time.Time                   `json:"completed_at"`
}

// Expired reports whether the session can no longer be written.
func (s OnboardingSession) Expired(now time.Time) bool {
	return !s.Status.Terminal() && !now.Before(s.ExpiresAt)
}

// Detection returns the CLI's report for one runtime.
func (s OnboardingSession) Detection(runtime string) (OnboardingDetection, bool) {
	for _, detection := range s.Detections {
		if detection.Runtime == runtime {
			return detection, true
		}
	}
	return OnboardingDetection{}, false
}

// Selected reports whether the person chose a runtime.
func (s OnboardingSession) Selected(runtime string) bool {
	for _, selected := range s.SelectedRuntimes {
		if selected == runtime {
			return true
		}
	}
	return false
}

// SuggestedTools lists the discovered tools of one runtime the server
// judged as needing a human: what the default rules gate before the
// person refines them.
func (s OnboardingSession) SuggestedTools(runtime string) []OnboardingTool {
	var suggested []OnboardingTool
	for _, tool := range s.Tools[runtime] {
		if tool.Suggested {
			suggested = append(suggested, tool)
		}
	}
	return suggested
}

// OnboardingTransitions is the closed table of allowed status changes and
// who makes them. A write that is not in the table is a conflict.
var OnboardingTransitions = map[OnboardingStatus][]OnboardingStatus{
	OnboardingAwaitingCLI:     {OnboardingConnected},
	OnboardingConnected:       {OnboardingDetected},
	OnboardingDetected:        {OnboardingDetected, OnboardingAgentsSelected},
	OnboardingAgentsSelected:  {OnboardingAgentsSelected, OnboardingToolsDiscovered},
	OnboardingToolsDiscovered: {OnboardingAgentsSelected, OnboardingToolsDiscovered, OnboardingProvisioning},
	OnboardingProvisioning:    {OnboardingEnrolled},
	OnboardingEnrolled:        {OnboardingInstalled},
	OnboardingInstalled:       {OnboardingTested},
	OnboardingTested:          {OnboardingCompleted},
}

// OnboardingCanTransition reports whether a session may move from one
// status to another. Skipping is allowed from every open status and is
// handled by the caller, not the table.
func OnboardingCanTransition(from, to OnboardingStatus) bool {
	if to == OnboardingSkipped {
		return !from.Terminal()
	}
	for _, allowed := range OnboardingTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// OnboardingSessionPatch carries the fields one write may replace. Nil
// pointers leave the stored value alone.
type OnboardingSessionPatch struct {
	Detections       *[]OnboardingDetection
	SelectedRuntimes *[]string
	Tools            *map[string][]OnboardingTool
	DiscoveryErrors  *map[string][]OnboardingDiscoveryError
	Generated        *OnboardingGenerated
	Enrollments      *[]OnboardingEnrollment
	InstallResults   *[]OnboardingInstallResult
	TestCommands     *map[string]OnboardingTestCommand
	TestRequestID    *ID
	TestedAt         *time.Time
	ChannelsSetAt    *time.Time
	RulesSetAt       *time.Time
	Assistant        *[]OnboardingAssistantMessage
	AssistantContext *json.RawMessage
	LastError        *OnboardingError
	ClearLastError   bool
}

// MarshalJSON keeps empty collections as [] and {} rather than null so
// both consumers can index them without nil checks.
func (s OnboardingSession) MarshalJSON() ([]byte, error) {
	type alias OnboardingSession
	copy := alias(s)
	if copy.Detections == nil {
		copy.Detections = []OnboardingDetection{}
	}
	if copy.SelectedRuntimes == nil {
		copy.SelectedRuntimes = []string{}
	}
	if copy.Tools == nil {
		copy.Tools = map[string][]OnboardingTool{}
	}
	if copy.DiscoveryErrors == nil {
		copy.DiscoveryErrors = map[string][]OnboardingDiscoveryError{}
	}
	if copy.Assistant == nil {
		copy.Assistant = []OnboardingAssistantMessage{}
	}
	if copy.Generated.Pipelines == nil {
		copy.Generated.Pipelines = map[string]OnboardingGeneratedPipeline{}
	}
	if copy.Enrollments == nil {
		copy.Enrollments = []OnboardingEnrollment{}
	}
	if copy.InstallResults == nil {
		copy.InstallResults = []OnboardingInstallResult{}
	}
	if copy.TestCommands == nil {
		copy.TestCommands = map[string]OnboardingTestCommand{}
	}
	return json.Marshal(copy)
}
