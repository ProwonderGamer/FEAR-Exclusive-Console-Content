package model

// Xbox 360 models: the DEC3N vertex rule and the X360-only files the mod ships.
//
// The X360 .mdl is the same LDOM v34 big-endian container as the PS3 one and uses the same decl type value (14)
// for its packed normal / tangent / binormal, but most X360 models pack them as D3DDECLTYPE_DEC3N, not RSX CMP.
// The rule was found by comparing the X360 words with the confirmed PC floats (every packed element of
// SM15machinePistol, then whole-file hashes):
//
//	x = bits 0-9, y = bits 10-19, z = bits 20-29, each a 10-bit two's-complement value / 511.0 (float64);
//	bits 30-31 unused (0 in every word seen); then re-normalised in float64 exactly like the CMP path
//	(length > 1e-6), stored as float32 rounded to nearest even.
//
// Everything else in the conversion (byte order, element order, COLOR0 drop, child names) is the PS3 rule.
// With it, Convert(..., Options{DEC3N: true}) reproduces the confirmed SM15machinePistol, SM15_Right,
// Base_BishopEvac_BishopSitting and IanHives_Holiday (from IanHives.mdl) byte for byte, and the pre-fix
// SM15_Left and player_holiday that the recipes below turn into the confirmed files. The PS3 copies of these six
// models hold CMP words and do NOT give the shipped bytes (checked: 22-33 KB differ in each), so these files
// need the Xbox 360 copy.

import (
	"errors"
	"fmt"
	"math"
)

// dec3nToFloat3 decodes an X360 DEC3N word (see the file comment).
func dec3nToFloat3(u uint32) (x, y, z float64) {
	x = float64(signExt(u&0x3FF, 10)) / 511.0
	y = float64(signExt((u>>10)&0x3FF, 10)) / 511.0
	z = float64(signExt((u>>20)&0x3FF, 10)) / 511.0
	// explicit float64() conversions forbid fused multiply-add
	ln := math.Sqrt(float64(x*x) + float64(y*y) + float64(z*z))
	if ln > 1e-6 {
		x, y, z = x/ln, y/ln, z/ln
	}
	return
}

// X360Model is one shipped model converted from the Xbox 360 catalog.
type X360Model struct {
	Console string // catalog path (case-insensitive lookup)
	PC      string // shipped game path
	DEC3N   bool   // Options.DEC3N
}

// X360Direct are the shipped models that are a plain DEC3N Convert of an X360 model (no further step).
// IanHives_Holiday is IanHives.mdl converted under its own console path (it has no child models).
var X360Direct = []X360Model{
	{`guns\models_hh\SM15machinePistol.mdl`, `guns\models_hh\SM15machinePistol.Model00p`, true},
	{`guns\models_hh\SM15_Right.mdl`, `guns\models_hh\SM15_Right.Model00p`, true},
	{`chars\models\Base_BishopEvac_BishopSitting.mdl`, `chars\models\Base_BishopEvac_BishopSitting.Model00p`, true},
	{`chars\models\IanHives.mdl`, `chars\models\IanHives_Holiday.Model00p`, true},
}

// ------------------------------------------------------------------------------------------------ player_holiday

// HolidayWatsonChild is the child model the project's Python script appends to player_holiday.
const HolidayWatsonChild = "player_watson.Model00p"

// MakeHolidayWatson appends HolidayWatsonChild to the child list of the converted Holiday player model; nothing
// else changes. Port of that script (writer byte-exactness on the input is
// asserted first, a model that already lists the child is refused).
func MakeHolidayWatson(holiday []byte) ([]byte, error) {
	m, err := Parse(holiday)
	if err != nil {
		return nil, err
	}
	rt, err := m.Serialize(m.BigEndian, m.Version)
	if err != nil {
		return nil, err
	}
	if string(rt) != string(holiday) {
		return nil, errors.New("model: writer is not byte-exact on the Holiday player model")
	}
	for _, c := range m.Children {
		if lowerLatin1(c) == lowerLatin1(HolidayWatsonChild) {
			return nil, fmt.Errorf("model: Holiday player already lists %s", HolidayWatsonChild)
		}
	}
	m.Children = append(m.Children, HolidayWatsonChild) // append only: existing order unchanged
	return m.Serialize(m.BigEndian, m.Version)
}

