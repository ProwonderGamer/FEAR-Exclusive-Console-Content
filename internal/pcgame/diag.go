package pcgame

import (
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Diagnose returns a plain-text report about a game folder: what the
// installer sees and the file sizes and SHA-256 values we need to confirm
// details of a store release we cannot test ourselves (GOG). It contains no
// game data, only names, sizes, hashes and the small Default.archcfg text. The
// user's name in a path is masked. Read-only; the larger archives are hashed, so
// the call can take several seconds.
func Diagnose(input string, src Sources) string {
	var b strings.Builder
	p := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	c := Inspect(input)
	p("F.E.A.R. Exclusive Console Content - game folder report")
	p("Folder:   %s", MaskUser(c.Path))
	p("Result:   valid=%v  %s", c.Valid, c.Message)
	p("Edition:  %s (guessed from the files)", orDash(c.Edition))
	p("Version:  %s  GameClient.dll sha256=%s size=%d from=%s", c.Version.Status, orDash(c.Version.SHA256), c.Version.Size, orDash(c.Version.Archive))
	p("          tested reference: sha256=%s size=%d", GameClientV108SHA256, GameClientV108Size)
	p("EchoPatch: %s  %s", c.EchoPatch, c.EchoMessage)
	p("Hook:     dinput8_hook.dll is %s    dxwrapper.dll present: %v", c.Hook, c.Wrapper)
	for _, w := range c.Warnings {
		p("Warning:  %s", w)
	}
	p("")
	p("Where the installer looked for the game:")
	for _, r := range src.SteamRoots {
		p("  Steam folder: %s (exists: %v)", MaskUser(r), isDir(r))
	}
	if len(src.GOGGames) == 0 {
		p("  GOG registry (HKLM\\SOFTWARE\\WOW6432Node\\GOG.com\\Games): no entries read")
	}
	for _, g := range src.GOGGames {
		p("  GOG registry entry: id=%s name=%q path=%q exe=%q dependsOn=%q", g.ID, g.Name, MaskUser(g.Path), MaskUser(g.Exe), g.DependsOn)
	}
	for _, r := range src.GOGRoots {
		line := fmt.Sprintf("  GOG games folder: %s (exists: %v)", MaskUser(r), isDir(r))
		if sub, err := os.ReadDir(r); err == nil {
			var names []string
			for i, e := range sub {
				if i >= 30 {
					names = append(names, "...")
					break
				}
				if e.IsDir() {
					names = append(names, e.Name())
				}
			}
			line += " holds: " + strings.Join(names, ", ")
		}
		p("%s", line)
	}
	for _, in := range DetectAll(src) {
		p("  Found: %s  %s (%s)", in.Store, MaskUser(in.Path), in.Source)
	}
	dir := c.Path
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return b.String()
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		p("cannot list the folder: %v", err)
		return b.String()
	}
	sort.Slice(ents, func(i, j int) bool { return strings.ToLower(ents[i].Name()) < strings.ToLower(ents[j].Name()) })

	p("")
	p("FEAR.exe:")
	if exe := findFile(dir, "FEAR.exe"); exe != "" {
		p("  %s", fileLine(exe, true))
		if f, err := pe.Open(exe); err == nil {
			p("  PE: machine=0x%x linkerTimestamp=%d largeAddressAware=%v", f.FileHeader.Machine, f.FileHeader.TimeDateStamp, f.FileHeader.Characteristics&0x20 != 0)
			f.Close()
		}
	} else {
		p("  not found")
	}

	p("")
	p("Archives (*.Arch00; hashed when 200 MB or smaller):")
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".arch00") {
			p("  %s", fileLine(filepath.Join(dir, e.Name()), true))
		}
	}

	p("")
	p("Other files that matter:")
	for _, n := range []string{"dinput8.dll", "dinput8_hook.dll", "d3d9.dll", "dxwrapper.dll", "dxwrapper.ini", "EchoPatch.ini", "Default.archcfg", "Config.exe", "FEARMP.exe", "FEARServer.exe"} {
		if f := findFile(dir, n); f != "" {
			p("  %s", fileLine(f, true))
		}
	}
	if ini := findFile(dir, "dxwrapper.ini"); ini != "" {
		for _, l := range strings.Split(string(readHead(ini, 64<<10)), "\n") {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(l)), "limitperframefps") {
				p("  dxwrapper.ini: %s", strings.TrimSpace(l))
			}
		}
	}

	p("")
	p("Default.archcfg, exactly as stored (each line in quotes; \\r \\n shown):")
	if f := findFile(dir, "Default.archcfg"); f != "" {
		raw := readHead(f, 8<<10)
		p("  %d bytes read%s", len(raw), map[bool]string{true: " (truncated to 8 KB)", false: ""}[len(raw) == 8<<10])
		p("  %q", string(raw))
	} else {
		p("  not found")
	}

	p("")
	p("Top-level folder listing:")
	n := 0
	for _, e := range ents {
		if n++; n > 400 {
			p("  ... more entries not listed")
			break
		}
		if e.IsDir() {
			sub, _ := os.ReadDir(filepath.Join(dir, e.Name()))
			p("  [folder] %s (%d entries)", e.Name(), len(sub))
			continue
		}
		if st, err := e.Info(); err == nil {
			p("  %-34s %d", e.Name(), st.Size())
		}
	}
	for _, sub := range []string{"FEARXP", "FEARXP2"} {
		if d := filepath.Join(dir, sub); isDir(d) {
			if f := findFile(d, "Default.archcfg"); f != "" {
				p("")
				p("%s\\Default.archcfg (expansion, not touched by the installer): %q", sub, string(readHead(f, 4<<10)))
			}
		}
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func fileLine(path string, hash bool) string {
	st, err := os.Stat(path)
	if err != nil {
		return filepath.Base(path) + "  (unreadable)"
	}
	s := fmt.Sprintf("%-26s %12d", filepath.Base(path), st.Size())
	if hash && st.Size() <= 200<<20 {
		if h := sha256File(path); h != "" {
			s += "  sha256=" + h
		}
	}
	return s
}

func sha256File(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

var userDir = regexp.MustCompile(`(?i)([\\/]Users[\\/])[^\\/]+`)

// MaskUser hides the account name in a path such as C:\Users\Name\Games.
func MaskUser(p string) string { return userDir.ReplaceAllString(p, "${1}<user>") }
