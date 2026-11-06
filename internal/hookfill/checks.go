package hookfill

// Before the hook patches the game, it compares every place it changes or calls with the bytes the place held
// in the game version it was made for (a "preimage check"), and turns the feature off when they differ. Those
// expected bytes are copied from the game's own program files, so the template ships them as zeros and
// fillChecks reads them back from the player's PC game.
//
// Check table layout (32-bit DLL, .rdata): rows of 16 bytes from the table's file offset:
//
//	u32 address (at the game DLL's preferred base); u8 length (1..8); [u8 "a hook may be here"];
//	u8 bytes[8] (the first length bytes are the expected bytes, the rest zero); u8 relocation mask;
//	u8 skip mask; padding.
//
// The objective HUD's table has no "a hook may be here" byte, so its bytes start one byte earlier. The
// expected bytes are the file's own bytes, relocated words included (they are stored at the preferred base and
// the hook adds the load delta itself), so they are copied unchanged.

import (
	"encoding/binary"
	"fmt"
)

// Game program files the check tables come from.
const (
	gameClient = iota
	gameServer
	gameDatabase
	stringEditRuntime
)

var moduleNames = [...]string{"GameClient.dll", "GameServer.dll", "GameDatabase.dll", "StringEditRuntime.dll"}

// preferredBase is where the four game DLLs are built to load; the table addresses use it.
const preferredBase = 0x10000000

// checkTable is one preimage table of the hook build of install.HookSHA256 (TestTemplate checks the places).
type checkTable struct {
	name   string
	off    int // file offset of the first row
	rows   int
	bytes  int // offset of the expected bytes inside a row
	module int
}

const checkRowSize = 16

var checkTables = []checkTable{
	{"objective HUD", 0x15800, 35, 5, gameClient},
	{"bonus mission menu", 0x15b64, 52, 6, gameClient},
	{"player body", 0x15fec, 17, 6, gameClient},
	{"console text", 0x160fc, 26, 6, stringEditRuntime},
	{"Instant Action results (client)", 0x16418, 63, 6, gameClient},
	{"Instant Action results (server)", 0x16828, 28, 6, gameServer},
	{"Xbox 360 HUD", 0x18ba8, 32, 6, gameDatabase},
	{"MedKit pickups", 0x18ec0, 100, 6, gameServer},
}

// checkRow is one parsed row: the game address, how many bytes are checked and where they sit in the hook.
type checkRow struct {
	addr uint32
	n    int
	at   int // file offset of the expected bytes
}

// parseChecks reads the rows of every check table of a hook image. It checks the row shape only (address in
// the game DLL's range, length 1..8, nothing after the expected bytes), not the bytes themselves.
func parseChecks(dll []byte) ([][]checkRow, error) {
	out := make([][]checkRow, len(checkTables))
	for ti, t := range checkTables {
		if t.off+t.rows*checkRowSize > len(dll) {
			return nil, fmt.Errorf("hook file too small for its %s checks", t.name)
		}
		for i := 0; i < t.rows; i++ {
			o := t.off + i*checkRowSize
			r := checkRow{addr: binary.LittleEndian.Uint32(dll[o:]), n: int(dll[o+4]), at: o + t.bytes}
			bad := r.addr < preferredBase || r.addr >= preferredBase+0x01000000 || r.n < 1 || r.n > 8
			for _, b := range dll[r.at+min(r.n, 8) : r.at+8] {
				bad = bad || b != 0
			}
			if bad {
				return nil, fmt.Errorf("%s check row %d is not where the hook build puts it", t.name, i)
			}
			out[ti] = append(out[ti], r)
		}
	}
	return out, nil
}

// blankChecks sets the expected bytes of every check row of out to zero.
func blankChecks(out []byte) error {
	tables, err := parseChecks(out)
	if err != nil {
		return err
	}
	for _, rows := range tables {
		for _, r := range rows {
			clear(out[r.at : r.at+r.n])
		}
	}
	return nil
}

// read returns n bytes of the image at virtual address va, or false when they are not in the file.
func (p *pe) read(b []byte, va uint32, n int) ([]byte, bool) {
	rva := va - p.base
	for _, s := range p.secs {
		if rva >= s[0] && rva < s[0]+s[3] {
			o := int(s[2] + rva - s[0])
			if rva+uint32(n) > s[0]+s[3] || o+n > len(b) {
				return nil, false
			}
			return b[o : o+n], true
		}
	}
	return nil, false
}

// fillChecks writes the expected bytes of every check row of out (a template: every row blank) from the
// player's game DLLs, in the order of moduleNames.
func fillChecks(out []byte, modules [4][]byte) error {
	tables, err := parseChecks(out)
	if err != nil {
		return err
	}
	var pes [4]*pe
	for i, m := range modules {
		if pes[i], err = readPE(m); err != nil {
			return fmt.Errorf("%s: %w", moduleNames[i], err)
		}
		if pes[i].base != preferredBase {
			return fmt.Errorf("%s is not the expected game file", moduleNames[i])
		}
	}
	for ti, rows := range tables {
		t := checkTables[ti]
		for i, r := range rows {
			dst := out[r.at : r.at+r.n]
			for _, b := range dst {
				if b != 0 {
					return fmt.Errorf("the hook template's %s check row %d is not blank", t.name, i)
				}
			}
			src, ok := pes[t.module].read(modules[t.module], r.addr, r.n)
			if !ok {
				return fmt.Errorf("%s is not the expected game file", moduleNames[t.module])
			}
			copy(dst, src)
		}
	}
	return nil
}
