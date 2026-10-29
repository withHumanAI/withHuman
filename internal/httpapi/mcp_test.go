package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/authn"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/entitlements"
	"github.com/withHumanAI/withHuman/internal/approvalpipeline"
	"github.com/withHumanAI/withHuman/mcptools"
)

// The management MCP surface is a door onto /api/v1 for API keys: these
// tests pin that the door admits keys and nothing else, that every tool
// re-enters the product API under the key's own authority, and that the
// compositions (save then activate) and refusals reach the agent intact.

// mcpPipelineStore is an in-memory entry pipeline: revision 1 empty and
// active, every create appends, activation checks the expected number
// the way the real store does. Every other key is unknown.
type mcpPipelineStore struct {
	application.RequestApprovalPipelineRepository
	organization domain.ID
	revisions    []domain.RequestApprovalPipelineRevision
	blocks       map[domain.ID]domain.RequestApprovalBlock
	activations  []application.ActivateRequestApprovalPipelineRevisionInput
}

func newMCPPipelineStore(organization domain.ID) *mcpPipelineStore {
	return &mcpPipelineStore{
		organization: organization,
		revisions: []domain.RequestApprovalPipelineRevision{{
			ID: domain.ID(uuid.NewString()), OrganizationID: organization, PipelineKey: domain.EntryPipelineKey, Name: "Entry pipeline", Revision: 1, IsActive: true,
			BlockIDs: []domain.ID{}, CreatedByActorID: "membership-1", CreatedAt: time.Unix(80_000, 0).UTC(),
		}},
		blocks: map[domain.ID]domain.RequestApprovalBlock{},
	}
}

func (s *mcpPipelineStore) find(key string, revision int64) (domain.RequestApprovalPipelineRevision, bool) {
	if key != domain.EntryPipelineKey {
		return domain.RequestApprovalPipelineRevision{}, false
	}
	for _, candidate := range s.revisions {
		if candidate.Revision == revision {
			return candidate, true
		}
	}
	return domain.RequestApprovalPipelineRevision{}, false
}

func (s *mcpPipelineStore) CreateRequestApprovalPipelineRevision(_ context.Context, input application.CreateRequestApprovalPipelineRevisionInput) (domain.RequestApprovalPipelineRevision, error) {
	if input.PipelineKey != domain.EntryPipelineKey {
		return domain.RequestApprovalPipelineRevision{}, domain.ErrNotFound
	}
	revision := domain.RequestApprovalPipelineRevision{
		ID: input.ID, OrganizationID: input.OrganizationID, PipelineKey: input.PipelineKey, Name: input.Name, Revision: int64(len(s.revisions) + 1),
		BlockIDs: []domain.ID{}, DefaultEscalationPath: input.DefaultEscalationPath, CreatedByActorID: input.CreatedByActorID, CreatedAt: input.Now,
	}
	for _, block := range input.Blocks {
		s.blocks[block.ID] = domain.RequestApprovalBlock{
			ID: block.ID, OrganizationID: input.OrganizationID, BlockKey: block.BlockKey, Name: block.Name, BlockType: block.BlockType,
			ConfigVersion: block.ConfigVersion, Config: block.Config, TimeoutMS: block.TimeoutMS, MaxAttempts: block.MaxAttempts,
			ContentHash: strings.Repeat("c", 64), CreatedByActorID: input.CreatedByActorID, CreatedAt: input.Now,
		}
		revision.BlockIDs = append(revision.BlockIDs, block.ID)
	}
	s.revisions = append(s.revisions, revision)
	return revision, nil
}

func (s *mcpPipelineStore) GetActiveRequestApprovalPipelineRevision(_ context.Context, input application.GetActiveRequestApprovalPipelineRevisionInput) (domain.RequestApprovalPipelineRevision, error) {
	if input.PipelineKey != domain.EntryPipelineKey {
		return domain.RequestApprovalPipelineRevision{}, domain.ErrNotFound
	}
	for _, revision := range s.revisions {
		if revision.IsActive {
			return revision, nil
		}
	}
	return domain.RequestApprovalPipelineRevision{}, domain.ErrNotFound
}

func (s *mcpPipelineStore) GetRequestApprovalPipelineRevision(_ context.Context, input application.GetRequestApprovalPipelineRevisionInput) (domain.RequestApprovalPipelineRevision, error) {
	revision, ok := s.find(input.PipelineKey, input.Revision)
	if !ok {
		return domain.RequestApprovalPipelineRevision{}, domain.ErrNotFound
	}
	return revision, nil
}

