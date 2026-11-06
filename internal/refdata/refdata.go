// Package refdata holds what the installer knows about the original console
// discs: which files it needs, and the SHA-256 of each as read from a known-good
// copy. The per-file table (hashes_gen.go) is produced by cmd/genhashes from the
// reference data; no game data is stored here, only sizes, hashes and the first
// 16 bytes of each file (used to tell "encrypted" from "different version").
package refdata

// Item is one required file.
type Item struct {
	Path   string // relative to the source root, '/' separators
	Size   int64
	SHA256 string // lowercase hex
	Magic  string // lowercase hex of the first 16 bytes
}

// Presence is a large file that is only checked for existence and size
// (hashing it would add minutes for little extra certainty). If SHA256 is set
// (it needs Exact too) the file is hashed like any other item: fill it in when
// a known-good copy of the file is available. Until then the result says that
// the file was only checked by size.
type Presence struct {
	Label  string
	Paths  []string // the expected locations (no recursive search), first match wins
	Size   int64    // expected size when Exact, else a lower bound
	Exact  bool
	SHA256 string // lowercase hex; empty = size-only check
}

// PS3 disc identity. PS3TitleID is the US disc the reference lists were made from.
//
// The European disc (BLES00035; English, French, Italian, Spanish) holds every file the installer reads
// byte for byte as the US disc: the published IRD file lists of both discs give the same MD5 and size for all
// of them, and differ only in PARAM.SFO, PS3_DISC.SFB and three extra voice languages (checked,
// with our US copy matching the US list on every file). The file hashes below therefore verify it unchanged.
//
// The German disc (BLES00036) holds German-language data only (Redump), without the English voice catalogs the
// bonus mission is built from, so it is recognized and turned away with a clear message.
const (
	PS3TitleID       = "BLUS30003"
	PS3TitleIDEurope = "BLES00035"
	PS3TitleIDGerman = "BLES00036"
)

// PS3Regions are the accepted PS3 releases by title ID, with the region shown to the player.
var PS3Regions = map[string]string{PS3TitleID: "USA", PS3TitleIDEurope: "Europe"}

// Xbox 360 disc identity. One disc (VU-2009, "USA, Europe", English/French/Italian/Spanish, region-free) was
// sold in both regions; its default.xex carries media ID 0x33FBCD0B, and the reference lists were made from it.
// The German disc holds German-language data only (Redump); its media ID (mastering code) is X360MediaIDGerman.
const X360MediaIDGerman = 0x60EA154B

// PS3Bases are where the game data lives on a disc: PS3_GAME/USRDIR/game.
// PS3Paths in the manifest are relative to it.
var PS3Bases = []string{"PS3_GAME/USRDIR/game", "USRDIR/game", "game", ""}

// PS3Big lists large PS3 files checked by presence and minimum size only.
// (No reference copy of ubercatalog.cat is available, so neither an exact size
// nor a hash is known; both fields stay empty until one is.)
var PS3Big = []Presence{
	// Lower bound = sum of the packed entry sizes in the catalog's table of
	// contents (the real file is a little larger because of headers/strings).
	{Label: "ubercatalog.cat", Paths: []string{"ubercatalog.cat"}, Size: 429801526, Exact: false},
}

// X360Big lists large Xbox 360 files checked by presence and size only. The
// exact size comes from the user's own copy; no hash is known yet.
var X360Big = []Presence{
	{Label: "uberCatalog.cat", Paths: []string{"Game/uberCatalog.cat", "uberCatalog.cat"}, Size: 428074094, Exact: true},
}
