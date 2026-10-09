// Package routing defines the serializable escalation path document, with
// the validation rules revision creation enforces. A pipeline's human exit
// names a path by its key; the path decides who is asked and when.
package routing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/withHumanAI/withHuman/approvalblock"
)

const (
	// DocumentVersion is stamped on every stored revision so the runtime
	// can reject document shapes it cannot safely interpret.
	DocumentVersion = 2

	MaximumDocumentBytes       = 64 * 1024
	MaximumNodes               = 100
	MaximumBranchDepth         = 4
	MaximumWorkingHoursSets    = 20
	MaximumIntervalsPerSet     = 21
	MaximumTargetsPerLevel     = 25
	MaximumConditionsPerBranch = 10
	MaximumRepeatTimes         = 9

	MinimumEscalateAfter = time.Minute
	MaximumEscalateAfter = 7 * 24 * time.Hour
)

type NodeType string

const (
	NodeLevel  NodeType = "level"
	NodeIfElse NodeType = "if_else"
	NodeDefer  NodeType = "defer"
)

type Urgency string

const (
	UrgencyStandard  Urgency = "standard"
	UrgencyInterrupt Urgency = "interrupt"
)

// DocumentKind says which document a node program belongs to. The two
// kinds share the node grammar and differ only in what a level may
// target: an organization path names people and teams, a team policy
// names people on its team or the whole team.
type DocumentKind string

const (
	DocumentKindPath       DocumentKind = "escalation_path"
	DocumentKindTeamPolicy DocumentKind = "team_policy"
)

type TargetType string

const (
	TargetUser TargetType = "user"
	// TargetTeam hands the level to a team: its policy when it has one,
	// otherwise every current member at once. Organization paths only.
	TargetTeam TargetType = "team"
	// TargetBroadcast notifies every current member of the team that owns
	// the policy. Team policies only; it carries no id.
	TargetBroadcast TargetType = "broadcast"
	// TargetRoundRobin and TargetOnCall are reserved for rotation and
	// schedule primitives. Every kind rejects them until they exist.
	TargetRoundRobin TargetType = "round_robin"
	TargetOnCall     TargetType = "on_call"
)

// Target is who a level notifies. Team membership resolves at
// notification time; documents store only the reference. A broadcast
// target has no id: the owning team is the policy's, never the document's.
type Target struct {
	Type TargetType `json:"type"`
	ID   string     `json:"id,omitempty"`
}

// Validate checks the target shape against the document kind it sits in.
// Whether the referenced membership or team exists is a repository
// question answered at revision creation and activation.
func (target Target) Validate(kind DocumentKind) error {
	switch target.Type {
	case TargetUser:
		return requireTargetID(target)
	case TargetTeam:
		if kind == DocumentKindTeamPolicy {
			return errors.New("a team policy cannot target other teams")
		}
		return requireTargetID(target)
	case TargetBroadcast:
		if kind != DocumentKindTeamPolicy {
			return errors.New("broadcast targets belong to a team escalation policy")
		}
		if target.ID != "" {
			return errors.New("broadcast target carries no id")
		}
		return nil
	case TargetRoundRobin, TargetOnCall:
		return fmt.Errorf("target type %q is reserved, not yet supported", target.Type)
	default:
		if kind == DocumentKindTeamPolicy {
			return fmt.Errorf("target type %q must be user or broadcast", target.Type)
		}
		return fmt.Errorf("target type %q must be user or team", target.Type)
	}
}

func requireTargetID(target Target) error {
	if _, err := uuid.Parse(target.ID); err != nil {
		return errors.New("target id must be a UUID")
	}
	return nil
}

// Decode reads a stored document strictly: unknown fields are an error,
// so a revision written by a newer grammar is refused rather than
// silently narrowed.
func Decode(raw json.RawMessage, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("document has trailing content")
	}
	return nil
}

// ValidPathKey reports whether key has the shape of a path key.
func ValidPathKey(key string) bool {
	return keyPattern.MatchString(key)
}

// ConditionType is what a branch tests.
type ConditionType string

const (
	// ConditionWorkingHours tests whether a named working-hours window is
	// open (active) or closed.
	ConditionWorkingHours ConditionType = "working_hours"
	// ConditionUrgency tests the request's current urgency.
	ConditionUrgency ConditionType = "urgency"
	// ConditionRequest tests the request itself with the pipeline's
	// condition envelope: tool, arguments, agent.
	ConditionRequest ConditionType = "request"
)

var (
	keyPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	timePattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
)

