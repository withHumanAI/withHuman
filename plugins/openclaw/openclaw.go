// Package openclaw embeds withHuman's OpenClaw plugin, which the CLI
// installs through the AAP library in place of its own plugin. The
// plugin's directory is also what ClawHub publishes as @withhuman/openclaw.
package openclaw

import (
	"embed"
	"io/fs"
)

// Name is the plugin's OpenClaw id and directory name.
const Name = "withhuman"

//go:embed withhuman/package.json withhuman/openclaw.plugin.json withhuman/index.js withhuman/hook.js withhuman/setup.js withhuman/README.md withhuman/assets/icon.png
var files embed.FS

// Files returns the plugin's files rooted at its directory.
func Files() fs.FS {
	sub, err := fs.Sub(files, Name)
	if err != nil {
		panic(err)
	}
	return sub
}
