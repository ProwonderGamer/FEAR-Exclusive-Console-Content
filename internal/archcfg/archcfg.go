// Package archcfg computes the new content of the game's Default.archcfg, the
// list of archives and folders the engine mounts in order (the last one wins).
//
// The installer adds ONE entry, the mod folder, as the last mount. Compute is a
// pure function: it never touches the disk, so the same code produces the
// check made before an install (Preview) and the bytes that are written.
//
// Nothing here assumes the exact content of the Steam file. It copes with
//
//   - a missing or empty file (the standard archive order is generated from the
//     archives that exist, and the result says so),
//   - UTF-8, UTF-8 with BOM, UTF-16 LE/BE with or without a BOM (the original
//     encoding and BOM are kept),
//   - LF, CRLF, lone CR and mixed line endings (the dominant style is kept, CRLF
//     when there is none),
//   - a missing final line break (the stock Steam file has none, which is how
//     glued lines such as "FEARE_8.Arch00<another mod's folder>" come about),
//   - glued lines ("<name>.Arch00<name>") from earlier appends without a line
//     break, which are split again,
//   - extra entries, comments and blank lines (kept in order; only blank lines
//     are dropped),
//   - earlier copies of the mod entry (removed; it ends up exactly once, last).
package archcfg

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxSize is the largest Default.archcfg that is processed. The real file is
// about 0.5 KB; anything much bigger is not what we think it is.
const MaxSize = 1 << 20

// Encoding names the text encoding of the file.
type Encoding string

const (
	UTF8      Encoding = "UTF-8"
	UTF8BOM   Encoding = "UTF-8 with BOM"
	UTF16LE   Encoding = "UTF-16 LE"
	UTF16LEno Encoding = "UTF-16 LE (no BOM)"
	UTF16BE   Encoding = "UTF-16 BE"
	UTF16BEno Encoding = "UTF-16 BE (no BOM)"
)

// ErrNotText is returned for a file that is binary rather than a list of names.
var ErrNotText = errors.New("Default.archcfg does not look like a text file")

// ErrTooLarge is returned for a file larger than MaxSize.
var ErrTooLarge = errors.New("Default.archcfg is unexpectedly large")

// Options for Compute.
type Options struct {
	// Mod is the entry to add, a folder name relative to the game folder.
	Mod string
	// Stock is used only when the file is missing or has no entries: the stock
	// archives that exist in the game folder, in mount order (see StockOrder).
	Stock []string
}

// Result describes what Compute did.
type Result struct {
	Existed    bool     `json:"existed"`    // the file was there
	Encoding   Encoding `json:"encoding"`   // of the original (UTF-8 for a new file)
	LineEnding string   `json:"lineEnding"` // "CRLF" or "LF"
	Entries    int      `json:"entries"`    // lines in the original that name something
	Before     []string `json:"before"`     // original lines (trimmed, without blanks)
	After      []string `json:"after"`      // new lines
	Content    []byte   `json:"-"`          // new file content, ready to write
	Changed    bool     `json:"changed"`    // Content differs from the original bytes
	Added      bool     `json:"added"`      // the mod entry is new or moves; false when it already was the last mount
	Repairs    []string `json:"repairs"`    // what was fixed besides adding the entry
	Warnings   []string `json:"warnings"`
	Summary    string   `json:"summary"` // one plain sentence
}

var glued = regexp.MustCompile(`(?i)^(.*?\.arch00)([A-Za-z0-9_].*)$`)
var allDigits = regexp.MustCompile(`^[0-9]+$`)

// isComment reports whether a line is a comment in the usual styles; such lines
// are copied untouched and never repaired or removed.
func isComment(s string) bool {
	return strings.HasPrefix(s, ";") || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "//")
}

// sameEntry reports whether a line names the mod folder (any case, with an
// optional leading ".\" and trailing separator).
func sameEntry(line, mod string) bool {
	l := strings.TrimSpace(line)
	for strings.HasPrefix(l, ".\\") || strings.HasPrefix(l, "./") {
		l = l[2:]
	}
	l = strings.TrimRight(l, `\/`)
	return strings.EqualFold(l, strings.TrimRight(mod, `\/`))
}