var weekdays = map[string]struct{}{
	"mon": {}, "tue": {}, "wed": {}, "thu": {}, "fri": {}, "sat": {}, "sun": {},
}

type WeekdayInterval struct {
	Weekdays []string `json:"weekdays"`
	Start    string   `json:"start"`
	End      string   `json:"end"`
}

type WorkingHoursSet struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Timezone  string            `json:"timezone"`
	Intervals []WeekdayInterval `json:"intervals"`
}

// Condition is one test on an if_else branch. It has exactly one shape
// selected by Type; fields belonging to the other shapes must stay zero.
// Every condition on a branch must hold for the then branch to run.
type Condition struct {
	Type ConditionType `json:"type"`

	// working_hours
	WorkingHoursID string `json:"working_hours_id,omitempty"`
	Active         bool   `json:"active,omitempty"`

	// urgency
	Urgency Urgency `json:"urgency,omitempty"`

	// request
	When *approvalblock.Condition `json:"when,omitempty"`
}

// Node has exactly one shape selected by Type; fields belonging to the
// other shapes must stay zero.
type Node struct {
	ID   string   `json:"id"`
	Type NodeType `json:"type"`

	// level
	Targets       []Target `json:"targets,omitempty"`
	EscalateAfter string   `json:"escalate_after,omitempty"`
	RaiseUrgency  bool     `json:"raise_urgency,omitempty"`

	// if_else
	Conditions []Condition `json:"conditions,omitempty"`
	Then       []Node      `json:"then,omitempty"`
	Else       []Node      `json:"else,omitempty"`

	// defer
	WorkingHoursID string `json:"working_hours_id,omitempty"`
	StandardOnly   bool   `json:"standard_only,omitempty"`
}

type Repeat struct {
	Times int    `json:"times"`
	After string `json:"after"`
}

// PathDocument is one escalation path revision's content: an
// organization-owned node program whose levels name people and teams.
type PathDocument struct {
	WorkingHours []WorkingHoursSet `json:"working_hours"`
	Nodes        []Node            `json:"nodes"`
	Repeat       *Repeat           `json:"repeat,omitempty"`
}

func (document PathDocument) Validate() error {
	return validateProgram(document.WorkingHours, document.Nodes, document.Repeat, DocumentKindPath, document)
}

// Targets returns every distinct target the document's level nodes
// reference, walking every if_else branch. This is the request-agnostic
// union the path editor validates and warns on; who may decide a
// particular request is ReachableTargets.
func (document PathDocument) Targets() []Target {
	return collectTargets(document.Nodes)
}

// ReachableTargets returns the distinct targets a request can be shown
// to, in walk order, given which of the document's request conditions
// hold for it. holds is asked by the root condition id; a condition the
// caller cannot answer reads false, exactly as the executor treats a
// missing fact. working_hours and urgency conditions are not decidable
// from the request alone, so both branches of a node carrying one count
// as reachable. Timing (escalate_after, defer, repeat) is ignored: every
// reachable level counts from the moment the request is routed.
func (document PathDocument) ReachableTargets(holds func(conditionID string) bool) []Target {
	return collectReachableTargets(document.Nodes, holds)
}

// MayReachNobody reports whether some request could walk the whole path
// without meeting a level: no top-level node guarantees a target, where a
// level guarantees one and an if_else guarantees one only when both its
// branches do. The path editor warns on it; it is request-agnostic.
func (document PathDocument) MayReachNobody() bool {
	return !guaranteesTarget(document.Nodes)
}

// LevelOrdinals numbers the document's level nodes in walk order, so a
// reader can say "level 2 of 3" from a node id alone.
func (document PathDocument) LevelOrdinals() LevelOrdinals {
	return levelOrdinals(document.Nodes)
}

// TeamPolicyDocument is one team escalation policy revision's content:
// the same node program as a path, owned by a team, whose levels name
// members of that team or everyone on it. A policy runs inline when an
// organization path level targets the team.
type TeamPolicyDocument struct {
	WorkingHours []WorkingHoursSet `json:"working_hours"`
	Nodes        []Node            `json:"nodes"`
	Repeat       *Repeat           `json:"repeat,omitempty"`
}

func (document TeamPolicyDocument) Validate() error {
	return validateProgram(document.WorkingHours, document.Nodes, document.Repeat, DocumentKindTeamPolicy, document)
}

// Targets returns every distinct target the policy's level nodes
// reference: user targets, plus at most one broadcast marker.
func (document TeamPolicyDocument) Targets() []Target {
	return collectTargets(document.Nodes)
}

