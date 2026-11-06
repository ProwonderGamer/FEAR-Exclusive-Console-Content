// Command makerelease packs the release download: one ZIP holding the installer, README.txt and a Licenses
// folder (the mod's LICENSE.txt and every third-party license file). Every entry gets the same fixed date,
// so the same inputs always give a byte-identical ZIP.
//
//	go run ./cmd/makerelease -exe dist/FEAR-Exclusive-Console-Content-Installer.exe \
//	    -date YYYY-MM-DD -out FEAR-Exclusive-Console-Content.zip
//
// Run it from the source root (or pass -src). It refuses a README whose source code link is still the
// placeholder, unless -draft is given for a trial ZIP.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	exeName     = "FEAR-Exclusive-Console-Content-Installer.exe"
	placeholder = "SOURCE-CODE-LINK-TO-BE-ADDED" // in release/README.txt until the repository exists
)

// entry is one file of the ZIP: its path inside the archive and its contents.
type entry struct {
	name string
	data []byte
}

func main() {
	src := flag.String("src", ".", "installer source root (holds LICENSE.txt, licenses/ and release/)")
	exe := flag.String("exe", "", "the built installer, named "+exeName)
	date := flag.String("date", "", "release date for every ZIP entry, YYYY-MM-DD")
	out := flag.String("out", "", "ZIP file to write")
	draft := flag.Bool("draft", false, "allow the README's source code link placeholder (trial ZIPs only)")
	flag.Parse()
	if *exe == "" || *date == "" || *out == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	sum, err := run(*src, *exe, *date, *out, *draft)
	if err != nil {
		fmt.Fprintln(os.Stderr, "makerelease:", err)
		os.Exit(1)
	}
	fmt.Printf("%s\nSHA-256 %x\n", *out, sum)
}

// run collects the entries, writes the ZIP to out and returns its SHA-256.
func run(src, exe, date, out string, draft bool) ([]byte, error) {
	when, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, fmt.Errorf("-date: %w", err)
	}
	entries, err := collect(src, exe, draft)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeZip(&buf, entries, when); err != nil {
		return nil, err
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(buf.Bytes())
	return sum[:], nil
}

// collect reads every file of the download and checks the parts that must not ship wrong.
func collect(src, exe string, draft bool) ([]entry, error) {
	if filepath.Base(exe) != exeName {
		return nil, fmt.Errorf("the installer must be named %s, not %s", exeName, filepath.Base(exe))
	}
	prog, err := os.ReadFile(exe)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(prog, []byte("MZ")) {
		return nil, fmt.Errorf("%s is not a Windows program", exe)
	}
	readme, err := os.ReadFile(filepath.Join(src, "release", "README.txt"))
	if err != nil {
		return nil, err
	}
	if bytes.Contains(readme, []byte(placeholder)) && !draft {
		return nil, errors.New("release/README.txt still has the source code link placeholder; add the link first")
	}
	license, err := os.ReadFile(filepath.Join(src, "LICENSE.txt"))
	if err != nil {
		return nil, err
	}
	entries := []entry{
		{exeName, prog},
		{"README.txt", readme},
		{"Licenses/LICENSE.txt", license},
	}
	third, err := filepath.Glob(filepath.Join(src, "licenses", "*.txt"))
	if err != nil {
		return nil, err
	}
	if len(third) == 0 {
		return nil, errors.New("licenses/ holds no license files")
	}
	sort.Strings(third)
	for _, p := range third {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if !bytes.Contains(readme, []byte(`Licenses\`+filepath.Base(p))) {
			return nil, fmt.Errorf("release/README.txt does not list Licenses\\%s", filepath.Base(p))
		}
		entries = append(entries, entry{"Licenses/" + filepath.Base(p), b})
	}
	return entries, nil
}

// writeZip writes the entries in order, deflated, all with the same date.
func writeZip(w io.Writer, entries []entry, when time.Time) error {
	zw := zip.NewWriter(w)
	for _, e := range entries {
		if strings.Contains(e.name, `\`) {
			return fmt.Errorf("ZIP names use forward slashes: %s", e.name)
		}
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: when})
		if err != nil {
			return err
		}
		if _, err := fw.Write(e.data); err != nil {
			return err
		}
	}
	return zw.Close()
}
