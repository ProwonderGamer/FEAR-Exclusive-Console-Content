package verify

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"

	"fearinstaller/internal/refdata"
	"fearinstaller/internal/vfs"
)

const (
	x360Title = "F.E.A.R. (Xbox 360)"
	// x360FolderMsg says what the search looks for, so a player can see what their folder lacks.
	x360FolderMsg = "Select the folder of the copy (its Game folder works too) or a folder that holds it. " +
		`It needs default.xex next to a Game folder with uberCatalog.cat and database\Loki.gamedb in it.`
)

// x360Order probes XDVDFS first: a real Xbox 360 image can carry an ISO 9660 or
// UDF "video partition" ahead of the game partition, and that must not win.
var x360Order = []vfs.Container{vfs.XDVDFS, vfs.ISO9660, vfs.UDF}

// VerifyX360 checks an Xbox 360 source: an XDVDFS ISO (XGD2/XGD3 offsets are
// handled by the reader) or an extracted folder holding default.xex and Game\.
// A panic caused by a malformed image is turned into an error result.
func VerifyX360(ctx context.Context, path string, progress func(Progress)) (r Result) {
	r = Result{Source: "x360"}
	guard(&r, func() { r = verifyX360(ctx, path, progress) })
	return r
}

func verifyX360(ctx context.Context, path string, progress func(Progress)) Result {
	r := Result{Source: "x360"}
	path = climb(path, x360RootAt) // a folder inside the copy (such as its Game folder) counts as the copy
	fsys, err := vfs.OpenOrdered(path, x360Order, recognizedLayout)
	if err != nil {
		var ue *vfs.UnsupportedError
		switch {
		case errors.As(err, &ue):
			r.Status, r.Message = StatusUnrecognized, "This disc image uses a format feature the installer cannot read."
			r.Hint = "Extract the disc to a folder and select that folder."
			r.Technical = err.Error()
		case errors.Is(err, vfs.ErrUnknownImage):
			r.Status, r.Message = StatusUnrecognized, "This file is not a disc image the installer can read."
			r.Hint = "Select an Xbox 360 ISO, or the extracted folder that contains default.xex."
		default:
			r.Status, r.Message, r.Technical = StatusError, friendlyError(err), err.Error()
			r.Hint = "Check the path and try again."
		}
		return r
	}
	defer fsys.Close()
	r.Container = fsys.Kind()

	l, ok := findX360Root(fsys)
	if !ok {
		switch {
		case hasPS3Game(fsys) || inPS3Copy(path) || (isPS3Data(located{fsys: fsys}) && !isX360Data(fsys, "")):
			r.Status, r.Message = StatusWrongDisc, "This is a PlayStation 3 copy, not an Xbox 360 one."
			r.Hint = "Use the PlayStation 3 row for this one."
		case folderHas(fsys, "Worlds") && folderHas(fsys, "database"):
			r.Status, r.Message = StatusIncomplete, "This is the Xbox 360 Game folder, but default.xex is missing from the folder that holds it."
			r.Hint = "Copy the whole disc, default.xex included, then select its folder or any folder in it."
		default:
			r.Status, r.Message = StatusUnrecognized, "No Xbox 360 F.E.A.R. files were found here."
			r.Hint = x360FolderMsg
		}
		return r
	}
	r.Root = diskRoot(fsys, l.prefix)

	if id, ok := readMediaID(l); ok && id == refdata.X360MediaIDGerman {
		r.Status = StatusWrongDisc
		r.Message = "This is the German edition of F.E.A.R. It has German voices only, and the mod needs the English ones."
		r.Hint = "Use the Xbox 360 disc sold in the US and the rest of Europe."
		return r
	}

	files, missing, badSize := checkFiles(l, refdata.X360Files)
	r.Files = files
	if missing > 0 {
		r.Status = StatusIncomplete
		r.Message = "Some Xbox 360 files are missing: " + list(namesWhere(files, "missing"), 3) + "."
		r.Hint = "Make sure the whole disc was extracted (default.xex and the " + filepath.FromSlash("Game") + " folder)."
		return r
	}
	problems, unreadable, found := checkBig(l, refdata.X360Big)
	if len(problems) > 0 {
		r.Status = StatusIncomplete
		r.Message = "The Xbox 360 copy is incomplete: " + list(problems, 2) + "."
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
	items := append(append([]refdata.Item{}, refdata.X360Files...), hashItems(found)...)
	all := append([]FileResult{}, files...)
	for _, it := range items[len(files):] {
		all = append(all, FileResult{Path: it.Path, State: "ok"})
	}
	bad, err := hashAll(ctx, l, items, all, progress)
	r.Files = all
	switch {
	case errors.Is(err, context.Canceled):
		r.Status, r.Message = StatusCancelled, "Verification cancelled."
	case err != nil:
		r.Status, r.Message, r.Technical = StatusError, "Could not finish checking this copy.", err.Error()
	case bad > 0:
		r.Status, r.Message, r.Hint = failureMessage(all)
	default:
		r.Status, r.Title = StatusOK, x360Title
		r.Message = "Verified — " + x360Title
		r.Caveats = bigCaveats(found)
	}
	return r
}

// findX360Root finds the F.E.A.R. Xbox 360 root (the folder holding default.xex and Game).
// The chosen folder itself wins when it has default.xex. For folders, sub-folders are searched
// breadth-first up to x360SearchDepth levels (people often pick a parent folder such as
// "Xbox 360 Games" that holds several games), and only a folder whose Game data looks like
// F.E.A.R. is accepted, so another game's default.xex is never taken for this one.
func findX360Root(fsys vfs.FS) (located, bool) {
	if _, err := fsys.Stat("default.xex"); err == nil {
		return located{fsys: fsys}, true
	}
	if fsys.Kind() != "folder" {
		return located{}, false
	}
	level, visited := []string{""}, 0
	for depth := 0; depth < x360SearchDepth && len(level) > 0; depth++ {
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
				if visited++; visited > x360SearchBudget {
					return located{}, false
				}
				sub := e.Name
				if dir != "" {
					sub = dir + "/" + e.Name
				}
				if _, err := fsys.Stat(sub + "/default.xex"); err == nil && looksLikeFEARX360(fsys, sub) {
					return located{fsys: fsys, prefix: sub}, true
				}
				next = append(next, sub)
			}
		}
		level = next
	}
	return located{}, false
}