// Broadcasts reports whether any level notifies the whole team.
func (document TeamPolicyDocument) Broadcasts() bool {
	for _, target := range document.Targets() {
		if target.Type == TargetBroadcast {
			return true
		}
	}
	return false
}

// UserTargetIDs lists the memberships the policy names directly.
func (document TeamPolicyDocument) UserTargetIDs() []string {
	ids := make([]string, 0)
	for _, target := range document.Targets() {
		if target.Type == TargetUser {
			ids = append(ids, target.ID)
		}
	}
	return ids
}

func (document TeamPolicyDocument) LevelOrdinals() LevelOrdinals {
	return levelOrdinals(document.Nodes)
}

// LevelOrdinals maps level node ids to their 1-based position in walk
// order; Count is how many level nodes the document holds.
type LevelOrdinals struct {
	Ordinal map[string]int
	Count   int
}

func levelOrdinals(nodes []Node) LevelOrdinals {
	ordinals := LevelOrdinals{Ordinal: make(map[string]int)}
	var walk func(nodes []Node)
	walk = func(nodes []Node) {
		for _, node := range nodes {
			if node.Type == NodeLevel {
				ordinals.Count++
				ordinals.Ordinal[node.ID] = ordinals.Count
			}
			walk(node.Then)
			walk(node.Else)
		}
	}
	walk(nodes)
	return ordinals
}

func validateProgram(workingHours []WorkingHoursSet, nodes []Node, repeat *Repeat, kind DocumentKind, document any) error {
	sets := make(map[string]struct{}, len(workingHours))
	if len(workingHours) > MaximumWorkingHoursSets {
		return fmt.Errorf("at most %d working hours sets are allowed", MaximumWorkingHoursSets)
	}
	for _, set := range workingHours {
		if err := set.validate(); err != nil {
			return err
		}
		if _, exists := sets[set.ID]; exists {
			return fmt.Errorf("working hours id %q is duplicated", set.ID)
		}
		sets[set.ID] = struct{}{}
	}

	if len(nodes) == 0 {
		return errors.New("an escalation path requires at least one node")
	}
	state := &pathValidationState{kind: kind, sets: sets, ids: make(map[string]struct{}), conditionIDs: make(map[string]struct{})}
	if err := state.validateNodes(nodes, 1); err != nil {
		return err
	}

	if repeat != nil {
		if repeat.Times < 1 || repeat.Times > MaximumRepeatTimes {
			return fmt.Errorf("repeat times must be between 1 and %d", MaximumRepeatTimes)
		}
		if err := validateEscalationDuration("repeat after", repeat.After); err != nil {
			return err
		}
	}

	return validateDocumentSize(document)
}

func collectTargets(nodes []Node) []Target {
	seen := make(map[Target]struct{})
	targets := make([]Target, 0)
	var walk func(nodes []Node)
	walk = func(nodes []Node) {
		for _, node := range nodes {
			for _, target := range node.Targets {
				if _, duplicate := seen[target]; duplicate {
					continue
				}
				seen[target] = struct{}{}
				targets = append(targets, target)
			}
			walk(node.Then)
			walk(node.Else)
		}
	}
	walk(nodes)
	return targets
}

func collectReachableTargets(nodes []Node, holds func(conditionID string) bool) []Target {
	seen := make(map[Target]struct{})
	targets := make([]Target, 0)
	var walk func(nodes []Node)
	walk = func(nodes []Node) {
		for _, node := range nodes {
			for _, target := range node.Targets {
				if _, duplicate := seen[target]; duplicate {
					continue
				}
				seen[target] = struct{}{}
				targets = append(targets, target)
			}
			if node.Type != NodeIfElse {
				continue
			}
			then, els := branchReachability(node.Conditions, holds)
			if then {
				walk(node.Then)
			}
			if els {
				walk(node.Else)
			}
		}
	}
	walk(nodes)
	return targets
}

// branchReachability says which branches of an if_else a request may
// take. The conditions are ANDed: one request condition that fails sends
// every such request down else; a working-hours or urgency condition can
// go either way, so it opens else without closing then. A condition of an
// unknown shape reads false, as it does in the executor.
func branchReachability(conditions []Condition, holds func(conditionID string) bool) (then, els bool) {
	then = true
	for _, condition := range conditions {
		switch condition.Type {
		case ConditionRequest:
			if condition.When == nil || !holds(condition.When.ID) {
				then, els = false, true
			}
		case ConditionWorkingHours, ConditionUrgency:
			els = true
		default:
			then, els = false, true
		}
	}
	return then, els
}

