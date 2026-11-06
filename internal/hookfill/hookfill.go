// Package hookfill fills in the game values of the hook DLL from the player's own copies.
//
// The hook (dinput8_hook.dll) is the mod's program that EchoPatch loads into the game. Some of its constants
// come from the games: the Xbox 360 HUD layout table (table.go), a handful of short texts the hook matches or
// shows (text.go), and the bytes it expects to find in the game's program files before it patches them
// (checks.go). The installer ships the hook with those constants set to zero ("the template"), so nothing
// taken from the games is distributed; Fill reads them from the player's Xbox 360 copy and PC game at install
// time and writes them back. The result is byte for byte the hook that was built and tested with the values
// compiled in, which the caller proves by comparing its SHA-256 (install.HookSHA256) before writing it.
package hookfill

// Inputs are the game files the hook's values come from.
type Inputs struct {
	X360GameDB  []byte // Xbox 360 Game\database\Loki.gamedb
	X360Strings []byte // Xbox 360 English Loki.strdb (from Game\uberCatalog.cat)
	PCFEARL     []byte // the PC game's stock DatabaseLocalized\FEARL.Gamdb00p
	PCStrings   []byte // the PC game's stock StringDatabase\FEAR.Strdb00p

	// The PC game's program files, in the order of moduleNames: GameClient.dll and GameServer.dll (from its
	// archives), GameDatabase.dll and StringEditRuntime.dll (in the game folder).
	PCModules [4][]byte
}

// Blank returns a copy of a hook image with every game value set to zero (how the template is made).
func Blank(dll []byte) ([]byte, error) {
	out := append([]byte(nil), dll...)
	if err := blankTable(out); err != nil {
		return nil, err
	}
	if err := blankText(out); err != nil {
		return nil, err
	}
	if err := blankChecks(out); err != nil {
		return nil, err
	}
	return out, nil
}

// Fill returns the hook with its game values filled in. template is the shipped hook; it is not changed.
func Fill(template []byte, in Inputs) ([]byte, error) {
	out := append([]byte(nil), template...)
	if err := fillTable(out, in.X360GameDB, in.PCFEARL); err != nil {
		return nil, err
	}
	if err := fillText(out, in.PCStrings, in.X360Strings); err != nil {
		return nil, err
	}
	if err := fillChecks(out, in.PCModules); err != nil {
		return nil, err
	}
	return out, nil
}
