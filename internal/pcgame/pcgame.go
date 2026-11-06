// Package pcgame finds and checks the PC installation of F.E.A.R. (Steam
// "FEAR Ultimate Shooter Edition" or GOG "F.E.A.R. Platinum", patch 1.08) and the
// EchoPatch the mod needs. A folder is accepted by what is in it (FEAR.exe and
// the game archives), never by which store it came from. Everything here is
// read-only.
package pcgame

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	// SteamFolder is the install directory name used by the Steam release.
	SteamFolder = "FEAR Ultimate Shooter Edition"
	steamAppID  = "21090"
)

// EchoPatchState describes the EchoPatch installation in the game folder.
type EchoPatchState string

const (
	EchoPatchOK      EchoPatchState = "ok"      // dinput8.dll is EchoPatch
	EchoPatchForeign EchoPatchState = "foreign" // a dinput8.dll exists but is not EchoPatch
	EchoPatchIniOnly EchoPatchState = "ini"     // EchoPatch.ini without its DLL
	EchoPatchWrapper EchoPatchState = "wrapper" // dinput8.dll is GOG's own "GOG.com Input wrapper", EchoPatch is not there yet
	EchoPatchMissing EchoPatchState = "missing"
)

// Check is the result of inspecting a candidate game folder.
type Check struct {
	Path        string         `json:"path"`     // the folder actually inspected (cleaned; a typed FEAR.exe path becomes its folder)
	Valid       bool           `json:"valid"`    // FEAR.exe and the base game archives found
	Message     string         `json:"message"`  // plain-language summary of the folder
	Patch108    bool           `json:"patch108"` // evidence of the v1.08 patch archives
	EchoPatch   EchoPatchState `json:"echoPatch"`
	EchoMessage string         `json:"echoMessage"` // what was found, one sentence
	EchoHelp    string         `json:"echoHelp"`    // how to install it (only when it is not OK)
	Ready       bool           `json:"ready"`       // valid and EchoPatch present

	Edition      string   `json:"edition"`      // "steam", "gog" or "unknown": a hint for messages, nothing depends on it
	EditionLabel string   `json:"editionLabel"` // "Steam", "GOG" or ""
	Version      Version  `json:"version"`      // GameClient.dll compared with the tested patch 1.08
	Hook         string   `json:"hook"`         // dinput8_hook.dll in the folder: "absent", "ours" or "foreign"
	HookIni      bool     `json:"hookIni"`      // a dinput8_hook.ini in the folder turns some of the hook's features off (hookini.go)
	Wrapper      bool     `json:"dxwrapper"`    // dxwrapper.dll is present (GOG ships it)
	Warnings     []string `json:"warnings"`     // things to tell the user that do not block the install
}

// EchoPatchURL is EchoPatch's GitHub page. The installer's "Open the EchoPatch page" button opens this address
// and no other: the page only asks for "the EchoPatch page", never passes an address of its own.
const EchoPatchURL = "https://github.com/Wemino/EchoPatch"

// echoHelp is the install instruction shown when EchoPatch is missing or wrong.
const echoHelp = "Download EchoPatch from its GitHub page and copy dinput8.dll and EchoPatch.ini into the game folder, then choose Verify."

// echoHelpWrapper is the variant for a folder that has GOG's own dinput8.dll (its input wrapper).
const echoHelpWrapper = "Download EchoPatch from its GitHub page (it supports the GOG version) and copy its dinput8.dll and EchoPatch.ini into the game folder, next to FEAR.exe. If Windows asks whether to replace dinput8.dll, choose Replace (keep a copy of GOG's file if you like; EchoPatch has its own Xbox controller support). Then choose Verify."

// NormalizeInput cleans what a person typed or pasted into the folder box:
// surrounding spaces and quotes (Explorer's "Copy as path" adds them), %VAR%
// references and a leading ~. It returns a message instead of a path when the
// text cannot be used (an unknown variable, or a relative path such as a bare
// drive letter "D:").
func NormalizeInput(in string) (path, problem string) {
	p := strings.TrimSpace(in)
	p = strings.Trim(p, "\"'\u2018\u2019\u201c\u201d")
	p = strings.TrimSpace(p)
	if p == "" {
		return "", ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", "Cannot work out your home folder; type the full path instead."
		}
		p = home + p[1:]
	}
	var undefined []string
	p = envRef.ReplaceAllStringFunc(p, func(m string) string {
		name := m[1 : len(m)-1]
		if v := os.Getenv(name); v != "" {
			return v
		}
		undefined = append(undefined, m)
		return m
	})
	if len(undefined) > 0 {
		return "", "The variable " + undefined[0] + " is not defined on this computer; type the full path instead."
	}
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		return "", "Type the full path, for example " + filepath.FromSlash(`C:/Games/F.E.A.R.`) + ", or use Browse."
	}
	return p, ""
}

