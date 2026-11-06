package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"fearinstaller/internal/catalog"
	"fearinstaller/internal/content"
	"fearinstaller/internal/hookfill"
	"fearinstaller/internal/install"
	"fearinstaller/internal/pcfs"
)

// BuildHook returns the hook to install: the shipped template with its game values filled in from the
// Xbox 360 copy (Loki.gamedb and the English Loki.strdb) and the PC game's stock databases (FEARL.Gamdb00p
// and FEAR.Strdb00p) and program files (GameClient.dll, GameServer.dll, GameDatabase.dll and
// StringEditRuntime.dll) by internal/hookfill. Only the hook that was tested in game is accepted
// (install.HookSHA256); anything else is an error, so a hook with wrong values is never written.
func BuildHook(pcDir, x360Dir string) ([]byte, error) {
	var in hookfill.Inputs
	p, err := content.FindCI(x360Dir, "Game", "database", "Loki.gamedb")
	if err == nil {
		in.X360GameDB, err = os.ReadFile(p)
	}
	if err != nil {
		return nil, fmt.Errorf("Xbox 360 database: %w", err)
	}
	p, err = content.FindCI(x360Dir, "Game", "uberCatalog.cat")
	if err != nil {
		return nil, fmt.Errorf("Xbox 360 catalog: %w", err)
	}
	c, err := catalog.Open(p)
	if err != nil {
		return nil, fmt.Errorf("Xbox 360 catalog: %w", err)
	}
	in.X360Strings, err = c.ReadByName(`database_string\English\Loki.strdb`)
	c.Close()
	if err != nil {
		return nil, fmt.Errorf("Xbox 360 string list: %w", err)
	}
	fs, _ := pcfs.Open(pcDir) // archive warnings only; a missing file shows up in Read
	defer fs.Close()
	if in.PCFEARL, err = fs.Read(`DatabaseLocalized\FEARL.Gamdb00p`); err != nil {
		return nil, fmt.Errorf("stock localized database: %w", err)
	}
	if in.PCStrings, err = fs.Read(`StringDatabase\FEAR.Strdb00p`); err != nil {
		return nil, fmt.Errorf("stock string list: %w", err)
	}
	if in.PCModules, err = readPCModules(pcDir, fs); err != nil {
		return nil, err
	}
	hook, err := hookfill.Fill(install.HookTemplate, in)
	if err != nil {
		return nil, err
	}
	s := sha256.Sum256(hook)
	if h := hex.EncodeToString(s[:]); h != install.HookSHA256 {
		return nil, fmt.Errorf("the hook built from your copies is %s, not the tested %s", h[:16], install.HookSHA256[:16])
	}
	return hook, nil
}

// readPCModules reads the PC game's program files the hook checks before it patches them, in the order
// hookfill.Inputs.PCModules names: GameClient.dll and GameServer.dll from the archives (the engine loads them
// from there), GameDatabase.dll and StringEditRuntime.dll from the game folder.
func readPCModules(pcDir string, fs *pcfs.FS) ([4][]byte, error) {
	var m [4][]byte
	var err error
	for i, name := range []string{"GameClient.dll", "GameServer.dll"} {
		if m[i], err = fs.Read(name); err != nil {
			return m, fmt.Errorf("game program file %s: %w", name, err)
		}
	}
	for i, name := range []string{"GameDatabase.dll", "StringEditRuntime.dll"} {
		p, err := content.FindCI(pcDir, name)
		if err == nil {
			m[2+i], err = os.ReadFile(p)
		}
		if err != nil {
			return m, fmt.Errorf("game program file %s: %w", name, err)
		}
	}
	return m, nil
}
