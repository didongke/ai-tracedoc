//go:build !windows

package tracedoc

import "os"

// replaceFile is the atomic replace used to publish the state file. rename(2)
// is atomic and does not need the retry the Windows path carries.
func replaceFile(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}
