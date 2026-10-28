// Package hermes embeds withHuman's Hermes Agent plugin, which the CLI
// installs through the AAP library in place of its shell hook. The
// plugin's directory is also what the Hermes plugin catalog lists.
package hermes

import (
	"embed"
	"io/fs"
)

// Name is the plugin's Hermes key and directory name.
const Name = "withhuman"

//go:embed withhuman/plugin.yaml withhuman/__init__.py withhuman/connect.py withhuman/README.md
var files embed.FS

// Files returns the plugin's files rooted at its directory.
func Files() fs.FS {
	sub, err := fs.Sub(files, Name)
	if err != nil {
		panic(err)
	}
	return sub
}
