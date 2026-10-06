package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/approvalblock"
	"github.com/withHumanAI/withHuman/domain"
)

// scriptedModel answers CompleteTurn from a queue and records every
// input, so a test can script the model's tool calls and check what the
// model was shown.
type scriptedModel struct {
	mu      sync.Mutex
	replies []application.ToolUseOutput
	err     error
	Inputs  []application.ToolUseInput
}

func (m *scriptedModel) Identity() application.ModelIdentity {
	return application.ModelIdentity{Provider: "scripted", Model: "scripted-assistant"}
}

func (m *scriptedModel) CompleteTurn(_ context.Context, input application.ToolUseInput) (application.ToolUseOutput, error) {
	if err := input.Validate(); err != nil {
		return application.ToolUseOutput{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Inputs = append(m.Inputs, input)
	if m.err != nil {
		return application.ToolUseOutput{}, m.err
	}
	if len(m.replies) == 0 {
		return application.ToolUseOutput{}, errors.New("no scripted reply")
	}
	reply := m.replies[0]
	m.replies = m.replies[1:]
	return reply, nil
}

func toolCall(id, name, input string) application.ToolUseOutput {
	return application.ToolUseOutput{StopReason: application.ToolUseStopToolUse, Blocks: []application.ToolUseBlock{
		{Type: application.ToolUseText, Text: "Working on it."},
		{Type: application.ToolUseCall, ID: id, Name: name, Input: json.RawMessage(input)},
	}}
}

func answer(text string) application.ToolUseOutput {
	return application.ToolUseOutput{StopReason: application.ToolUseStopEndTurn, Blocks: []application.ToolUseBlock{{Type: application.ToolUseText, Text: text}}}
}

// grammarStub stands in for the deployment's block grammar appendix.
type grammarStub struct{ grammar string }

func (g *grammarStub) Grammar() string { return g.grammar }

// provisioned drives a session to provisioning with codex connected, so
// its agent exists for the assistant to write.
func provisioned(t *testing.T, h *harness) domain.OnboardingSession {
	t.Helper()
	ctx := context.Background()
	begun, err := h.service.Begin(ctx, h.principal)
	if err != nil {
		t.Fatal(err)
	}
	exchanged, err := h.service.Exchange(ctx, begun.SetupCode, domain.OnboardingMachine{Hostname: "box"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := h.service.ReportDetections(ctx, exchanged.Session, detections("codex"))
	if err != nil {
		t.Fatal(err)
	}
	session, err = h.service.SelectAgents(ctx, h.principal, []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	session, err = h.service.ReportTools(ctx, session, map[string]domain.OnboardingReportedTools{"codex": {
		Builtin: []domain.OnboardingReportedTool{{Name: "Bash", InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"number"}},"required":["command"]}`)}, {Name: "Read"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.ConfirmChannels(ctx, h.principal); err != nil {
		t.Fatal(err)
	}
	session, err = h.service.Provision(ctx, h.principal)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestAssistantWritesRulesIntoTheAgentsBranch(t *testing.T) {
	ctx := context.Background()
	h := newHarness()
	model := &scriptedModel{replies: []application.ToolUseOutput{
		toolCall("c1", getRulesTool, `{"agent_slug":"codex"}`),
		toolCall("c2", updateRulesTool, `{"agent_slug":"codex","blocks":[{"id":"codex-review-kubectl","name":"Review kubectl changes","type":"branch","enabled":true,"timeout":"50ms","max_attempts":1,"config":{"when":{"id":"5b7d7a8e-2d4e-4d3a-9a1c-1f2e3d4c5b6a","field":"/request/tool","operator":"equals","value":"Bash"},"blocks":[{"id":"codex-ask-about-kubectl","name":"Ask about kubectl changes","type":"outcome","enabled":true,"timeout":"50ms","max_attempts":1,"config":{"outcome":"human","reason":"kubectl changes wait."}}],"reason":""}}]}`),
		toolCall("c3", previewRulesTool, `{"agent_slug":"codex","blocks":[],"sample":{"tool":"Bash","arguments":{"command":"kubectl delete pod demo"}}}`),
		toolCall("c4", "list_agents", `{}`),
		toolCall("c5", updateRulesTool, `{"agent_slug":"claude-code","blocks":[]}`),
		toolCall("c6", updateRulesTool, `{"blocks":[]}`),
		toolCall("c7", setTestPromptTool, `{"agent_slug":"codex","tool":"Bash","prompt":"Run kubectl delete pod demo --dry-run=client and show me the output","sample_arguments":{"command":"kubectl delete pod demo --dry-run=client"}}`),
		answer("Codex now waits for you on kubectl commands that change the cluster; everything else runs. The test asks it to dry-run a pod deletion."),
	}}
	h.service.RegisterAssistantModel(model)
	h.service.RegisterAssistantGrammar(&grammarStub{grammar: "\n## scripted\n\nA kind this deployment adds.\n"})
	if !h.service.AssistantEnabled() {
		t.Fatal("assistant should be enabled with a model and a grammar")
	}
	provisioned(t, h)

	session, err := h.service.Ask(ctx, h.principal, "  Only kubectl commands that change the cluster should wait for me.  ")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	// The transcript: the person's message, then the assistant's answer
	// with one action per tool call, refusals included.
	if len(session.Assistant) != 2 {
		t.Fatalf("transcript: %+v", session.Assistant)
	}
	if person := session.Assistant[0]; person.Role != domain.OnboardingAssistantUser || person.Text != "Only kubectl commands that change the cluster should wait for me." {
		t.Fatalf("person's message: %+v", person)
	}
	reply := session.Assistant[1]
	if reply.Role != domain.OnboardingAssistantAssistant || reply.Status != domain.OnboardingAssistantDone || !strings.HasPrefix(reply.Text, "Codex now waits") {
		t.Fatalf("assistant's message: %+v", reply)
	}
	var summaries []string
	for _, action := range reply.Actions {
		summaries = append(summaries, action.Summary)
	}
	want := []string{
		"Read Codex's current rules",
		"Wrote Codex's rules (revision 3)",
		"Tested a Bash call for Codex: waits for a person",
		"Refused a list_agents call: list_agents is not available here.",
		"Refused a update_agent_rules call for claude-code: Only the agents this setup connected can be changed here: codex.",
		"Refused a update_agent_rules call: Only the agents this setup connected can be changed here: codex.",
		"Chose Codex's test: a Bash call",
	}
	if strings.Join(summaries, "\n") != strings.Join(want, "\n") {
		t.Fatalf("actions:\n%s\nwant:\n%s", strings.Join(summaries, "\n"), strings.Join(want, "\n"))
	}
	for index, action := range reply.Actions {
		if refused := strings.HasPrefix(action.Summary, "Refused"); action.OK == refused {
			t.Fatalf("action %d ok=%v: %+v", index, action.OK, action)
		}
	}

	// The write replaced codex's branch alone, with the person's path on
	// the human exit the model left bare, and activated the entry.
	entry := h.pipelines.entryBlocks()
	branch, ok := RuntimeBranchOf(entry, "codex")
	if !ok || len(branch.Branch.Blocks) != 1 || branch.Branch.Blocks[0].ID != "codex-review-kubectl" || branch.Branch.Blocks[0].Branch == nil {
		t.Fatalf("codex branch after the write: %+v", entry)
	}
	review := branch.Branch.Blocks[0].Branch
	if len(review.Blocks) != 1 || review.Blocks[0].Outcome == nil || review.Blocks[0].Outcome.EscalationPath != "notify-chris-battarbee" || !review.Blocks[0].Outcome.AskAgentOwner {
		t.Fatalf("the nested human exit must carry the person's path: %+v", review.Blocks)
	}
	if review.EscalationPath != "notify-chris-battarbee" || !review.AskAgentOwner {
		t.Fatalf("the rule's own end must carry the person's path: %+v", review)
	}
	if h.pipelines.activated[EntryPipelineKey] != 2 {
		t.Fatalf("the write must activate against the provisioned revision, got %v", h.pipelines.activated)
	}

	// The preview and the test check both ran the whole entry document
	// as the codex agent; the test command follows the assistant's
	// prompt once the sample was shown to reach a person.
	if len(h.pipelines.previews) != 2 || h.pipelines.previews[0].Agent.Slug != "codex" || h.pipelines.previews[1].Request.Tool != "Bash" || !strings.Contains(string(h.pipelines.previews[1].Request.Arguments), "dry-run") {
		t.Fatalf("sample previews: %+v", h.pipelines.previews)
	}
	if _, ok := RuntimeBranchOf(h.pipelines.previewed, "codex"); !ok {
		t.Fatalf("the test check must preview the entry document with the branch: %+v", h.pipelines.previewed)
	}
	command := session.TestCommands["codex"]
	if command.Tool != "Bash" || !strings.Contains(command.Command, "kubectl delete pod demo --dry-run=client") || !strings.HasPrefix(command.Command, "codex exec") {
		t.Fatalf("test command: %+v", command)
	}

	// What the model was shown: the pipeline tools plus the setup's own,
	// never the rest of the catalog; the agents and their tools in the
	// prompt; and a context that pairs every call with its result.
	first := model.Inputs[0]
	var names []string
	for _, tool := range first.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "get_agent_rules,update_agent_rules,preview_agent_rules,set_test_prompt" {
		t.Fatalf("tools offered: %v", names)
	}
	for _, needle := range []string{"agent slug `codex`", "- Bash (built-in, shell): Run shell commands. Arguments: command, timeout. Waits", "- Read (built-in)", "notify-chris-battarbee", "# Pipeline blocks", "## scripted\n\nA kind this deployment adds.", "# Conditions"} {
		if !strings.Contains(first.SystemPrompt, needle) {
			t.Fatalf("system prompt lacks %q:\n%s", needle, first.SystemPrompt)
		}
	}
	last := model.Inputs[len(model.Inputs)-1]
	if got := len(last.Messages); got != 15 {
		t.Fatalf("context should hold the person's message and seven call/result pairs, got %d messages", got)
	}
	if result := last.Messages[14].Blocks[0]; result.Type != application.ToolUseResult || result.ToolUseID != "c7" || result.IsError || !strings.Contains(result.Content, `"ok":true`) {
		t.Fatalf("last result: %+v", result)
	}
	if refused := last.Messages[8].Blocks[0]; !refused.IsError || refused.ToolUseID != "c4" {
		t.Fatalf("refusal should reach the model as an error result: %+v", refused)
	}
	var stored []application.ToolUseMessage
	if err := json.Unmarshal(session.AssistantContext, &stored); err != nil || len(stored) != 16 {
		t.Fatalf("persisted context: %v %d", err, len(stored))
	}

	// The next message continues from the stored context.
	model.replies = []application.ToolUseOutput{answer("Nothing else changed.")}
	session, err = h.service.Ask(ctx, h.principal, "Thanks.")
	if err != nil {
		t.Fatalf("second Ask: %v", err)
	}
	if len(session.Assistant) != 4 || len(model.Inputs[len(model.Inputs)-1].Messages) != 17 {
		t.Fatalf("second turn: %d messages, %d in context", len(session.Assistant), len(model.Inputs[len(model.Inputs)-1].Messages))
	}
}

func TestAssistantGuards(t *testing.T) {
	ctx := context.Background()
	h := newHarness()
	if _, err := h.service.Ask(ctx, h.principal, "hello"); !errors.Is(err, ErrAssistantUnavailable) {
		t.Fatalf("without a model: %v", err)
	}
	model := &scriptedModel{}
	h.service.RegisterAssistantModel(model)
	h.service.RegisterAssistantGrammar(&grammarStub{})

	// Before provisioning the agents do not exist.
	begun, _ := h.service.Begin(ctx, h.principal)
	if _, err := h.service.Ask(ctx, h.principal, "hello"); !errors.Is(err, domain.ErrOnboardingState) {
		t.Fatalf("before the agents exist: %v", err)
	}
	if _, err := h.service.Ask(ctx, h.principal, "   "); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty message: %v", err)
	}
	h.sessions.sessions[begun.Session.ID].Status = domain.OnboardingInstalled

	// A model failure is recorded on the transcript and reported.
	model.err = application.ErrModelUnavailable
	session, err := h.service.Ask(ctx, h.principal, "Gate everything.")
	if !errors.Is(err, ErrAssistantFailed) || !errors.Is(err, application.ErrModelUnavailable) {
		t.Fatalf("model failure: %v", err)
	}
	if len(session.Assistant) != 2 || session.Assistant[1].Status != domain.OnboardingAssistantFailed || !strings.Contains(session.Assistant[1].Error, "could not be reached") {
		t.Fatalf("failed message: %+v", session.Assistant)
	}

	// A message still running blocks the next one until it is stale.
	model.err = nil
	model.replies = []application.ToolUseOutput{answer("Done.")}
	stored := h.sessions.sessions[begun.Session.ID]
	stored.Assistant[1].Status = domain.OnboardingAssistantRunning
	stored.Assistant[1].At = h.service.clock.Now()
	if _, err := h.service.Ask(ctx, h.principal, "Again."); !errors.Is(err, ErrAssistantBusy) {
		t.Fatalf("busy: %v", err)
	}
	stored.Assistant[1].At = h.service.clock.Now().Add(-assistantBusyWindow)
	if _, err := h.service.Ask(ctx, h.principal, "Again."); err != nil {
		t.Fatalf("stale running message should not block: %v", err)
	}

	// Starting over forgets the messages and the context; the running
	// guard applies to it too.
	cleared, err := h.service.ClearAssistant(ctx, h.principal)
	if err != nil || len(cleared.Assistant) != 0 || string(cleared.AssistantContext) != "[]" {
		t.Fatalf("ClearAssistant: %v %+v %s", err, cleared.Assistant, cleared.AssistantContext)
	}

	// A turn that never answers within the step budget fails clearly.
	model.replies = nil
	for step := 0; step < maxAssistantSteps; step++ {
		model.replies = append(model.replies, toolCall("loop", getRulesTool, `{"agent_slug":"codex"}`))
	}
	h.sessions.sessions[begun.Session.ID].SelectedRuntimes = []string{"codex"}
	session, err = h.service.Ask(ctx, h.principal, "Loop.")
	if !errors.Is(err, ErrAssistantFailed) || !strings.Contains(session.Assistant[len(session.Assistant)-1].Error, "ran out of steps") {
		t.Fatalf("step budget: %v %+v", err, session.Assistant[len(session.Assistant)-1])
	}
}

func TestAssistantTestPromptRefusesCallsTheRulesWouldNotHold(t *testing.T) {
	ctx := context.Background()
	h := newHarness()
	model := &scriptedModel{replies: []application.ToolUseOutput{
		toolCall("c1", setTestPromptTool, `{"agent_slug":"codex","tool":"Bash","prompt":"Run echo hi","sample_arguments":{"command":"echo hi"}}`),
		toolCall("c2", setTestPromptTool, `{"agent_slug":"codex","tool":"nope","prompt":"x","sample_arguments":{}}`),
		answer("I could not pick a test."),
	}}
	h.service.RegisterAssistantModel(model)
	h.service.RegisterAssistantGrammar(&grammarStub{})
	before := provisioned(t, h).TestCommands
	h.pipelines.previewOutcome = "approve"
	session, err := h.service.Ask(ctx, h.principal, "Only kubectl.")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	actions := session.Assistant[1].Actions
	if len(actions) != 2 || actions[0].OK || !strings.Contains(actions[0].Summary, "runs on its own") || actions[1].OK || !strings.Contains(actions[1].Summary, "not a tool Codex can call") {
		t.Fatalf("actions: %+v", actions)
	}
	if session.TestCommands["codex"] != before["codex"] {
		t.Fatalf("a refused prompt must leave the test command alone: %+v", session.TestCommands)
	}
}

func TestTrimAssistantContext(t *testing.T) {
	var history []application.ToolUseMessage
	for turn := 0; turn < assistantContextTurns+2; turn++ {
		history = append(history,
			application.ToolUseMessage{Role: application.ToolUseRoleUser, Blocks: []application.ToolUseBlock{{Type: application.ToolUseText, Text: "q"}}},
			application.ToolUseMessage{Role: application.ToolUseRoleAssistant, Blocks: []application.ToolUseBlock{{Type: application.ToolUseCall, ID: "c", Name: "get_pipeline"}}},
			application.ToolUseMessage{Role: application.ToolUseRoleUser, Blocks: []application.ToolUseBlock{{Type: application.ToolUseResult, ToolUseID: "c", Content: "{}"}}},
			application.ToolUseMessage{Role: application.ToolUseRoleAssistant, Blocks: []application.ToolUseBlock{{Type: application.ToolUseText, Text: "a"}}},
		)
	}
	trimmed := trimAssistantContext(history)
	if len(trimmed) != assistantContextTurns*4 || trimmed[0].Blocks[0].Type != application.ToolUseText {
		t.Fatalf("trimmed to %d messages starting with %+v", len(trimmed), trimmed[0])
	}
}

func TestSetGatedToolsRewritesTheStartingRules(t *testing.T) {
	ctx := context.Background()
	h := newHarness()
	if _, err := h.service.SetGatedTools(ctx, h.principal, "codex", []string{"Bash"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("without a session: %v", err)
	}
	provisioned(t, h)
	var step *StepError
	if _, err := h.service.SetGatedTools(ctx, h.principal, "codex", []string{"nope"}); !errors.As(err, &step) || step.Code != "tool_not_discovered" {
		t.Fatalf("unknown tool: %v", err)
	}
	if _, err := h.service.SetGatedTools(ctx, h.principal, "claude-code", []string{"Bash"}); !errors.As(err, &step) || step.Code != "runtime_not_selected" {
		t.Fatalf("foreign runtime: %v", err)
	}
	session, err := h.service.SetGatedTools(ctx, h.principal, "codex", []string{"Read", "Bash", "Read"})
	if err != nil {
		t.Fatalf("SetGatedTools: %v", err)
	}
	if got := GatedToolsOf(h.pipelines.entryBlocks(), "codex"); strings.Join(got, ",") != "Bash,Read" {
		t.Fatalf("gated tools: %v", got)
	}
	if h.pipelines.activated[EntryPipelineKey] != 2 {
		t.Fatalf("activation should carry the provisioned revision, got %v", h.pipelines.activated[EntryPipelineKey])
	}
	if command := session.TestCommands["codex"]; command.Tool != "Bash" {
		t.Fatalf("test command should follow the choice: %+v", command)
	}
	session, err = h.service.SetGatedTools(ctx, h.principal, "codex", nil)
	if err != nil {
		t.Fatalf("empty list: %v", err)
	}
	if _, ok := session.TestCommands["codex"]; ok || len(GatedToolsOf(h.pipelines.entryBlocks(), "codex")) != 0 {
		t.Fatalf("nothing gated should leave no test: %+v", session.TestCommands)
	}
}

// logLines decodes a JSON log buffer into one map per line.
func logLines(t *testing.T, buffer *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line %q: %v", raw, err)
		}
		lines = append(lines, line)
	}
	return lines
}

func findLog(lines []map[string]any, message string) map[string]any {
	for _, line := range lines {
		if line["msg"] == message {
			return line
		}
	}
	return nil
}

// A failed turn leaves enough in the logs to tell why: the step, the
// provider's stop reason and response id, what each call spent, and the
// tool calls that ran before it.
func TestAssistantLogsEachStep(t *testing.T) {
	ctx := context.Background()
	h := newHarness()
	var buffer bytes.Buffer
	h.service.logger = slog.New(slog.NewJSONHandler(&buffer, nil))
	// The second call spends its whole budget reasoning and answers
	// nothing, which the adapter rejects with the response's usage.
	model := &rejectingModel{
		scriptedModel: &scriptedModel{replies: []application.ToolUseOutput{
			withUsage(toolCall("c1", getRulesTool, `{"agent_slug":"codex"}`), 9000, 200, 100),
		}},
		after:    1,
		rejected: application.ToolUseOutput{StopReason: application.ToolUseStopMaxTokens, ProviderStop: "length", ResponseID: "chatcmpl-9", InputTokens: 9800, OutputTokens: 4096, ReasoningTokens: 4096},
		err:      errors.New("model returned no content"),
	}
	h.service.RegisterAssistantModel(model)
	h.service.RegisterAssistantGrammar(&grammarStub{})
	provisioned(t, h)
	buffer.Reset()

	if _, err := h.service.Ask(ctx, h.principal, "Gate kubectl changes."); !errors.Is(err, ErrAssistantFailed) {
		t.Fatalf("expected a failed turn: %v", err)
	}
	lines := logLines(t, &buffer)

	started := findLog(lines, "setup assistant turn started")
	if started == nil || started["agents"] != "codex" || started["model"] != "scripted-assistant" || started["system_prompt_bytes"].(float64) == 0 {
		t.Fatalf("turn started: %v", started)
	}
	call := findLog(lines, "setup assistant model call")
	if call == nil || call["step"].(float64) != 1 || call["tool_calls"] != getRulesTool || call["step_input_tokens"].(float64) != 9000 {
		t.Fatalf("model call: %v", call)
	}
	tool := findLog(lines, "setup assistant tool call")
	if tool == nil || tool["tool"] != getRulesTool || tool["agent_slug"] != "codex" || tool["ok"] != true || tool["step"].(float64) != 1 {
		t.Fatalf("tool call: %v", tool)
	}
	failed := findLog(lines, "setup assistant model call failed")
	if failed == nil || failed["step"].(float64) != 2 || failed["provider_stop"] != "length" || failed["response_id"] != "chatcmpl-9" ||
		failed["step_reasoning_tokens"].(float64) != 4096 || failed["max_output_tokens"].(float64) != assistantMaxOutputTokens || failed["error"] != "model returned no content" {
		t.Fatalf("failed model call: %v", failed)
	}
	turn := findLog(lines, "setup assistant turn failed")
	if turn == nil || turn["steps"].(float64) != 2 || turn["output_tokens"].(float64) != 4296 || turn["reasoning_tokens"].(float64) != 4196 || turn["actions"].(float64) != 1 {
		t.Fatalf("turn failed: %v", turn)
	}
}

func withUsage(output application.ToolUseOutput, input, out, reasoning int64) application.ToolUseOutput {
	output.InputTokens, output.OutputTokens, output.ReasoningTokens = input, out, reasoning
	return output
}

// rejectingModel answers from the script for the first calls, then
// fails the way an adapter rejects a response: with its usage and stop
// reason alongside the error.
type rejectingModel struct {
	*scriptedModel
	after    int
	calls    int
	rejected application.ToolUseOutput
	err      error
}

func (m *rejectingModel) CompleteTurn(ctx context.Context, input application.ToolUseInput) (application.ToolUseOutput, error) {
	m.calls++
	if m.calls > m.after {
		return m.rejected, m.err
	}
	return m.scriptedModel.CompleteTurn(ctx, input)
}

// A rule is a branch whose outcome block decides, so a preview decided
// by that nested block is decided inside the agent's rules.
func TestHoldsBlockFindsTheDecidingBlockOfANestedRule(t *testing.T) {
	ask := approvalblock.DefineOutcome("codex-ask-about-kubectl", approvalblock.Human, approvalblock.Reason("kubectl changes wait for a person."))
	rule := approvalblock.DefineBranch("codex-review-kubectl", []approvalblock.Definition{*ask},
		approvalblock.When(approvalblock.Field("/request/tool").Equals("Bash")))
	rest := approvalblock.DefineOutcome("codex-approve-the-rest", approvalblock.Approve, approvalblock.Reason("Everything else runs."))
	rules := []approvalblock.Definition{*rule, *rest}
	for _, key := range []string{"codex-review-kubectl", "codex-ask-about-kubectl", "codex-approve-the-rest"} {
		if !holdsBlock(rules, key) {
			t.Fatalf("%s is part of the agent's rules", key)
		}
	}
	if holdsBlock(rules, "organization-deny-deletes") {
		t.Fatal("a block outside the agent's branch is not part of its rules")
	}
}
