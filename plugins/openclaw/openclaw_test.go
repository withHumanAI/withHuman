package openclaw

import (
	"io/fs"
	"os"
	"slices"
	"testing"
)

// The embed list names each file, so a file added to the plugin must be
// added there too.
func TestFilesEmbedsThePluginDirectory(t *testing.T) {
	var onDisk, embedded []string
	err := fs.WalkDir(os.DirFS(Name), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			onDisk = append(onDisk, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = fs.WalkDir(Files(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			embedded = append(embedded, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(onDisk)
	slices.Sort(embedded)
	if !slices.Equal(onDisk, embedded) {
		t.Fatalf("embedded %q, plugin directory has %q", embedded, onDisk)
	}
}
