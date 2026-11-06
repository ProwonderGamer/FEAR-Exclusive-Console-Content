package verify

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"fearinstaller/internal/refdata"
	"fearinstaller/internal/vfs"
)

const (
	ps3Title    = "F.E.A.R. (PlayStation 3)" // the region is added when PARAM.SFO was read (ps3TitleFor)
	decryptHint = "Please decrypt it first, then select the decrypted folder or ISO."
	// ps3FolderMsg says what the search looks for, so a player can see what their folder lacks.
	ps3FolderMsg = "Select the folder of a decrypted copy (any folder inside it works too) or a folder that holds it. " +
		`It needs PS3_GAME\USRDIR\game with database\loki.gamedb and minicatalog.cat in it.`
)

// ps3Order is the container probing order for a PS3 image: ISO 9660 first.
var ps3Order = []vfs.Container{vfs.ISO9660, vfs.UDF, vfs.XDVDFS}

// VerifyPS3 checks a PS3 source: a decrypted disc folder or a decrypted ISO.
// The installer never decrypts anything; an encrypted image is only recognized.
// A panic caused by a malformed image is turned into an error result.
func VerifyPS3(ctx context.Context, path string, progress func(Progress)) (r Result) {
	r = Result{Source: "ps3"}
	guard(&r, func() { r = verifyPS3(ctx, path, progress) })
	return r
}

func verifyPS3(ctx context.Context, path string, progress func(Progress)) Result {
	r := Result{Source: "ps3"}
	path = climb(path, ps3RootAt) // a folder inside the copy counts as the copy
	fsys, err := vfs.OpenOrdered(path, ps3Order, recognizedLayout)
	if err != nil {
		return ps3OpenFailure(r, path, err)
	}
	defer fsys.Close()
	r.Container = fsys.Kind()

	// An Xbox 360 disc also has a "Game" folder with a "database" inside, which a
	// case-insensitive search would mistake for the PS3 layout, so rule it out first.
	if isX360Layout(fsys) && !hasPS3Game(fsys) {
		r.Status, r.Message = StatusWrongDisc, "This is an Xbox 360 copy, not a PlayStation 3 one."
		r.Hint = "Use the Xbox 360 row for this one."
		return r
	}
	l, base, ok := findPS3Root(fsys)
	if !ok {
		r.Status, r.Message = StatusUnrecognized, "No PlayStation 3 F.E.A.R. files were found here."
		r.Hint = ps3FolderMsg
		return r
	}
	r.Root = diskRoot(fsys, joinPrefix(l.prefix, base))

	// Disc identity from PARAM.SFO. It is only part of the selection when the
	// whole disc (or PS3_GAME) was chosen; when it cannot be checked, the result
	// says so instead of staying silent.
	var caveats []string
	region := ""
	switch sfo := sfoPathFor(base); {
	case sfo == "":
		caveats = append(caveats, "You selected a folder inside the disc, so the disc's title ID (PARAM.SFO) was not checked; the files were matched by checksum.")
	case !l.exists(sfo):
		caveats = append(caveats, "PARAM.SFO was not found, so the disc's title ID was not checked; the files were matched by checksum.")
	default:
		id, readable := readTitleID(fsys, l.path(sfo))
		switch {
		case !readable:
			caveats = append(caveats, "PARAM.SFO could not be read, so the disc's title ID was not checked; the files were matched by checksum.")
		case id == refdata.PS3TitleIDGerman:
			r.TitleID = id
			r.Status = StatusWrongDisc
			r.Message = "This is the German edition of F.E.A.R. (" + id + "). It has German voices only, and the mod needs the English ones."
			r.Hint = ps3Supported
			return r
		case refdata.PS3Regions[id] == "":
			r.TitleID = id
			r.Status = StatusWrongDisc
			r.Message = "This PS3 game is not a supported F.E.A.R. disc (title ID " + id + ")."
			r.Hint = ps3Supported
			return r
		default:
			r.TitleID, region = id, refdata.PS3Regions[id]
		}
	}

	// The game data sits under `base` inside the located root.
	gl := located{fsys: fsys, prefix: joinPrefix(l.prefix, base)}
	files, missing, badSize := checkFiles(gl, refdata.PS3Files)
	r.Files = files

	// Encrypted dumps keep every file size, so test the contents before size/hash noise.
	if enc, n := encryptionHint(gl, refdata.PS3Files); enc && n > 0 {
		r.Status = StatusEncrypted
		r.Message = "This looks like an encrypted PS3 disc dump."
		r.Hint = decryptHint
		return r
	}
	if missing > 0 {
		r.Status = StatusIncomplete
		r.Message = "Some PS3 files are missing: " + list(namesWhere(files, "missing"), 3) + "."
		r.Hint = "Make sure the whole disc was dumped/extracted (" + filepath.FromSlash("PS3_GAME/USRDIR/game") + ")."
		return r
	}
	problems, unreadable, found := checkBig(gl, refdata.PS3Big)
	if len(problems) > 0 {
		r.Status = StatusIncomplete
		r.Message = "The PS3 copy is incomplete: " + list(problems, 2) + "."
		r.Hint = "Re-extract the disc so that every file is present."
		return r
	}
	if len(unreadable) > 0 {
		r.Status, r.Message, r.Hint = unreadableMessage(unreadable)
		return r
	}
	if badSize > 0 {
		r.Status = StatusMismatch
		r.Message = "These files have the wrong size: " + list(namesWhere(files, "size"), 3) + "."
		r.Hint = "This may be a different release or a damaged copy."
		return r
	}

	items := append(append([]refdata.Item{}, refdata.PS3Files...), hashItems(found)...)
	all := append([]FileResult{}, files...)
	for _, it := range items[len(files):] {
		all = append(all, FileResult{Path: it.Path, State: "ok"})
	}
	bad, err := hashAll(ctx, gl, items, all, progress)
	r.Files = all
	switch {
	case errors.Is(err, context.Canceled):
		r.Status, r.Message = StatusCancelled, "Verification cancelled."
		return r
	case err != nil:
		r.Status, r.Message, r.Technical = StatusError, "Could not finish checking this copy.", err.Error()
		return r
	case bad > 0:
		r.Status, r.Message, r.Hint = failureMessage(all)
		return r
	}

	r.Status, r.Title = StatusOK, ps3TitleFor(region)
	r.Message = "Verified \u2014 " + r.Title
	r.Caveats = append(caveats, bigCaveats(found)...)
	r.ArtPath = findArt(l, base)
	return r
}

