package tracedoc

import (
	"os"
	"path/filepath"
)

// RecordingEnabled reports whether a project has opted in, which is what the
// presence of the marker file means.
//
// The contents are not read: the file only has to exist, so `touch
// .tracedoc-on`, a zero-byte write from `tracedoc --enable`, and the line the
// /ai-tracedoc:on command writes are all equally valid. The check is
// deliberately this permissive, because the three platforms have no single
// command in common that creates a file AND the command that turns recording
// on has to be writable by a model with no delete tool.
//
// A directory in the marker's place is not a marker. That is the one shape a
// wayward script can produce that would otherwise silently opt a project in.
func RecordingEnabled(cwd string) bool {
	info, err := os.Stat(filepath.Join(cwd, MarkerFilename))
	return err == nil && !info.IsDir()
}
