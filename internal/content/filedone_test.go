package content

import "testing"

// TestWriterReportsEachFile: FileDone is called once per written file, after it is in place, with the path as
// it was written (mod layout: no "Game/" level), and not for a refused path.
func TestWriterReportsEachFile(t *testing.T) {
	var got []string
	w := &writer{root: t.TempDir(), modLayout: true, done: func(rel string) { got = append(got, rel) }}
	for _, rel := range []string{"Game/Worlds/Release/a.bin", "b.bin"} {
		if err := w.write(rel, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.write("../escape.bin", []byte("x")); err == nil {
		t.Fatal("a path outside the folder was written")
	}
	if len(got) != 2 || got[0] != "Worlds/Release/a.bin" || got[1] != "b.bin" || len(w.files) != 2 {
		t.Fatalf("reported %q, wrote %d files", got, len(w.files))
	}
}
