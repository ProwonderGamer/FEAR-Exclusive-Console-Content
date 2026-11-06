package app

import (
	"testing"

	"fearinstaller/internal/pcgame"
)

// The EchoPatch panel's button opens exactly EchoPatch's GitHub page, through Post (outside the browser callback),
// and does nothing when the host gives no way to open a page.
func TestOpenEchoPatchPage(t *testing.T) {
	var opened []string
	posted := 0
	br := &Bridge{
		Post:    func(fn func()) { posted++; fn() },
		OpenURL: func(url string) { opened = append(opened, url) },
	}
	br.OpenEchoPatchPage()
	if pcgame.EchoPatchURL != "https://github.com/Wemino/EchoPatch" {
		t.Fatalf("EchoPatchURL = %q", pcgame.EchoPatchURL)
	}
	if len(opened) != 1 || opened[0] != pcgame.EchoPatchURL {
		t.Fatalf("opened %q, want only %q", opened, pcgame.EchoPatchURL)
	}
	if posted != 1 {
		t.Fatalf("Post called %d times, want 1", posted)
	}
	(&Bridge{}).OpenEchoPatchPage() // no OpenURL: no panic, nothing opened
}
