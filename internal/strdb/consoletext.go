package strdb

import "fmt"

// ConsoleTextFile is where the mod's own text goes, relative to the mod folder. The hook (dinput8_hook.dll,
// strtext.c) serves it on top of the stock list. The stock StringDatabase\FEAR.Strdb00p is never copied or
// replaced: online play sends strings by their index in the stock list, so that list must stay exactly as shipped.
const ConsoleTextFile = "StringDatabase/ConsoleText.Strdb00p"

// InstantActionKeys are the main-menu Instant Action item and its map screen. Their text comes from the player's
// own console English Loki.strdb (the Xbox 360 one is the reference; the PS3 one holds the same 8 texts).
var InstantActionKeys = []string{
	"IDS_INSTANTACTION", "IDS_HELP_INSTANTACTION", "IDS_TITLE_INSTANTACTION",
	"IDS_INSTANT_ACTION_MAP1", "IDS_INSTANT_ACTION_MAP2", "IDS_INSTANT_ACTION_MAP3", "IDS_INSTANT_ACTION_MAP4",
	"IDS_HELP_INSTANT_ACTION_MAP",
}

// BuildConsoleText builds ConsoleText.Strdb00p, the Go port of the project's Python console text builder:
//   - every key of merged (the stock list plus the bonus-mission keys) that the stock list lacks, with its text
//     and voice path; merged may be nil when no bonus-mission text is built (no PS3 copy);
//   - the Instant Action menu texts from console (text only: the console voice paths do not exist on PC);
//     console may be nil when Instant Action is not installed.
//
// A key already in the stock list is never repeated here. The result is _stricmp-sorted (the engine and the hook
// binary-search it) and is written as the PC format with Bytes(false, 2).
func BuildConsoleText(stock, merged, console *DB) (*DB, error) {
	if stock == nil {
		return nil, fmt.Errorf("strdb: the stock string list is required")
	}
	out := &DB{Version: 2}
	if merged != nil {
		for i, k := range merged.Keys {
			if stock.IndexOf(k) >= 0 {
				continue
			}
			e := merged.Entries[i]
			var text []uint16
			var voice string
			if e[0] >= 0 {
				text = merged.Texts[e[0]]
			}
			if e[1] >= 0 {
				voice = merged.Voices[e[1]]
			}
			if _, err := out.Set(k, text, e[0] >= 0, voice, e[1] >= 0, false); err != nil {
				return nil, err
			}
		}
	}
	if console != nil {
		for _, k := range InstantActionKeys {
			v, ok := console.Get(k)
			if !ok || !v.HasText {
				return nil, fmt.Errorf("strdb: the console string database lacks %s", k)
			}
			if stock.IndexOf(k) >= 0 {
				return nil, fmt.Errorf("strdb: %s already exists in the stock PC list", k)
			}
			if out.IndexOf(k) < 0 {
				if _, err := out.Set(k, v.Text, true, "", false, false); err != nil {
					return nil, err
				}
			}
		}
	}
	if !out.IsSorted() {
		return nil, fmt.Errorf("strdb: ConsoleText is not sorted")
	}
	return out, nil
}
