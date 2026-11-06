package model

// Post-convert fix steps, byte-exact ports of the project's Python recipes. Each takes model bytes and
// returns model bytes; float math follows the Python (float64 arithmetic in the same order, float32 storage
// rounded to nearest even, as struct.pack('<f') does).

import (
	"errors"
	"fmt"
	"math"
)

func f32bits(x float64) uint32 { return math.Float32bits(float32(x)) } // Python struct.pack('<f', x)
func f32val(u uint32) float64  { return float64(math.Float32frombits(u)) }

// ------------------------------------------------------------------------------------------------ Watson body

// AddWatsonBody gives the converted Watson SAS-12 model (guns\models_hh\WatsonSAS.Model00p, the Convert output
// of the PS3 WatsonSAS.mdl) one rigid physics body, so GameServer's dropped-pickup routine (0x10125de0) records
// the drop (it only does so for models with >= 1 body). Port of the project's Python Watson body script.
//
// ref is the stock shotgun model whose body triple and physics weight entry are copied: the PS3
// guns\models_hh\shotgun.mdl (console bytes, as read from the catalog) gives the shipped file; a PC model with
// the same values works too (values are kept as raw u32, so the byte order of ref does not matter).
func AddWatsonBody(watson, ref []byte) ([]byte, error) {
	m, err := Parse(watson)
	if err != nil {
		return nil, err
	}
	r, err := Parse(ref)
	if err != nil {
		return nil, fmt.Errorf("reference shotgun: %w", err)
	}
	if m.HasGeometry == 0 || len(m.Physics.Bodies) != 0 {
		return nil, errors.New("model: AddWatsonBody expects a model with geometry and without bodies")
	}
	if r.HasGeometry == 0 || len(r.Physics.Bodies) == 0 || len(r.Physics.WeightSets) == 0 || len(r.Physics.WeightSets[0].W) == 0 {
		return nil, errors.New("model: reference shotgun has no physics body / weight set")
	}
	rb, rw := r.Physics.Bodies[0], r.Physics.WeightSets[0]
	if rb.Shape.T != 6 || len(rb.Shape.Children) != 2 {
		return nil, errors.New("model: reference shotgun body is not a two-shape compound")
	}
	// type-2 shape payload (loader 0x448760): [mass?, 2.0, radius, dimX, dimY, dimZ]
	box := func(mass, rad, hx, hy, hz float64) *Shape {
		return &Shape{T: 2, V: []uint32{f32bits(mass), f32bits(2.0), f32bits(rad), f32bits(hx), f32bits(hy), f32bits(hz)}}
	}
	mainBox := box(26.955, 0.47, 2.5, 6.0, 66.0)
	grip := &Shape{T: 5, Xf: []uint32{f32bits(0), f32bits(-10.0), f32bits(-18.0), 0, 0, 0, f32bits(1.0)},
		Children: []*Shape{box(2.995, 0.46, 2.5, 8.0, 12.0)}}
	body := Body{
		Node:  0,                                                                       // bone_root
		Xform: [7]uint32{f32bits(0), f32bits(0), f32bits(12.0), 0, 0, 0, f32bits(1.0)}, // pos (0,0,12), identity
		V:     rb.V,
		Shape: &Shape{T: 6, Children: []*Shape{mainBox, grip}},
	}
	m.Physics.Bodies = []Body{body}
	// the physics weight set must carry one entry per body (loader 0x431e80)
	if len(m.Physics.WeightSets) != 1 {
		return nil, errors.New("model: AddWatsonBody expects exactly one physics weight set")
	}
	m.Physics.WeightSets[0].W = []PhysWeight{rw.W[0]}
	return m.Serialize(m.BigEndian, m.Version)
}

// ------------------------------------------------------------------------------------------------ player_console

// PlayerConsoleChildren are the child models appended to the stock PC player model.
var PlayerConsoleChildren = []string{"player_SM15.Model00p", "player_dualSM15.Model00p", "player_watson.Model00p"}

// MakePlayerConsole builds chars\models\player_console.Model00p: the stock PC chars\models\player.Model00p
// (FEAR_7.Arch00, identical to FEAR_3's copy; NOT the older one in FEAR.Arch00) with PlayerConsoleChildren
// appended to its child list. Nothing else changes. Port of the project's Python player model script
// (default variant; its PS3 left-hand variant is not used by the mod and not ported).
func MakePlayerConsole(stock []byte) ([]byte, error) {
	m, err := Parse(stock)
	if err != nil {
		return nil, err
	}
	rt, err := m.Serialize(m.BigEndian, m.Version)
	if err != nil {
		return nil, err
	}
	if string(rt) != string(stock) {
		return nil, errors.New("model: writer is not byte-exact on the stock player model")
	}
	have := map[string]bool{}
	for _, c := range m.Children {
		have[lowerLatin1(c)] = true
	}
	for _, c := range PlayerConsoleChildren {
		if have[lowerLatin1(c)] {
			return nil, fmt.Errorf("model: stock player already lists %s", c)
		}
	}
	m.Children = append(m.Children, PlayerConsoleChildren...) // append only: existing order unchanged
	return m.Serialize(m.BigEndian, m.Version)
}

