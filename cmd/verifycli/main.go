// Command verifycli runs the installer's checks from a command line (any OS).
//
//	verifycli pc  <game folder>
//	verifycli diag <game folder>       text report to send us for a store release we cannot test (GOG)
//	verifycli archcfg <game folder>    dry run of the Default.archcfg change (nothing is written)
//	verifycli ps3 <disc folder | ISO>
//	verifycli x360 <extracted folder | ISO>
//
// Exit status: 0 verified, 1 not verified, 2 usage error. Read-only.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"fearinstaller/internal/archcfg"
	"fearinstaller/internal/pcgame"
	"fearinstaller/internal/verify"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: verifycli pc|diag|archcfg|ps3|x360 <path>")
		os.Exit(2)
	}
	kind, path := os.Args[1], os.Args[2]
	switch kind {
	case "diag":
		fmt.Print(pcgame.Diagnose(path, pcgame.SystemSources()))
		return
	case "archcfg":
		r, ro, err := archcfg.Preview(path, "FEAR_ConsoleRestoration")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%s\nencoding: %s, line breaks: %s, read-only: %v, entries: %d\n", r.Summary, r.Encoding, r.LineEnding, ro, r.Entries)
		for _, x := range append(r.Repairs, r.Warnings...) {
			fmt.Println("note:", x)
		}
		fmt.Printf("---- new file (%d bytes) ----\n%s", len(r.Content), r.Content)
		return
	}
	ctx := context.Background()
	progress := func(p verify.Progress) {
		fmt.Fprintf(os.Stderr, "\r  hashing %-40s file %d/%d  %3d%%", p.File, p.Index, p.Count, p.Done*100/p.Total)
	}
	var out any
	ok := false
	switch kind {
	case "pc":
		c := pcgame.Inspect(path)
		out, ok = c, c.Ready
	case "ps3":
		r := verify.VerifyPS3(ctx, path, progress)
		fmt.Fprintln(os.Stderr)
		out, ok = r, r.Verified()
	case "x360":
		r := verify.VerifyX360(ctx, path, progress)
		fmt.Fprintln(os.Stderr)
		out, ok = r, r.Verified()
	default:
		fmt.Fprintln(os.Stderr, "unknown kind", kind)
		os.Exit(2)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	if !ok {
		os.Exit(1)
	}
}
