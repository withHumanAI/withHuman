package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/withHumanAI/withHuman/internal/cli/configfile"
	"maps"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/withHumanAI/withHuman/domain"
)

// claudeKeychainService is the macOS keychain item Claude Code keeps its
// credentials in, one per OS account name. Claude Code writes it through
// the security tool, so reading it back the same way raises no access
// prompt.
const claudeKeychainService = "Claude Code-credentials"

// claudeSignInHint tells the person how to fix a remote server Claude
// Code holds no working token for.
const claudeSignInHint = "sign in to it from Claude Code with /mcp, then run setup again"

// claudeCredentialsPath is where Claude Code keeps credentials on
// platforms without a keychain.
func claudeCredentialsPath(env Env) string {
	return filepath.Join(env.Home, ".claude", ".credentials.json")
}

// claudeMCPTokens returns the OAuth access token Claude Code stored for
// each remote MCP server it signed in to, keyed by server URL. Those
// tokens live in the credentials store, never in the MCP configuration,
// so a configuration-only probe would reach the servers unauthenticated.
func claudeMCPTokens(ctx context.Context, env Env) map[string]string {
	content, existed, err := configfile.ReadFile(claudeCredentialsPath(env))
	if err == nil && existed {
		return parseClaudeMCPTokens(content, time.Now())
	}
	if runtime.GOOS != "darwin" {
		return nil
	}
	out, err := env.run(ctx, "security", "find-generic-password", "-s", claudeKeychainService, "-a", claudeKeychainAccount(env), "-w")
	if err != nil {
		return nil
	}
	return parseClaudeMCPTokens(out, time.Now())
}

// claudeKeychainAccount is the account Claude Code files its keychain
// item under: the OS user name.
func claudeKeychainAccount(env Env) string {
	if current, err := user.Current(); err == nil && current.Username != "" {
		return current.Username
	}
	return env.getenv("USER")
}

// parseClaudeMCPTokens reads the mcpOAuth map of a credentials store. An
// entry without a token, or whose token has expired, is one Claude Code
// itself cannot use without signing in again.
func parseClaudeMCPTokens(content []byte, now time.Time) map[string]string {
	var store struct {
		MCPOAuth map[string]struct {
			ServerURL   string `json:"serverUrl"`
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"mcpOAuth"`
	}
	if json.Unmarshal(bytes.TrimSpace(content), &store) != nil {
		return nil
	}
	tokens := map[string]string{}
	for _, entry := range store.MCPOAuth {
		if entry.ServerURL == "" || entry.AccessToken == "" {
			continue
		}
		if entry.ExpiresAt > 0 && time.UnixMilli(entry.ExpiresAt).Before(now) {
			continue
		}
		tokens[entry.ServerURL] = entry.AccessToken
	}
	return tokens
}

// withClaudeTokens lends each remote server the token Claude Code stored
// for its URL. A server whose configuration carries its own Authorization
// header keeps it.
func withClaudeTokens(servers []mcpServer, tokens map[string]string) []mcpServer {
	for i, server := range servers {
		token := tokens[server.Spec.URL]
		if server.Spec.URL == "" || token == "" || hasHeader(server.Spec.Headers, "Authorization") {
			continue
		}
		headers := maps.Clone(server.Spec.Headers)
		if headers == nil {
			headers = map[string]string{}
		}
		headers["Authorization"] = "Bearer " + token
		servers[i].Spec.Headers = headers
	}
	return servers
}

func hasHeader(headers map[string]string, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

// withClaudeSignInHints tells the person what to do about a remote
// server that turned the probe away: whatever token Claude Code holds for
// it (if any) is not one the server accepts.
func withClaudeSignInHints(found Discovered, servers []mcpServer) Discovered {
	remote := map[string]bool{}
	for _, server := range servers {
		remote[server.Name] = server.Spec.URL != ""
	}
	for i, failure := range found.Errors {
		if remote[failure.Server] && strings.Contains(failure.Message, "HTTP 401") {
			found.Errors[i] = domain.OnboardingDiscoveryError{Server: failure.Server, Message: failure.Message + " (" + claudeSignInHint + ")"}
		}
	}
	return found
}
