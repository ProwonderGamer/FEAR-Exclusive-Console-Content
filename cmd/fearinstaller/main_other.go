//go:build !windows

// Command fearinstaller is Windows-only. On other systems use cmd/verifycli (to
// check a copy from the command line) or cmd/uipreview (to open the UI in a browser).
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "fearinstaller is a Windows program; see cmd/verifycli and cmd/uipreview for other systems.")
	os.Exit(2)
}
