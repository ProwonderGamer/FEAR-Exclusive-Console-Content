//go:build !windows

package pcgame

import (
	"os"
	"path/filepath"
)

// SystemSources on non-Windows hosts (tests, the CLI and the browser preview
// only): FEAR_STEAM_ROOT is a Steam folder, FEAR_GOG_ROOT a folder holding one
// sub-folder per GOG game.
func SystemSources() Sources {
	var s Sources
	if v := os.Getenv("FEAR_STEAM_ROOT"); v != "" {
		s.SteamRoots = []string{v}
	}
	if v := os.Getenv("FEAR_GOG_ROOT"); v != "" {
		s.GOGRoots = []string{filepath.Clean(v)}
	}
	return s
}