var envRef = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_()]*%`)

// Inspect checks a folder the user picked, typed or auto-detection found.
func Inspect(input string) Check {
	c := Check{EchoPatch: EchoPatchMissing, Edition: "unknown", Hook: "absent", Version: Version{Status: VersionUnchecked}, Warnings: []string{}}
	dir, problem := NormalizeInput(input)
	c.Path = dir
	if problem != "" {
		c.Path = strings.TrimSpace(input)
		c.Message = problem
		return c
	}
	if dir == "" {
		c.Message = "Select the folder containing FEAR.exe."
		return c
	}
	st, err := os.Stat(dir)
	if err != nil {
		c.Message = "That folder does not exist."
		return c
	}
	if !st.IsDir() {
		// A path to FEAR.exe itself is fine: use its folder.
		if strings.EqualFold(filepath.Base(dir), "FEAR.exe") {
			dir = filepath.Dir(dir)
			c.Path = dir
		} else {
			c.Message = "That is a file, not a folder. Select the folder that contains FEAR.exe."
			return c
		}
	}
	exe := findFile(dir, "FEAR.exe")
	if exe == "" {
		c.Message = "FEAR.exe was not found in this folder." + whereIsTheGame(dir)
		return c
	}
	if !hasBaseArchive(dir) {
		c.Message = "FEAR.exe is here, but the game's data archives (FEAR.Arch00 and the FEAR_1 to FEAR_8 archives) are missing. This does not look like a complete installation."
		return c
	}
	c.Valid = true
	// Patch 1.08 ships FEAR_8/FEARL_8 archives; their presence is cheap evidence.
	c.Patch108 = findFile(dir, "FEAR_8.Arch00") != "" || findFile(dir, "FEARL_8.Arch00") != ""
	c.Edition, c.EditionLabel = detectEdition(dir)
	c.Version = checkVersion(dir)
	c.Wrapper = findFile(dir, "dxwrapper.dll") != ""
	c.Hook = hookState(dir)
	c.HookIni = HookIniTurnsOff(dir) // an ini that only repeats the hook's defaults is left alone, unmentioned

	what := "F.E.A.R. found"
	if c.EditionLabel != "" {
		what += " (" + c.EditionLabel + " version)"
	}
	switch {
	case c.Version.Status == VersionVerified:
		c.Message = what + ", patch 1.08 confirmed."
	case c.Version.Status == VersionUnchecked && !c.Patch108:
		c.Message = what + ", but patch 1.08 does not seem to be installed (FEAR_8.Arch00 is missing). The Steam and GOG versions include it."
	case c.Version.Status == VersionUnchecked:
		c.Message = what + ". Patch 1.08 could not be confirmed (FEARE_8.Arch00 is missing)."
	default:
		c.Message = what + "."
	}
	if c.Version.Status == VersionDifferent || c.Version.Status == VersionUnreadable {
		c.Warnings = append(c.Warnings, UntestedVersionWarning)
	}
	if c.Hook == "foreign" {
		c.Warnings = append(c.Warnings, "A dinput8_hook.dll that does not come from this mod is already in the folder (EchoPatch uses that file name to chain another DirectInput wrapper). The install would back it up and replace it, and the other wrapper stops working while the mod is installed.")
	}
	c.EchoPatch, c.EchoMessage = detectEchoPatch(dir)
	if c.EchoPatch != EchoPatchOK {
		c.EchoHelp = echoHelp
		if c.EchoPatch == EchoPatchWrapper {
			c.EchoHelp = echoHelpWrapper
		}
	}
	if n := dxwrapperFPSCap(dir); n > 0 {
		c.Warnings = append(c.Warnings, fmt.Sprintf("dxwrapper (shipped with the GOG version) limits the game to %d fps (LimitPerFrameFPS in dxwrapper.ini). EchoPatch has its own frame limiter (MaxFPS in EchoPatch.ini); to use a MaxFPS above %d, set LimitPerFrameFPS = 0 in dxwrapper.ini. The installer does not change either file.", n, n))
	}
	c.Ready = c.EchoPatch == EchoPatchOK
	return c
}

// dxwrapperFPSCap returns LimitPerFrameFPS from dxwrapper.ini (GOG ships 60), or 0 when
// there is no such file, no such line, or the limiter is off. Only the [d3d9] value counts.
func dxwrapperFPSCap(dir string) int {
	ini := findFile(dir, "dxwrapper.ini")
	if ini == "" {
		return 0
	}
	section := ""
	for _, l := range strings.Split(string(readHead(ini, 64<<10)), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || l[0] == ';' || l[0] == '#' {
			continue
		}
		if l[0] == '[' {
			section = strings.ToLower(strings.Trim(l, "[] \t"))
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok || section != "d3d9" || !strings.EqualFold(strings.TrimSpace(k), "LimitPerFrameFPS") {
			continue
		}
		if i := strings.IndexAny(v, ";#"); i >= 0 {
			v = v[:i]
		}
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
		return 0
	}
	return 0
}

// UntestedVersionWarning is shown when the game files are not the tested patch 1.08.
const UntestedVersionWarning = "This F.E.A.R. version has not been tested; the objective display and Bonus mission menu may switch themselves off. You can still install: if they do, nothing is damaged."

// hasBaseArchive reports whether the folder has at least one archive of the base game
// (FEAR.Arch00 or one of the FEAR_1 to FEAR_8 patch archives).
func hasBaseArchive(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := strings.ToLower(e.Name())
		if n == "fear.arch00" || (strings.HasPrefix(n, "fear_") && strings.HasSuffix(n, ".arch00")) {
			return true
		}
	}
	return false
}

// whereIsTheGame builds the hint shown when FEAR.exe is not in the folder: an
// expansion folder (GOG puts FEARXP and FEARXP2 inside the main folder), or a
// sub-folder that does contain the game.
func whereIsTheGame(dir string) string {
	if findFile(dir, "FEARXP.exe") != "" || findFile(dir, "FEARXP2.exe") != "" {
		h := " This is an expansion folder (Extraction Point or Perseus Mandate)."
		if p := filepath.Dir(dir); p != dir && findFile(p, "FEAR.exe") != "" {
			h += " Choose the main F.E.A.R. folder one level up: " + p
		} else {
			h += " Choose the main F.E.A.R. folder that contains FEAR.exe."
		}
		return h
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	n := 0
	for _, e := range ents {
		if n++; n > 500 {
			break
		}
		if e.IsDir() && findFile(filepath.Join(dir, e.Name()), "FEAR.exe") != "" {
			return " Try the \"" + e.Name() + "\" folder inside it."
		}
	}
	return ""
}

// detectEchoPatch looks for EchoPatch's dinput8.dll (identified by its strings)
// and EchoPatch.ini in the game folder. The message says exactly what was found.
// The GOG version ships its own dinput8.dll ("GOG.com Input wrapper", a
// controller/input wrapper; confirmed from its version resource) next to
// dxwrapper (d3d9.dll stub + dxwrapper.dll). That file is told apart from
// EchoPatch and from foreign loaders, and dxwrapper's files (dxwrapper.dll,
// dxwrapper.ini, d3d9.dll) are never mistaken for EchoPatch.
func detectEchoPatch(dir string) (EchoPatchState, string) {
	dll := findFile(dir, "dinput8.dll")
	ini := findFile(dir, "EchoPatch.ini")
	if dll != "" {
		switch classifyDinput8(dll) {
		case dinputEchoPatch:
			return EchoPatchOK, "EchoPatch by Wemino (GitHub) found."
		case dinputWrapper:
			return EchoPatchWrapper, "This folder has GOG's own dinput8.dll (the \"GOG.com Input wrapper\"), not EchoPatch. EchoPatch replaces it."
		}
		return EchoPatchForeign, "This folder has a dinput8.dll that is not EchoPatch (another mod's loader, perhaps)."
	}
	if ini != "" {
		return EchoPatchIniOnly, "EchoPatch.ini is here, but dinput8.dll is missing."
	}
	return EchoPatchMissing, "EchoPatch by Wemino (GitHub) is not installed in this folder."
}

type dinputKind int

const (
	dinputOther dinputKind = iota
	dinputEchoPatch
	dinputWrapper
)

var (
	// "GOG.com Input wrapper" in the version resource (UTF-16LE) of the dinput8.dll
	// shipped by GOG F.E.A.R. Platinum (466,432 bytes, FileVersion 1.0.0.5577).
	gogInputUTF16 = []byte("G\x00O\x00G\x00.\x00c\x00o\x00m\x00 \x00I\x00n\x00p\x00u\x00t\x00 \x00w\x00r\x00a\x00p\x00p\x00e\x00r\x00")
	dxwASCII      = []byte("dxwrapper")
	dxwUTF16      = []byte("d\x00x\x00w\x00r\x00a\x00p\x00p\x00e\x00r\x00")
)

// classifyDinput8 decides what a dinput8.dll is from its strings: EchoPatch,
// GOG's input wrapper (or a dxwrapper stub renamed to dinput8.dll), or
// something else. A DLL that is not EchoPatch is also taken for GOG's when
// dxwrapper.dll sits next to it, so a later GOG build with other strings is
// still explained as GOG's file rather than as a foreign mod.
func classifyDinput8(path string) dinputKind {
	b := readHead(path, 8<<20)
	if bytes.Contains(b, echoASCII) || bytes.Contains(b, echoUTF16) {
		return dinputEchoPatch
	}
	if bytes.Contains(b, gogInputUTF16) {
		return dinputWrapper
	}
	lb := bytes.ToLower(b)
	if bytes.Contains(lb, dxwASCII) || bytes.Contains(lb, bytes.ToLower(dxwUTF16)) {
		return dinputWrapper
	}
	if findFile(filepath.Dir(path), "dxwrapper.dll") != "" {
		return dinputWrapper
	}
	return dinputOther
}

func readHead(path string, n int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, n))
	return b
}

var (
	echoASCII = []byte("EchoPatch")
	echoUTF16 = []byte("E\x00c\x00h\x00o\x00P\x00a\x00t\x00c\x00h\x00")
)

// findFile returns the real path of name inside dir, matching case-insensitively.
func findFile(dir, name string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if !e.IsDir() && strings.EqualFold(e.Name(), name) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// ------------------------------------------------------------- Steam ---

// FindSteamInstall returns the F.E.A.R. folder from Steam libraries, or "".
// steamRoots are Steam installation folders (from the registry or defaults).
func FindSteamInstall(steamRoots []string) string {
	seen := map[string]bool{}
	for _, root := range steamRoots {
		if root == "" {
			continue
		}
		libs := append([]string{root}, ParseLibraryFolders(filepath.Join(root, "steamapps", "libraryfolders.vdf"))...)
		for _, lib := range libs {
			key := strings.ToLower(filepath.Clean(lib))
			if seen[key] {
				continue
			}
			seen[key] = true
			common := filepath.Join(lib, "steamapps", "common")
			// The app manifest names the real install directory.
			names := []string{}
			if n := installDirFromManifest(filepath.Join(lib, "steamapps", "appmanifest_"+steamAppID+".acf")); n != "" {
				names = append(names, n)
			}
			names = append(names, SteamFolder)
			for _, n := range names {
				if n == "" || n == "." || n == ".." || strings.ContainsAny(n, `/\`) || strings.Contains(n, "\x00") {
					continue // the manifest must name a folder directly under "common"
				}
				d := filepath.Join(common, n)
				if findFile(d, "FEAR.exe") != "" {
					return d
				}
			}
		}
	}
	return ""
}

var vdfPair = regexp.MustCompile(`"([^"]+)"\s+"((?:[^"\\]|\\.)*)"`)

// vdfUnescape resolves the escapes Steam uses in values: \\ \" \n \t.
func vdfUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

var digits = regexp.MustCompile(`^[0-9]+$`)

// ParseLibraryFolders extracts the Steam library folders from a
// libraryfolders.vdf. Two formats exist: since 2021 each library is a block
// with a "path" value; before that the file held pairs such as "1" "D:\\Lib".
func ParseLibraryFolders(vdf string) []string {
	b, err := os.ReadFile(vdf)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range vdfPair.FindAllStringSubmatch(string(b), -1) {
		val := vdfUnescape(m[2])
		switch {
		case strings.EqualFold(m[1], "path"):
			out = append(out, val)
		case digits.MatchString(m[1]) && looksLikePath(val): // pre-2021 format
			out = append(out, val)
		}
	}
	return out
}

// looksLikePath tells a folder value from the numbers and flags that also sit
// next to numeric keys in the VDF (app ids, sizes, "1").
func looksLikePath(v string) bool {
	return strings.ContainsAny(v, `\/`) || (len(v) >= 2 && v[1] == ':')
}

func installDirFromManifest(acf string) string {
	b, err := os.ReadFile(acf)
	if err != nil {
		return ""
	}
	for _, m := range vdfPair.FindAllStringSubmatch(string(b), -1) {
		if strings.EqualFold(m[1], "installdir") {
			return vdfUnescape(m[2])
		}
	}
	return ""
}
