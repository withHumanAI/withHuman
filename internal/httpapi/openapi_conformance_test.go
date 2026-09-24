package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/onboarding"
	"gopkg.in/yaml.v3"
)

// specOperations reads every operation from the embedded contract, keyed
// by method and path with each path parameter normalised to {}.
func specOperations(t *testing.T) map[routeKey]string {
	t.Helper()
	var document struct {
		Paths map[string]map[string]struct {
			Permission string `yaml:"x-withhuman-permission"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openAPISpec, &document); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	operations := make(map[routeKey]string)
	for path, methods := range document.Paths {
		for method, operation := range methods {
			switch method {
			case "get", "post", "put", "patch", "delete":
			default:
				continue
			}
			operations[routeKey{Method: strings.ToUpper(method), Path: normaliseSpecPath(path)}] = operation.Permission
		}
	}
	return operations
}

// specSecurity reads the security schemes every operation declares.
func specSecurity(t *testing.T) map[routeKey][]string {
	t.Helper()
	var document struct {
		Paths map[string]map[string]struct {
			Security []map[string][]string `yaml:"security"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openAPISpec, &document); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	schemes := make(map[routeKey][]string)
	for path, methods := range document.Paths {
		for method, operation := range methods {
			switch method {
			case "get", "post", "put", "patch", "delete":
			default:
				continue
			}
			var names []string
			for _, requirement := range operation.Security {
				for name := range requirement {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			schemes[routeKey{Method: strings.ToUpper(method), Path: normaliseSpecPath(path)}] = names
		}
	}
	return schemes
}

var specParameter = regexp.MustCompile(`\{[^}]+\}`)
var ginParameter = regexp.MustCompile(`[:*][^/]+`)

func TestAAPOperationsUseProtocolResponseSchema(t *testing.T) {
	var document struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema struct {
						Ref string `yaml:"$ref"`
					} `yaml:"schema"`
				} `yaml:"content"`
			} `yaml:"responses"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openAPISpec, &document); err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct{ path, method, status string }{
		{"/api/aap/v1/requests", "post", "201"},
		{"/api/aap/v1/requests/{id}", "get", "200"},
		{"/api/aap/v1/requests/{id}", "delete", "200"},
	} {
		ref := document.Paths[op.path][op.method].Responses[op.status].Content["application/json"].Schema.Ref
		if ref != "#/components/schemas/AAPApprovalRequest" {
			t.Errorf("%s %s uses %s", op.method, op.path, ref)
		}
	}
}

func normaliseSpecPath(path string) string { return specParameter.ReplaceAllString(path, "{}") }
func normaliseGinPath(path string) string  { return ginParameter.ReplaceAllString(path, "{}") }

// conformanceAPI builds the router with every optional surface present:
// onboarding is the only one whose routes are gated on a nil service, and
// registration needs nothing more than a non-nil pointer.
func conformanceAPI(t *testing.T) (*API, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	api := newAPI(Dependencies{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Onboarding: &onboarding.Service{},
	})
	return api, api.router()
}

// The router's route table and the contract's operations are the same
// set, so a route cannot ship undocumented and a documented operation
// cannot be missing.
func TestRouterAndContractRegisterTheSameRoutes(t *testing.T) {
	api, router := conformanceAPI(t)
	registered := make(map[routeKey]struct{})
	for _, route := range router.Routes() {
		registered[routeKey{Method: route.Method, Path: normaliseGinPath(route.Path)}] = struct{}{}
	}
	documented := make(map[routeKey]struct{})
	for route := range specOperations(t) {
		documented[route] = struct{}{}
	}
	labelled := make(map[routeKey]struct{})
	for route := range api.Access() {
		labelled[routeKey{Method: route.Method, Path: normaliseGinPath(route.Path)}] = struct{}{}
	}

	var undocumented, unregistered, unlabelled []string
	for route := range registered {
		if _, ok := documented[route]; !ok {
			undocumented = append(undocumented, route.Method+" "+route.Path)
		}
		if _, ok := labelled[route]; !ok {
			unlabelled = append(unlabelled, route.Method+" "+route.Path)
		}
	}
	for route := range documented {
		if _, ok := registered[route]; !ok {
			unregistered = append(unregistered, route.Method+" "+route.Path)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(unregistered)
	sort.Strings(unlabelled)
	if len(undocumented) > 0 {
		t.Errorf("routes registered but absent from openapi.yaml:\n  %s", strings.Join(undocumented, "\n  "))
	}
	if len(unregistered) > 0 {
		t.Errorf("operations in openapi.yaml with no registered route:\n  %s", strings.Join(unregistered, "\n  "))
	}
	if len(unlabelled) > 0 {
		t.Errorf("routes registered outside handle, so they carry no access label:\n  %s", strings.Join(unlabelled, "\n  "))
	}
}

// Every operation's label is public, self, or a key the registry knows,
// and equals the label the router gates the route on.
func TestContractLabelsMatchRouterAccess(t *testing.T) {
	api, _ := conformanceAPI(t)
	access := make(map[routeKey]string)
	for route, label := range api.Access() {
		access[routeKey{Method: route.Method, Path: normaliseGinPath(route.Path)}] = label
	}
	for route, label := range specOperations(t) {
		name := route.Method + " " + route.Path
		switch label {
		case "":
			t.Errorf("%s: no x-withhuman-permission", name)
			continue
		case AccessPublic, AccessSelf:
		default:
			if _, known := domain.LookupPermission(domain.Permission(label)); !known {
				t.Errorf("%s: label %q is not a registered permission", name, label)
				continue
			}
		}
		if got, ok := access[route]; ok && got != label {
			t.Errorf("%s: openapi.yaml says %q, router gates on %q", name, label, got)
		}
	}
}

// A route registered twice would silently shadow the first registration
// in gin; handle refuses it at startup instead.
func TestHandleRefusesDuplicateRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api := &API{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	router := gin.New()
	group := router.Group("/api/v1")
	noop := func(*gin.Context) {}
	api.handle(group, "GET", "/teams", string(domain.PermissionTeamRead), noop)
	defer func() {
		if recover() == nil {
			t.Fatal("second registration of GET /api/v1/teams did not panic")
		}
	}()
	api.handle(group, "GET", "/teams", string(domain.PermissionTeamRead), noop)
}

// The contract's x-withhuman-permissions block is generated from the
// registry, so the docs site renders the vocabulary the server enforces.
// A registry change without a regenerated block fails here.
func TestContractPermissionsBlockMatchesRegistry(t *testing.T) {
	rendered, err := RenderSpecPermissions(openAPISpec)
	if err != nil {
		t.Fatalf("render permission vocabulary: %v", err)
	}
	if !bytes.Equal(rendered, openAPISpec) {
		t.Fatalf("openapi.yaml's x-withhuman-permissions block does not match domain.Permissions(); run make openapi-permissions")
	}
}

// Every protected /api/v1 operation accepts a personal API key alongside the
// session cookie unless the router registers it browser-only, and the
// contract says which: an operation that lists apiKeyAuth must not be
// browser-only, one that does not must be. A browser-only entry that
// matches no registered route is a typo.
func TestContractSecurityMatchesBrowserOnlyRoutes(t *testing.T) {
	api, router := conformanceAPI(t)
	registered := make(map[routeKey]struct{})
	for _, route := range router.Routes() {
		registered[routeKey{Method: route.Method, Path: normaliseGinPath(route.Path)}] = struct{}{}
	}
	for route := range browserOnlyRoutes {
		if _, ok := registered[routeKey{Method: route.Method, Path: normaliseGinPath(route.Path)}]; !ok {
			t.Errorf("browser-only entry %s %s matches no registered route", route.Method, route.Path)
		}
	}
	browserOnly := make(map[routeKey]bool)
	public := make(map[routeKey]bool)
	for route, access := range api.Access() {
		key := routeKey{Method: route.Method, Path: normaliseGinPath(route.Path)}
		browserOnly[key] = BrowserOnly(route.Method, route.Path)
		public[key] = access == AccessPublic
	}
	for route, schemes := range specSecurity(t) {
		if !strings.HasPrefix(route.Path, "/api/v1/") {
			continue
		}
		name := route.Method + " " + route.Path
		if route.Path == "/api/v1/agent_instances/self" || route.Path == "/api/v1/agent_instances/self/tool_catalog" {
			if len(schemes) != 1 || schemes[0] != "bearerAuth" {
				t.Errorf("%s must accept only the instance bearer", name)
			}
			continue
		}
		if public[route] {
			if len(schemes) != 0 {
				t.Errorf("%s: public route declares authentication schemes", name)
			}
			continue
		}
		acceptsKey, acceptsCookie := false, false
		for _, scheme := range schemes {
			switch scheme {
			case "apiKeyAuth":
				acceptsKey = true
			case "cookieAuth":
				acceptsCookie = true
			default:
				t.Errorf("%s: unexpected security scheme %q on a management route", name, scheme)
			}
		}
		if !acceptsKey && !acceptsCookie {
			t.Errorf("%s: declares neither cookieAuth nor apiKeyAuth", name)
		}
		if got, ok := browserOnly[route]; ok && got == acceptsKey {
			if got {
				t.Errorf("%s: router registers it browser-only but openapi.yaml lists apiKeyAuth", name)
			} else {
				t.Errorf("%s: openapi.yaml lists cookieAuth only but the router accepts API keys; add it to browserOnlyRoutes or declare apiKeyAuth", name)
			}
		}
	}
}

func TestCredentialKindContractMatchesRouter(t *testing.T) {
	if !bytes.Equal(openAPISpec, RenderSpecCredentialKinds(openAPISpec)) {
		t.Fatal("credential kind declarations drifted; run make openapi-permissions")
	}
}

func TestOrganizationKeyAllowlistContainsOnlyRegisteredManagementRoutes(t *testing.T) {
	api, _ := conformanceAPI(t)
	for route := range organizationKeyRoutes {
		access, registered := api.Access()[route]
		if !registered || !OrganizationKeyAllowed(route.Method, route.Path, access) {
			t.Errorf("invalid organization-key route: %+v", route)
		}
	}
}
