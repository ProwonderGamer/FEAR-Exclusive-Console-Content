package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeExe writes a stand-in installer (only the "MZ" start is checked) into a temporary folder.
func fakeExe(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("MZ stand-in installer"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestPackage builds the ZIP from the real license files and README twice: same layout, same bytes.
func TestPackage(t *testing.T) {
	exe := fakeExe(t, exeName)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.zip"), filepath.Join(dir, "b.zip")
	for _, out := range []string{a, b} {
		if _, err := run("../..", exe, "2026-11-06", out, true); err != nil {
			t.Fatal(err)
		}
	}
	za, _ := os.ReadFile(a)
	zb, _ := os.ReadFile(b)
	if !bytes.Equal(za, zb) {
		t.Error("two runs gave different ZIPs")
	}

	zr, err := zip.NewReader(bytes.NewReader(za), int64(len(za)))
	if err != nil {
		t.Fatal(err)
	}
	third, _ := filepath.Glob("../../licenses/*.txt")
	if want := 3 + len(third); len(zr.File) != want {
		t.Errorf("%d entries, want %d", len(zr.File), want)
	}
	for i, name := range []string{exeName, "README.txt", "Licenses/LICENSE.txt"} {
		if zr.File[i].Name != name {
			t.Errorf("entry %d is %s, want %s", i, zr.File[i].Name, name)
		}
	}
	for _, f := range zr.File[3:] {
		if !strings.HasPrefix(f.Name, "Licenses/") || !strings.HasSuffix(f.Name, "-LICENSE.txt") && !strings.HasSuffix(f.Name, "-PATENTS.txt") {
			t.Errorf("unexpected entry %s", f.Name)
		}
		src, _ := os.ReadFile(filepath.Join("../../licenses", strings.TrimPrefix(f.Name, "Licenses/")))
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		got.ReadFrom(rc)
		rc.Close()
		if !bytes.Equal(got.Bytes(), src) {
			t.Errorf("%s differs from its source file", f.Name)
		}
	}
}

// TestRefusals: the placeholder link, a wrongly named installer and a file that is not a program are refused.
func TestRefusals(t *testing.T) {
	out := filepath.Join(t.TempDir(), "x.zip")
	readme, err := os.ReadFile("../../release/README.txt")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(readme, []byte(placeholder)) {
		if _, err := run("../..", fakeExe(t, exeName), "2026-11-06", out, false); err == nil {
			t.Error("a README with the placeholder link was accepted without -draft")
		}
	}
	if _, err := run("../..", fakeExe(t, "installer.exe"), "2026-11-06", out, true); err == nil {
		t.Error("a wrongly named installer was accepted")
	}
	notExe := filepath.Join(t.TempDir(), exeName)
	if err := os.WriteFile(notExe, []byte("not a program"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run("../..", notExe, "2026-11-06", out, true); err == nil {
		t.Error("a file that is not a program was accepted")
	}
	if _, err := run("../..", fakeExe(t, exeName), "06/11/2026", out, true); err == nil {
		t.Error("a malformed date was accepted")
	}
}