// guaranteesTarget reports whether every walk through nodes meets a
// level. Validation guarantees a level names at least one target.
func guaranteesTarget(nodes []Node) bool {
	for _, node := range nodes {
		switch node.Type {
		case NodeLevel:
			return true
		case NodeIfElse:
			if guaranteesTarget(node.Then) && guaranteesTarget(node.Else) {
				return true
			}
		}
	}
	return false
}

func (set WorkingHoursSet) validate() error {
	if !keyPattern.MatchString(set.ID) {
		return errors.New("working hours id must match ^[a-z0-9][a-z0-9._-]{0,62}$")
	}
	if strings.TrimSpace(set.Name) == "" {
		return fmt.Errorf("working hours %q requires a name", set.ID)
	}
	if _, err := time.LoadLocation(set.Timezone); err != nil || set.Timezone == "" || set.Timezone == "Local" {
		return fmt.Errorf("working hours %q requires an IANA timezone", set.ID)
	}
	if len(set.Intervals) == 0 || len(set.Intervals) > MaximumIntervalsPerSet {
		return fmt.Errorf("working hours %q requires between 1 and %d intervals", set.ID, MaximumIntervalsPerSet)
	}
	for _, interval := range set.Intervals {
		if len(interval.Weekdays) == 0 {
			return fmt.Errorf("working hours %q has an interval without weekdays", set.ID)
		}
		seen := make(map[string]struct{}, len(interval.Weekdays))
		for _, day := range interval.Weekdays {
			if _, known := weekdays[day]; !known {
				return fmt.Errorf("working hours %q has unknown weekday %q", set.ID, day)
			}
			if _, duplicate := seen[day]; duplicate {
				return fmt.Errorf("working hours %q repeats weekday %q", set.ID, day)
			}
			seen[day] = struct{}{}
		}
		if !timePattern.MatchString(interval.Start) || !timePattern.MatchString(interval.End) {
			return fmt.Errorf("working hours %q interval times must be 24-hour HH:MM", set.ID)
		}
		if interval.Start >= interval.End {
			return fmt.Errorf("working hours %q interval must start before it ends", set.ID)
		}
	}
	return nil
}

type pathValidationState struct {
	kind         DocumentKind
	sets         map[string]struct{}
	ids          map[string]struct{}
	conditionIDs map[string]struct{}
	nodes        int
}

func (state *pathValidationState) validateNodes(nodes []Node, depth int) error {
	if depth > MaximumBranchDepth {
		return fmt.Errorf("branches nest at most %d levels deep", MaximumBranchDepth)
	}
	for _, node := range nodes {
		if err := state.validateNode(node, depth); err != nil {
			return err
		}
	}
	return nil
}

func (state *pathValidationState) validateNode(node Node, depth int) error {
	state.nodes++
	if state.nodes > MaximumNodes {
		return fmt.Errorf("an escalation path holds at most %d nodes", MaximumNodes)
	}
	if _, err := uuid.Parse(node.ID); err != nil {
		return fmt.Errorf("node id %q must be a UUID", node.ID)
	}
	if _, duplicate := state.ids[node.ID]; duplicate {
		return fmt.Errorf("node id %q is duplicated", node.ID)
	}
	state.ids[node.ID] = struct{}{}

	switch node.Type {
	case NodeLevel:
		if node.Conditions != nil || node.WorkingHoursID != "" || node.Then != nil || node.Else != nil || node.StandardOnly {
			return fmt.Errorf("level node %q carries fields from another node type", node.ID)
		}
		if len(node.Targets) == 0 || len(node.Targets) > MaximumTargetsPerLevel {
			return fmt.Errorf("level node %q requires between 1 and %d targets", node.ID, MaximumTargetsPerLevel)
		}
		seen := make(map[Target]struct{}, len(node.Targets))
		for _, target := range node.Targets {
			if err := target.Validate(state.kind); err != nil {
				return fmt.Errorf("level node %q: %w", node.ID, err)
			}
			if _, duplicate := seen[target]; duplicate {
				return fmt.Errorf("level node %q repeats a target", node.ID)
			}
			seen[target] = struct{}{}
		}
		return validateEscalationDuration(fmt.Sprintf("level node %q escalate_after", node.ID), node.EscalateAfter)
	case NodeIfElse:
		if node.Targets != nil || node.EscalateAfter != "" || node.RaiseUrgency || node.WorkingHoursID != "" || node.StandardOnly {
			return fmt.Errorf("if_else node %q carries fields from another node type", node.ID)
		}
		if len(node.Conditions) == 0 || len(node.Conditions) > MaximumConditionsPerBranch {
			return fmt.Errorf("if_else node %q requires between 1 and %d conditions", node.ID, MaximumConditionsPerBranch)
		}
		for _, condition := range node.Conditions {
			if err := state.validateCondition(node.ID, condition); err != nil {
				return err
			}
		}
		if err := state.validateNodes(node.Then, depth+1); err != nil {
			return err
		}
		return state.validateNodes(node.Else, depth+1)
	case NodeDefer:
		if node.Targets != nil || node.EscalateAfter != "" || node.RaiseUrgency || node.Conditions != nil || node.Then != nil || node.Else != nil {
			return fmt.Errorf("defer node %q carries fields from another node type", node.ID)
		}
		return state.requireWorkingHours(string(node.Type)+" node", node.ID, node.WorkingHoursID)
	default:
		return fmt.Errorf("node %q has unknown type %q", node.ID, node.Type)
	}
}