// ------------------------------------------------------------------------------------------------ LeftHand socket

type quat [4]float64 // x, y, z, w

func qnorm(q quat) quat {
	// Python: sum() left to right from 0; float64() conversions forbid fused multiply-add
	n := math.Sqrt(float64(q[0]*q[0]) + float64(q[1]*q[1]) + float64(q[2]*q[2]) + float64(q[3]*q[3]))
	return quat{q[0] / n, q[1] / n, q[2] / n, q[3] / n}
}

type sockXf struct {
	q    quat
	p    [3]float64
	node string
}

func socketIndex(m *Model, name string) (int, error) {
	hit := -1
	for i, s := range m.Sockets {
		if n, ok := m.String(s[1]); ok && n == name {
			if hit >= 0 {
				return 0, fmt.Errorf("model: socket %s found twice", name)
			}
			hit = i
		}
	}
	if hit < 0 {
		return 0, fmt.Errorf("model: socket %s not found", name)
	}
	return hit, nil
}

// sockXform returns a socket's normalised rotation, position and node name (fix_lefthand.sock_xf).
func sockXform(m *Model, name string) (sockXf, error) {
	i, err := socketIndex(m, name)
	if err != nil {
		return sockXf{}, err
	}
	s := m.Sockets[i]
	if math.Abs(f32val(s[9])-1.0) >= 1e-6 {
		return sockXf{}, fmt.Errorf("model: socket %s scale is not 1", name)
	}
	if uint64(s[0]) >= uint64(len(m.Nodes)) {
		return sockXf{}, fmt.Errorf("model: socket %s node out of range", name)
	}
	node, ok := m.String(m.Nodes[s[0]].Name)
	if !ok {
		return sockXf{}, errors.New("model: bad node name offset")
	}
	return sockXf{
		q:    qnorm(quat{f32val(s[2]), f32val(s[3]), f32val(s[4]), f32val(s[5])}),
		p:    [3]float64{f32val(s[6]), f32val(s[7]), f32val(s[8])},
		node: node,
	}, nil
}

func maxDiff(a, b sockXf) float64 {
	d := 0.0
	for k := 0; k < 4; k++ {
		d = math.Max(d, math.Abs(a.q[k]-b.q[k]))
	}
	for k := 0; k < 3; k++ {
		d = math.Max(d, math.Abs(a.p[k]-b.p[k]))
	}
	return d
}

// SetLeftHandSocket gives a console player body (player_Deltaforce, from Convert of the PS3 .mdl) the PC
// LeftHand socket of pcPlayer (the stock PC player.Model00p or player_console.Model00p: they carry the same
// sockets), so the corrected SM15_Left gun and the stock Dual Pistols sit right on it. Port of the body part of
// the project's Python left-hand fix; its cross-check that player_Deltaforce and player_holiday carry the same
// console socket is not repeated here (player_holiday is an X360 model outside this package), and the
// SM15_Left geometry bake is a separate step (BakeSM15Left).
func SetLeftHandSocket(body, pcPlayer []byte) ([]byte, error) {
	pcm, err := Parse(pcPlayer)
	if err != nil {
		return nil, fmt.Errorf("PC player: %w", err)
	}
	m, err := Parse(body)
	if err != nil {
		return nil, err
	}
	pc, err := sockXform(pcm, "LeftHand")
	if err != nil {
		return nil, err
	}
	con, err := sockXform(m, "LeftHand")
	if err != nil {
		return nil, err
	}
	ra, err := sockXform(m, "RightHand")
	if err != nil {
		return nil, err
	}
	rb, err := sockXform(pcm, "RightHand")
	if err != nil {
		return nil, err
	}
	// same skeleton binding: LeftHand on Left_hand; RightHand identical to the PC body
	if !(con.node == "Left_hand" && pc.node == "Left_hand" && ra.node == rb.node && maxDiff(ra, rb) < 1e-3) {
		return nil, errors.New("model: body and PC player do not share the hand sockets")
	}
	i, _ := socketIndex(m, "LeftHand")
	s := &m.Sockets[i]
	for k := 0; k < 4; k++ {
		s[2+k] = f32bits(pc.q[k])
	}
	for k := 0; k < 3; k++ {
		s[6+k] = f32bits(pc.p[k])
	}
	out, err := m.Serialize(m.BigEndian, m.Version)
	if err != nil {
		return nil, err
	}
	// the recipe asserts the result re-serialises unchanged
	chk, err := Parse(out)
	if err != nil {
		return nil, err
	}
	if rt, _ := chk.Serialize(chk.BigEndian, chk.Version); string(rt) != string(out) {
		return nil, errors.New("model: SetLeftHandSocket output does not round-trip")
	}
	return out, nil
}