// PlayerHoliday builds chars\models\player_holiday.Model00p: DEC3N Convert of the X360
// chars\models\player_Holiday.mdl, MakeHolidayWatson, then SetLeftHandSocket with the stock PC player model
// (or player_console) as pcPlayer (the body part of the project's Python left-hand fix).
func PlayerHoliday(holidayMdl []byte, holidayName string, pcPlayer []byte) ([]byte, error) {
	c, _, err := Convert(holidayMdl, holidayName, Options{DEC3N: true})
	if err != nil {
		return nil, fmt.Errorf("player_Holiday: %w", err)
	}
	w, err := MakeHolidayWatson(c)
	if err != nil {
		return nil, err
	}
	return SetLeftHandSocket(w, pcPlayer)
}

// ------------------------------------------------------------------------------------------------ SM15_Left bake

// leftHandCorrection is fix_lefthand.correction: X with S_pc * X == S_con, in the hand bone's frame.
func leftHandCorrection(pc, con sockXf) (quat, vec3) {
	qx := qnorm(qmul(qconj(pc.q), con.q))
	tx := qrot(qconj(pc.q), vec3{con.p[0] - pc.p[0], con.p[1] - pc.p[1], con.p[2] - pc.p[2]})
	return qx, tx
}

// consoleLeftHand reads the console LeftHand socket from the console player bodies the way fix_lefthand.main
// does: the first body gives the value; every body must carry LeftHand on Left_hand, a RightHand equal to the
// PC body's (1e-3) and a LeftHand within 1e-3 of the first.
func consoleLeftHand(pc *Model, bodies [][]byte) (sockXf, error) {
	if len(bodies) == 0 {
		return sockXf{}, errors.New("model: no console player body for the LeftHand socket")
	}
	pcl, err := sockXform(pc, "LeftHand")
	if err != nil {
		return sockXf{}, err
	}
	rb, err := sockXform(pc, "RightHand")
	if err != nil {
		return sockXf{}, err
	}
	var con sockXf
	for i, b := range bodies {
		m, err := Parse(b)
		if err != nil {
			return sockXf{}, fmt.Errorf("console body %d: %w", i, err)
		}
		c, err := sockXform(m, "LeftHand")
		if err != nil {
			return sockXf{}, err
		}
		if i == 0 {
			con = c
		}
		if maxDiff(c, con) >= 1e-3 {
			return sockXf{}, errors.New("model: console LeftHand sockets differ")
		}
		ra, err := sockXform(m, "RightHand")
		if err != nil {
			return sockXf{}, err
		}
		if !(c.node == "Left_hand" && pcl.node == "Left_hand" && ra.node == rb.node && maxDiff(ra, rb) < 1e-3) {
			return sockXf{}, errors.New("model: console body and PC player do not share the hand sockets")
		}
	}
	return con, nil
}

const (
	d3dFloat3                                    = 2
	usagePos, usageNrm, usageTan, usageBin uint8 = 0, 3, 6, 7
)

