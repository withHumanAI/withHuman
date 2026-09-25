package domain

import "testing"

func TestAgentMCPServerValidate(t *testing.T) {
	valid := AgentMCPServer{Name: "withhuman", URL: "https://app.withhuman.ai/gateway/mcp", Label: "withHuman MCP gateway"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (AgentMCPServer{Name: "withhuman", URL: "http://localhost:8080/gateway/mcp", Label: "dev"}).Validate(); err != nil {
		t.Fatalf("localhost over http: %v", err)
	}
	for name, server := range map[string]AgentMCPServer{
		"bad name":     {Name: "With Human", URL: valid.URL, Label: valid.Label},
		"no label":     {Name: valid.Name, URL: valid.URL},
		"plain http":   {Name: valid.Name, URL: "http://app.withhuman.ai/gateway/mcp", Label: valid.Label},
		"credentials":  {Name: valid.Name, URL: "https://user:pw@app.withhuman.ai/gateway/mcp", Label: valid.Label},
		"fragment":     {Name: valid.Name, URL: valid.URL + "#x", Label: valid.Label},
		"relative url": {Name: valid.Name, URL: "/gateway/mcp", Label: valid.Label},
	} {
		if err := server.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if err := ValidateAgentMCPServers([]AgentMCPServer{valid, valid}); err == nil {
		t.Fatal("a repeated name must be refused")
	}
	if err := ValidateAgentMCPServers(nil); err != nil {
		t.Fatal(err)
	}
}