// ps3Supported names the accepted releases.
const ps3Supported = "Use the US (" + refdata.PS3TitleID + ") or European (" + refdata.PS3TitleIDEurope + ") release."

// ps3TitleFor is the verified title, with the region when the disc's title ID was read.
func ps3TitleFor(region string) string {
	if region == "" {
		return ps3Title
	}
	return "F.E.A.R. (" + region + ", PlayStation 3)"
}

// readTitleID reads TITLE_ID from the PARAM.SFO at p. ok is false when the file
// cannot be read or has no title ID.
func readTitleID(fsys vfs.FS, p string) (id string, ok bool) {
	f, err := fsys.Open(p)
	if err != nil {
		return "", false
	}
	defer f.Close()
	n := min(max(f.Size(), 0), 64<<10)
	b := make([]byte, n)
	if got, _ := f.ReadAt(b, 0); got < len(b) {
		b = b[:got]
	}
	s, err := ParseSFO(b)
	if err != nil || s.TitleID == "" {
		return "", false
	}
	return s.TitleID, true
}

// recognizedLayout says whether an opened container holds something that looks
// like a F.E.A.R. disc of either console. Image probing keeps the first
// container that passes, so an unrelated video partition cannot hide the game.
func recognizedLayout(fsys vfs.FS) bool {
	if isX360Layout(fsys) || hasPS3Game(fsys) {
		return true
	}
	_, _, ok := findPS3Root(fsys)
	return ok
}

