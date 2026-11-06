//go:build windows

package edge

import (
	"fmt"
	"os"
)

// ErrorHandler is called with a plain message when WebView2 cannot start or
// breaks (MODIFIED, see third_party/NOTICE.md: upstream called log.Fatalf, which
// prints to a console that a GUI program does not have, so the program just
// vanished). The handler should tell the user (a message box); the process
// exits afterwards either way.
var ErrorHandler func(msg string)

// fatalf reports an unrecoverable error and exits, like log.Fatalf, but through ErrorHandler.
func fatalf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if ErrorHandler != nil {
		func() {
			defer func() { _ = recover() }()
			ErrorHandler(msg)
		}()
	}
	os.Exit(1)
}
