package pcgame

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"fearinstaller/internal/ltar"
)

// Reference for the tested game version: the GameClient.dll inside the patch
// 1.08 archive FEARE_8.Arch00 of the Steam release ("FEAR Ultimate Shooter
// Edition"). The hook verifies this very build (its preimage tables) and
// switches itself off on any other. The hash was computed with the ltar reader
// from the Steam FEARE_8.Arch00 (4,579,744 bytes) and equals the SHA-256 of the
// file extracted from it.
const (
	GameClientV108SHA256 = "38604fbfbc91d27427368ff93739816b85365eb6d3666990d16ab9a876237db8"
	GameClientV108Size   = 1851392
)

// VersionStatus tells how the game's GameClient.dll compares with the tested one.
type VersionStatus string

const (
	VersionVerified   VersionStatus = "verified"   // identical to the tested patch 1.08 file
	VersionDifferent  VersionStatus = "different"  // read fine, but another file
	VersionUnreadable VersionStatus = "unreadable" // FEARE_8.Arch00 is there but GameClient.dll could not be read
	VersionUnchecked  VersionStatus = "unchecked"  // FEARE_8.Arch00 is not there: nothing to compare
)

// Version is the result of reading GameClient.dll out of the game archives.
type Version struct {
	Status  VersionStatus `json:"status"`
	SHA256  string        `json:"sha256,omitempty"` // of the GameClient.dll that was read
	Size    int64         `json:"size,omitempty"`
	Archive string        `json:"archive,omitempty"` // archive the file came from
}

// maxGameClient bounds what is read: the real file is 1.8 MB.
const maxGameClient = 64 << 20

// checkVersion reads GameClient.dll from FEARE_8.Arch00 (the archive mounted
// last among the Extraction Point ones, so its copy is the one the game runs;
// older FEARE_n archives are consulted only if it holds no copy) and compares
// its SHA-256 with the Steam patch 1.08 reference. It never decides by store.
func checkVersion(dir string) Version {
	v := Version{Status: VersionUnchecked}
	for _, n := range []string{"FEARE_8", "FEARE_7", "FEARE_6", "FEARE_5", "FEARE_4", "FEARE_3", "FEARE_2", "FEARE_1", "FEARE"} {
		p := findFile(dir, n+".Arch00")
		if p == "" {
			continue
		}
		v.Archive = filepath.Base(p)
		sum, size, found, err := gameClientHash(p)
		if err != nil {
			v.Status = VersionUnreadable // the archive that the game would use cannot be read
			return v
		}
		if !found {
			v.Status = VersionUnreadable // readable archive without a copy: try the next older one
			continue
		}
		v.SHA256, v.Size = sum, size
		if sum == GameClientV108SHA256 {
			v.Status = VersionVerified
		} else {
			v.Status = VersionDifferent
		}
		return v
	}
	return v
}

// gameClientHash returns the SHA-256 of the top-level GameClient.dll of an
// archive. found is false when the archive is fine but holds no such file; err
// is set when the archive or the file cannot be read.
func gameClientHash(archive string) (sum string, size int64, found bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			sum, size, found, err = "", 0, false, errUnreadable
		}
	}()
	a, err := ltar.Open(archive)
	if err != nil {
		return "", 0, false, err
	}
	defer a.Close()
	for _, e := range a.Entries {
		if !strings.EqualFold(e.Path, "GameClient.dll") {
			continue
		}
		if e.Size == 0 || e.Size > maxGameClient || e.PackedSize > maxGameClient {
			return "", 0, false, errUnreadable
		}
		b, err := a.Read(e)
		if err != nil {
			return "", 0, false, err
		}
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:]), int64(len(b)), true, nil
	}
	return "", 0, false, nil
}

var errUnreadable = errors.New("pcgame: unreadable archive")

// detectEdition guesses the store from what is in the folder. It is a hint for
// wording only; nothing in the install depends on it. Evidence for GOG: Galaxy's
// goggame-*.info files, or dxwrapper.dll together with the FEARXP/FEARXP2
// sub-folders (both in the file list of the GOG depot), or a GOG folder name.
// Evidence for Steam: a steamapps folder in the path, steam_appid.txt, or
// Steam's 21090_install.vdf. Conflicting or missing evidence gives "unknown".
func detectEdition(dir string) (id, label string) {
	gog, steam := false, false
	if m, _ := filepath.Glob(filepath.Join(dir, "goggame-*.info")); len(m) > 0 {
		gog = true
	}
	if !gog {
		if ents, err := os.ReadDir(dir); err == nil {
			for _, e := range ents {
				n := strings.ToLower(e.Name())
				if !e.IsDir() && strings.HasPrefix(n, "goggame-") && strings.HasSuffix(n, ".info") {
					gog = true
				}
			}
		}
	}
	if findFile(dir, "dxwrapper.dll") != "" && (isDir(filepath.Join(dir, "FEARXP")) || isDir(filepath.Join(dir, "FEARXP2"))) {
		gog = true
	}
	for _, seg := range strings.Split(strings.ToLower(filepath.ToSlash(dir)), "/") {
		switch {
		case seg == "steamapps":
			steam = true
		case strings.HasPrefix(seg, "gog games"), strings.HasPrefix(seg, "gog galaxy"):
			gog = true
		}
	}
	if findFile(dir, "steam_appid.txt") != "" || findFile(dir, "21090_install.vdf") != "" {
		steam = true
	}
	switch {
	case gog && !steam:
		return "gog", "GOG"
	case steam && !gog:
		return "steam", "Steam"
	}
	return "unknown", ""
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// hookState looks at dinput8_hook.dll in the game folder: "absent", "ours" (it
// carries the mod's bonus-mission names) or "foreign" (some other mod's chained
// DirectInput wrapper that the install must not overwrite without a backup).
func hookState(dir string) string {
	p := findFile(dir, "dinput8_hook.dll")
	if p == "" {
		return "absent"
	}
	if b := readHead(p, 4<<20); strings.Contains(string(b), "zSFODD_Bonus_") {
		return "ours"
	}
	return "foreign"
}
