package model

import (
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"
)

// Golden SHA-256 of the X360 model stages. The final hashes equal the confirmed mod's files (the in-game shipped
// files, as listed in the content coverage table); the intermediate ones are the Python recipes' output
// (mdl2model00p.py with the DEC3N rule, make_holiday_watson.py), whose fix_lefthand.py result reproduced the
// confirmed files.
const (
	goldenSM15LeftConvert     = "0ab60558bac22fda6811967328b7be0933d67c34e33ec57376b2d15cb58aae83"
	goldenSM15Left            = "f5d0029f29f71b426ff2d39b5d8aed1d17129fef0080d0f5dbbc5395b31ef45d" // live
	goldenHolidayConvert      = "aaffbe74c9689aa14333ae72cf7fc08e9fb2c7e4090d164a572599aedca4cffb"
	goldenHolidayWatsonChild  = "c5dc73ed87ca9f8e134fa5743e00d4667dd1ecd0e93daf739fd192244a65af84"
	goldenPlayerHoliday       = "b9032d243291b713b8d52c44ea1336b78e3adf40c9104386560aca4b8aed48a9" // live
	goldenSM15machinePistol   = "36f23e9655925a96a04a1f943352fee9cddefdf1bbc8cf96d3b9d7b1e151ca74" // live
	goldenSM15Right           = "bcca6b39acbb2984f95bbcd4c39a5d89038aa5457622b0808e65dc9cd7a7d488" // live
	goldenBishopSitting       = "fb25c8d293abef649639209e4a2605f820df38a19bdc0d67f0bfc08425e1860c" // live
	goldenIanHivesHoliday     = "d3abfeb7596528a4e9669e15d281c901899f315032275e240a0bea5a0f4540f3" // live
	x360BodyName              = `chars\models\player_Deltaforce.mdl`
	x360HolidayName           = `chars\models\player_Holiday.mdl`
	x360SM15LeftName          = `guns\models_hh\SM15_Left.mdl`
	maxConsolePackedNormalGap = 0.0025 // X360 DEC3N vs PS3 CMP of the same vertex: both quantised (measured max 0.00195)
)

var x360DirectGolden = map[string]string{
	`guns\models_hh\SM15machinePistol.Model00p`:           goldenSM15machinePistol,
	`guns\models_hh\SM15_Right.Model00p`:                  goldenSM15Right,
	`chars\models\Base_BishopEvac_BishopSitting.Model00p`: goldenBishopSitting,
	`chars\models\IanHives_Holiday.Model00p`:              goldenIanHivesHoliday,
}

func TestDEC3NDecode(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-12 }
	cases := []struct {
		u       uint32
		x, y, z float64
	}{
		{511, 1, 0, 0},
		{0x201 << 10, 0, -1, 0}, // y = -511
		{0x1FF << 20, 0, 0, 1},  // z = +511
		{0x3 << 30, 0, 0, 0},    // bits 30-31 are ignored; the zero vector stays zero
		{511 | 511<<10, 1 / math.Sqrt2, 1 / math.Sqrt2, 0},
	}
	for _, c := range cases {
		x, y, z := dec3nToFloat3(c.u)
		if !near(x, c.x) || !near(y, c.y) || !near(z, c.z) {
			t.Errorf("dec3n(%#x) = %v %v %v, want %v %v %v", c.u, x, y, z, c.x, c.y, c.z)
		}
	}
}

