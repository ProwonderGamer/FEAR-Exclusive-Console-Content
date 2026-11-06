package dbmerge

import (
	"strings"
	"testing"

	"fearinstaller/internal/gadb"
)

// SHA-256 of the confirmed mod databases and of the steps that lead to them. Each was produced by the Python
// recipes from an installed mod; here they are rebuilt from the console copies alone.
const (
	goldCampaignFEAR = "536c20faecddc1ab1e7f9d7a8c66d27df54354d0631bd3980ba08312c1846ec8" // with the campaign player records
	goldIAFEAR       = "3da96af39ae9285f8df236dd447dc3c422f31c6dd00e84f47a3c6b857aa3c8b2" // + Instant Action records
	goldLiveFEAR     = "ab7bbf5d1f861086328b038bb2f23baba9325b5357cd224da9ee800eafaa1715" // + Watson reload (the mod's file)
	goldLiveFEARL    = "cdcad79ed7b8d063df42bb5354566c9351df9cfc89f7a744abf1a974a7b677bf" // + Instant Action load screens (the mod's file)

	// Xbox-only Instant Action (stock + IA records): Go regression pins, not reference hashes
	goldIAOnlyFEAR  = "ace1eecc4162ea18eb7a95375119be62197f64242b0874458c9b0e1c18bf323f"
	goldIAOnlyFEARL = "11c1b4175c35d02ea9bd5ae4ef04972b8f6de14bdb17635c9cd10637eec2432d"
)

func TestGoldenLiveDatabases(t *testing.T) {
	in := load(t)
	in.InstantAction, in.WatsonReload = true, true
	out, err := BuildAll(in)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "live FEAR.gamdb00p (with Watson reload)", out.Live.FEAR, goldLiveFEAR)
	check(t, "live FEARL.Gamdb00p (with loading bar)", out.Live.FEARL, goldLiveFEARL)
	check(t, "Holiday FEAR (unchanged by stage 4)", out.Holiday.FEAR, goldHolFEAR)

	// the stage 4 changes one step at a time, so a failure points at one change
	base := LiveInputs{PC: in.PC, PCL: in.PCL, HolidayFEAR: out.Holiday.FEAR, HolidayFEARL: out.Holiday.FEARL}
	run := func(label string, mod func(*LiveInputs), wantFEAR, wantFEARL string) {
		t.Helper()
		li := base
		mod(&li)
		r, err := BuildLive(li)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if wantFEAR != "" {
			check(t, label+" FEAR", r.FEAR, wantFEAR)
		}
		if wantFEARL != "" {
			check(t, label+" FEARL", r.FEARL, wantFEARL)
		}
	}
	run("no stage 4 changes", func(li *LiveInputs) { li.NoCampaignPlayer = true }, goldHolFEAR, goldHolFEARL)
	run("campaign player", func(li *LiveInputs) {}, goldCampaignFEAR, goldHolFEARL)
	run("Instant Action", func(li *LiveInputs) { li.InstantAction = true }, goldIAFEAR, goldLiveFEARL)
	run("Watson reload", func(li *LiveInputs) { li.InstantAction, li.WatsonReload = true, true }, goldLiveFEAR, goldLiveFEARL)
}

