package verify

import (
	"errors"
	"fmt"

	"fearinstaller/internal/vfs"
)

// Source is a console copy opened for reading after it has been verified: the folder or image, plus the
// sub-folder that holds the game data (the user may have picked the whole disc, PS3_GAME, one folder
// above a disc, and so on). It finds the data exactly like the verifier did, so a relative path such as
// "worlds/release/sfod_d.wld" (PS3) or "Game/Worlds/Release/Holiday.wld" (Xbox 360) means the same here as
// in refdata. It only reads.
type Source struct {
	fsys vfs.FS
	l    located
}

// OpenSource opens a PS3 ("ps3") or Xbox 360 ("x360") copy the same way VerifyPS3 / VerifyX360 do.
func OpenSource(kind, path string) (*Source, error) {
	switch kind {
	case "ps3":
		fsys, err := vfs.OpenOrdered(climb(path, ps3RootAt), ps3Order, recognizedLayout)
		if err != nil {
			return nil, err
		}
		l, base, ok := findPS3Root(fsys)
		if !ok {
			fsys.Close()
			return nil, errors.New("this does not look like the PS3 version of F.E.A.R")
		}
		return &Source{fsys: fsys, l: located{fsys: fsys, prefix: joinPrefix(l.prefix, base)}}, nil
	case "x360":
		fsys, err := vfs.OpenOrdered(climb(path, x360RootAt), x360Order, recognizedLayout)
		if err != nil {
			return nil, err
		}
		l, ok := findX360Root(fsys)
		if !ok {
			fsys.Close()
			return nil, errors.New("this does not look like the Xbox 360 version of F.E.A.R")
		}
		return &Source{fsys: fsys, l: l}, nil
	}
	return nil, fmt.Errorf("unknown console %q", kind)
}

// Stat describes a file inside the copy (rel uses '/' and is relative to the game data root).
func (s *Source) Stat(rel string) (vfs.Entry, error) { return s.fsys.Stat(s.l.path(rel)) }

// Open opens a file inside the copy for reading.
func (s *Source) Open(rel string) (vfs.File, error) { return s.fsys.Open(s.l.path(rel)) }

// Kind names the container ("folder", "ISO 9660", ...).
func (s *Source) Kind() string { return s.fsys.Kind() }

// Close releases the folder or image.
func (s *Source) Close() error { return s.fsys.Close() }