// decode returns the text, the encoding and the original BOM bytes.
func decode(b []byte) (string, Encoding, []string, error) {
	var warn []string
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return string(b[3:]), UTF8BOM, warn, nil
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		s, w := utf16s(b[2:], false)
		return s, UTF16LE, w, nil
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		s, w := utf16s(b[2:], true)
		return s, UTF16BE, w, nil
	}
	// No BOM: ASCII text stored as UTF-16 has a zero in every second byte.
	if len(b) >= 4 {
		var even, odd int
		for i, c := range b {
			if c == 0 {
				if i%2 == 0 {
					even++
				} else {
					odd++
				}
			}
		}
		half := len(b) / 2
		if odd*10 >= half*4 && even == 0 {
			s, w := utf16s(b, false)
			return s, UTF16LEno, w, nil
		}
		if even*10 >= half*4 && odd == 0 {
			s, w := utf16s(b, true)
			return s, UTF16BEno, w, nil
		}
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return "", "", nil, ErrNotText
	}
	return string(b), UTF8, warn, nil
}

func utf16s(b []byte, be bool) (string, []string) {
	var w []string
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
		w = append(w, "The file ends in the middle of a UTF-16 character; the last byte was ignored.")
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		if be {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			u[i] = uint16(b[2*i+1])<<8 | uint16(b[2*i])
		}
	}
	return string(utf16.Decode(u)), w
}

func encode(s string, enc Encoding) []byte {
	switch enc {
	case UTF8BOM:
		return append([]byte{0xEF, 0xBB, 0xBF}, s...)
	case UTF16LE, UTF16LEno, UTF16BE, UTF16BEno:
		be := enc == UTF16BE || enc == UTF16BEno
		var out []byte
		if enc == UTF16LE {
			out = append(out, 0xFF, 0xFE)
		} else if enc == UTF16BE {
			out = append(out, 0xFE, 0xFF)
		}
		for _, u := range utf16.Encode([]rune(s)) {
			if be {
				out = append(out, byte(u>>8), byte(u))
			} else {
				out = append(out, byte(u), byte(u>>8))
			}
		}
		return out
	}
	return []byte(s)
}

// splitLines splits on CRLF, LF or CR and reports the dominant line ending.
func splitLines(s string) (lines []string, eol string, mixed bool) {
	var crlf, lf, cr int
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\r':
			if i+1 < len(s) && s[i+1] == '\n' {
				crlf++
				i++
			} else {
				cr++
			}
			lines = append(lines, cur.String())
			cur.Reset()
		case '\n':
			lf++
			lines = append(lines, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	lines = append(lines, cur.String())
	kinds := 0
	for _, n := range []int{crlf, lf, cr} {
		if n > 0 {
			kinds++
		}
	}
	mixed = kinds > 1
	switch {
	case crlf == 0 && lf == 0 && cr == 0:
		eol = "CRLF" // no line break at all: use the stock style
	case crlf >= lf && crlf >= cr:
		eol = "CRLF"
	default:
		eol = "LF" // a lone CR file is rewritten with LF, never kept
	}
	return
}

func trim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == 0x1A || r == 0xFEFF || r == 0 })
}

