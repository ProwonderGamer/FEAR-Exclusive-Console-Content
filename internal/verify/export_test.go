package verify

import "fearinstaller/internal/vfs"

// Test hooks (compiled only into the package's tests).
var Guard = guard

func ReadTitleID(fsys vfs.FS, p string) (string, bool) { return readTitleID(fsys, p) }
