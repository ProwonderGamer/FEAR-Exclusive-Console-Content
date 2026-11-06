package ui

// Checks for the license files that ship with the release:
//   - LICENSE.txt and release/README.txt (our own text): ASCII, CRLF only, final CRLF, no tabs, at most 95
//     columns, US spelling, and they name every third-party license file;
//   - licenses/*.txt: our intro (same text rules), a separator, then the upstream text, whose SHA-256 must
//     match licenses/SOURCES.md (upstream texts are never re-wrapped or re-spelled);
//   - THIRD_PARTY_LICENSES.txt (embedded in the installer) is exactly those files joined in SOURCES.md order.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	srcRoot   = ".."
	separator = "\r\n\r\n" + "------------------------------------------------------------------------" + "\r\n\r\n"
	banner    = "========================================================================\r\n"
)

// sourceRows reads licenses/SOURCES.md: file name and SHA-256 of its upstream text, in table order.
func sourceRows(t *testing.T) (names, hashes []string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(srcRoot, "licenses", "SOURCES.md"))
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("^\\| `([^`]+\\.txt)` \\| .* \\| `([0-9a-f]{64})` \\| [0-9]+ \\| `[0-9a-f]{64}` \\|$")
	for _, line := range strings.Split(string(b), "\n") {
		if m := row.FindStringSubmatch(line); m != nil {
			names, hashes = append(names, m[1]), append(hashes, m[2])
		}
	}
	if len(names) == 0 {
		t.Fatal("licenses/SOURCES.md lists no files")
	}
	return names, hashes
}

// checkOwnText applies the rules for text this project wrote.
func checkOwnText(t *testing.T, name string, b []byte) {
	t.Helper()
	if !bytes.HasSuffix(b, []byte("\r\n")) {
		t.Errorf("%s: does not end with CRLF", name)
	}
	if bytes.Count(b, []byte("\n")) != bytes.Count(b, []byte("\r\n")) {
		t.Errorf("%s: has bare LF line endings", name)
	}
	if strings.Contains(strings.ToLower(string(b)), "icence") {
		t.Errorf("%s: uses the UK spelling; write \"license\"", name)
	}
	for i, line := range strings.Split(strings.TrimSuffix(string(b), "\r\n"), "\r\n") {
		for _, c := range []byte(line) {
			if c >= 0x80 || (c < 0x20 && c != '\r') {
				t.Errorf("%s line %d: non-ASCII or control byte 0x%02x", name, i+1, c)
				break
			}
		}
		if len(line) > 95 {
			t.Errorf("%s line %d: %d columns (max 95)", name, i+1, len(line))
		}
	}
}

func TestLicenseFiles(t *testing.T) {
	names, hashes := sourceRows(t)
	files, err := filepath.Glob(filepath.Join(srcRoot, "licenses", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(names) {
		t.Errorf("licenses/ holds %d .txt files, SOURCES.md lists %d", len(files), len(names))
	}

	license, err := os.ReadFile(filepath.Join(srcRoot, "LICENSE.txt"))
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile(filepath.Join(srcRoot, "release", "README.txt"))
	if err != nil {
		t.Fatal(err)
	}
	checkOwnText(t, "LICENSE.txt", license)
	checkOwnText(t, "release/README.txt", readme)
	if !bytes.Contains(readme, []byte(`Licenses\LICENSE.txt`)) {
		t.Error(`release/README.txt does not list Licenses\LICENSE.txt`)
	}

	var joined []byte
	for i, name := range names {
		b, err := os.ReadFile(filepath.Join(srcRoot, "licenses", name))
		if err != nil {
			t.Fatal(err)
		}
		intro, upstream, ok := bytes.Cut(b, []byte(separator))
		if !ok {
			t.Errorf("%s: no separator between the intro and the upstream text", name)
			continue
		}
		checkOwnText(t, name+" (intro)", append(intro, '\r', '\n'))
		sum := sha256.Sum256(upstream)
		if got := hex.EncodeToString(sum[:]); got != hashes[i] {
			t.Errorf("%s: upstream text SHA-256 %s, SOURCES.md says %s", name, got, hashes[i])
		}
		// LICENSE.txt names every file without line breaks inside a name; the README lists each one.
		if !bytes.Contains(license, []byte(name)) {
			t.Errorf("LICENSE.txt does not name %s", name)
		}
		if !bytes.Contains(readme, []byte(`Licenses\`+name)) {
			t.Errorf(`release/README.txt does not list Licenses\%s`, name)
		}
		joined = append(joined, "\r\n"+banner+name+"\r\n"+banner+"\r\n"...)
		joined = append(joined, b...)
		if !bytes.HasSuffix(b, []byte("\n")) {
			joined = append(joined, '\r', '\n')
		}
	}

	head, rest, ok := bytes.Cut(LicensesText, []byte("\r\n"+banner))
	if !ok {
		t.Fatal("THIRD_PARTY_LICENSES.txt: no section banner")
	}
	checkOwnText(t, "THIRD_PARTY_LICENSES.txt (header)", head)
	if !bytes.Equal(append([]byte("\r\n"+banner), rest...), joined) {
		t.Error("THIRD_PARTY_LICENSES.txt is not the licenses/ files joined in SOURCES.md order; rebuild it")
	}
}