const (
	x360SearchDepth  = 3   // chosen folder + 3 levels below it
	x360SearchBudget = 400 // folders looked at, at most (keeps a pick of a whole drive fast)
)

// looksLikeFEARX360 reports whether dir holds F.E.A.R.'s Xbox 360 game data (not only some default.xex).
func looksLikeFEARX360(fsys vfs.FS, dir string) bool {
	for _, p := range []string{"Game/uberCatalog.cat", "Game/Worlds/Release/Holiday.wld", "Game/database/Loki.gamedb"} {
		if _, err := fsys.Stat(dir + "/" + p); err == nil {
			return true
		}
	}
	return false
}

// readMediaID reads the media ID from default.xex (XEX2 optional header 0x00040006, execution info). ok is
// false when the file cannot be read or is not an XEX2 file; the caller then relies on the file checks.
func readMediaID(l located) (id uint32, ok bool) {
	f, err := l.fsys.Open(l.path("default.xex"))
	if err != nil {
		return 0, false
	}
	defer f.Close()
	b := make([]byte, 64<<10) // the XEX header (optional headers included) sits before the image data
	n, _ := f.ReadAt(b, 0)
	b = b[:n]
	if len(b) < 24 || string(b[:4]) != "XEX2" {
		return 0, false
	}
	count := binary.BigEndian.Uint32(b[20:])
	for i := uint32(0); i < count && i < 64; i++ {
		o := 24 + 8*int(i)
		if o+8 > len(b) {
			break
		}
		if binary.BigEndian.Uint32(b[o:]) == 0x00040006 {
			at := int(binary.BigEndian.Uint32(b[o+4:]))
			if at < 0 || at+4 > len(b) {
				return 0, false
			}
			return binary.BigEndian.Uint32(b[at:]), true
		}
	}
	return 0, false
}

func hasPS3Game(fsys vfs.FS) bool { _, err := fsys.Stat("PS3_GAME"); return err == nil }

func folderHas(fsys vfs.FS, name string) bool { _, err := fsys.Stat(name); return err == nil }
