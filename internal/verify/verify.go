// Package verify decides whether a user-selected folder or disc image is a
// complete copy of the right console release of F.E.A.R. It only ever reads.
//
// The checks run cheapest first: container -> disc identity -> file presence and
// size -> "does it look encrypted?" -> SHA-256 of every needed file.
package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"fearinstaller/internal/refdata"
	"fearinstaller/internal/vfs"
)

// Status is the machine-readable outcome shown to the UI.
type Status string

const (
	StatusOK           Status = "ok"           // verified
	StatusWrongDisc    Status = "wrong_disc"   // a different game/console
	StatusEncrypted    Status = "encrypted"    // PS3 data is still encrypted
	StatusIncomplete   Status = "incomplete"   // files missing / truncated
	StatusMismatch     Status = "mismatch"     // files differ from the known release
	StatusUnreadable   Status = "unreadable"   // image truncated, or a read failed
	StatusUnrecognized Status = "unrecognized" // not a folder/image we understand
	StatusCancelled    Status = "cancelled"
	StatusError        Status = "error"
)

// FileResult is the outcome for one required file.
type FileResult struct {
	Path   string `json:"path"`
	State  string `json:"state"` // ok | missing | size | hash | unreadable
	Detail string `json:"detail,omitempty"`
}

// Result is what the UI shows for one console source.
type Result struct {
	Source    string       `json:"source"` // "ps3" or "x360"
	Status    Status       `json:"status"`
	Title     string       `json:"title,omitempty"`     // e.g. "F.E.A.R. (USA, PlayStation 3)" when verified
	Message   string       `json:"message"`             // one plain-language sentence
	Hint      string       `json:"hint,omitempty"`      // what to do next
	Caveats   []string     `json:"caveats,omitempty"`   // what was NOT fully checked (shown with a Verified result)
	Technical string       `json:"technical,omitempty"` // raw error text for support; never the main message
	Container string       `json:"container,omitempty"` // "folder", "ISO 9660"...
	TitleID   string       `json:"titleId,omitempty"`   // PS3 only
	Files     []FileResult `json:"files,omitempty"`
	ArtPath   string       `json:"artPath,omitempty"` // PS3: ICON0.PNG/PIC1.PNG inside the source
	// Root is the folder on disk that the content build reads, found from the selection (which may be a
	// folder above or inside the copy): PS3 = the game data folder (PS3_GAME/USRDIR/game), Xbox 360 = the disc
	// folder holding default.xex and Game. Empty for a disc image or when no copy was found.
	Root string `json:"root,omitempty"`
}

// Progress is reported while hashing.
type Progress struct {
	Done, Total int64  // bytes
	File        string // file being hashed
	Index       int    // 1-based file number
	Count       int
}

// Verified reports whether the source can be used.
func (r Result) Verified() bool { return r.Status == StatusOK }

// ---------------------------------------------------------------- engine ---

// located is a source plus the sub-path that acts as the root of the game data.
type located struct {
	fsys   vfs.FS
	prefix string // sub-folder (for folders chosen one level too high)
}

func (l located) path(rel string) string {
	if l.prefix == "" {
		return rel
	}
	return l.prefix + "/" + rel
}

func (l located) exists(rel string) bool {
	_, err := l.fsys.Stat(l.path(rel))
	return err == nil
}

// checkFiles compares presence and size of every item (cheap, no hashing).
func checkFiles(l located, items []refdata.Item) (results []FileResult, missing, badSize int) {
	for _, it := range items {
		st, err := l.fsys.Stat(l.path(it.Path))
		switch {
		case err != nil || st.IsDir:
			results = append(results, FileResult{Path: it.Path, State: "missing"})
			missing++
		case st.Size != it.Size:
			results = append(results, FileResult{Path: it.Path, State: "size",
				Detail: fmt.Sprintf("is %d bytes, expected %d", st.Size, it.Size)})
			badSize++
		default:
			results = append(results, FileResult{Path: it.Path, State: "ok"})
		}
	}
	return
}

