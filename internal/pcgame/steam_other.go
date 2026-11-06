//go:build !windows

package pcgame

import "os"

// SteamRoots on non-Windows hosts (used only by tests / the CLI): FEAR_STEAM_ROOT.
func SteamRoots() []string {
	if v := os.Getenv("FEAR_STEAM_ROOT"); v != "" {
		return []string{v}
	}
	return nil
}
