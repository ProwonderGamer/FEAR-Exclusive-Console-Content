//go:build windows

package pcgame

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// SteamRoots lists Steam installation folders on this machine.
func SteamRoots() []string {
	var roots []string
	read := func(k registry.Key, path, val string) {
		if key, err := registry.OpenKey(k, path, registry.QUERY_VALUE); err == nil {
			defer key.Close()
			if s, _, err := key.GetStringValue(val); err == nil && s != "" {
				roots = append(roots, s)
			}
		}
	}
	read(registry.CURRENT_USER, `Software\Valve\Steam`, "SteamPath")
	read(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Valve\Steam`, "InstallPath")
	read(registry.LOCAL_MACHINE, `SOFTWARE\Valve\Steam`, "InstallPath")
	for _, d := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
		if d != "" {
			roots = append(roots, d+`\Steam`)
		}
	}
	return roots
}
