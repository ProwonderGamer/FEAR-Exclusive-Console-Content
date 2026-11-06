package hookfill

import (
	"encoding/binary"
	"fmt"
	"strings"

	"fearinstaller/internal/strdb"
)

// textSlot is one UTF-16 string constant of the hook whose text comes from a game string list. In the template
// its units (and the terminating zero) are zero; fillText writes the text back from the player's copy.
type textSlot struct {
	off   int    // file offset of the string
	n     int    // length in UTF-16 units; a zero unit follows
	pc    bool   // the text is in the PC game's FEAR.Strdb00p (else in the Xbox 360 English Loki.strdb)
	key   string // string id
	after string // keep only the text after this marker
	trim  string // drop this suffix
}

// textSlots are the hook's game texts:
//   - the objective header the PS3-style objective HUD lays out;
//   - the tutorial line about the loose boards: the hook recognizes the PC wording and, with a pad on the
//     Xbox 360 layout, shows the Xbox 360 wording after the button name instead;
//   - the Instant Action results page title and lines ("Mission Completed" without "!" is the run state
//     the hook's status log reports).
var textSlots = []textSlot{
	{off: 0x15a30, n: 24, key: "Objective_Header"},
	{off: 0x16e1e, n: 49, pc: true, key: "Training_Boards"},
	{off: 0x16e9c, n: 45, key: "Training_Boards", after: "|Fire|"},
	{off: 0x1aa52, n: 17, key: "INSTANT_ACTION_RESULT_SUCCESSFUL", trim: "!"},
	{off: 0x1ab6c, n: 7, key: "IDS_TITLE_INSTANTACTION_SUMMARY"},
	{off: 0x1ac38, n: 22, key: "INSTANT_ACTION_RESULT_BOOSTER"},
	{off: 0x1ac66, n: 18, key: "INSTANT_ACTION_RESULT_SUCCESSFUL"},
}

// blankText sets every text slot of out (and its terminator) to zero.
func blankText(out []byte) error {
	for _, s := range textSlots {
		if s.off+2*(s.n+1) > len(out) {
			return fmt.Errorf("hook file too small for its texts")
		}
		clear(out[s.off : s.off+2*(s.n+1)])
	}
	return nil
}

// fillText writes the text slots of out (a template: every slot zero) from the PC FEAR.Strdb00p and the
// Xbox 360 English Loki.strdb.
func fillText(out, pcStrdb, x360Strdb []byte) error {
	pc, err := strdb.Parse(pcStrdb)
	if err != nil {
		return fmt.Errorf("PC FEAR.Strdb00p: %w", err)
	}
	x, err := strdb.Parse(x360Strdb)
	if err != nil {
		return fmt.Errorf("Xbox 360 Loki.strdb: %w", err)
	}
	for _, s := range textSlots {
		db, what := x, "Xbox 360 Loki.strdb"
		if s.pc {
			db, what = pc, "PC FEAR.Strdb00p"
		}
		v, ok := db.Get(s.key)
		if !ok || !v.HasText {
			return fmt.Errorf("%s: %s missing", what, s.key)
		}
		t := strdb.StringOf(v.Text)
		if s.after != "" {
			i := strings.Index(t, s.after)
			if i < 0 {
				return fmt.Errorf("%s: %s is not the expected text", what, s.key)
			}
			t = t[i+len(s.after):]
		}
		t = strings.TrimSuffix(t, s.trim)
		u := strdb.Units(t)
		if len(u) != s.n {
			return fmt.Errorf("%s: %s is %d characters, the hook expects %d", what, s.key, len(u), s.n)
		}
		slot := out[s.off : s.off+2*(s.n+1)]
		for _, b := range slot {
			if b != 0 {
				return fmt.Errorf("the hook template's text at 0x%x is not blank", s.off)
			}
		}
		for i, c := range u {
			binary.LittleEndian.PutUint16(slot[2*i:], c)
		}
	}
	return nil
}