// bigFile is a large file found at its expected location.
type bigFile struct {
	Presence refdata.Presence
	Path     string // path as given in Presence.Paths (relative to the root)
	Entry    vfs.Entry
}

// checkBig checks the large files that are only tested for presence and size.
// They are looked up at their expected locations only: a same-named file found
// somewhere else on the disc is not accepted. It returns the problems and the
// files that were found and still need hashing (those with a known SHA-256).
func checkBig(l located, big []refdata.Presence) (problems, unreadable []string, found []bigFile) {
	for _, b := range big {
		var st vfs.Entry
		var at string
		ok := false
		for _, p := range b.Paths {
			if e, err := l.fsys.Stat(l.path(p)); err == nil && !e.IsDir {
				st, at, ok = e, p, true
				break
			}
		}
		switch {
		case !ok:
			problems = append(problems, b.Label+" is missing")
		case b.Exact && st.Size != b.Size:
			problems = append(problems, fmt.Sprintf("%s is %s bytes (expected %s)", b.Label, commas(st.Size), commas(b.Size)))
		case !b.Exact && st.Size < b.Size:
			problems = append(problems, fmt.Sprintf("%s looks truncated (%s bytes)", b.Label, commas(st.Size)))
		case !readsToEnd(l.fsys, l.path(at), st.Size):
			// The directory says the file is there, but its data is not: a
			// truncated image, or a read error.
			unreadable = append(unreadable, b.Label)
		default:
			found = append(found, bigFile{Presence: b, Path: at, Entry: st})
		}
	}
	return
}

// readsToEnd reads the first and last bytes of a file. The size check alone
// cannot notice an image that was cut short after its directory was written.
func readsToEnd(fsys vfs.FS, path string, size int64) bool {
	f, err := fsys.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var b [1]byte
	for _, off := range []int64{0, size - 1} {
		if size <= 0 {
			break
		}
		if n, err := f.ReadAt(b[:], off); n != 1 && err != nil {
			return false
		}
	}
	return true
}

// bigCaveats says, for the big files that passed, how far they were checked.
func bigCaveats(found []bigFile) (out []string) {
	for _, f := range found {
		switch {
		case f.Presence.SHA256 != "":
		case f.Presence.Exact:
			out = append(out, f.Presence.Label+" was checked by its exact size ("+commas(f.Presence.Size)+" bytes) only, not by checksum.")
		default:
			out = append(out, f.Presence.Label+" was checked only for being present and at least "+commas(f.Presence.Size)+" bytes, not by checksum.")
		}
	}
	return
}

// hashItems turns big files that have a known SHA-256 into hashable items.
func hashItems(found []bigFile) (out []refdata.Item) {
	for _, f := range found {
		if f.Presence.SHA256 != "" && f.Presence.Exact {
			out = append(out, refdata.Item{Path: f.Path, Size: f.Presence.Size, SHA256: f.Presence.SHA256})
		}
	}
	return
}

