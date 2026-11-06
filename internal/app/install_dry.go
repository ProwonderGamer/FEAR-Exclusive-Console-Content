package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

	"fearinstaller/internal/archcfg"
	"fearinstaller/internal/pcgame"
	"fearinstaller/internal/refdata"
	"fearinstaller/internal/verify"
)

// DryRunInstaller is the check-only Installer. It reads, for real, every console file the chosen items need
// (from folders or inside ISO images, through the same read-only readers the verification uses), hashes
// them and checks them against the known release, and re-checks the game folder. It writes NOTHING: no game
// file, no temporary file, no log. Its progress is the real read progress, which makes the progress screen,
// Cancel and the error messages honest and testable without writing anything.
type DryRunInstaller struct {
	// ChunkDelay slows every read down (tests and the browser preview use it to make a short job visible
	// and cancellable); zero in the real program.
	ChunkDelay time.Duration
	// Inputs lists the files for a request. Nil means DefaultInputs. Tests replace it.
	Inputs func(InstallRequest) []InputFile
	// SkipGameCheck switches off the first step, the re-check of the game folder (EchoPatch still there,
	// Default.archcfg still usable). Only tests with a synthetic game folder need it.
	SkipGameCheck bool
}

// NewDryRunInstaller returns the check-only installer.
func NewDryRunInstaller() *DryRunInstaller { return &DryRunInstaller{} }

// IsPreview reports that this installer writes nothing.
func (*DryRunInstaller) IsPreview() bool { return true }

// InputFile is one file a job reads from a console copy.
type InputFile struct {
	Console string   // "ps3" or "x360"
	Rel     string   // path inside the copy ('/' separators); also what the UI shows
	Alt     []string // other places the same file may be (the first existing one among Rel, Alt... is read)
	Size    int64    // expected size when Exact, else a lower bound; 0 = unknown
	Exact   bool
	SHA256  string // expected SHA-256 (hex), "" = not known
}

const readChunk = 1 << 20