func (s *mcpPipelineStore) GetRequestApprovalBlocks(_ context.Context, input application.GetRequestApprovalBlocksInput) ([]domain.RequestApprovalBlock, error) {
	blocks := make([]domain.RequestApprovalBlock, 0, len(input.BlockIDs))
	for _, id := range input.BlockIDs {
		block, ok := s.blocks[id]
		if !ok {
			return nil, domain.ErrNotFound
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}

func (s *mcpPipelineStore) ListActiveRequestApprovalPipelineRevisions(context.Context, application.ListActiveRequestApprovalPipelineRevisionsInput) ([]domain.RequestApprovalPipelineRevision, error) {
	return []domain.RequestApprovalPipelineRevision{}, nil
}

func (s *mcpPipelineStore) ActivateRequestApprovalPipelineRevision(_ context.Context, input application.ActivateRequestApprovalPipelineRevisionInput) (domain.RequestApprovalPipelineRevision, error) {
	s.activations = append(s.activations, input)
	target, ok := s.find(input.PipelineKey, input.Revision)
	if !ok {
		return domain.RequestApprovalPipelineRevision{}, domain.ErrNotFound
	}
	for index := range s.revisions {
		if s.revisions[index].IsActive && s.revisions[index].Revision != input.ExpectedActiveRevision {
			return domain.RequestApprovalPipelineRevision{}, domain.ErrPreconditionFailed
		}
	}
	for index := range s.revisions {
		s.revisions[index].IsActive = s.revisions[index].Revision == input.Revision
	}
	target.IsActive = true
	return target, nil
}

// mcpHarness is a full router whose only authenticator is the API key
// stub from api_keys_test.go: whk_<id>_secret is the owner narrowed to
// the pipeline permissions, and the organization variant is an
// ActorAPIKey holding the same.
type mcpHarness struct {
	server   *httptest.Server
	router   *gin.Engine
	auth     *authn.Service
	store    *mcpPipelineStore
	keys     *mcpAPIKeyStore
	token    string
	orgToken string
}

func newMCPHarness(t *testing.T, tools ...mcptools.Registration) *mcpHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organization := domain.ID(uuid.NewString())
	principal := domain.Principal{
		ActorType: domain.ActorHuman, ActorID: domain.ID(uuid.NewString()), OrganizationID: organization,
		MembershipID: domain.ID(uuid.NewString()),
		Auth:         domain.AuthContext{Method: "password", Assurance: "strong", AuthenticatedAt: time.Now(), SessionID: "session-1"},
	}.WithGrants(domain.GrantsForRoles([]domain.Role{domain.RoleOwner}))
	identities := &apiKeyIdentityStub{principal: principal}
	auth := authn.NewService(identities, nil, nil, nil, entitlements.NewUnlimited(), nil, apiKeyClock{}, authn.ServiceConfig{SessionTTL: time.Hour})
	keys := &mcpAPIKeyStore{principal: principal}
	auth.RegisterAPIKeys(keys)
	store := newMCPPipelineStore(organization)
	registry := approvalpipeline.NewBlockRegistry()
	router := NewRouter(Dependencies{
		Auth: auth, Identities: identities, Logger: logger,
		Pipelines: approvalpipeline.NewManagementService(store, approvalpipeline.NewCompiledPipelineState(store, registry), pipelineHTTPClock{}),
		Config:    Config{PublicOrigin: "http://localhost:8080", Version: "test"},
		MCPTools:  tools,
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return &mcpHarness{
		server: server, router: router, auth: auth, store: store, keys: keys,
		token:    "whk_" + apiKeyTestKeyID + "_secret",
		orgToken: "who_" + apiKeyTestKeyID + "_secret",
	}
}

// mcpAPIKeyStore resolves whk_ and who_ tokens with the same id and
// secret: the personal one as the person with the pipeline permissions,
// the organization one as an ActorAPIKey with the same grants.
type mcpAPIKeyStore struct {
	apiKeyStoreStub
	principal domain.Principal
	mu        sync.Mutex
	uses      []application.RecordAPIKeyUseInput
}

func (s *mcpAPIKeyStore) RecordAPIKeyUse(_ context.Context, input application.RecordAPIKeyUseInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uses = append(s.uses, input)
	return nil
}

func (s *mcpAPIKeyStore) recordedUses() []application.RecordAPIKeyUseInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]application.RecordAPIKeyUseInput(nil), s.uses...)
}

