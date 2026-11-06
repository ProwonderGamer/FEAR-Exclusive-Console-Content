package strdb

import (
	"fmt"
	"regexp"
	"sort"

	"fearinstaller/internal/wld"
)

// KeySpec is one string key of the SFOD-D mission and why it is needed.
type KeySpec struct{ Key, Group, Why string }

// sfoddDataKeys are the mission keys that are not B20xx dialogue lines (group "optional" = front-end menu).
var sfoddDataKeys = []KeySpec{
	{"Mission_Bonus", "required", "PS3 DB Missions/Missions/Bonus_SFOD-D.Name"},
	{"Level_SFOD-D", "required", "PS3 DB Missions/Levels/Bonus_SFOD-D.Name (load screen level title)"},
	{"Briefing_SFOD", "required", "PS3 DB Missions/Levels/Bonus_SFOD-D.Briefing (load screen briefing)"},
	{"Help_Moody", "required", "PS3 DB Missions/Levels/Bonus_SFOD-D.Help (load screen tip) - already on PC"},
	{"Objective47", "required", "sfod_d IntroDialog.CleanUpCommand objective add objective47"},
	{"Objective46", "required", "PS3 DB Interface/Objective/Objective46.Objective"},
	{"IDS_WEAPON_101", "required", "Machine Pistol / Dual Machine Pistols ShortNameId+LongNameId"},
	{"IDS_WEAPON_102", "required", "AutoShotgun ShortNameId+LongNameId"},
	{"IDS_AMMO_101", "required", "MachinePistol ammo ShortNameId+LongNameId"},
	{"IDS_AMMO_DESC_101", "required", "MachinePistol ammo DescriptionId"},
	{"IDS_AMMO_2", "required", "AutoShotgun ammo names - already on PC"},
	{"IDS_AMMO_DESC_2", "required", "AutoShotgun ammo description - already on PC"},
	{"IDS_BONUSMISSION", "optional", "front-end menu item"},
	{"IDS_HELP_BONUSMISSION", "optional", "front-end menu help text"},
	{"IDS_TITLE_BONUS_MISSION", "optional", "front-end screen title"},
}

// SFODDKeys returns the ordered key list: the data keys, then B2001..B2031 each followed by its radio
// variant "B20xxr" when the PS3 database has one.
func SFODDKeys(ps3 *DB) []KeySpec {
	out := append([]KeySpec(nil), sfoddDataKeys...)
	for n := 2001; n < 2032; n++ {
		k := fmt.Sprintf("B%d", n)
		out = append(out, KeySpec{k, "dialogue", "B20xx dialogue line"})
		if ps3.IndexOf(k+"r") >= 0 {
			out = append(out, KeySpec{k + "r", "completeness", "PS3 radio variant key"})
		}
	}
	return out
}

// BuildSFODD merges the SFOD-D mission keys, with the original PS3 text and voice path, into the stock PC
// English database. Existing PC keys are never modified. Returns the new database and the number of keys added.
func BuildSFODD(pc, ps3 *DB) (*DB, int, error) {
	added := 0
	for _, ks := range SFODDKeys(ps3) {
		v, ok := ps3.Get(ks.Key)
		if !ok {
			return nil, 0, fmt.Errorf("strdb: key %s is not in the PS3 string database", ks.Key)
		}
		r, err := pc.Set(ks.Key, v.Text, v.HasText, v.Voice, v.HasVoice, false)
		if err != nil {
			return nil, 0, err
		}
		if r == Added {
			added++
		}
	}
	return pc, added, nil
}

var dialogueProp = regexp.MustCompile(`^Dialogue\d+$`)

// WorldDialogueIDs lists the string ids named by the Dialogue objects of a world ("<StringId> <speaker>[ radio]").
func WorldDialogueIDs(objs []wld.Object) []string {
	seen := map[string]bool{}
	var ids []string
	for _, o := range objs {
		if o.Class != "Dialogue" {
			continue
		}
		for _, p := range o.Props {
			if !dialogueProp.MatchString(p.Name) || (p.Type != wld.TString && p.Type != wld.TCommand && p.Type != wld.TStringID) || p.S == "" {
				continue
			}
			f := splitFields(p.S)
			if len(f) > 0 && !seen[f[0]] {
				seen[f[0]] = true
				ids = append(ids, f[0])
			}
		}
	}
	sort.SliceStable(ids, func(i, j int) bool { return StrKey(ids[i]) < StrKey(ids[j]) })
	return ids
}

func splitFields(s string) []string {
	var out []string
	cur := []byte{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
			if len(cur) > 0 {
				out = append(out, string(cur))
				cur = cur[:0]
			}
			continue
		}
		cur = append(cur, c)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

// HolidayMissionKeys are the mission record keys (named by the database records and the menu DLL).
var HolidayMissionKeys = []string{"Level_Holiday", "Briefing_Holiday"}

// HolidayMenuKeys are the Bonus mission menu help lines (menu wording).
var HolidayMenuKeys = [][2]string{
	{"IDS_HELP_BONUS_PS3", "Play the PlayStation 3 Bonus Mission"},
	{"IDS_HELP_BONUS_X360", "Play the Xbox 360 Bonus Mission"},
}

// BuildHoliday adds the Holiday keys on top of base (the SFOD-D database): every dialogue id used by the
// Holiday world that base lacks (plus the PS3 radio variants "<id>r"), the two mission keys and the menu help
// lines. The text and voice paths come from the PS3 English database (the Xbox 360 strings live in its
// uberCatalog.cat; the PS3 disc ships the Holiday keys). Returns the number of keys added.
func BuildHoliday(base, ps3 *DB, dialogueIDs []string) (int, error) {
	var newKeys []string
	for _, k := range dialogueIDs {
		if _, ok := base.Get(k); ok {
			continue
		}
		newKeys = append(newKeys, k)
		if _, ok := ps3.Get(k + "r"); ok {
			newKeys = append(newKeys, k+"r")
		}
	}
	newKeys = append(newKeys, HolidayMissionKeys...)
	added := 0
	for _, k := range newKeys {
		v, ok := ps3.Get(k)
		if !ok {
			return 0, fmt.Errorf("strdb: %s: not in the PS3 string database", k)
		}
		text, voice := v.Text, v.Voice
		if !v.HasText {
			text = nil
		}
		if !v.HasVoice {
			voice = ""
		}
		r, err := base.Set(k, text, true, voice, true, false)
		if err != nil {
			return 0, err
		}
		if r != Added {
			return 0, fmt.Errorf("strdb: %s already present", k)
		}
		added++
	}
	for _, m := range HolidayMenuKeys {
		r, err := base.Set(m[0], Units(m[1]), true, "", true, false)
		if err != nil {
			return 0, err
		}
		if r != Added {
			return 0, fmt.Errorf("strdb: %s already present", m[0])
		}
		added++
	}
	if !base.IsSorted() {
		return 0, fmt.Errorf("strdb: result is not sorted")
	}
	return added, nil
}