// BakeSM15Left is the SM15_Left left-hand step (the project's Python bake_bind, as called by
// the left-hand fix): the LeftHand correction X = S_pc^-1 * S_console, expressed in joint1's frame
// (Y = B * X * B^-1), is baked into every render-mesh vertex (position, normal, tangent, binormal); the
// Laser_Sight / Flash sockets move with the mesh; every anim header radius grows to the farthest vertex.
//
// left is the converted SM15_Left (Convert with DEC3N of the X360 guns\models_hh\SM15_Left.mdl). pcPlayer is
// the stock PC player.Model00p or player_console.Model00p (PC LeftHand socket). bodies are console player
// bodies carrying the console LeftHand socket, first one used: the recipe read the converted PS3
// player_Deltaforce; the X360 player_Deltaforce.mdl (raw console bytes are fine, only the socket is read)
// carries the identical socket words. The converted player_holiday carries a socket that differs in the last
// bits; it passes the 1e-3 check but must not come first (the result would differ).
func BakeSM15Left(left, pcPlayer []byte, bodies ...[]byte) ([]byte, error) {
	pcm, err := Parse(pcPlayer)
	if err != nil {
		return nil, fmt.Errorf("PC player: %w", err)
	}
	con, err := consoleLeftHand(pcm, bodies)
	if err != nil {
		return nil, err
	}
	pc, err := sockXform(pcm, "LeftHand")
	if err != nil {
		return nil, err
	}
	qx, tx := leftHandCorrection(pc, con)

	m, err := parsePC(left)
	if err != nil {
		return nil, err
	}
	// bake_bind's checks: the original SM15_Left (three named nodes, identity root, no root translation)
	names := []string{"joint1", "Hand_attach_Jnt", "Clip_Jnt"}
	if len(m.Nodes) != len(names) {
		return nil, errors.New("model: SM15_Left: unexpected node list")
	}
	for i, n := range names {
		if s, ok := m.String(m.Nodes[i].Name); !ok || s != n {
			return nil, errors.New("model: SM15_Left: unexpected node list")
		}
	}
	if len(m.AnimData) < 8 || i16x4(m.AnimData, 0) != [4]int16{0, 0, 0, 32767} {
		return nil, errors.New("model: SM15_Left is not the original (root rotation is not identity)")
	}
	bx := m.Nodes[0].Xform
	bq := qnorm(quat{f32val(bx[3]), f32val(bx[4]), f32val(bx[5]), f32val(bx[6])})
	for k := 0; k < 3; k++ {
		if math.Abs(f32val(bx[k])) >= 1e-6 {
			return nil, errors.New("model: SM15_Left: joint1 rest has a translation")
		}
	}
	q := qnorm(qmul(qmul(bq, qx), qconj(bq)))
	t := qrot(bq, tx)
	if err := bakeMesh(m, q, t); err != nil {
		return nil, err
	}
	out, err := m.Serialize(m.BigEndian, m.Version)
	if err != nil {
		return nil, err
	}
	chk, err := Parse(out)
	if err != nil {
		return nil, err
	}
	if rt, _ := chk.Serialize(chk.BigEndian, chk.Version); string(rt) != string(out) {
		return nil, errors.New("model: BakeSM15Left output does not round-trip")
	}
	return out, nil
}

