//go:build windows

package pcgame

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// SystemSources reads the registry and the usual folders of this machine.
func SystemSources() Sources {
	games, extra := gogRegistry()
	var drives []string
	if mask, err := windows.GetLogicalDrives(); err == nil {
		for i := 0; i < 26; i++ {
			if mask&(1<<uint(i)) == 0 {
				continue
			}
			root := string(rune('A'+i)) + `:\`
			p, err := windows.UTF16PtrFromString(root)
			if err == nil && windows.GetDriveType(p) == windows.DRIVE_FIXED {
				drives = append(drives, root)
			}
		}
	}
	return Sources{
		SteamRoots: SteamRoots(),
		GOGGames:   games,
		GOGRoots:   DefaultGOGRoots([]string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")}, drives, extra),
	}
}

// gogRegistry reads HKLM\SOFTWARE\WOW6432Node\GOG.com\Games\<id> (the 32-bit
// view of SOFTWARE\GOG.com\Games, the way GameFinder reads it) and the folders
// of the Galaxy client.
func gogRegistry() (games []GOGGame, libraries []string) {
	seen := map[string]bool{}
	type view struct {
		path   string
		access uint32
	}
	for _, v := range []view{
		{`SOFTWARE\GOG.com\Games`, registry.WOW64_32KEY},
		{`SOFTWARE\WOW6432Node\GOG.com\Games`, 0},
		{`SOFTWARE\GOG.com\Games`, registry.WOW64_64KEY},
	} {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, v.path, registry.ENUMERATE_SUB_KEYS|v.access)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, id := range names {
			if seen[id] {
				continue
			}
			sk, err := registry.OpenKey(registry.LOCAL_MACHINE, v.path+`\`+id, registry.QUERY_VALUE|v.access)
			if err != nil {
				continue
			}
			str := func(n string) string { s, _, _ := sk.GetStringValue(n); return s }
			g := GOGGame{ID: id, Name: str("gameName"), Path: str("path"), Exe: str("exe"), DependsOn: str("dependsOn")}
			if g.Name == "" {
				g.Name = str("productName")
			}
			sk.Close()
			seen[id] = true
			games = append(games, g)
		}
	}
	// Galaxy: "client" under GalaxyClient\paths is its folder (games live in <client>\Games by default);
	// "libraryPath" under GalaxyClient\settings is the chosen games folder (not confirmed by any source
	// we found, harmless if absent).
	read := func(path, val string, access uint32) string {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|access)
		if err != nil {
			return ""
		}
		defer k.Close()
		s, _, _ := k.GetStringValue(val)
		return s
	}
	for _, a := range []uint32{registry.WOW64_32KEY, registry.WOW64_64KEY} {
		if c := read(`SOFTWARE\GOG.com\GalaxyClient\paths`, "client", a); c != "" {
			libraries = append(libraries, filepath.Join(c, "Games"))
		}
		if l := read(`SOFTWARE\GOG.com\GalaxyClient\settings`, "libraryPath", a); l != "" {
			libraries = append(libraries, l)
		}
	}
	return games, libraries
}