// Run implements Installer.
func (d *DryRunInstaller) Run(ctx context.Context, req InstallRequest, report func(InstallProgress)) error {
	inputs := d.Inputs
	if inputs == nil {
		inputs = DefaultInputs
	}
	rep := func(p InstallProgress) {
		if report != nil {
			report(p)
		}
	}
	cur := InstallProgress{Phase: PhasePrepare, Message: "Getting ready…"}
	rep(cur)

	// 1. The game folder: still there, EchoPatch still in place, Default.archcfg still usable. Read-only.
	if !d.SkipGameCheck {
		cur.Phase, cur.Message = PhaseGame, "Checking your F.E.A.R. folder…"
		rep(cur)
		if err := checkGameFolder(req.GameDir); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	// 2. Open the copies and add up what has to be read.
	files := inputs(req)
	srcs := map[string]*verify.Source{}
	defer func() {
		for _, s := range srcs {
			s.Close()
		}
	}()
	type job struct {
		in   InputFile
		rel  string
		size int64
	}
	var jobs []job
	var total int64
	for _, k := range []string{PS3, X360} {
		path := req.PS3Path
		if k == X360 {
			path = req.X360
		}
		var mine []InputFile
		for _, f := range files {
			if f.Console == k {
				mine = append(mine, f)
			}
		}
		if len(mine) == 0 {
			continue
		}
		if path == "" {
			return &InstallError{Msg: "The " + consoleName(k) + " copy is missing. Go back and select it again.", Technical: "no path for " + k}
		}
		cur.Phase, cur.Message = k, "Reading your "+consoleName(k)+" copy…"
		rep(cur)
		s, err := verify.OpenSource(k, path)
		if err != nil {
			return &InstallError{Msg: "The installer could not open your " + consoleName(k) + " copy any more. Is the disc image or folder still there?", Technical: err.Error()}
		}
		srcs[k] = s
		for _, f := range mine {
			rel, size, err := locate(s, f)
			if err != nil {
				return mismatch(f, consoleName(k), err)
			}
			jobs = append(jobs, job{in: f, rel: rel, size: size})
			total += size
		}
	}
	cur.TotalBytes, cur.Files = total, len(jobs)
	rep(cur)

	// 3. Read every file in chunks, hash it, compare with what the verification saw.
	buf := make([]byte, readChunk)
	var done int64
	lastPhase := ""
	for i, j := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		k := j.in.Console
		if k != lastPhase {
			lastPhase = k
			cur.Phase, cur.Message = k, "Reading your "+consoleName(k)+" copy…"
		}
		cur.File, cur.FilesDone = j.in.Rel, i
		rep(withBytes(cur, done))
		f, err := srcs[k].Open(j.rel)
		if err != nil {
			return mismatch(j.in, consoleName(k), err)
		}
		h := sha256.New()
		var off int64
		for off < j.size {
			if err := ctx.Err(); err != nil {
				f.Close()
				return err
			}
			n, rerr := f.ReadAt(buf, off)
			if n > 0 {
				h.Write(buf[:n])
				off += int64(n)
				done += int64(n)
				rep(withBytes(cur, done))
			}
			if rerr != nil && !errors.Is(rerr, io.EOF) {
				f.Close()
				return &InstallError{Msg: "A file in your " + consoleName(k) + " copy could not be read to the end: " + j.in.Rel + ". Copy or extract the disc again, then select it again.", Technical: rerr.Error()}
			}
			if n == 0 {
				break
			}
			if d.ChunkDelay > 0 {
				select {
				case <-ctx.Done():
				case <-time.After(d.ChunkDelay):
				}
			}
		}
		f.Close()
		if off < j.size {
			return &InstallError{Msg: "A file in your " + consoleName(k) + " copy ends early: " + j.in.Rel + ". Copy or extract the disc again, then select it again."}
		}
		if j.in.SHA256 != "" && hex.EncodeToString(h.Sum(nil)) != j.in.SHA256 {
			return &InstallError{Msg: "A file in your " + consoleName(k) + " copy is not what it was when it was checked: " + j.in.Rel + ". Go back and select the copy again."}
		}
		cur.FilesDone = i + 1
		rep(withBytes(cur, done))
	}

	// 4. The check writes nothing.
	cur.Phase, cur.Message, cur.File = PhaseFinish, "Finishing up…", ""
	cur.FilesDone = len(jobs)
	rep(withBytes(cur, done))
	return ctx.Err()
}

func withBytes(p InstallProgress, done int64) InstallProgress { p.DoneBytes = done; return p }

func consoleName(k string) string {
	if k == PS3 {
		return "PlayStation 3"
	}
	return "Xbox 360"
}

// locate finds the file (Rel or one of Alt) and checks its size.
func locate(s *verify.Source, f InputFile) (rel string, size int64, err error) {
	for _, c := range append([]string{f.Rel}, f.Alt...) {
		e, serr := s.Stat(c)
		if serr != nil || e.IsDir {
			continue
		}
		switch {
		case f.Exact && f.Size > 0 && e.Size != f.Size:
			return "", 0, errors.New("size differs")
		case !f.Exact && e.Size < f.Size:
			return "", 0, errors.New("too small")
		}
		return c, e.Size, nil
	}
	return "", 0, errors.New("missing")
}

func mismatch(f InputFile, console string, err error) error {
	return &InstallError{Msg: "A file in your " + console + " copy is missing or has changed since it was checked: " + f.Rel + ". Go back and select the copy again.", Technical: err.Error()}
}

// checkGameFolder re-reads the game folder: EchoPatch must still be there and Default.archcfg usable.
func checkGameFolder(dir string) error {
	ck := pcgame.Inspect(dir)
	if !ck.Valid {
		return &InstallError{Msg: "The F.E.A.R. folder is not there any more, or FEAR.exe is missing. Go back and select it again."}
	}
	if !ck.Ready {
		return &InstallError{Msg: "EchoPatch is no longer found in the F.E.A.R. folder. Go back to the first step and check the folder again."}
	}
	if _, _, err := archcfg.Preview(dir, ModFolder); err != nil {
		return &InstallError{Msg: "Default.archcfg in the F.E.A.R. folder cannot be used any more. Go back and check the folder again.", Technical: err.Error()}
	}
	return nil
}