// bakeMesh is bake_lefthand.bake: turn every render-mesh vertex by (q, t), grow the anim radii, move the sockets.
func bakeMesh(m *Model, q quat, t vec3) error {
	if m.HasGeometry == 0 || m.Render == nil {
		return errors.New("model: SM15_Left has no geometry")
	}
	ord := m.order()
	far := 0.0 // farthest turned vertex from the model origin
	for _, ms := range m.Render.Meshes {
		if ms.External != 0 {
			return errors.New("model: SM15_Left: external mesh")
		}
		if len(ms.Decls) != 1 || len(ms.Sections) != 1 {
			return errors.New("model: SM15_Left: expected one decl and one section per mesh")
		}
		stride, nv := uint64(ms.Sections[0].F[2]), uint64(ms.Sections[0].F[1])
		if stride == 0 || stride*nv != uint64(len(ms.Vtx)) {
			return errors.New("model: SM15_Left: section stride does not cover the vertex buffer")
		}
		type el struct {
			off   uint64
			usage uint8
		}
		var els []el
		hasPos := false
		for _, e := range ms.Decls[0] {
			if e.Type == d3dFloat3 && (e.Usage == usagePos || e.Usage == usageNrm || e.Usage == usageTan || e.Usage == usageBin) {
				if uint64(e.Offset)+12 > stride {
					return errors.New("model: SM15_Left: element outside the vertex")
				}
				els = append(els, el{uint64(e.Offset), e.Usage})
				hasPos = hasPos || e.Usage == usagePos
			}
		}
		if !hasPos {
			return errors.New("model: SM15_Left: mesh without positions")
		}
		vtx := append([]byte(nil), ms.Vtx...)
		for base := uint64(0); base < uint64(len(vtx)); base += stride {
			for _, e := range els {
				p := base + e.off
				v := vec3{f32val(ord.Uint32(vtx[p:])), f32val(ord.Uint32(vtx[p+4:])), f32val(ord.Uint32(vtx[p+8:]))}
				r := qrot(q, v)
				if e.usage == usagePos {
					r = vec3{r[0] + t[0], r[1] + t[1], r[2] + t[2]}
					far = math.Max(far, math.Sqrt(float64(r[0]*r[0])+float64(r[1]*r[1])+float64(r[2]*r[2])))
				}
				for k := 0; k < 3; k++ {
					ord.PutUint32(vtx[p+4*uint64(k):], f32bits(r[k]))
				}
			}
		}
		ms.Vtx = vtx
	}
	// anim radius = max(stored, far * (1 + 1e-6)); the factor is computed at run time like Python's 1 + 1e-6
	eps := 1e-6
	grow := float64(far * (1 + eps))
	for i := range m.Anims {
		if grow > f32val(m.Anims[i].H[3]) { // Python max() keeps the first argument on a tie
			m.Anims[i].H[3] = f32bits(grow)
		}
	}
	// each socket is local to its node: new local = N^-1 * Y * N * S keeps it on the turned mesh
	for i := range m.Sockets {
		s := &m.Sockets[i]
		if uint64(s[0]) >= uint64(len(m.Nodes)) {
			return errors.New("model: SM15_Left: socket node out of range")
		}
		nx := m.Nodes[s[0]].Xform
		nq := qnorm(quat{f32val(nx[3]), f32val(nx[4]), f32val(nx[5]), f32val(nx[6])})
		np := vec3{f32val(nx[0]), f32val(nx[1]), f32val(nx[2])}
		sq := quat{f32val(s[2]), f32val(s[3]), f32val(s[4]), f32val(s[5])}
		sp := vec3{f32val(s[6]), f32val(s[7]), f32val(s[8])}
		wq := qmul(nq, sq)
		wp := vadd(np, qrot(nq, sp)) // socket in model space
		wq = qmul(q, wq)
		wp = vadd(qrot(q, wp), t) // turned with the mesh
		lq := qnorm(qmul(qconj(nq), wq))
		lp := qrot(qconj(nq), vsub(wp, np))
		for k := 0; k < 4; k++ {
			s[2+k] = f32bits(lq[k])
		}
		for k := 0; k < 3; k++ {
			s[6+k] = f32bits(lp[k])
		}
	}
	return nil
}

// SM15Left builds guns\models_hh\SM15_Left.Model00p from the X360 copy: DEC3N Convert of SM15_Left.mdl, then
// BakeSM15Left with the console LeftHand socket of bodyMdl (the X360 chars\models\player_Deltaforce.mdl) and
// the PC LeftHand socket of pcPlayer (stock PC player.Model00p or player_console.Model00p).
func SM15Left(leftMdl []byte, leftName string, bodyMdl, pcPlayer []byte) ([]byte, error) {
	c, _, err := Convert(leftMdl, leftName, Options{DEC3N: true})
	if err != nil {
		return nil, fmt.Errorf("SM15_Left: %w", err)
	}
	return BakeSM15Left(c, pcPlayer, bodyMdl)
}