func (s *mcpAPIKeyStore) ResolveAPIKey(_ context.Context, input application.ResolveAPIKeyInput) (domain.Principal, error) {
	if string(input.KeyID) != apiKeyTestKeyID || string(input.SecretHash) != string(authn.HashToken("secret")) {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	principal := s.principal
	principal.Auth = domain.AuthContext{Method: domain.AuthMethodAPIKey, Assurance: "strong", AuthenticatedAt: input.Now, CredentialID: input.KeyID}
	if input.Kind == domain.APIKeyOrganization {
		principal.ActorType, principal.ActorID, principal.MembershipID = domain.ActorAPIKey, input.KeyID, ""
	}
	return principal.WithGrants(domain.NarrowGrants(s.principal.Grants, []domain.Permission{
		domain.PermissionPipelineRead, domain.PermissionPipelineWrite, domain.PermissionPipelineActivate, domain.PermissionTeamWrite,
	})), nil
}

type mcpBearerTransport struct{ token string }

func (t mcpBearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(request)
}

func (h *mcpHarness) connect(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: h.server.URL + MCPPath, HTTPClient: &http.Client{Transport: mcpBearerTransport{token: token}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func (h *mcpHarness) call(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func mcpText(result *mcp.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if block, ok := content.(*mcp.TextContent); ok {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

func mcpStructured(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool refused: %s", mcpText(result))
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("structured content %s: %v", encoded, err)
	}
	return decoded
}

func TestMCPAdmitsAPIKeysOnly(t *testing.T) {
	h := newMCPHarness(t)
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	post := func(bearer string, cookie bool) *http.Response {
		request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, h.server.URL+MCPPath, strings.NewReader(initialize))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie {
			request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: apiKeyCookieToken})
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	if response := post("", false); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no bearer = %d, want 401", response.StatusCode)
	}
	// A browser session never opens the MCP surface, even a valid one.
	if response := post("", true); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cookie only = %d, want 401", response.StatusCode)
	}
	if response := post("whk_"+apiKeyTestKeyID+"_wrong", false); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong secret = %d, want 401", response.StatusCode)
	}
	response := post(h.token, false)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("initialize with key = %d", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), `"instructions"`) || !strings.Contains(string(body), `"withhuman"`) {
		t.Fatalf("initialize answer lacks the server identity and instructions: %s", body)
	}
	// Stateless: no standalone stream and nothing to terminate.
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		request, _ := http.NewRequestWithContext(t.Context(), method, h.server.URL+MCPPath, nil)
		request.Header.Set("Authorization", "Bearer "+h.token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s = %d, want 405", method, response.StatusCode)
		}
	}
}

func TestMCPKeyUseNamesTheToolCalled(t *testing.T) {
	h := newMCPHarness(t)
	session := h.connect(t, h.token)
	h.call(t, session, "get_pipeline", map[string]any{"pipeline_key": "entry"})
	type use struct{ method, route, mcpMethod, mcpTool string }
	var got []use
	for _, recorded := range h.keys.recordedUses() {
		got = append(got, use{recorded.Method, recorded.Route, recorded.MCPMethod, recorded.MCPTool})
	}
	// The MCP request names its tool; the loopback call it makes is
	// recorded as the product API route it reached.
	for _, want := range []use{
		{http.MethodPost, MCPPath, "tools/call", "get_pipeline"},
		{http.MethodGet, "/api/v1/request-approval-pipelines/:pipeline_key", "", ""},
	} {
		if !slices.Contains(got, want) {
			t.Errorf("key uses %v lack %v", got, want)
		}
	}
	for _, recorded := range got {
		if recorded.route == MCPPath && recorded.mcpMethod == "" {
			t.Errorf("an MCP request was recorded without its method: %v", recorded)
		}
	}
}

func TestPeekMCPCallRestoresTheBody(t *testing.T) {
	for _, test := range []struct {
		name, body string
		want       mcpCallUse
		ok         bool
	}{
		{"tool call", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_pipeline","arguments":{"pipeline_key":"entry"}}}`, mcpCallUse{Method: "tools/call", Tool: "get_pipeline"}, true},
		{"list", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, mcpCallUse{Method: "tools/list"}, true},
		{"a name outside tools/call is not a tool", `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"x"}}`, mcpCallUse{Method: "prompts/get"}, true},
		{"overlong tool", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + strings.Repeat("x", application.MaxMCPUseNameLength+1) + `"}}`, mcpCallUse{Method: "tools/call"}, true},
		{"batch", `[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]`, mcpCallUse{}, false},
		{"not json", `nope`, mcpCallUse{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, MCPPath, strings.NewReader(test.body))
			got, ok := peekMCPCall(request)
			if got != test.want || ok != test.ok {
				t.Fatalf("peekMCPCall = %v %v, want %v %v", got, ok, test.want, test.ok)
			}
			if body, _ := io.ReadAll(request.Body); string(body) != test.body {
				t.Fatalf("body after peeking = %q", body)
			}
		})
	}
}

