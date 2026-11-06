// Package content runs the ported content-building stages: the databases, the mod text
// (StringDatabase\ConsoleText.Strdb00p; the stock FEAR.Strdb00p is never written) and the campaign Objects00p
// overrides, built from the player's own copies. It only READS the source folders and writes below one output
// folder; nothing here touches a game folder or the UI.
package content

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fearinstaller/internal/catalog"
	"fearinstaller/internal/dbmerge"
	"fearinstaller/internal/objects00p"
	"fearinstaller/internal/pcfs"
	"fearinstaller/internal/strdb"
	"fearinstaller/internal/wld"
)

// Options select sources, stages and variants.
type Options struct {
	PCDir   string // PC game folder with the *.Arch00 archives (required)
	PS3Dir  string // PS3 ".../USRDIR/game" folder (decrypted)
	X360Dir string // Xbox 360 disc root (contains Game\)
	OutDir  string // output folder (mount layout: <OutDir>\Game\...)

	// Testing overrides
	TOCFile     string // use this archive listing instead of reading every archive
	PCWorldsDir string // read stock PC worlds from this folder instead of FEAR.Arch00
	PS3Strdb    string // use this extracted English Loki.strdb instead of reading ubercatalog.cat

	Stages      []string // "db", "strings", "objects", "assets", "sounds", "iamaps"; empty = all that the sources allow
	Weapons     []string // objects stage: "sm15", "watson"; empty = both available
	NoAppend    bool
	SFODPS3Art  bool
	BishopStock bool
	NoDropFix   bool // database: leave the SM15 drop fix out of the SFOD-D database
	// strings: add the Instant Action menu texts to ConsoleText. Set it ONLY when the Instant Action maps and
	// database records are installed too: the hook shows the main-menu item whenever ConsoleText serves its text
	// (the hook does this), so the texts alone would add a menu item that leads to missing maps.
	InstantActionText bool
	// database: also add the Instant Action records (with InstantActionText; same rule) and the Watson reload-split
	// records. WatsonReload is valid only when the Watson reload sounds and model of that fix are installed too.
	WatsonReload bool
	// ModLayout: OutDir is the installer's work folder inside the game folder (internal/install): files are
	// written in the mod layout (no "Game\" level) and no "info\" folder is written. Only then may OutDir be
	// inside the PC game folder; it must still never be inside a console copy.
	ModLayout bool
	// Choice: the items the assets stage builds for (zero = what the given copies allow, without Instant Action).
	Choice Choice
	// ArialBold: the Arial Bold font file for the IA loading pictures; empty = %WINDIR%\Fonts\arialbd.ttf.
	ArialBold string
	Progress  func(string)
	// FileDone is called after each file is written (slash path below OutDir), so a caller can show how
	// many of the expected files are done. The stages write one file at a time.
	FileDone func(rel string)
}

// Report lists what was written.
type Report struct {
	Files []FileInfo
	Log   string
}

// FileInfo describes one written file.
type FileInfo struct {
	Rel    string // slash separated path below OutDir
	Size   int
	SHA256 string
}

func (o *Options) say(f string, a ...any) {
	if o.Progress != nil {
		o.Progress(fmt.Sprintf(f, a...))
	}
}

func (o *Options) want(stage string) bool {
	if len(o.Stages) == 0 {
		return true
	}
	for _, s := range o.Stages {
		if s == stage {
			return true
		}
	}
	return false
}

// FindCI resolves a relative path below root ignoring case (needed on case-sensitive file systems).
func FindCI(root string, rel ...string) (string, error) {
	cur := root
	for _, part := range rel {
		for _, seg := range strings.FieldsFunc(part, func(r rune) bool { return r == '/' || r == '\\' }) {
			if seg == ".." || seg == "." {
				return "", fmt.Errorf("bad path component %q", seg)
			}
			ents, err := os.ReadDir(cur)
			if err != nil {
				return "", err
			}
			found := ""
			for _, e := range ents {
				if e.Name() == seg {
					found = e.Name()
					break
				}
			}
			if found == "" {
				for _, e := range ents {
					if strings.EqualFold(e.Name(), seg) {
						found = e.Name()
						break
					}
				}
			}
			if found == "" {
				return "", fmt.Errorf("%s: %s not found", cur, seg)
			}
			cur = filepath.Join(cur, found)
		}
	}
	return cur, nil
}