// ------------------------------------------------------------- the inputs ---

// What each item reads from its console copy. These are the files the content converters take as input;
// every one of them is part of the verified file list, so the job can compare it with the known release.
// The big catalogs have no hash, only a size check. The F.E.A.R. folder's own archives are not read here:
// nothing verifies them, and reading several gigabytes would only make the check slow.
var (
	ps3Sfod = []string{
		"worlds/release/sfod_d.wldsrvr", "worlds/release/sfod_d.wld", "worlds/release/sfod_d.wldclnt",
		"worlds/release/sfod_d.matcat", "worlds/release/sfod_d.texcat",
		"database/loki.gamedb", "database_anim/anim.gamedb", "profiledatabase/defaults.gamedb",
		"minicatalog.cat", "lipsenglish.cat", "snd/english.cat", "ubercatalog.cat",
	}
	ps3Watson = []string{
		"snd/sfx.cat", "worlds/release/sfod_d.wldsrvr", "worlds/release/sfod_d.matcat",
		"worlds/release/sfod_d.texcat", "database/loki.gamedb",
	}
	x360Holiday = []string{
		"Game/Worlds/Release/Holiday.wldsrvr", "Game/Worlds/Release/Holiday.wld", "Game/Worlds/Release/Holiday.wldclnt",
		"Game/Worlds/Release/Holiday.matCat", "Game/Worlds/Release/Holiday.texCat",
		"Game/database/Loki.gamedb", "Game/database_anim/Anim.gamedb", "Game/database_anim/Anim.anmtree",
		"Game/ProfileDatabase/Defaults.gamedb", "Game/lipsEnglish.cat", "Game/Snd/VoiceEnglish.xwb", "Game/uberCatalog.cat",
	}
	x360SM15 = []string{
		"Game/Snd/SFX.xwb", "Game/Worlds/Release/Holiday.wldsrvr", "Game/Worlds/Release/Holiday.matCat",
		"Game/Worlds/Release/Holiday.texCat", "Game/database/Loki.gamedb",
	}
)

// DefaultInputs lists the console files the chosen items read, each once, with what the verification knows
// about it (size, SHA-256).
func DefaultInputs(req InstallRequest) []InputFile {
	o := req.Options
	var out []InputFile
	seen := map[string]bool{}
	add := func(console string, rels []string) {
		for _, r := range rels {
			key := console + "|" + strings.ToLower(r)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, expected(console, r))
		}
	}
	// The complete mod: every list of each console copy that takes part.
	if req.PS3Path != "" && o.UsePS3 {
		add(PS3, ps3Sfod)
		add(PS3, ps3Watson)
	}
	if req.X360 != "" && o.UseX360 {
		add(X360, x360Holiday)
		add(X360, x360SM15)
	}
	return out
}

// expected fills in size and hash from the reference lists the verification uses.
func expected(console, rel string) InputFile {
	f := InputFile{Console: console, Rel: rel}
	items, big := refdata.PS3Files, refdata.PS3Big
	if console == X360 {
		items, big = refdata.X360Files, refdata.X360Big
	}
	for _, it := range items {
		if strings.EqualFold(it.Path, rel) {
			f.Size, f.Exact, f.SHA256 = it.Size, true, it.SHA256
			return f
		}
	}
	for _, b := range big {
		for _, p := range b.Paths {
			if strings.EqualFold(p, rel) {
				f.Size, f.Exact, f.SHA256 = b.Size, b.Exact, b.SHA256
				if b.Exact && b.SHA256 == "" {
					f.SHA256 = ""
				}
				f.Alt = append([]string{}, b.Paths...)
				return f
			}
		}
	}
	return f // not in the lists: read without a size or hash check
}
