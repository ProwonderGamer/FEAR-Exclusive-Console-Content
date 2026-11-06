package app

import "testing"

// recordingDialogs remembers which key each picker call was given.
type recordingDialogs struct{ calls []string }

func (d *recordingDialogs) PickFolder(key, _ string) string {
	d.calls = append(d.calls, "folder:"+key)
	return ""
}

// Each Browse button must hand the native dialog its own key, so the dialog can
// remember a separate folder per row; only the "folder" mode opens a dialog.
func TestBrowsePassesPerRowDialogKeys(t *testing.T) {
	b, _ := newTest()
	d := &recordingDialogs{}
	g := &Bridge{B: b, D: d} // Post is nil: the dialog runs inside Browse

	steps := []struct{ kind, wantKey, want string }{
		{PC, "pc", "folder:pc"},
		{PS3, "ps3", "folder:ps3"},
		{X360, "x360", "folder:x360"},
	}
	for _, s := range steps {
		g.Browse(s.kind, "folder")
	}
	if len(d.calls) != len(steps) {
		t.Fatalf("expected %d dialog calls, got %v", len(steps), d.calls)
	}
	// Every row must use a distinct key, so no pick can reset another's folder.
	keys := map[string]bool{}
	for i, s := range steps {
		if d.calls[i] != s.want {
			t.Errorf("Browse(%q): dialog call %q, want %q", s.kind, d.calls[i], s.want)
		}
		if keys[s.wantKey] {
			t.Errorf("key %q is used by more than one row", s.wantKey)
		}
		keys[s.wantKey] = true
	}
}

// Any mode other than "folder" (the old disc-image "file" pick included) opens no dialog.
func TestBrowseRejectsOtherModes(t *testing.T) {
	b, _ := newTest()
	d := &recordingDialogs{}
	g := &Bridge{B: b, D: d}
	for _, kind := range []string{PC, PS3, X360} {
		for _, mode := range []string{"file", "", "other"} {
			g.Browse(kind, mode)
		}
	}
	if len(d.calls) != 0 {
		t.Fatalf("unexpected dialog calls: %v", d.calls)
	}
}
