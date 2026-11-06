package model

// Watson SAS-12 player arms (chars\models\player_watson.Model00p): the three post-convert fix steps the mod
// ships, byte-exact ports of the project's Python recipes:
//
//	Convert(PS3 chars\models\player_watson.mdl)            ee5ed497...  (convert.go)
//	FixWatsonReload       fix_watson_reload.py      7b76c14f...  (left hand fly-off at reload end)
//	SplitWatsonReloadCues watson_reload_split.py    8aaab345...  (reload sound cues: model part only)
//	WatsonLeftHandGrip    watson_lefthand_grip.py   d9d9d6c0...  (left hand on the handguard)
//
// Console copies: the PS3 player_watson.mdl and player_Deltaforce.mdl. The Xbox 360 copies do NOT give the same
// file: the X360 models store DEC3N normals that Convert does not decode (convert_test.go), so an Xbox-only
// install cannot build this model.
//
// The float math follows numpy: float64 arithmetic in the recipe's order, products wrapped in float64() so the
// compiler cannot fuse them, dot products summed left to right (numpy's dot for these short vectors, checked
// against Python 3.12 / numpy 2.5.4 on this PC), quaternions stored as 4 x i16 / 32768.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

type vec3 [3]float64

func vadd(a, b vec3) vec3 { return vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func vsub(a, b vec3) vec3 { return vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func vscale(a vec3, s float64) vec3 {
	return vec3{float64(a[0] * s), float64(a[1] * s), float64(a[2] * s)}
}
func vdiv(a vec3, s float64) vec3 { return vec3{a[0] / s, a[1] / s, a[2] / s} }

// vdot is numpy's a @ b for 3-vectors (summed left to right from 0).
func vdot(a, b vec3) float64 {
	return float64(a[0]*b[0]) + float64(a[1]*b[1]) + float64(a[2]*b[2])
}

func vnorm(a vec3) float64 { return math.Sqrt(vdot(a, a)) } // np.linalg.norm

func vcross(a, b vec3) vec3 { // np.cross
	return vec3{float64(a[1]*b[2]) - float64(a[2]*b[1]), float64(a[2]*b[0]) - float64(a[0]*b[2]),
		float64(a[0]*b[1]) - float64(a[1]*b[0])}
}

func qdot(a, b quat) float64 {
	return float64(a[0]*b[0]) + float64(a[1]*b[1]) + float64(a[2]*b[2]) + float64(a[3]*b[3])
}

// qmul is animlib.qmul (x, y, z, w), each component evaluated left to right.
func qmul(a, b quat) quat {
	ax, ay, az, aw := a[0], a[1], a[2], a[3]
	bx, by, bz, bw := b[0], b[1], b[2], b[3]
	return quat{
		float64(aw*bx) + float64(ax*bw) + float64(ay*bz) - float64(az*by),
		float64(aw*by) - float64(ax*bz) + float64(ay*bw) + float64(az*bx),
		float64(aw*bz) + float64(ax*by) - float64(ay*bx) + float64(az*bw),
		float64(aw*bw) - float64(ax*bx) - float64(ay*by) - float64(az*bz),
	}
}

func qconj(a quat) quat { return quat{-a[0], -a[1], -a[2], a[3]} }
func qneg(a quat) quat  { return quat{-a[0], -a[1], -a[2], -a[3]} }

// qrot is animlib.qrot: q * (v, 0) * conj(q), the full product (no shortcut, so rounding matches).
func qrot(q quat, v vec3) vec3 {
	r := qmul(qmul(q, quat{v[0], v[1], v[2], 0.0}), qconj(q))
	return vec3{r[0], r[1], r[2]}
}

var rad2deg = 180.0 / piVar // numpy degrees(): x * (180.0 / NPY_PI), the quotient rounded at run time
var piVar = math.Pi

// qang is animlib.qang: the angle between two rotations in degrees.
func qang(a, b quat) float64 {
	return 2 * float64(math.Acos(math.Min(1, math.Abs(qdot(a, b))))*rad2deg)
}

// slerp is animlib.slerp.
func slerp(a, b quat, t float64) quat {
	d := qdot(a, b)
	if d < 0 {
		b = qneg(b)
		d = -d
	}
	if d > 0.9995 {
		var r quat
		for i := range r {
			r[i] = a[i] + float64((b[i]-a[i])*t)
		}
		return qnorm(r)
	}
	th := math.Acos(d)
	s1, s2, s3 := math.Sin(float64((1-t)*th)), math.Sin(float64(t*th)), math.Sin(th)
	var r quat
	for i := range r {
		r[i] = (float64(s1*a[i]) + float64(s2*b[i])) / s3
	}
	return r
}

func smoothstep(u float64) float64 { return float64(float64(u*u) * (3 - float64(2*u))) }

// quatToI16 is to_i16 of both recipes: of the 16 floor/ceil roundings of q*32768 (clipped to i16) the one
// closest to q, ties broken by the length closest to 32768 (first in itertools.product order wins).
func quatToI16(q quat) ([4]int16, error) {
	var base [4]float64
	for i := range q {
		if math.IsNaN(q[i]) || math.IsInf(q[i], 0) {
			return [4]int16{}, errors.New("model: quaternion is not finite")
		}
		base[i] = float64(q[i] * 32768.0)
	}
	var best [4]int16
	var bErr, bN float64
	for f := 0; f < 16; f++ {
		var v [4]int64
		for i := 0; i < 4; i++ {
			x := int64(math.Floor(base[i])) + int64((f>>(3-i))&1)
			v[i] = max(-32768, min(32767, x))
		}
		var e float64
		var n int64
		for i := 0; i < 4; i++ {
			d := float64(v[i])/32768.0 - q[i]
			e += float64(d * d)
			n += v[i] * v[i]
		}
		nf := math.Abs(float64(n) - 32768.0*32768.0)
		if f == 0 || e < bErr || (e == bErr && nf < bN) {
			bErr, bN = e, nf
			for i := range v {
				best[i] = int16(v[i])
			}
		}
	}
	return best, nil
}

// ------------------------------------------------------------------------------------------------ anim view

// animView is animlib.Anim: one animation's track layout in AnimData (PC models: little endian).
type animView struct {
	m    *Model
	name string
	ts   *TrackSet
	base int
	nk   int
	comp bool
	t    []uint32 // key times (ms)
}

func newAnimView(m *Model, i int) (*animView, error) {
	a := &m.Anims[i]
	name, ok := m.String(a.H[4])
	if !ok {
		return nil, errors.New("model: bad animation name offset")
	}
	if uint64(a.H[6]) >= uint64(len(m.TrackSets)) {
		return nil, fmt.Errorf("model: animation %s: track set out of range", name)
	}
	v := &animView{m: m, name: name, ts: &m.TrackSets[a.H[6]], base: int(a.H[7]), nk: int(a.H[9]), comp: a.H[8] != 0}
	for k := 0; k < v.nk; k++ {
		v.t = append(v.t, a.Keys[2*k])
	}
	return v, nil
}

// loc returns the byte offset of node's position (kind 0) or rotation (kind 1) at key k and whether the track
// is constant; ok=false when the node has no such track.
func (a *animView) loc(node, kind, k int) (off int, isConst, ok bool, err error) {
	j := 2*node + kind
	if j >= len(a.ts.Tracks) {
		return 0, false, false, fmt.Errorf("model: animation %s: node %d outside the track set", a.name, node)
	}
	o := int(a.ts.Tracks[j])
	if o == 0xFFFF {
		return 0, false, false, nil
	}
	if o&0x8000 != 0 {
		off, isConst = a.base+(o&0x7FFF), true
	} else {
		off = a.base + int(a.ts.Const) + k*int(a.ts.Frame) + o
	}
	size := 8
	if kind == 0 && !a.comp {
		size = 12
	} else if kind == 0 {
		size = 6
	}
	if off < 0 || off+size > len(a.m.AnimData) {
		return 0, false, false, fmt.Errorf("model: animation %s: track data out of range", a.name)
	}
	return off, isConst, true, nil
}

func i16x4(d []byte, off int) [4]int16 {
	var v [4]int16
	for i := range v {
		v[i] = int16(binary.LittleEndian.Uint16(d[off+2*i:]))
	}
	return v
}

func putI16x4(d []byte, off int, v [4]int16) {
	for i := range v {
		binary.LittleEndian.PutUint16(d[off+2*i:], uint16(v[i]))
	}
}

func i16quat(v [4]int16) quat {
	return quat{float64(v[0]), float64(v[1]), float64(v[2]), float64(v[3])}
}

func findAnim(m *Model, name string) (int, error) {
	for i, a := range m.Anims {
		if n, ok := m.String(a.H[4]); ok && lowerLatin1(n) == lowerLatin1(name) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("model: animation %s not found", name)
}

func parsePC(data []byte) (*Model, error) {
	m, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if m.BigEndian || m.Version != 33 {
		return nil, errors.New("model: expected a PC model (MODL v33); convert the console file first")
	}
	return m, nil
}

// ------------------------------------------------------------------------------ reload: left hand fly-off

// watsonReloadLeft are the left shoulder..finger nodes with animated rotation tracks in the reload.
var watsonReloadLeft = []int{9, 10, 11, 12, 13, 16, 17, 18, 19, 20, 22, 23, 24, 25, 26, 27}

// watsonReloadAnchor is the last good key (1966 ms) of the reloads: the left hand is still on the foregrip.
const watsonReloadAnchor = 34

// FixWatsonReload fixes the left hand flying off at the end of the Watson reload (watson_reload,
// watson_crouch_reload): the left-arm rotation tracks of keys 35..last are re-keyed as an eased slerp from key
// 34 to the next pose (watson_aim key 0 for the standing reload, whose last key takes those exact values; the
// crouch reload eases to its own last key, which is kept). Nothing else changes. Port of
// the project's Python fix_watson_reload (default mode, both animations).
//
// watson is the PC player_watson model from Convert of the PS3 .mdl (ee5ed497... gives 7b76c14f...).
func FixWatsonReload(watson []byte) ([]byte, error) {
	m, err := parsePC(watson)
	if err != nil {
		return nil, err
	}
	data := append([]byte(nil), m.AnimData...)
	for _, spec := range []struct{ name, next string }{{"watson_reload", "watson_aim"}, {"watson_crouch_reload", ""}} {
		ai, err := findAnim(m, spec.name)
		if err != nil {
			return nil, err
		}
		A, err := newAnimView(m, ai)
		if err != nil {
			return nil, err
		}
		last := A.nk - 1
		if last <= watsonReloadAnchor {
			return nil, fmt.Errorf("model: %s has only %d keys", spec.name, A.nk)
		}
		var T *animView
		if spec.next != "" {
			ti, err := findAnim(m, spec.next)
			if err != nil {
				return nil, err
			}
			if T, err = newAnimView(m, ti); err != nil {
				return nil, err
			}
		}
		for _, n := range watsonReloadLeft {
			_, c, ok, err := A.loc(n, 1, 0)
			if err != nil {
				return nil, err
			}
			if !ok || c {
				return nil, fmt.Errorf("model: %s node %d: rotation track must be animated", spec.name, n)
			}
			raw := func(k int) (quat, int, error) {
				o, _, _, err := A.loc(n, 1, k)
				if err != nil {
					return quat{}, 0, err
				}
				return i16quat(i16x4(data, o)), o, nil
			}
			ra, _, err := raw(watsonReloadAnchor)
			if err != nil {
				return nil, err
			}
			qa := qnorm(quat{ra[0] / 32768.0, ra[1] / 32768.0, ra[2] / 32768.0, ra[3] / 32768.0})
			var rb quat
			var lb [4]int16
			if T != nil {
				o, _, ok, err := T.loc(n, 1, 0)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("model: %s node %d has no rotation track", spec.next, n)
				}
				lb = i16x4(m.AnimData, o)
				rb = i16quat(lb)
			} else {
				if rb, _, err = raw(last); err != nil {
					return nil, err
				}
			}
			qb := qnorm(quat{rb[0] / 32768.0, rb[1] / 32768.0, rb[2] / 32768.0, rb[3] / 32768.0})
			qbAl := qb
			if qdot(qa, qb) < 0 {
				qbAl = qneg(qb)
			}
			prev := ra
			for k := watsonReloadAnchor + 1; k <= last; k++ {
				o, _, _, err := A.loc(n, 1, k)
				if err != nil {
					return nil, err
				}
				var nv [4]int16
				switch {
				case k == last && T != nil:
					nv = lb // exact first-frame pose of the next animation
				case k == last:
					continue // crouch: last key kept as is
				default:
					u := float64(int64(A.t[k])-int64(A.t[watsonReloadAnchor])) / float64(int64(A.t[last])-int64(A.t[watsonReloadAnchor]))
					q := qnorm(slerp(qa, qbAl, smoothstep(u)))
					if qdot(q, quat{prev[0] / 32768.0, prev[1] / 32768.0, prev[2] / 32768.0, prev[3] / 32768.0}) < 0 {
						q = qneg(q) // same hemisphere as the previous key
					}
					if nv, err = quatToI16(q); err != nil {
						return nil, err
					}
				}
				putI16x4(data, o, nv)
				prev = i16quat(nv)
			}
		}
	}
	m.AnimData = data
	return m.Serialize(m.BigEndian, m.Version)
}

// ------------------------------------------------------------------------------ reload: sound cues

// SplitWatsonReloadCues is the model part of the reload-sound fix (cue times set in game): in
// watson_reload and watson_crouch_reload the "sound_key 2" cue moves from 966 ms to 1866 ms and a new
// "sound_key 3" cue (string appended) plays at 1966 ms, so the split recording's parts 2 and 3 play with the
// drum going in and the drum wheel turning. Port of section 2 of the project's Python reload split (its WAV split
// and database parts are separate steps). 7b76c14f... gives 8aaab345...
func SplitWatsonReloadCues(watson []byte) ([]byte, error) {
	m, err := parsePC(watson)
	if err != nil {
		return nil, err
	}
	if len(m.Strings)%4 != 0 {
		return nil, errors.New("model: string block is not 4-aligned")
	}
	strOfs := func(s string) (uint32, error) {
		k := bytes.Index(m.Strings, append([]byte(s), 0))
		if k <= 0 || m.Strings[k-1] != 0 { // a whole string, not the tail of a longer one
			return 0, fmt.Errorf("model: string %q not found", s)
		}
		return uint32(k), nil
	}
	sk1, err := strOfs("sound_key 1")
	if err != nil {
		return nil, err
	}
	sk2, err := strOfs("sound_key 2")
	if err != nil {
		return nil, err
	}
	if bytes.Contains(m.Strings, []byte("sound_key 3\x00")) {
		return nil, errors.New("model: sound_key 3 already present")
	}
	sk3 := uint32(len(m.Strings))
	m.Strings = append(append([]byte(nil), m.Strings...), "sound_key 3\x00"...) // 12 bytes: stays 4-aligned
	times := []uint32{433, 966, 1566, 1866, 1966}
	before := map[uint32]uint32{433: sk1, 966: sk2, 1566: 0, 1866: 0, 1966: 0}
	cues := map[uint32]uint32{433: sk1, 966: 0, 1566: 0, 1866: sk2, 1966: sk3} // key time (ms) -> command
	done := 0
	for ai := range m.Anims {
		a := &m.Anims[ai]
		name, _ := m.String(a.H[4])
		if name != "watson_reload" && name != "watson_crouch_reload" {
			continue
		}
		idx := map[uint32]int{}
		for k := 0; 2*k < len(a.Keys); k++ {
			if _, seen := idx[a.Keys[2*k]]; !seen {
				idx[a.Keys[2*k]] = k
			}
		}
		for _, t := range times {
			k, ok := idx[t]
			if !ok {
				return nil, fmt.Errorf("model: %s has no key at %d ms", name, t)
			}
			if a.Keys[2*k+1] != before[t] {
				return nil, fmt.Errorf("model: %s: unexpected cue at %d ms", name, t)
			}
		}
		keys := append([]uint32(nil), a.Keys...)
		for _, t := range times {
			keys[2*idx[t]+1] = cues[t]
		}
		var got []string
		for k := 0; 2*k < len(keys); k++ {
			if c := keys[2*k+1]; c != 0 {
				s, _ := m.String(c)
				got = append(got, fmt.Sprintf("%d:%s", keys[2*k], s))
			}
		}
		if fmt.Sprint(got) != "[433:sound_key 1 1866:sound_key 2 1966:sound_key 3]" {
			return nil, fmt.Errorf("model: %s: cues after the move are %v", name, got)
		}
		a.Keys = keys
		done++
	}
	if done != 2 {
		return nil, fmt.Errorf("model: found %d of the 2 Watson reload animations", done)
	}
	return m.Serialize(m.BigEndian, m.Version)
}

// ------------------------------------------------------------------------------ left hand grip

// rig is animlib.Rig: the parent body's skeleton (names, parents, bind pose).
type rig struct {
	par   []int
	bindP []vec3
	bindR []quat
}

func newRig(body *Model) *rig {
	n := len(body.Nodes)
	r := &rig{par: make([]int, n), bindP: make([]vec3, n), bindR: make([]quat, n)}
	type ent struct{ i, left int }
	var st []ent
	for i, nd := range body.Nodes {
		for len(st) > 0 && st[len(st)-1].left == 0 {
			st = st[:len(st)-1]
		}
		r.par[i] = -1
		if len(st) > 0 {
			r.par[i] = st[len(st)-1].i
			st[len(st)-1].left--
		}
		st = append(st, ent{i, int(nd.NChildren)})
		x := nd.Xform
		r.bindP[i] = vec3{f32val(x[0]), f32val(x[1]), f32val(x[2])}
		r.bindR[i] = quat{f32val(x[3]), f32val(x[4]), f32val(x[5]), f32val(x[6])}
	}
	return r
}

type xf struct {
	p vec3
	q quat
}

// local returns every node's local (pos, rot) at key k of A, the bind-local value where A has no track.
func (r *rig) local(A *animView, k int) ([]xf, error) {
	out := make([]xf, len(r.par))
	d := A.m.AnimData
	for i := range r.par {
		po, _, pok, err := A.loc(i, 0, k)
		if err != nil {
			return nil, err
		}
		ro, _, rok, err := A.loc(i, 1, k)
		if err != nil {
			return nil, err
		}
		var bp vec3
		var br quat
		if !pok || !rok {
			if pa := r.par[i]; pa < 0 {
				bp, br = r.bindP[i], r.bindR[i]
			} else {
				br = qmul(qconj(r.bindR[pa]), r.bindR[i])
				bp = qrot(qconj(r.bindR[pa]), vsub(r.bindP[i], r.bindP[pa]))
			}
		}
		var p vec3
		switch {
		case !pok:
			p = bp
		case A.comp:
			for j := 0; j < 3; j++ {
				p[j] = float64(int16(binary.LittleEndian.Uint16(d[po+2*j:]))) / 64.0
			}
		default:
			for j := 0; j < 3; j++ {
				p[j] = f32val(binary.LittleEndian.Uint32(d[po+4*j:]))
			}
		}
		q := br
		if rok {
			v := i16quat(i16x4(d, ro))
			q = qnorm(quat{v[0] / 32768., v[1] / 32768., v[2] / 32768., v[3] / 32768.})
		}
		out[i] = xf{p, q}
	}
	return out, nil
}

func (r *rig) fk(loc []xf) []xf {
	w := make([]xf, len(loc))
	for i, l := range loc {
		if pa := r.par[i]; pa < 0 {
			w[i] = l
		} else {
			w[i] = xf{vadd(w[pa].p, qrot(w[pa].q, l.p)), qnorm(qmul(w[pa].q, l.q))}
		}
	}
	return w
}

// rotBetween is the shortest rotation taking direction a to direction b.
func rotBetween(a, b vec3) quat {
	a = vdiv(a, vnorm(a))
	b = vdiv(b, vnorm(b))
	c := vcross(a, b)
	return qnorm(quat{c[0], c[1], c[2], 1 + vdot(a, b)})
}

// Left hand grip design values (from the project's Python grip script; not game data).
var gripMove = vec3{3.0, -3.0, 0.0} // gun frame: 3 units in towards the barrel, 3 units down

const (
	gripWFull, gripWZero, gripAngScale = 1.0, 6.0, 3.0
	gripReachMargin                    = 0.05
	gripRampMS                         = 200.0
	gripShoulder, gripArmU, gripArmL   = 9, 10, 11
	gripHand, gripRightHand            = 12, 31
)

// WatsonLeftHandGrip moves the left hand of every Watson animation rigidly by (+3, -3, 0) in the gun frame
// (closer to the underside of the barrel), weighted by how close the hand is to the standard grip, and re-solves
// Left_armu/Left_arml/Left_hand with two-bone IK; every other byte stays. Port of the project's Python grip script.
//
// watson is the SplitWatsonReloadCues output (8aaab345... gives d9d9d6c0...); body is the PC player body the
// arms attach to (Convert of the PS3 chars\models\player_Deltaforce.mdl): its skeleton and RightHand socket
// define the gun frame.
func WatsonLeftHandGrip(watson, body []byte) ([]byte, error) {
	m, err := parsePC(watson)
	if err != nil {
		return nil, err
	}
	bm, err := Parse(body)
	if err != nil {
		return nil, fmt.Errorf("body: %w", err)
	}
	if len(bm.Nodes) <= gripRightHand {
		return nil, errors.New("model: body skeleton too small")
	}
	rg := newRig(bm)
	if rg.par[gripArmU] != gripShoulder || rg.par[gripArmL] != gripArmU || rg.par[gripHand] != gripArmL {
		return nil, errors.New("model: body left arm chain is not nodes 9-10-11-12")
	}
	si, err := socketIndex(bm, "RightHand")
	if err != nil {
		return nil, err
	}
	s := bm.Sockets[si]
	SQ := qnorm(quat{f32val(s[2]), f32val(s[3]), f32val(s[4]), f32val(s[5])})
	SP := vec3{f32val(s[6]), f32val(s[7]), f32val(s[8])}
	gun := func(W []xf) (vec3, quat) { // gun frame = Right_hand world pose x RightHand socket
		h := W[gripRightHand]
		return vadd(h.p, qrot(h.q, SP)), qnorm(qmul(h.q, SQ))
	}
	handInGun := func(W []xf) (vec3, quat) {
		gp, gq := gun(W)
		return qrot(qconj(gq), vsub(W[gripHand].p, gp)), qnorm(qmul(qconj(gq), W[gripHand].q))
	}
	pose := func(A *animView, k int) ([]xf, error) {
		l, err := rg.local(A, k)
		if err != nil {
			return nil, err
		}
		return rg.fk(l), nil
	}
	ai0, err := findAnim(m, "watson_aim")
	if err != nil {
		return nil, err
	}
	A0, err := newAnimView(m, ai0)
	if err != nil {
		return nil, err
	}
	if A0.nk == 0 {
		return nil, errors.New("model: watson_aim has no keys")
	}
	W0, err := pose(A0, 0)
	if err != nil {
		return nil, err
	}
	refP, refQ := handInGun(W0)
	arm := [3]int{gripArmU, gripArmL, gripHand}
	data := append([]byte(nil), m.AnimData...)
	for ai := range m.Anims {
		A, err := newAnimView(m, ai)
		if err != nil {
			return nil, err
		}
		type pend struct {
			v [4]int16
		}
		pending := map[int]pend{}
		var order []int
		poses := make([][]xf, A.nk)
		for k := range poses {
			if poses[k], err = pose(A, k); err != nil {
				return nil, err
			}
		}
		// pass 1: grip weight w and the largest reachable share s of the move, per key
		ws, smax := make([]float64, A.nk), make([]float64, A.nk)
		for k, W := range poses {
			p, q := handInGun(W)
			u := vnorm(vsub(p, refP))
			if a := qang(q, refQ) / gripAngScale; a > u {
				u = a
			}
			w := 0.0
			switch {
			case u <= gripWFull:
				w = 1.0
			case u >= gripWZero:
				w = 0.0
			default:
				w = 1 - smoothstep((u-gripWFull)/(gripWZero-gripWFull))
			}
			_, gq := gun(W)
			Sh, E, Wr := W[gripArmU].p, W[gripArmL].p, W[gripHand].p
			reach := vnorm(vsub(E, Sh)) + vnorm(vsub(Wr, E)) - gripReachMargin
			sc := 100 // s = sc/100, Python round(s - 0.01, 2)
			for sc > 0 && vnorm(vsub(vadd(Wr, qrot(gq, vscale(vscale(gripMove, w), float64(sc)/100))), Sh)) > reach {
				sc--
			}
			ws[k], smax[k] = w, float64(sc)/100
		}
		// pass 2: where s < 1, ease it in and out over gripRampMS so the hand never jumps between keys
		sm := make([]float64, A.nk)
		for k := range sm {
			best := 0.0
			for j := 0; j < A.nk; j++ {
				x := smax[j] + math.Abs(float64(A.t[k])-float64(A.t[j]))/gripRampMS
				if j == 0 || x < best {
					best = x
				}
			}
			sm[k] = math.Min(1.0, best)
		}
		for k, W := range poses {
			w := float64(ws[k] * sm[k])
			var nq [3]*quat
			if w != 0 {
				_, gq := gun(W)
				Sh, E, Wr := W[gripArmU].p, W[gripArmL].p, W[gripHand].p
				a, b := vnorm(vsub(E, Sh)), vnorm(vsub(Wr, E))
				T := vadd(Wr, qrot(gq, vscale(gripMove, w))) // moved wrist; hand world rotation unchanged
				d := vnorm(vsub(T, Sh))
				ax := vdiv(vsub(T, Sh), d)
				x := (float64(a*a) - float64(b*b) + float64(d*d)) / float64(2*d)
				h := math.Sqrt(float64(a*a) - float64(x*x))
				es := vsub(E, Sh)
				pole := vsub(es, vscale(ax, vdot(es, ax))) // keep the elbow's side
				pole = vdiv(pole, vnorm(pole))
				E2 := vadd(vadd(Sh, vscale(ax, x)), vscale(pole, h))
				d10 := rotBetween(vsub(E, Sh), vsub(E2, Sh))
				R10 := qnorm(qmul(d10, W[gripArmU].q))
				R11 := qnorm(qmul(rotBetween(qrot(d10, vsub(Wr, E)), vsub(T, E2)), qmul(d10, W[gripArmL].q)))
				n10 := qnorm(qmul(qconj(W[gripShoulder].q), R10))
				n11 := qnorm(qmul(qconj(R10), R11))
				n12 := qnorm(qmul(qconj(R11), W[gripHand].q))
				nq = [3]*quat{&n10, &n11, &n12}
			}
			for j, n := range arm {
				o, c, ok, err := A.loc(n, 1, k)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("model: %s node %d: no rotation track", A.name, n)
				}
				old := i16x4(m.AnimData, o)
				v := old
				if nq[j] != nil {
					qn := *nq[j]
					if qdot(qn, i16quat(old)) < 0 { // keep the stored hemisphere
						qn = qneg(qn)
					}
					if v, err = quatToI16(qn); err != nil {
						return nil, fmt.Errorf("model: %s key %d: %w", A.name, k, err)
					}
				}
				if pv, seen := pending[o]; seen && c && pv.v != v {
					return nil, fmt.Errorf("model: %s node %d: constant track would vary", A.name, n)
				}
				if _, seen := pending[o]; !seen {
					order = append(order, o)
				}
				pending[o] = pend{v}
			}
		}
		for _, o := range order {
			putI16x4(data, o, pending[o].v)
		}
	}
	m.AnimData = data
	return m.Serialize(m.BigEndian, m.Version)
}

// PlayerWatson builds chars\models\player_watson.Model00p from the PS3 copies: Convert of
// chars\models\player_watson.mdl and of chars\models\player_Deltaforce.mdl (the body whose skeleton and
// RightHand socket the grip step uses), then FixWatsonReload, SplitWatsonReloadCues and WatsonLeftHandGrip.
// watsonName and bodyName are the stored catalog names (Convert uses them to map child-model paths).
func PlayerWatson(watsonMdl []byte, watsonName string, bodyMdl []byte, bodyName string) ([]byte, error) {
	w, _, err := Convert(watsonMdl, watsonName, Options{})
	if err != nil {
		return nil, fmt.Errorf("player_watson: %w", err)
	}
	b, _, err := Convert(bodyMdl, bodyName, Options{})
	if err != nil {
		return nil, fmt.Errorf("player_Deltaforce: %w", err)
	}
	if w, err = FixWatsonReload(w); err != nil {
		return nil, err
	}
	if w, err = SplitWatsonReloadCues(w); err != nil {
		return nil, err
	}
	return WatsonLeftHandGrip(w, b)
}
