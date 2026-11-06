package holworld

// Shader-family vertex inputs: a port of the project's Python vertex input tools (console_shaders,
// shader_family_requirements), the Holiday builder's holiday_feed and the IMTL header part of the material
// parser (Material.parse).
//
// A PC material's shader reads fixed vertex semantics. For a Holiday material that no PC reference world renders,
// the recipe takes the union of the inputs the PC worlds feed every material that uses the same console shader,
// restricted to COLOR0/NORMAL0/TANGENT0/BINORMAL0, and asks convertRender to synthesise the missing ones. Only the
// Xbox 360 Holiday.matCat names shaders here: the recipe also read five PS3 catalogs, but the X360 entry always
// wins (shader_of) and dropping the PS3 catalogs gives the same output, so no PS3 copy is needed.

import (
	"bytes"
	"encoding/binary"
	"strings"

	"fearinstaller/internal/catalog"
)

// safeSems: the semantics _augment can synthesise (COLOR0, NORMAL0, TANGENT0, BINORMAL0).
var safeSems = []sem{{10, 0}, {3, 0}, {6, 0}, {7, 0}}

// matShader is ltmi Material.parse reduced to what the recipe uses: magic 'IMTL' (big endian) or 'LTMI' (little
// endian), u32 version, u16 shader length, shader path. ok is false where Material.parse raises (the recipe
// then skips the material).
func matShader(d []byte) (string, bool) {
	if len(d) < 10 {
		return "", false
	}
	var bo binary.ByteOrder
	switch string(d[:4]) {
	case "IMTL":
		bo = binary.BigEndian
	case "LTMI":
		bo = binary.LittleEndian
	default:
		return "", false
	}
	n := int(bo.Uint16(d[8:]))
	if 10+n > len(d) {
		return "", false // 'truncated shader path'
	}
	return string(d[10 : 10+n]), true
}

// matKey: catalog / render material name -> the recipe's key (Latin-1 lower case, "/" -> "\").
func matKey(s string) string { return strings.ReplaceAll(latin1Lower(s), "/", `\`) }

// consoleShaders (console_shaders with the X360 catalog only): {lower material path without ".mat": lower
// shader path}. Entries that do not end in .mat, cannot be read or do not parse are skipped, as in the recipe.
func consoleShaders(matCat []byte) map[string]string {
	c, err := catalog.New(bytes.NewReader(matCat), int64(len(matCat)))
	if err != nil {
		fail("Xbox 360 Holiday.matCat: %v", err)
	}
	out := map[string]string{}
	for _, e := range c.Entries {
		if !strings.HasSuffix(latin1Lower(e.Name), ".mat") {
			continue
		}
		d, err := c.Read(e)
		if err != nil {
			continue
		}
		sh, ok := matShader(d)
		if !ok {
			continue
		}
		k := matKey(e.Name)
		out[k[:len(k)-4]] = latin1Lower(sh) // a later entry of the same name wins (dict assignment)
	}
	return out
}

// holidayFeed (make_holiday_world.holiday_feed): {lower material path without ".mat": semantics the X360 world
// feeds it} over the sections of the first render mesh.
func holidayFeed(clnt []byte) map[string]map[sem]bool {
	if len(clnt) < 8 {
		fail("Xbox 360 .wldclnt too small")
	}
	R := readRender(clnt, 8, binary.BigEndian)
	if len(R.meshes) == 0 {
		fail("Xbox 360 render block has no mesh")
	}
	m := &R.meshes[0]
	feed := map[string]map[sem]bool{}
	for _, f := range m.secs {
		if int(f[8]) >= len(m.decls) || int(f[6]) >= len(m.materials) {
			fail("section refers to a missing declaration or material")
		}
		k := matKey(m.materials[f[6]])
		k = k[:max(len(k)-4, 0)] // Python [:-4]
		s := feed[k]
		if s == nil {
			s = map[sem]bool{}
			feed[k] = s
		}
		for _, e := range m.decls[f[8]] {
			if e.stream != 0xFF {
				s[sem{e.use, e.usage}] = true
			}
		}
	}
	return feed
}

// shaderFamilyRequirements (shader_family_requirements): {lower "<material>.mat00": semantics to add} for the
// Holiday materials that no PC world renders. pcReq is the union of the PC worlds' render requirements keyed by
// the full lower PC material name (render_requirements; the recipe strips ".mat00" with [:-6]).
func shaderFamilyRequirements(feed map[string]map[sem]bool, pcReq map[string]map[sem]bool, shaders map[string]string) map[string]map[sem]bool {
	strip6 := func(s string) string { return s[:max(len(s)-6, 0)] }
	pc := map[string]map[sem]bool{} // pcreq with the recipe's [:-6] keys (key collisions merge, as in the recipe)
	for k, v := range pcReq {
		kk := strip6(k)
		s := pc[kk]
		if s == nil {
			s = map[sem]bool{}
			pc[kk] = s
		}
		for x := range v {
			s[x] = true
		}
	}
	fam := map[string]map[sem]bool{} // shader -> union of the PC materials' inputs
	for mat, s := range pc {
		sh, ok := shaders[mat]
		if !ok || sh == "" {
			continue
		}
		u := fam[sh]
		if u == nil {
			u = map[sem]bool{}
			fam[sh] = u
		}
		for x := range s {
			u[x] = true
		}
	}
	req := map[string]map[sem]bool{}
	for mat, have := range feed {
		if _, direct := pc[mat]; direct {
			continue // direct evidence: render_requirements covers it
		}
		sh, ok := shaders[mat]
		if !ok || sh == "" {
			continue
		}
		u, ok := fam[sh]
		if !ok {
			continue
		}
		miss := map[sem]bool{}
		for _, x := range safeSems {
			if u[x] && !have[x] {
				miss[x] = true
			}
		}
		if len(miss) > 0 {
			req[mat+".mat00"] = miss
		}
	}
	return req
}