// TestLiveRecordRules checks the database rules on the live build: every category sorted by _stricmp name, every
// added record named with the zSFODD_ prefix and appended after every stock record, IA records only with the option.
func TestLiveRecordRules(t *testing.T) {
	in := load(t)
	in.InstantAction, in.WatsonReload = true, true
	out, err := BuildAll(in)
	if err != nil {
		t.Fatal(err)
	}
	stock, _ := gadb.Read(in.PC)
	for _, b := range [][]byte{out.Live.FEAR, out.Live.FEARL} {
		d, err := gadb.Read(b)
		if err != nil {
			t.Fatal(err)
		}
		if bad := gadb.CheckSorted(d, "live"); len(bad) > 0 {
			t.Error(bad)
		}
		n := 0
		for _, c := range d.Cats {
			for _, r := range c.Records {
				if strings.HasPrefix(r.Name, IAPrefix) {
					n++
				}
			}
		}
		if want := map[bool]int{true: 12, false: 4}[len(b) > 1000000]; n != want {
			t.Errorf("%d IA records, want %d", n, want)
		}
	}
	// stock records: only the SM15 drop fix (stage 3) and the campaign player changed their values in FEAR,
	// only ScreenFailure in FEARL
	for _, p := range [][3]string{{string(out.Live.FEAR), string(in.PC), "Arsenal/Global/Global|Character/Models/Player"},
		{string(out.Live.FEARL), string(in.PCL), "Interface/Screens/ScreenFailure"}} {
		x, err1 := readSym([]byte(p[0]))
		y, err2 := readSym([]byte(p[1]))
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}
		if got := strings.Join(changedRecords(x, y), "|"); got != p[2] {
			t.Errorf("stock records changed: %q, want %q", got, p[2])
		}
	}
	// stock record indices are unchanged
	d, _ := gadb.Read(out.Live.FEAR)
	for ci, c := range stock.Cats {
		for ri, r := range c.Records {
			if d.Cats[ci].Records[ri].Name != r.Name {
				t.Fatalf("stock %s/%s moved", c.Name, r.Name)
			}
		}
	}
	// IA off: no IA record at all
	in.InstantAction = false
	off, err := BuildAll(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range [][]byte{off.Live.FEAR, off.Live.FEARL} {
		if strings.Contains(string(b), IAPrefix) {
			t.Error("IA records present with InstantAction off")
		}
	}
	if sha(off.Live.FEARL) != goldHolFEARL {
		t.Error("FEARL must be the stage 3 FEARL with InstantAction off")
	}
}

// TestIAOnlyEqualsFullBuild: the Xbox-only Instant Action databases (no PS3 data) carry exactly the IA records of
// the confirmed live build, and change only the ScreenFailure layout among the stock records.
func TestIAOnlyEqualsFullBuild(t *testing.T) {
	in := load(t)
	in.InstantAction, in.WatsonReload = true, true
	full, err := BuildAll(in)
	if err != nil {
		t.Fatal(err)
	}
	ia, err := BuildIAOnly(in.PC, in.PCL, in.X360DB)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		label       string
		full, only  []byte
		stock       []byte
		wantChanged string
	}{{"FEAR", full.Live.FEAR, ia.FEAR, in.PC, ""}, {"FEARL", full.Live.FEARL, ia.FEARL, in.PCL, "Interface/Screens/ScreenFailure"}} {
		f, err1 := readSym(p.full)
		o, err2 := readSym(p.only)
		s, err3 := readSym(p.stock)
		if err1 != nil || err2 != nil || err3 != nil {
			t.Fatal(err1, err2, err3)
		}
		if bad := gadb.CheckSorted(o, p.label); len(bad) > 0 {
			t.Error(bad)
		}
		if got := strings.Join(changedRecords(o, s), "|"); got != p.wantChanged {
			t.Errorf("%s: stock records changed: %q, want %q", p.label, got, p.wantChanged)
		}
		fr := f.RecMap()
		n := 0
		for ci, c := range o.Cats {
			for ri, r := range c.Records {
				if ri < len(s.Cats[ci].Records) {
					if s.Cats[ci].Records[ri].Name != r.Name {
						t.Fatalf("%s: stock %s/%s moved", p.label, c.Name, r.Name)
					}
					continue
				}
				n++
				if !strings.HasPrefix(r.Name, IAPrefix) {
					t.Errorf("%s: unexpected added record %s/%s", p.label, c.Name, r.Name)
				}
				if x := fr[gadb.RecKey{Cat: c.Name, Rec: r.Name}]; x == nil || !sigEqual(x, r) {
					t.Errorf("%s: %s/%s differs from the full build", p.label, c.Name, r.Name)
				}
			}
		}
		if want := map[string]int{"FEAR": 12, "FEARL": 4}[p.label]; n != want {
			t.Errorf("%s: %d records added, want %d", p.label, n, want)
		}
		t.Logf("Xbox-only IA %s: %d bytes, sha256 %s", p.label, len(p.only), sha(p.only))
	}
	// regression pins of this Go build (no Python reference exists for the Xbox-only variant; the records are
	// proven equal to the confirmed live ones above)
	check(t, "Xbox-only IA FEAR.gamdb00p", ia.FEAR, goldIAOnlyFEAR)
	check(t, "Xbox-only IA FEARL.Gamdb00p", ia.FEARL, goldIAOnlyFEARL)
}
