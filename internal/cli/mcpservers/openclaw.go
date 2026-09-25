package mcpservers

import (
	"fmt"
	"path/filepath"
)

// installOpenClaw edits openclaw.json when it is strict JSON. OpenClaw
// reads JSON5, so a file with comments gets a snippet to paste instead of
// an edit that would flatten it.
func installOpenClaw(path string, env Env, reg Registration) (Result, error) {
	restart := fmt.Sprintf("Restart the OpenClaw Gateway so it connects to %s.", labels(reg.Servers))
	result, err := installClaudeStyle(path, env, reg, []string{restart})
	if !isNotStrictJSON(err) {
		return result, err
	}
	return Result{
		Manual:  true,
		Message: fmt.Sprintf("%s is not strict JSON, so it was left alone. Add %s to its mcpServers, then restart the OpenClaw Gateway.", filepath.Base(path), labels(reg.Servers)),
		Snippet: snippet(env, reg),
	}, nil
}
