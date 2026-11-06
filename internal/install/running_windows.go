//go:build windows

package install

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// GameRunning reports whether a process named FEAR.exe (the game) is running. Installing or uninstalling
// while it runs would fail on locked files or leave the running game with a half-changed folder.
// If the process list cannot be read it answers false; the file writes then fail with their own message.
func GameRunning() bool { return processRunning("FEAR.exe") }

// processRunning reports whether a process with this executable name (any letter case) is running.
func processRunning(name string) bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), name) {
			return true
		}
	}
	return false
}
