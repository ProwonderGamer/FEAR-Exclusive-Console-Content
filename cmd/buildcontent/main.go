// Command buildcontent runs the ported content converters into an output folder. It is a maintainer tool for
// debugging one stage without the UI. It reads the player's own copies and never writes anywhere but the
// output folder.
//
//	buildcontent build  -pc <game folder> [-ps3 <USRDIR/game>] [-x360 <disc root>] -out <folder>
//	                    [-stages db,strings,objects,assets,sounds,iamaps] [-weapons sm15,watson] [-compare manifest.txt] ...
//	buildcontent cat list    <catalog file>
//	buildcontent cat extract <catalog file> <out folder> [name-prefix]
//	buildcontent ltar list   <archive.Arch00>
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fearinstaller/internal/catalog"
	"fearinstaller/internal/content"
	"fearinstaller/internal/ltar"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  buildcontent build -pc DIR [-ps3 DIR] [-x360 DIR] -out DIR [options]   (-h for the options)
  buildcontent cat list FILE
  buildcontent cat extract FILE OUTDIR [prefix]
  buildcontent ltar list FILE`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "build":
		build(os.Args[2:])
	case "cat":
		if len(os.Args) < 4 {
			usage()
		}
		catCmd(os.Args[2], os.Args[3:])
	case "ltar":
		if len(os.Args) != 4 || os.Args[2] != "list" {
			usage()
		}
		a, err := ltar.Open(os.Args[3])
		fatal(err)
		defer a.Close()
		for _, e := range a.Entries {
			z := " "
			if e.Compressed {
				z = "Z"
			}
			fmt.Printf("%11d %s %s\n", e.Size, z, e.Path)
		}
	default:
		usage()
	}
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func build(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	var o content.Options
	var stages, weapons, compare string
	fs.StringVar(&o.PCDir, "pc", "", "PC game folder (the *.Arch00 archives)")
	fs.StringVar(&o.PS3Dir, "ps3", "", "PS3 USRDIR/game folder (decrypted)")
	fs.StringVar(&o.X360Dir, "x360", "", "Xbox 360 disc root (contains Game\\)")
	fs.StringVar(&o.OutDir, "out", "", "output folder (must not be inside a source or game folder)")
	fs.StringVar(&stages, "stages", "", "comma list of db,strings,objects,assets,sounds,iamaps (default: all the sources allow)")
	fs.StringVar(&weapons, "weapons", "", "objects stage: sm15,watson (default: both available)")
	fs.BoolVar(&o.NoAppend, "no-append", false, "objects stage: property edits only")
	fs.BoolVar(&o.SFODPS3Art, "sfod-ps3-art", false, "database: SFOD-D uses its own PS3 loading screen layout")
	fs.BoolVar(&o.NoDropFix, "no-drop-fix", false, "database: leave the SM15 drop-on-death fix out of the SFOD-D database")
	fs.BoolVar(&o.BishopStock, "bishop-stock-tree", false, "database: keep the stock Bishop animation tree")
	fs.BoolVar(&o.InstantActionText, "instant-action-text", false, "strings and db: add the Instant Action menu texts and database records (only together with the Instant Action maps)")
	fs.BoolVar(&o.WatsonReload, "watson-reload", false, "db: add the Watson reload-split records (only together with its sounds and model)")
	fs.StringVar(&o.TOCFile, "toc", "", "testing: archive listing file instead of reading every archive")
	fs.StringVar(&o.PCWorldsDir, "pc-worlds", "", "testing: folder with the stock *.World00p instead of FEAR.Arch00")
	fs.StringVar(&o.PS3Strdb, "ps3-strdb", "", "testing: extracted English Loki.strdb instead of ubercatalog.cat")
	fs.StringVar(&compare, "compare", "", "manifest of \"<sha256>  <path>\" lines to compare the result with")
	fs.Parse(args)
	if o.PCDir == "" || o.OutDir == "" {
		fs.Usage()
		os.Exit(2)
	}
	o.Stages, o.Weapons = split(stages), split(weapons)
	o.Progress = func(s string) { fmt.Println(s) }
	rep, err := content.Build(o)
	fatal(err)
	for _, f := range rep.Files {
		fmt.Printf("%s  %10d  %s\n", f.SHA256, f.Size, f.Rel)
	}
	if compare != "" {
		b, err := os.ReadFile(compare)
		fatal(err)
		m, d, x := content.Compare(rep, string(b))
		fmt.Printf("compared with %s: %d identical, %d DIFFERENT, %d not in the manifest\n", compare, len(m), len(d), len(x))
		for _, f := range d {
			fmt.Println("  DIFFERENT:", f)
		}
		if len(d) > 0 {
			os.Exit(1)
		}
	}
}

func catCmd(sub string, args []string) {
	switch sub {
	case "list":
		c, err := catalog.Open(args[0])
		fatal(err)
		defer c.Close()
		for _, e := range c.Entries {
			fmt.Printf("%10d %10d %#010x %s\n", e.Size, e.PackedSize, e.Offset, e.Name)
		}
	case "extract":
		if len(args) < 2 {
			usage()
		}
		c, err := catalog.Open(args[0])
		fatal(err)
		defer c.Close()
		prefix := ""
		if len(args) > 2 {
			prefix = catalog.PathKey(args[2])
		}
		n := 0
		for _, e := range c.Entries {
			if !strings.HasPrefix(catalog.PathKey(e.Name), prefix) {
				continue
			}
			rel, ok := catalog.SafeRelPath(e.Name)
			if !ok {
				fmt.Fprintf(os.Stderr, "skipping unsafe name %q\n", e.Name)
				continue
			}
			d, err := c.Read(e)
			fatal(err)
			p := filepath.Join(args[1], rel)
			fatal(os.MkdirAll(filepath.Dir(p), 0o755))
			fatal(os.WriteFile(p, d, 0o644))
			n++
		}
		fmt.Printf("extracted %d files to %s\n", n, args[1])
	default:
		usage()
	}
}