func readCI(root string, rel ...string) ([]byte, error) {
	p, err := FindCI(root, rel...)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// inside reports whether path is dir or below it.
func inside(path, dir string) bool {
	p, err1 := filepath.Abs(path)
	d, err2 := filepath.Abs(dir)
	if err1 != nil || err2 != nil {
		return true
	}
	r, err := filepath.Rel(d, p)
	return err == nil && (r == "." || (!strings.HasPrefix(r, "..") && !filepath.IsAbs(r)))
}

type writer struct {
	root      string
	modLayout bool // drop the leading "Game/" (Options.ModLayout)
	files     []FileInfo
	done      func(rel string) // Options.FileDone
}

func (w *writer) write(rel string, data []byte) error {
	if w.modLayout {
		rel = strings.TrimPrefix(rel, "Game/")
	}
	if strings.Contains(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to write %q", rel)
	}
	p := filepath.Join(w.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		os.Remove(tmp) // a failed write can leave a partial file behind
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	s := sha256.Sum256(data)
	w.files = append(w.files, FileInfo{Rel: rel, Size: len(data), SHA256: hex.EncodeToString(s[:])})
	if w.done != nil {
		w.done(rel)
	}
	return nil
}

// Build runs the requested stages.
func Build(o Options) (*Report, error) {
	if o.PCDir == "" || o.OutDir == "" {
		return nil, errors.New("content: PCDir and OutDir are required")
	}
	for i, src := range []string{o.PCDir, o.PS3Dir, o.X360Dir} {
		if i == 0 && o.ModLayout {
			continue // the installer's work folder is in the game folder by design
		}
		if src != "" && inside(o.OutDir, src) {
			return nil, fmt.Errorf("content: the output folder must not be inside a source or game folder (%s)", src)
		}
	}
	log := dbmerge.NewLog()
	w := &writer{root: o.OutDir, modLayout: o.ModLayout, done: o.FileDone}

	o.say("Reading the PC game archives")
	fs, errs := pcfs.Open(o.PCDir)
	defer fs.Close()
	for _, e := range errs {
		log.Add("archive warnings", e.Error())
	}
	var toc dbmerge.TOC = fs.TOC
	if o.TOCFile != "" {
		t, err := pcfs.ReadTOCFile(o.TOCFile)
		if err != nil {
			return nil, err
		}
		toc = t
	}
	pcWorld := func(name string) ([]byte, error) {
		if o.PCWorldsDir != "" {
			return readCI(o.PCWorldsDir, name+".World00p")
		}
		return fs.Read(`Worlds\Release\` + name + `.World00p`)
	}

	var sfodStr *strdb.DB // the SFOD-D string database (in memory only), needed by the DB consistency check
	var ps3Str, x360Str *strdb.DB
	explicit := len(o.Stages) > 0
	doDB, doStr, doObj := o.want("db"), o.want("strings"), o.want("objects")
	if doDB && o.PS3Dir == "" {
		if explicit {
			return nil, errors.New("content: the PS3 copy is required for the db stage")
		}
		doDB = false
	}
	// strings: the bonus-mission text needs the PS3 copy; the Instant Action texts need either console copy
	if doStr && o.PS3Dir == "" && !(o.InstantActionText && o.X360Dir != "") {
		if explicit {
			return nil, errors.New("content: the strings stage needs the PS3 copy, or the Xbox 360 copy with the Instant Action texts")
		}
		doStr = false
	}

	if (doDB || doStr) && o.PS3Dir != "" {
		b, err := o.ps3Strdb()
		if err != nil {
			return nil, err
		}
		if ps3Str, err = strdb.Parse(b); err != nil {
			return nil, fmt.Errorf("PS3 string database: %w", err)
		}
	}
	if doStr && o.InstantActionText && o.X360Dir != "" {
		b, err := o.x360Strdb()
		if err != nil {
			return nil, err
		}
		if x360Str, err = strdb.Parse(b); err != nil {
			return nil, fmt.Errorf("Xbox 360 string database: %w", err)
		}
	}

	if doStr || doDB {
		o.say("Building the mod text")
		sb, err := fs.Read(`StringDatabase\FEAR.Strdb00p`)
		if err != nil {
			return nil, fmt.Errorf("stock string database: %w", err)
		}
		// The stock list is only read. Every key it lacks goes into ConsoleText.Strdb00p, which the hook serves;
		// the stock FEAR.Strdb00p is never copied or replaced (string indices must stay as shipped).
		stock, err := strdb.Parse(sb)
		if err != nil {
			return nil, err
		}
		var merged *strdb.DB // stock + bonus-mission keys, in memory only
		if ps3Str != nil {
			pcStr, err := strdb.Parse(sb) // BuildSFODD adds to its base: give it its own copy
			if err != nil {
				return nil, err
			}
			if sfodStr, _, err = strdb.BuildSFODD(pcStr, ps3Str); err != nil {
				return nil, err
			}
			merged = sfodStr
			if o.X360Dir != "" {
				wb, err := readCI(o.X360Dir, "Game", "Worlds", "Release", "Holiday.wldsrvr")
				if err != nil {
					return nil, fmt.Errorf("Xbox 360 Holiday.wldsrvr: %w", err)
				}
				_, objs, err := wld.ParseConsoleServer(wb)
				if err != nil {
					return nil, err
				}
				// BuildHoliday modifies its base: work on a re-parse so the SFOD-D-only database stays available
				tmp, err := sfodStr.Bytes(false, 2)
				if err != nil {
					return nil, err
				}
				if merged, err = strdb.Parse(tmp); err != nil {
					return nil, err
				}
				if _, err := strdb.BuildHoliday(merged, ps3Str, strdb.WorldDialogueIDs(objs)); err != nil {
					return nil, err
				}
			}
		}
		if doStr {
			var console *strdb.DB // Instant Action texts: the Xbox 360 list is the reference, the PS3 one has the same
			if o.InstantActionText {
				if console = x360Str; console == nil {
					console = ps3Str
				}
			}
			ct, err := strdb.BuildConsoleText(stock, merged, console)
			if err != nil {
				return nil, err
			}
			data, err := ct.Bytes(false, 2)
			if err != nil {
				return nil, err
			}
			if err := w.write("Game/"+strdb.ConsoleTextFile, data); err != nil {
				return nil, err
			}
		}
	}

	if doDB {
		o.say("Merging the game databases")
		pc, err := fs.Read(`Database\FEAR.gamdb00p`)
		if err != nil {
			return nil, fmt.Errorf("stock game database: %w", err)
		}
		pcl, err := fs.Read(`DatabaseLocalized\FEARL.Gamdb00p`)
		if err != nil {
			return nil, fmt.Errorf("stock localized database: %w", err)
		}
		in := dbmerge.Inputs{PC: pc, PCL: pcl, TOC: toc, Strings: sfodStr, SFODPS3Art: o.SFODPS3Art, BishopStock: o.BishopStock, NoDropFix: o.NoDropFix, Log: log,
			InstantAction: o.InstantActionText, WatsonReload: o.WatsonReload}
		if in.PS3DB, err = readCI(o.PS3Dir, "database", "loki.gamedb"); err != nil {
			return nil, err
		}
		if in.PS3World, err = readCI(o.PS3Dir, "worlds", "release", "sfod_d.wldsrvr"); err != nil {
			return nil, err
		}
		for _, n := range []string{"Admin", "Aftermath", "Moody", "ATC_Roof", "Mapes_Elevator"} {
			b, err := pcWorld(n)
			if err != nil {
				return nil, fmt.Errorf("stock PC world %s: %w", n, err)
			}
			in.PCWorlds = append(in.PCWorlds, b)
		}
		if o.X360Dir != "" {
			if in.X360DB, err = readCI(o.X360Dir, "Game", "database", "Loki.gamedb"); err != nil {
				return nil, err
			}
		}
		out, err := dbmerge.BuildAll(in)
		if err != nil {
			return nil, err
		}
		fear, fearl := out.SFOD.FEAR, out.SFOD.FEARL
		if out.Holiday != nil {
			fear, fearl = out.Holiday.FEAR, out.Holiday.FEARL
		}
		if out.Live != nil { // campaign player, Instant Action and Watson reload records on top (stage 4)
			fear, fearl = out.Live.FEAR, out.Live.FEARL
		}
		if err := w.write("Game/Database/FEAR.gamdb00p", fear); err != nil {
			return nil, err
		}
		if err := w.write("Game/DatabaseLocalized/FEARL.Gamdb00p", fearl); err != nil {
			return nil, err
		}
	}

	if doObj {
		spec, err := objects00p.EmbeddedSpec()
		if err != nil {
			return nil, err
		}
		weapons := o.Weapons
		if len(weapons) == 0 {
			if o.PS3Dir != "" {
				weapons = append(weapons, "watson")
			}
			if o.X360Dir != "" {
				weapons = append(weapons, "sm15")
			}
		}
		if len(weapons) == 0 {
			return nil, errors.New("content: the objects stage needs the PS3 copy (Watson SAS-12) or the Xbox 360 copy (SM15)")
		}
		o.say("Building the campaign Objects00p files (%s)", strings.Join(weapons, ", "))
		// The placements are read from the console worlds: SM15 from the Xbox 360 copy, Watson from the PS3 copy.
		consoleRoots := map[string]string{objects00p.X360: o.X360Dir, objects00p.PS3: o.PS3Dir}
		readConsole := func(console string, rel []string, max int) ([]byte, error) {
			root := consoleRoots[console]
			if root == "" {
				return nil, fmt.Errorf("the %s copy is needed", console)
			}
			p, err := FindCI(root, rel...)
			if err != nil {
				return nil, err
			}
			if max <= 0 {
				return os.ReadFile(p)
			}
			f, err := os.Open(p)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			b := make([]byte, max)
			n, err := io.ReadFull(f, b)
			if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, err
			}
			return b[:n], nil
		}
		for _, name := range spec.Worlds() {
			ops := spec[name].Ops
			// skip worlds with no operation of the selected weapons
			n := 0
			for _, op := range ops {
				for _, wp := range weapons {
					if op.Weapon == wp {
						n++
					}
				}
			}
			if n == 0 {
				continue
			}
			wb, err := pcWorld(name)
			if err != nil {
				return nil, fmt.Errorf("stock PC world %s: %w", name, err)
			}
			res, err := objects00p.Build(name, wb, ops, objects00p.Options{NoAppend: o.NoAppend, Weapons: weapons, Console: readConsole})
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			// A file built with every operation must be the mod's confirmed file: a different stock world or
			// console copy must never produce a wrong one. (A weapon or append subset has no confirmed hash.)
			if res.Complete {
				rel := "Worlds/Release/" + name + ".Objects00p"
				sum := sha256.Sum256(res.Data)
				if want := confirmedHash(rel); want == "" || hex.EncodeToString(sum[:]) != want {
					return nil, fmt.Errorf("%s: the built file does not match the confirmed mod file (sha256 %s, expected %q)", rel, hex.EncodeToString(sum[:]), want)
				}
			}
			if err := w.write("Game/Worlds/Release/"+name+".Objects00p", res.Data); err != nil {
				return nil, err
			}
		}
	}

	if o.want("assets") {
		if err := o.buildAssets(w, fs, log); err != nil {
			return nil, err
		}
	}
	if o.want("sounds") {
		if err := o.buildSounds(w, log); err != nil {
			return nil, err
		}
	}
	if o.want("iamaps") {
		if err := o.buildIAMaps(w, fs, log); err != nil {
			return nil, err
		}
	}

	sort.Slice(w.files, func(i, j int) bool { return w.files[i].Rel < w.files[j].Rel })
	if o.ModLayout {
		return &Report{Files: w.files, Log: log.String()}, nil
	}
	var sums strings.Builder
	for _, f := range w.files {
		fmt.Fprintf(&sums, "%s  %s\n", f.SHA256, f.Rel)
	}
	if err := os.MkdirAll(filepath.Join(o.OutDir, "info"), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(o.OutDir, "info", "SHA256SUMS.txt"), []byte(sums.String()), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(o.OutDir, "info", "build_log.txt"), []byte(log.String()), 0o644); err != nil {
		return nil, err
	}
	return &Report{Files: w.files, Log: log.String()}, nil
}

// ps3Strdb returns the PS3 English Loki.strdb.
func (o *Options) ps3Strdb() ([]byte, error) {
	if o.PS3Strdb != "" {
		return os.ReadFile(o.PS3Strdb)
	}
	p, err := FindCI(o.PS3Dir, "ubercatalog.cat")
	if err != nil {
		return nil, fmt.Errorf("PS3 ubercatalog.cat not found (or give the extracted Loki.strdb): %w", err)
	}
	c, err := catalog.Open(p)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.ReadByName(`database_string\English\Loki.strdb`)
}

// x360Strdb returns the Xbox 360 English Loki.strdb from Game\uberCatalog.cat.
func (o *Options) x360Strdb() ([]byte, error) {
	p, err := FindCI(o.X360Dir, "Game", "uberCatalog.cat")
	if err != nil {
		return nil, fmt.Errorf("Xbox 360 uberCatalog.cat not found: %w", err)
	}
	c, err := catalog.Open(p)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.ReadByName(`database_string\English\Loki.strdb`)
}

// Compare checks the written files against a manifest of "<sha256>  <path>" lines (either slash style,
// optional leading "Game\" or "Game/" is kept as written). Only files present in both are compared.
func Compare(r *Report, manifest string) (matched, differing, missing []string) {
	exp := map[string]string{}
	for _, l := range strings.Split(manifest, "\n") {
		l = strings.TrimSpace(l)
		if len(l) < 66 {
			continue
		}
		exp[strings.ToLower(strings.ReplaceAll(strings.TrimSpace(l[64:]), "\\", "/"))] = l[:64]
	}
	for _, f := range r.Files {
		want, ok := exp[strings.ToLower(f.Rel)]
		switch {
		case !ok:
			missing = append(missing, f.Rel)
		case want == f.SHA256:
			matched = append(matched, f.Rel)
		default:
			differing = append(differing, f.Rel)
		}
	}
	return
}