func TestMCPListsTheManagementCatalog(t *testing.T) {
	h := newMCPHarness(t)
	session := h.connect(t, h.token)
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]*mcp.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		names[tool.Name] = tool
	}
	for _, want := range []string{
		"list_agents", "get_agent", "create_agent", "update_agent", "archive_agent", "restore_agent", "update_agent_instance",
		"create_agent_instance", "create_provisioner_token", "revoke_provisioner_token", "revoke_agent_credential",
		"list_tool_catalog", "get_tool_catalog_entry", "list_tool_definitions",
		"list_members", "update_member", "remove_member", "list_roles", "list_permissions", "list_member_permission_policies",
		"list_teams", "get_team", "create_team", "rename_team", "archive_team", "set_team_members", "list_team_permission_policies",
		"get_team_escalation_policy", "list_team_escalation_policy_revisions", "get_team_escalation_policy_revision",
		"update_team_escalation_policy", "activate_team_escalation_policy_revision", "archive_team_escalation_policy",
		"list_pipelines", "get_pipeline", "create_pipeline", "archive_pipeline", "list_pipeline_uses", "list_pipeline_revisions", "get_pipeline_revision",
		"get_pipeline_revision_references", "update_pipeline", "activate_pipeline_revision", "preview_pipeline",
		"list_escalation_paths", "get_escalation_path", "list_escalation_path_revisions", "get_escalation_path_revision", "list_escalation_path_uses",
		"create_escalation_path", "update_escalation_path", "activate_escalation_path_revision", "archive_escalation_path",
		"list_webhook_endpoints", "get_webhook_endpoint", "list_webhook_endpoint_revisions", "get_webhook_endpoint_revision", "list_webhook_endpoint_uses",
		"save_webhook_endpoint", "activate_webhook_endpoint_revision", "test_webhook_endpoint", "archive_webhook_endpoint", "rotate_webhook_endpoint_secret",
		"read_reference",
	} {
		tool, ok := names[want]
		if !ok {
			t.Errorf("tool %s missing", want)
			continue
		}
		if tool.Description == "" || tool.Annotations == nil {
			t.Errorf("tool %s lacks a description or annotations", want)
		}
	}
	// Directories list a connector only when every tool has a title and
	// says whether it reads or changes data.
	for _, tool := range listed.Tools {
		if tool.Title == "" || tool.Annotations == nil || tool.Annotations.Title != tool.Title {
			t.Errorf("tool %s has title %q, want it repeated in its annotations", tool.Name, tool.Title)
			continue
		}
		if !tool.Annotations.ReadOnlyHint && tool.Annotations.DestructiveHint == nil {
			t.Errorf("tool %s changes data but does not say whether destructively", tool.Name)
		}
	}
	if got := names["list_pipelines"].Title; got != "List pipelines" {
		t.Errorf("list_pipelines title = %q", got)
	}
	// The published skills (skills/*/SKILL.md) name these tools; a
	// renamed or removed tool must take the skill's text with it.
	skills, err := filepath.Glob(filepath.Join("..", "..", "skills", "*", "SKILL.md"))
	if err != nil || len(skills) == 0 {
		t.Fatalf("no skills found: %v", err)
	}
	toolMention := regexp.MustCompile("`((?:list|get|create|update|delete|remove|archive|restore|activate|preview|save|set|read|rename|revoke|test|rotate)_[a-z_]+)`")
	for _, path := range skills {
		text, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range toolMention.FindAllStringSubmatch(string(text), -1) {
			if names[match[1]] == nil {
				t.Errorf("%s names %s, which the catalog does not serve", path, match[1])
			}
		}
	}
	if !names["get_pipeline"].Annotations.ReadOnlyHint || names["update_pipeline"].Annotations.ReadOnlyHint {
		t.Fatal("read-only hints do not match the tools' effects")
	}
	for _, name := range []string{"archive_agent", "revoke_agent_credential", "remove_member", "archive_team", "rotate_webhook_endpoint_secret"} {
		if hint := names[name].Annotations.DestructiveHint; hint == nil || !*hint {
			t.Errorf("%s ends something for good but is not marked destructive", name)
		}
	}
	for _, name := range []string{"update_agent", "update_member", "set_team_members", "activate_pipeline_revision"} {
		if hint := names[name].Annotations.DestructiveHint; hint == nil || !*hint {
			t.Errorf("%s replaces a value but is not marked destructive", name)
		}
	}
	for _, name := range []string{"create_agent", "update_pipeline", "test_webhook_endpoint"} {
		if hint := names[name].Annotations.DestructiveHint; hint == nil || *hint {
			t.Errorf("%s only adds but is marked destructive", name)
		}
	}
	// Routes a key can never use have no tool: handing out authority
	// stays a person's act in the browser.
	for _, name := range []string{"set_member_role", "create_member_permission_policy", "create_invitation"} {
		if names[name] != nil {
			t.Errorf("%s is served, but its route admits browser sessions only", name)
		}
	}
	schema, _ := json.Marshal(names["update_pipeline"].InputSchema)
	for _, property := range []string{`"pipeline_key"`, `"name"`, `"blocks"`, `"default_escalation_path"`, `"activate"`} {
		if !strings.Contains(string(schema), property) {
			t.Errorf("update_pipeline schema lacks %s: %s", property, schema)
		}
	}
}

