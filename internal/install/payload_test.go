package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPayload(t *testing.T) {
	if sum(HookTemplate) != TemplateSHA256 {
		t.Fatalf("payload/dinput8_hook_template.dll is %s, TemplateSHA256 says %s", sum(HookTemplate), TemplateSHA256)
	}
	if !IsOurHook(HookTemplate) {
		t.Fatal("the embedded hook is not recognised as ours")
	}
}

// The process check finds this test's own process (Windows only) and not a made-up name.
func TestProcessRunning(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	if !processRunning(filepath.Base(os.Args[0])) {
		t.Fatalf("%s not found in the process list", filepath.Base(os.Args[0]))
	}
	if processRunning("no-such-process-7f3a.exe") {
		t.Fatal("found a process that does not exist")
	}
}