// Compute returns the new file content for the given original (nil or empty
// when the file does not exist).
func Compute(orig []byte, existed bool, o Options) (Result, error) {
	r := Result{Existed: existed, Encoding: UTF8, LineEnding: "CRLF"}
	if o.Mod == "" || strings.ContainsAny(o.Mod, "\r\n\x00") {
		return r, errors.New("archcfg: invalid mod entry")
	}
	if len(orig) > MaxSize {
		return r, ErrTooLarge
	}
	var text string
	if len(orig) > 0 {
		var warn []string
		var err error
		text, r.Encoding, warn, err = decode(orig)
		if err != nil {
			return r, err
		}
		r.Warnings = append(r.Warnings, warn...)
		if !utf8.ValidString(text) && (r.Encoding == UTF8 || r.Encoding == UTF8BOM) {
			// Legacy 8-bit text: the bytes are kept as they are.
			r.Warnings = append(r.Warnings, "The file is not valid UTF-8 (probably an 8-bit code page); its bytes are kept as they are.")
		}
	}
	lines, eol, mixed := splitLines(text)
	r.LineEnding = eol
	if mixed {
		r.Repairs = append(r.Repairs, "Mixed line endings were made uniform ("+eol+").")
	}
	endsWithBreak := text == "" || strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\r")

	for _, l := range lines {
		if t := trim(l); t != "" {
			r.Before = append(r.Before, t)
		}
	}
	r.Entries = 0
	for _, l := range r.Before {
		if !isComment(l) {
			r.Entries++
		}
	}

	// Repair glued lines, keep everything else in order.
	var out []string
	for _, l := range r.Before {
		if isComment(l) {
			out = append(out, l)
			continue
		}
		rest := l
		for {
			m := glued.FindStringSubmatch(rest)
			if m == nil || allDigits.MatchString(m[2]) {
				break
			}
			out = append(out, m[1])
			r.Repairs = append(r.Repairs, fmt.Sprintf("Split the joined line %q into %q and %q.", l, strings.TrimSpace(m[1]), strings.TrimSpace(m[2])))
			rest = m[2]
		}
		out = append(out, rest)
	}
	if r.Entries == 0 || len(out) == 0 {
		if len(o.Stock) > 0 {
			r.Warnings = append(r.Warnings, "Default.archcfg "+map[bool]string{true: "has no entries", false: "was not found"}[existed]+
				"; it would be created from the standard archive order of the archives that exist in this folder.")
			out = append(out, o.Stock...) // out holds only comments here
		} else {
			return r, errors.New("archcfg: the file has no entries and no archives are known to rebuild it from")
		}
	}

	// The mod entry: exactly once, last.
	removed, wasLast := 0, false
	kept := out[:0:0]
	for _, l := range out {
		if !isComment(l) && sameEntry(l, o.Mod) {
			removed++
			continue
		}
		kept = append(kept, l)
	}
	if removed > 0 {
		wasLast = sameEntry(out[len(out)-1], o.Mod)
		if removed > 1 || !wasLast {
			r.Repairs = append(r.Repairs, fmt.Sprintf("%s was already listed; it now appears once, as the last mount.", o.Mod))
		}
	}
	r.After = append(kept, o.Mod)
	r.Added = !(removed == 1 && wasLast)

	minor := 0 // repairs that are not worth mentioning in the one-line summary
	if !endsWithBreak && existed && len(orig) > 0 {
		r.Repairs = append(r.Repairs, "The original file had no line break at the end (the stock file has none); one is now written.")
		minor++
	}
	sep := "\r\n"
	if eol == "LF" {
		sep = "\n"
	}
	r.Content = encode(strings.Join(r.After, sep)+sep, r.Encoding)
	if r.Encoding == UTF8 && (bytes.HasPrefix(r.Content, []byte{0xFF, 0xFE}) || bytes.HasPrefix(r.Content, []byte{0xFE, 0xFF}) || bytes.HasPrefix(r.Content, []byte{0xEF, 0xBB, 0xBF})) {
		return r, ErrNotText // would be read back as a different encoding
	}
	r.Changed = !existed || !bytes.Equal(r.Content, orig)

	switch {
	case !existed:
		r.Summary = "Default.archcfg is missing; it would be created with the standard archives and " + o.Mod + " as the last mount."
	case !r.Changed:
		r.Summary = o.Mod + " is already the last mount; the file stays as it is."
	case removed == 1 && wasLast && len(r.Repairs) == minor:
		r.Summary = o.Mod + " is already the last mount; only blank space would be tidied."
	case len(r.Repairs) > minor:
		n := len(r.Repairs) - minor
		r.Summary = "Add " + o.Mod + " as the last mount and repair the file (" + fmt.Sprint(n) + " repair" + map[bool]string{true: "", false: "s"}[n == 1] + ")."
	default:
		r.Summary = "Add " + o.Mod + " as the last mount."
	}
	return r, nil
}

// StockOrder returns the stock archive names that exist, in the engine's mount
// order (FEAR, FEARA, FEARL, FEARE, then _1 to _8 of each). present holds file
// names such as "FEAR_4.Arch00" in any case.
func StockOrder(present []string) []string {
	have := map[string]string{}
	for _, p := range present {
		have[strings.ToLower(p)] = p
	}
	var out []string
	add := func(n string) {
		if p, ok := have[strings.ToLower(n)+".arch00"]; ok {
			out = append(out, p)
		}
	}
	for _, b := range []string{"FEAR", "FEARA", "FEARL", "FEARE"} {
		add(b)
	}
	for i := 1; i <= 8; i++ {
		for _, b := range []string{"FEAR", "FEARA", "FEARL", "FEARE"} {
			add(fmt.Sprintf("%s_%d", b, i))
		}
	}
	return out
}
