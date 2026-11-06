package app

import (
	"os"
	"strings"
	"testing"
)

// The real writer is used only for a complete choice; everything else stays a preview.
func TestReadyToInstall(t *testing.T) {
	dir := t.TempDir()
	// nothing chosen: never ready (an empty choice needs no file, which must not read as "complete")
	if ok, _ := ReadyToInstall(InstallRequest{GameDir: dir}); ok {
		t.Fatal("an empty request is ready")
	}
	if ok, _ := ReadyToInstall(InstallRequest{GameDir: dir, Options: bothCopies}); ok {
		t.Fatal("ready without a copy path")
	}
	// one copy only: an empty choice, never ready
	if ok, _ := ReadyToInstall(InstallRequest{GameDir: dir, X360: dir, Options: bothCopies}); ok {
		t.Fatal("ready with the Xbox 360 copy alone")
	}
	if ok, _ := ReadyToInstall(InstallRequest{GameDir: dir, PS3Path: dir, X360: dir, Options: Options{UsePS3: true}}); ok {
		t.Fatal("ready with the Xbox 360 copy not taking part")
	}
	// both copies, given as disc images (files): not ready, with the plain-language reason
	img := dir + "/copy.iso"
	if err := os.WriteFile(img, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, why := ReadyToInstall(InstallRequest{GameDir: dir, PS3Path: img, X360: dir, Options: bothCopies}); ok || !strings.Contains(why, "disc image") {
		t.Fatalf("disc image: ready %v, %q", ok, why)
	}
	// both copies as folders: ready
	if ok, why := ReadyToInstall(InstallRequest{GameDir: dir, PS3Path: dir, X360: dir, Options: bothCopies}); !ok {
		t.Fatalf("both folders: %q", why)
	}
}

// The plan says when INSTALL would only read and check.
func TestPlanPreview(t *testing.T) {
	b := New(func(State) {})
	p := b.Plan(Options{})
	if !p.Preview {
		t.Fatal("a plan with nothing chosen is not a preview")
	}
}