// ps3OpenFailure turns a failed open into the most helpful message. An encrypted
// disc image normally still opens (its file system is plain), but if it does
// not, the region table in sector 0 is the tell-tale.
func ps3OpenFailure(r Result, path string, err error) Result {
	var ue *vfs.UnsupportedError
	switch {
	case errors.As(err, &ue):
		r.Status = StatusUnrecognized
		r.Message = "This disc image uses a format feature the installer cannot read."
		r.Hint = "Extract the disc to a folder and select that folder."
		r.Technical = err.Error()
	case errors.Is(err, vfs.ErrUnknownImage):
		if f, ferr := os.Open(path); ferr == nil {
			defer f.Close()
			s0 := make([]byte, 2048)
			if st, _ := f.Stat(); st != nil {
				if _, rerr := f.ReadAt(s0, 0); rerr == nil && LooksLikeEncryptedPS3Header(s0, st.Size()) {
					r.Status = StatusEncrypted
					r.Message = "This looks like an encrypted PS3 disc dump."
					r.Hint = decryptHint
					return r
				}
			}
		}
		r.Status = StatusUnrecognized
		r.Message = "This file is not a disc image the installer can read."
		r.Hint = "Select a decrypted PS3 ISO, or the extracted disc folder."
	default:
		r.Status, r.Message, r.Technical = StatusError, friendlyError(err), err.Error()
		r.Hint = "Check the path and try again."
	}
	return r
}

// PS3 data may be selected at several levels; these are the layouts accepted,
// each with where PARAM.SFO sits relative to the same root.
var ps3Layouts = []struct{ base, sfo string }{
	{"PS3_GAME/USRDIR/game", "PS3_GAME/PARAM.SFO"}, // the whole disc
	{"USRDIR/game", "PARAM.SFO"},                   // the PS3_GAME folder
	{"game", ""},                                   // the USRDIR folder
	{"", ""},                                       // the game folder itself
}

func sfoPathFor(base string) string {
	for _, l := range ps3Layouts {
		if l.base == base {
			return l.sfo
		}
	}
	return ""
}

// findPS3Root locates the PS3 game data. It looks at the selection itself and, for folders, the folders
// below it breadth-first up to ps3SearchDepth levels (people often pick a folder that holds the disc folder,
// or several games). Below the selection, the Xbox 360 copy's game data is never taken for the PS3 one (both
// have a database folder).
func findPS3Root(fsys vfs.FS) (l located, base string, ok bool) {
	try := func(prefix string) (string, bool) {
		for _, lay := range ps3Layouts {
			if isPS3Data(located{fsys: fsys, prefix: joinPrefix(prefix, lay.base)}) {
				return lay.base, true
			}
		}
		return "", false
	}
	if b, ok := try(""); ok {
		return located{fsys: fsys}, b, true
	}
	if fsys.Kind() != "folder" {
		return located{}, "", false
	}
	level, visited := []string{""}, 0
	for depth := 0; depth < ps3SearchDepth && len(level) > 0; depth++ {
		var next []string
		for _, dir := range level {
			ents, err := fsys.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range ents {
				if !e.IsDir {
					continue
				}
				if visited++; visited > ps3SearchBudget {
					return located{}, "", false
				}
				sub := joinPrefix(dir, e.Name)
				if b, ok := try(sub); ok && !isX360Data(fsys, joinPrefix(sub, b)) {
					return located{fsys: fsys, prefix: sub}, b, true
				}
				next = append(next, sub)
			}
		}
		level = next
	}
	return located{}, "", false
}

const (
	ps3SearchDepth  = 3   // chosen folder + 3 levels below it
	ps3SearchBudget = 400 // folders looked at, at most (keeps a pick of a whole drive fast)
)

// isPS3Data reports whether g looks like F.E.A.R.'s game data folder: any characteristic entry is enough to
// pick a layout (the file checks then say exactly what is missing).
func isPS3Data(g located) bool {
	return g.exists("database/loki.gamedb") || g.exists("worlds/release") || g.exists("minicatalog.cat")
}