func TestX360DirectGolden(t *testing.T) {
	for _, j := range X360Direct {
		j := j
		t.Run(filepath.Base(filepath.FromSlash(j.PC)), func(t *testing.T) {
			want, ok := x360DirectGolden[j.PC]
			if !ok {
				t.Fatalf("no golden for %s", j.PC)
			}
			src, stored := consoleModel(t, "FEAR_REF_X360", j.Console)
			out, _, err := Convert(src, stored, Options{DEC3N: j.DEC3N})
			if err != nil {
				t.Fatal(err)
			}
			checkStage(t, "convert (DEC3N)", out, want)
			// the same file read as CMP (the PS3 rule) is not the shipped one
			cmp, _, err := Convert(src, stored, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if sha(cmp) == want {
				t.Errorf("CMP decoding also gives the shipped file: the DEC3N rule is not what matters here")
			}
		})
	}
}

func TestPlayerHolidayGolden(t *testing.T) {
	src, stored := consoleModel(t, "FEAR_REF_X360", x360HolidayName)
	pc := stockPlayer(t)
	c, _, err := Convert(src, stored, Options{DEC3N: true})
	if err != nil {
		t.Fatal(err)
	}
	checkStage(t, "convert (DEC3N)", c, goldenHolidayConvert)
	w, err := MakeHolidayWatson(c)
	if err != nil {
		t.Fatal(err)
	}
	checkStage(t, "MakeHolidayWatson", w, goldenHolidayWatsonChild)
	if _, err := MakeHolidayWatson(w); err == nil {
		t.Error("MakeHolidayWatson accepted its own output (child would be duplicated)")
	}
	f, err := SetLeftHandSocket(w, pc)
	if err != nil {
		t.Fatal(err)
	}
	checkStage(t, "SetLeftHandSocket", f, goldenPlayerHoliday)
	// one call; the recipe read the socket from player_console, which carries the stock player's sockets
	pcon, err := MakePlayerConsole(pc)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range [][]byte{pc, pcon} {
		all, err := PlayerHoliday(src, stored, p)
		if err != nil {
			t.Fatal(err)
		}
		checkStage(t, "PlayerHoliday", all, goldenPlayerHoliday)
	}
}

func TestSM15LeftGolden(t *testing.T) {
	src, stored := consoleModel(t, "FEAR_REF_X360", x360SM15LeftName)
	body, _ := consoleModel(t, "FEAR_REF_X360", x360BodyName)
	hol, hstored := consoleModel(t, "FEAR_REF_X360", x360HolidayName)
	pc := stockPlayer(t)
	c, _, err := Convert(src, stored, Options{DEC3N: true})
	if err != nil {
		t.Fatal(err)
	}
	checkStage(t, "convert (DEC3N)", c, goldenSM15LeftConvert)
	holiday, err := PlayerHoliday(hol, hstored, pc)
	if err != nil {
		t.Fatal(err)
	}
	holConv, _, err := Convert(hol, hstored, Options{DEC3N: true})
	if err != nil {
		t.Fatal(err)
	}
	// the recipe's check: player_Deltaforce first (its socket is used), player_holiday must agree within 1e-3
	b, err := BakeSM15Left(c, pc, body, holConv)
	if err != nil {
		t.Fatal(err)
	}
	checkStage(t, "BakeSM15Left (X360 player_Deltaforce)", b, goldenSM15Left)
	all, err := SM15Left(src, stored, body, pc)
	if err != nil {
		t.Fatal(err)
	}
	checkStage(t, "SM15Left", all, goldenSM15Left)
	// a body that already carries the PC socket (the shipped player_holiday) must not pass as a console body
	if _, err := BakeSM15Left(c, pc, body, holiday); err == nil {
		t.Error("a body with the PC LeftHand socket passed the console-socket check")
	}
	if _, err := BakeSM15Left(src, pc, body); err == nil {
		t.Error("BakeSM15Left accepted a console (unconverted) model")
	}
}

// TestSM15LeftPS3BodyGolden: the recipe read the socket from the converted PS3 player_Deltaforce; the X360 .mdl
// carries the same socket words, so both give the shipped file.
func TestSM15LeftPS3BodyGolden(t *testing.T) {
	src, stored := consoleModel(t, "FEAR_REF_X360", x360SM15LeftName)
	pc := stockPlayer(t)
	body := convertPS3(t, x360BodyName)
	out, err := SM15Left(src, stored, body, pc)
	if err != nil {
		t.Fatal(err)
	}
	checkStage(t, "SM15Left (PS3 player_Deltaforce)", out, goldenSM15Left)
}

// packedWords lists every packed (decl type 14) word of a console model's render meshes in file order.
func packedWords(t *testing.T, m *Model) []uint32 {
	t.Helper()
	var w []uint32
	for _, ms := range m.Render.Meshes {
		if ms.External != 0 {
			continue
		}
		for _, s := range ms.Sections {
			f := s.F
			for _, e := range ms.Decls[f[8]] {
				if e.Stream == 0xFF || e.Type != tCMP {
					continue
				}
				for k := uint64(0); k < uint64(f[1]); k++ {
					o := (uint64(f[4])+uint64(f[0])+k)*uint64(f[2]) + uint64(e.Offset)
					w = append(w, binary.BigEndian.Uint32(ms.Vtx[o:]))
				}
			}
		}
	}
	return w
}

// TestX360PS3PackedCrossCheck: the PS3 copies of the six models carry the same meshes with CMP words. Decoded
// with their own rule, the X360 (DEC3N) and PS3 (CMP) vectors of every vertex must agree to quantisation
// level; the X360 words read as CMP must not (control).
func TestX360PS3PackedCrossCheck(t *testing.T) {
	for _, name := range []string{x360SM15LeftName, `guns\models_hh\SM15_Right.mdl`, `guns\models_hh\SM15machinePistol.mdl`,
		x360HolidayName, `chars\models\Base_BishopEvac_BishopSitting.mdl`, `chars\models\IanHives.mdl`} {
		name := name
		t.Run(filepath.Base(filepath.FromSlash(name)), func(t *testing.T) {
			xb, _ := consoleModel(t, "FEAR_REF_X360", name)
			pb, _ := consoleModel(t, "FEAR_REF_PS3", name)
			xm, err := Parse(xb)
			if err != nil {
				t.Fatal(err)
			}
			pm, err := Parse(pb)
			if err != nil {
				t.Fatal(err)
			}
			xw, pw := packedWords(t, xm), packedWords(t, pm)
			if len(xw) == 0 || len(xw) != len(pw) {
				t.Fatalf("packed word counts %d / %d", len(xw), len(pw))
			}
			gap, ctrl := 0.0, 0.0
			for i := range xw {
				ax, ay, az := dec3nToFloat3(xw[i])
				bx, by, bz := cmpToFloat3(pw[i])
				cx, cy, cz := cmpToFloat3(xw[i])
				gap = math.Max(gap, math.Max(math.Abs(ax-bx), math.Max(math.Abs(ay-by), math.Abs(az-bz))))
				ctrl = math.Max(ctrl, math.Max(math.Abs(cx-bx), math.Max(math.Abs(cy-by), math.Abs(cz-bz))))
			}
			t.Logf("%d words: DEC3N vs PS3 CMP max gap %.5f; X360 read as CMP %.3f", len(xw), gap, ctrl)
			if gap > maxConsolePackedNormalGap {
				t.Errorf("DEC3N and PS3 CMP vectors differ by %.5f", gap)
			}
			if ctrl < 0.5 {
				t.Errorf("control: X360 words read as CMP are close to the PS3 vectors (%.3f)", ctrl)
			}
		})
	}
}