func (state *pathValidationState) validateCondition(nodeID string, condition Condition) error {
	switch condition.Type {
	case ConditionWorkingHours:
		if condition.Urgency != "" || condition.When != nil {
			return fmt.Errorf("if_else node %q working_hours condition carries fields from another kind", nodeID)
		}
		return state.requireWorkingHours("if_else node", nodeID, condition.WorkingHoursID)
	case ConditionUrgency:
		if condition.WorkingHoursID != "" || condition.Active || condition.When != nil {
			return fmt.Errorf("if_else node %q urgency condition carries fields from another kind", nodeID)
		}
		if condition.Urgency != UrgencyStandard && condition.Urgency != UrgencyInterrupt {
			return fmt.Errorf("if_else node %q urgency condition must test standard or interrupt", nodeID)
		}
		return nil
	case ConditionRequest:
		if condition.WorkingHoursID != "" || condition.Active || condition.Urgency != "" {
			return fmt.Errorf("if_else node %q request condition carries fields from another kind", nodeID)
		}
		if condition.When == nil {
			return fmt.Errorf("if_else node %q request condition requires a when clause", nodeID)
		}
		if err := approvalblock.ValidateCondition(*condition.When); err != nil {
			return fmt.Errorf("if_else node %q request condition: %w", nodeID, err)
		}
		if duplicate := collectConditionIDs(*condition.When, state.conditionIDs); duplicate != "" {
			return fmt.Errorf("condition id %q is duplicated across the path", duplicate)
		}
		return nil
	default:
		return fmt.Errorf("if_else node %q has unknown condition type %q", nodeID, condition.Type)
	}
}

func (state *pathValidationState) requireWorkingHours(kind, nodeID, workingHoursID string) error {
	if _, known := state.sets[workingHoursID]; !known {
		return fmt.Errorf("%s %q references unknown working hours %q", kind, nodeID, workingHoursID)
	}
	return nil
}

func validateEscalationDuration(field, raw string) error {
	duration, err := time.ParseDuration(raw)
	if err != nil || duration < MinimumEscalateAfter || duration > MaximumEscalateAfter {
		return fmt.Errorf("%s must be a duration between %s and %s", field, MinimumEscalateAfter, MaximumEscalateAfter)
	}
	return nil
}

func collectConditionIDs(condition approvalblock.Condition, ids map[string]struct{}) string {
	if _, duplicate := ids[condition.ID]; duplicate {
		return condition.ID
	}
	ids[condition.ID] = struct{}{}
	for _, child := range condition.All {
		if duplicate := collectConditionIDs(child, ids); duplicate != "" {
			return duplicate
		}
	}
	for _, child := range condition.Any {
		if duplicate := collectConditionIDs(child, ids); duplicate != "" {
			return duplicate
		}
	}
	if condition.Some != nil {
		if duplicate := collectConditionIDs(condition.Some.Where, ids); duplicate != "" {
			return duplicate
		}
	}
	if condition.Every != nil {
		if duplicate := collectConditionIDs(condition.Every.Where, ids); duplicate != "" {
			return duplicate
		}
	}
	return ""
}

func validateDocumentSize(document any) error {
	serialized, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("document must be serializable: %w", err)
	}
	if len(serialized) > MaximumDocumentBytes {
		return fmt.Errorf("document exceeds %d bytes", MaximumDocumentBytes)
	}
	return nil
}
