package pcgame

import (
	"os"
	"strings"
	"unicode/utf16"
)

// hookIniKey is one setting the mod's hook reads from dinput8_hook.ini, with the value the hook uses when the
// key is absent. The hook reads each with GetPrivateProfileIntW and treats any non-zero value as "on"
// (its worker thread); keep this list in step with the hook's source.
type hookIniKey struct {
	section, key string
	def          int
}

var hookIniKeys = []hookIniKey{
	{"ObjectiveHUD", "Enabled", 1},
	{"ObjectiveHUD", "HideCenteredText", 1},
	{"BonusMenu", "Enabled", 1},
	{"PlayerBody", "Enabled", 1},
	{"ConsoleText", "Enabled", 1},
	{"InstantAction", "Enabled", 1},
	{"InstantAction", "TestResultsAtStart", 0},
	{"X360HUD", "Enabled", 1},
	{"PickupFix", "Enabled", 1},
}

// HookIniTurnsOff reports whether the game folder holds a dinput8_hook.ini that changes what the hook does
// (see hookIniChangesDefaults). Only such a file is offered to be set aside.
func HookIniTurnsOff(gameDir string) bool {
	ini := findFile(gameDir, "dinput8_hook.ini")
	return ini != "" && hookIniChangesDefaults(ini)
}

// hookIniChangesDefaults reports whether the dinput8_hook.ini at path makes the hook behave differently from
// having no ini at all: some key turns a feature off (or a test mode on). The mod ships no ini, so an ini that
// only repeats the defaults needs no attention. A file that cannot be read,
// or a value this reader is not sure how Windows would read, counts as a change, so the player is still asked.
func hookIniChangesDefaults(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	vals := parseIni(iniText(raw))
	for _, k := range hookIniKeys {
		v, ok := vals[strings.ToLower(k.section)+"\x00"+strings.ToLower(k.key)]
		if !ok {
			continue
		}
		n, sure := iniInt(v)
		if !sure || (n != 0) != (k.def != 0) {
			return true
		}
	}
	return false
}

// iniText decodes the file as Windows' profile functions do for the two encodings that matter: UTF-16LE with
// its byte-order mark, otherwise single bytes (the keys and the values the hook reads are plain ASCII).
func iniText(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		return string(utf16.Decode(u))
	}
	return strings.TrimPrefix(string(b), string([]byte{0xEF, 0xBB, 0xBF}))
}

// parseIni maps "section\x00key" (both lower case, as Windows matches them case-insensitively) to the raw value.
// The first occurrence wins, as it does for GetPrivateProfileIntW; lines starting with ';' are comments.
func parseIni(text string) map[string]string {
	out := map[string]string{}
	section := ""
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || line[0] == ';':
		case line[0] == '[':
			if end := strings.IndexByte(line, ']'); end > 0 {
				section = strings.ToLower(strings.TrimSpace(line[1:end]))
			}
		default:
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				continue
			}
			key := section + "\x00" + strings.ToLower(strings.TrimSpace(line[:eq]))
			if _, seen := out[key]; !seen {
				out[key] = strings.TrimSpace(line[eq+1:])
			}
		}
	}
	return out
}

// iniInt reads a value's leading decimal number the way the hook's GetPrivateProfileIntW does (an optional
// sign, then digits; anything after them is ignored; no digits reads as 0). sure is false for a number
// written with a 0x/0o/0b prefix, which Windows reads in another base.
func iniInt(v string) (n int, sure bool) {
	if len(v) >= 2 && v[0] == '0' && strings.ContainsRune("xXoObB", rune(v[1])) {
		return 0, false
	}
	neg := false
	if v != "" && (v[0] == '+' || v[0] == '-') {
		neg, v = v[0] == '-', v[1:]
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			break
		}
		if n < 1<<30 {
			n = n*10 + int(c-'0')
		}
	}
	if neg {
		n = -n
	}
	return n, true
}