// An edition's tools join core's catalog and reach its own routes the
// way core's do: in process, with the caller's bearer, refusals relayed.
func TestMCPServesTheEditionsTools(t *testing.T) {
	h := newMCPHarness(t, func(catalog *mcptools.Catalog) {
		mcptools.Add(catalog, &mcp.Tool{Name: "list_edition_records", Description: "List the edition's records.", Annotations: mcptools.ReadOnly},
			func(call *mcptools.Call, input struct {
				Kind string `json:"kind,omitempty"`
			}) (*mcp.CallToolResult, any, error) {
				return mcptools.Answer(call.Get("/api/v1/edition_records", mcptools.Query("kind", input.Kind)))
			})
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.router.GET("/api/v1/edition_records", PrincipalAuth(h.auth, logger), func(c *gin.Context) {
		if c.Query("kind") == "secret" {
			WriteError(c, logger, domain.ErrForbidden)
			return
		}
		principal := Principal(c)
		c.JSON(http.StatusOK, gin.H{"records": []string{}, "actor_id": principal.ActorID, "kind": c.Query("kind")})
	})
	session := h.connect(t, h.token)
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(listed.Tools, func(tool *mcp.Tool) bool {
		return tool.Name == "list_edition_records" && tool.Title == "List edition records" && tool.Annotations.Title == tool.Title
	}) ||
		!slices.ContainsFunc(listed.Tools, func(tool *mcp.Tool) bool { return tool.Name == "get_pipeline" }) {
		t.Fatal("the edition's tool must join core's catalog")
	}
	answer := mcpStructured(t, h.call(t, session, "list_edition_records", map[string]any{"kind": "audit"}))
	if answer["actor_id"] != string(h.keys.principal.ActorID) || answer["kind"] != "audit" {
		t.Fatalf("edition tool answer = %v", answer)
	}
	refused := h.call(t, session, "list_edition_records", map[string]any{"kind": "secret"})
	if !refused.IsError || !strings.HasPrefix(mcpText(refused), "forbidden: ") {
		t.Fatalf("edition refusal = %q", mcpText(refused))
	}
}

func TestMCPUpdatePipelineSavesThenActivatesAgainstTheActiveRevision(t *testing.T) {
	h := newMCPHarness(t)
	session := h.connect(t, h.token)
	block := map[string]any{
		"id": "hold-everything", "name": "Everything needs a person", "type": "outcome", "enabled": true, "timeout": "50ms", "max_attempts": 1,
		"config": map[string]any{"outcome": "human", "reason": "A person decides."},
	}
	result := mcpStructured(t, h.call(t, session, "update_pipeline", map[string]any{"pipeline_key": "entry", "blocks": []any{block}}))
	if result["activated"] != true {
		t.Fatalf("activated = %v", result["activated"])
	}
	revision := result["revision"].(map[string]any)
	if revision["revision"] != float64(2) || revision["is_active"] != true {
		t.Fatalf("revision = %v", revision)
	}
	if len(h.store.activations) != 1 || h.store.activations[0].Revision != 2 || h.store.activations[0].ExpectedActiveRevision != 1 {
		t.Fatalf("activation inputs = %+v, want revision 2 expecting 1 active", h.store.activations)
	}
	blocks := revision["blocks"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["id"] != "hold-everything" {
		t.Fatalf("blocks = %v", blocks)
	}
	// A draft stays a draft, and the next activation names the right
	// active number.
	draft := mcpStructured(t, h.call(t, session, "update_pipeline", map[string]any{"pipeline_key": "entry", "blocks": []any{}, "activate": false}))
	if draft["activated"] != false || draft["revision"].(map[string]any)["revision"] != float64(3) {
		t.Fatalf("draft = %v", draft)
	}
	if len(h.store.activations) != 1 {
		t.Fatal("a draft must not activate")
	}
	rollback := mcpStructured(t, h.call(t, session, "activate_pipeline_revision", map[string]any{"pipeline_key": "entry", "revision": 3}))
	if rollback["revision"] != float64(3) || h.store.activations[1].ExpectedActiveRevision != 2 {
		t.Fatalf("rollback = %v, activations = %+v", rollback, h.store.activations)
	}
	current := mcpStructured(t, h.call(t, session, "get_pipeline", map[string]any{"pipeline_key": "entry"}))
	if current["revision"] != float64(3) {
		t.Fatalf("get_pipeline = %v", current)
	}
}

func TestMCPRelaysProductAPIRefusals(t *testing.T) {
	h := newMCPHarness(t)
	session := h.connect(t, h.token)
	// A validation refusal keeps the product API's code and message.
	invalid := h.call(t, session, "update_pipeline", map[string]any{"pipeline_key": "entry", "blocks": []any{map[string]any{"id": "x", "name": "x", "type": "outcome", "config": map[string]any{"outcome": "maybe"}}}})
	if !invalid.IsError || !strings.HasPrefix(mcpText(invalid), "invalid_request: ") {
		t.Fatalf("invalid block = %v %q", invalid.IsError, mcpText(invalid))
	}
	if len(h.store.revisions) != 1 {
		t.Fatal("a refused revision must not be stored")
	}
	missing := h.call(t, session, "get_pipeline", map[string]any{"pipeline_key": "nobody"})
	if !missing.IsError || !strings.HasPrefix(mcpText(missing), "not_found: ") {
		t.Fatalf("unknown pipeline = %q", mcpText(missing))
	}
	// A route the key may not use (teams need a person's session or
	// key, never an organization key) is refused with the API's code.
	organization := h.connect(t, h.orgToken)
	refused := h.call(t, organization, "create_team", map[string]any{"name": "Payments"})
	if !refused.IsError || !strings.HasPrefix(mcpText(refused), "api_key_not_allowed: ") {
		t.Fatalf("organization key creating a team = %q", mcpText(refused))
	}
	// The organization key still reads pipelines.
	if listed := h.call(t, organization, "get_pipeline", map[string]any{"pipeline_key": "entry"}); listed.IsError {
		t.Fatalf("organization key reading the pipeline: %s", mcpText(listed))
	}
	unknown := h.call(t, session, "read_reference", map[string]any{"topic": "everything"})
	if !unknown.IsError || !strings.Contains(mcpText(unknown), "pipeline_blocks") {
		t.Fatalf("unknown topic = %q", mcpText(unknown))
	}
}

func TestMCPReferenceTopicsExplainTheGrammar(t *testing.T) {
	h := newMCPHarness(t)
	session := h.connect(t, h.token)
	for topic, marker := range map[string]string{
		"pipeline_blocks":  `"type": "outcome"`,
		"conditions":       "greater_than_or_equal",
		"escalation_paths": `"type": "level"`,
	} {
		result := h.call(t, session, "read_reference", map[string]any{"topic": topic})
		if result.IsError || !strings.Contains(mcpText(result), marker) {
			t.Errorf("topic %s: error=%v, text lacks %q", topic, result.IsError, marker)
		}
	}
	// The retired block types are no longer taught.
	blocks := mcpText(h.call(t, session, "read_reference", map[string]any{"topic": "pipeline_blocks"}))
	for _, retired := range []string{`"type": "cel"`, `"type": "always"`, `"on_match"`} {
		if strings.Contains(blocks, retired) {
			t.Errorf("pipeline_blocks still teaches %s", retired)
		}
	}
}