// isX360Data reports whether the game data folder dataDir belongs to an Xbox 360 copy: only it has the Holiday
// mission, and its disc folder holds default.xex (which also catches an Xbox copy that lacks Holiday).
func isX360Data(fsys vfs.FS, dataDir string) bool {
	if (located{fsys: fsys, prefix: dataDir}).exists("Worlds/Release/Holiday.wld") {
		return true
	}
	if dataDir == "" { // the selection itself: what holds it is not part of this source
		return false
	}
	parent := ""
	if i := strings.LastIndex(dataDir, "/"); i >= 0 {
		parent = dataDir[:i]
	}
	return (located{fsys: fsys, prefix: parent}).exists("default.xex")
}

func joinPrefix(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "/" + b
}

// isX360Layout recognises an Xbox 360 disc by default.xex or its Holiday world (also when its Game folder
// itself was selected).
func isX360Layout(fsys vfs.FS) bool {
	for _, p := range []string{"default.xex", "Game/Worlds/Release/Holiday.wld", "Worlds/Release/Holiday.wld"} {
		if _, err := fsys.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// findArt returns the path (inside the source) of the disc artwork, if any.
// Only the user's own copy is ever used; nothing is bundled.
func findArt(l located, base string) string {
	sfo := sfoPathFor(base)
	if sfo == "" {
		return ""
	}
	dir := sfo[:len(sfo)-len("PARAM.SFO")]
	for _, n := range []string{"ICON0.PNG", "PIC1.PNG"} {
		if e, err := l.fsys.Stat(l.path(dir + n)); err == nil && !e.IsDir && e.Size > 0 && e.Size < 12<<20 {
			return l.path(dir + n)
		}
	}
	return ""
}

// ------------------------------------------------------------ PARAM.SFO ---

// SFO holds the fields we read from PARAM.SFO.
type SFO struct{ TitleID, Title string }

// ParseSFO reads TITLE_ID and TITLE from a PARAM.SFO.
func ParseSFO(b []byte) (SFO, error) {
	var s SFO
	if len(b) < 20 || string(b[:4]) != "\x00PSF" {
		return s, errors.New("not a PARAM.SFO")
	}
	keyOff := int(binary.LittleEndian.Uint32(b[8:]))
	dataOff := int(binary.LittleEndian.Uint32(b[12:]))
	count := int(binary.LittleEndian.Uint32(b[16:]))
	cstr := func(p int) string {
		if p < 0 || p >= len(b) {
			return ""
		}
		e := p
		for e < len(b) && b[e] != 0 {
			e++
		}
		return string(b[p:e])
	}
	for i := 0; i < count && 20+16*i+16 <= len(b); i++ {
		e := b[20+16*i:]
		key := cstr(keyOff + int(binary.LittleEndian.Uint16(e)))
		val := cstr(dataOff + int(binary.LittleEndian.Uint32(e[12:])))
		switch key {
		case "TITLE_ID":
			s.TitleID = val
		case "TITLE":
			s.Title = val
		}
	}
	return s, nil
}

// ------------------------------------------------- encrypted-image hints ---

// LooksLikeEncryptedPS3Header reports whether sector 0 holds the encryption
// region table that raw PS3 disc images start with: a big-endian region
// count N, a zero word, then 2N sector numbers that start at 0 and never
// decrease. (The layout comes from documentation of the format, not from a real
// image, so it is only a supporting hint; the main test is the content check in
// encryptionHint.)
func LooksLikeEncryptedPS3Header(sector0 []byte, fileSize int64) bool {
	if len(sector0) < 8 {
		return false
	}
	n := int(binary.BigEndian.Uint32(sector0))
	if n < 1 || n > 64 || binary.BigEndian.Uint32(sector0[4:]) != 0 || 8+8*n > len(sector0) {
		return false
	}
	prev := uint32(0)
	for i := 0; i < 2*n; i++ {
		v := binary.BigEndian.Uint32(sector0[8+4*i:])
		if i == 0 && v != 0 || v < prev || (fileSize > 0 && int64(v) > fileSize/2048+1) {
			return false
		}
		prev = v
	}
	return prev > 0
}