// commas formats 1234567 as 1,234,567.
func commas(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// encryptionHint inspects the first 4 KiB of every file that exists with the
// right size. A file whose start does not match its expected magic AND looks
// random is almost certainly encrypted. Dumps can be encrypted only in some
// regions of the disc, so a single such file is enough; a different release
// of the game would still carry the same file headers. Returns (encrypted, sampled).
func encryptionHint(l located, items []refdata.Item) (encrypted bool, sampled int) {
	mismatched := 0
	buf := make([]byte, 4096)
	for _, it := range items {
		f, err := l.fsys.Open(l.path(it.Path))
		if err != nil {
			continue
		}
		n, _ := f.ReadAt(buf, 0)
		f.Close()
		if n < 16 {
			continue
		}
		sampled++
		if hex.EncodeToString(buf[:16]) != it.Magic && entropy(buf[:n]) > 7.5 {
			mismatched++
		}
	}
	return mismatched > 0, sampled
}

// entropy returns the Shannon entropy of b in bits per byte (0..8).
func entropy(b []byte) float64 {
	var c [256]int
	for _, x := range b {
		c[x]++
	}
	var h float64
	for _, n := range c {
		if n > 0 {
			p := float64(n) / float64(len(b))
			h -= p * math.Log2(p)
		}
	}
	return h
}

// errTruncated means a file ended before the size the disc's directory gives.
var errTruncated = errors.New("file ends early")

// hashAll hashes every item and records the failures in results (same order as
// items). It returns the number of bad files, or ctx.Err() when cancelled.
func hashAll(ctx context.Context, l located, items []refdata.Item, results []FileResult, progress func(Progress)) (bad int, err error) {
	var total, done int64
	for _, it := range items {
		total += it.Size
	}
	buf := make([]byte, 1<<20)
	for i, it := range items {
		sum, herr := hashOne(ctx, l, it, buf, func(n int64) {
			done += n
			if progress != nil {
				progress(Progress{Done: done, Total: total, File: it.Path, Index: i + 1, Count: len(items)})
			}
		})
		switch {
		case ctx.Err() != nil:
			return bad, ctx.Err()
		case herr != nil:
			state := "unreadable"
			if errors.Is(herr, errTruncated) {
				state = "truncated"
			}
			results[i] = FileResult{Path: it.Path, State: state, Detail: herr.Error()}
			bad++
		case sum != it.SHA256:
			results[i] = FileResult{Path: it.Path, State: "hash", Detail: "contents differ from the original disc"}
			bad++
		}
	}
	return bad, nil
}

// hashOne returns the SHA-256 (hex) of one file, calling step with each chunk size.
func hashOne(ctx context.Context, l located, it refdata.Item, buf []byte, step func(int64)) (string, error) {
	f, err := l.fsys.Open(l.path(it.Path))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	size := f.Size()
	var off int64
	for off < size {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		n, err := f.ReadAt(buf, off)
		h.Write(buf[:n])
		off += int64(n)
		step(int64(n))
		if err != nil && err != io.EOF {
			return "", err
		}
		if n == 0 {
			break
		}
	}
	if off < size {
		return "", errTruncated
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// failureMessage turns the per-file results of a finished hash run into the
// status, message and hint. Unreadable or truncated data is reported before
// "differs", because the cause is the copy, not the release.
func failureMessage(files []FileResult) (Status, string, string) {
	if bad := append(namesWhere(files, "truncated"), namesWhere(files, "unreadable")...); len(bad) > 0 {
		return unreadableMessage(bad)
	}
	return StatusMismatch, "These files differ from the original disc: " + list(namesWhere(files, "hash"), 3) + ".",
		"This may be a different release or a damaged copy."
}

// unreadableMessage is the answer for files whose data could not be read to
// the end: the copy is damaged or cut short, which is not the same as a
// different release.
func unreadableMessage(names []string) (Status, string, string) {
	return StatusUnreadable, "The image is truncated or unreadable: " + list(names, 3) + " could not be read completely.",
		"Copy the file again or extract the disc again (check free space and the drive), then select it again."
}

// friendlyError describes a failure to open the selection in plain language.
// The raw error is kept separately in Result.Technical.
func friendlyError(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "That file or folder does not exist (any more)."
	case errors.Is(err, os.ErrPermission):
		return "Windows would not let the installer read that file or folder (access denied)."
	}
	return "The installer could not read that file or folder."
}

// Guard runs fn and turns a panic (a malformed image the readers did not
// anticipate) into an error result instead of crashing the program.
func guard(r *Result, fn func()) {
	defer func() {
		if p := recover(); p != nil {
			*r = Result{Source: r.Source, Status: StatusError,
				Message:   "Could not read this file.",
				Hint:      "It may be damaged. Try another copy, or extract the disc to a folder.",
				Technical: fmt.Sprint("internal error: ", p)}
		}
	}()
	fn()
}

// list formats up to n names for a message.
func list(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:n], ", "), len(names)-n)
}

func namesWhere(r []FileResult, state string) (out []string) {
	for _, f := range r {
		if f.State == state {
			out = append(out, f.Path)
		}
	}
	return
}
