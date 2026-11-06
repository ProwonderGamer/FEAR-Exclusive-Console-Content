package pcgame

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// An ini that repeats every default of the hook, plus a key the hook no longer reads (Active). It must not be
// offered for removal.
const defaultsOnlyIni = "; dinput8_hook.dll settings (loaded by EchoPatch's dinput8.dll).\r\n" +
	"[ObjectiveHUD]\r\nEnabled=1\r\nHideCenteredText=1\r\n[BonusMenu]\r\nEnabled=1\r\n" +
	"[X360HUD]\r\nEnabled=1\r\nActive=1\r\n"

func TestHookIniTurnsOff(t *testing.T) {
	cases := []struct {
		name, text string
		want       bool
	}{
		{"defaults only", defaultsOnlyIni, false},
		{"empty file", "", false},
		{"comments and unknown keys only", "; nothing\n[Other]\nEnabled=0\n[X360HUD]\nActive=0\n", false},
		{"a feature turned off", "[X360HUD]\nEnabled=0\n", true},
		{"section and key in other case", "[x360hud]\nENABLED = 0\n", true},
		{"test mode turned on", "[InstantAction]\nTestResultsAtStart=1\n", true},
		{"non-numeric reads as 0, so off", "[PickupFix]\nEnabled=no\n", true},
		{"trailing text after the number", "[BonusMenu]\nEnabled=1 ; keep\n", false},
		{"first occurrence wins", "[PlayerBody]\nEnabled=1\nEnabled=0\n", false},
		{"any non-zero is on", "[ConsoleText]\nEnabled=2\n", false},
		{"hex prefix is not read here, so ask", "[ConsoleText]\nEnabled=0x1\n", true},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "dinput8_hook.ini"), []byte(c.text), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := HookIniTurnsOff(dir); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHookIniTurnsOffUTF16AndAbsent(t *testing.T) {
	dir := t.TempDir()
	if HookIniTurnsOff(dir) {
		t.Error("no ini: got true")
	}
	u := utf16.Encode([]rune("[ObjectiveHUD]\r\nEnabled=0\r\n"))
	b := []byte{0xFF, 0xFE}
	for _, r := range u {
		b = append(b, byte(r), byte(r>>8))
	}
	if err := os.WriteFile(filepath.Join(dir, "DINPUT8_HOOK.INI"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if !HookIniTurnsOff(dir) {
		t.Error("UTF-16 ini turning a feature off: got false")
	}
}

func TestHookIniUnreadableCountsAsChange(t *testing.T) {
	// a folder named like the file cannot be read as one: the player is still asked
	if !hookIniChangesDefaults(t.TempDir()) {
		t.Error("unreadable ini: got false")
	}
}
