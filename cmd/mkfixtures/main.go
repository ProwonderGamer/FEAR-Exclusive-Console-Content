// Command mkfixtures builds a set of sample folders and disc images from the
// reference game copies, for exercising the UI and the CLI by hand:
//
//	go run ./cmd/mkfixtures -ps3 <.../USRDIR/game> -x360 <disc root> -out <dir>
//
// Big catalogs that are only checked for presence/size are sparse stand-ins.
package main

import (
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"fearinstaller/internal/fixtures"
	"fearinstaller/internal/isobuild"
	"fearinstaller/internal/refdata"
)

type tb struct{}

func (tb) Helper()        {}
func (tb) Fatal(a ...any) { log.Fatal(a...) }

func main() {
	ps3 := flag.String("ps3", "", "reference PS3_GAME/USRDIR/game folder")
	x360 := flag.String("x360", "", "reference Xbox 360 disc root")
	out := flag.String("out", "", "output directory")
	flag.Parse()
	if *ps3 == "" || *x360 == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	t := tb{}
	must := func(err error) {
		if err != nil {
			log.Fatal(err)
		}
	}
	must(os.MkdirAll(*out, 0o755))
	stand := filepath.Join(*out, "_standins")
	must(os.MkdirAll(stand, 0o755))

	// Fake Steam library with a game folder that has EchoPatch.
	steam := filepath.Join(*out, "Steam")
	game := filepath.Join(steam, "steamapps", "common", "FEAR Ultimate Shooter Edition")
	must(os.MkdirAll(game, 0o755))
	must(os.WriteFile(filepath.Join(game, "FEAR.exe"), []byte("MZ"), 0o644))
	must(os.WriteFile(filepath.Join(game, "FEAR_8.Arch00"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(game, "dinput8.dll"), []byte("...EchoPatch..."), 0o644))
	must(os.WriteFile(filepath.Join(game, "EchoPatch.ini"), []byte("[Fixes]"), 0o644))
	noEcho := filepath.Join(*out, "FEAR_without_EchoPatch")
	must(os.MkdirAll(noEcho, 0o755))
	must(os.WriteFile(filepath.Join(noEcho, "FEAR.exe"), []byte("MZ"), 0o644))
	must(os.WriteFile(filepath.Join(noEcho, "FEAR_8.Arch00"), []byte("x"), 0o644))

	ps3Files := fixtures.PS3Files(t, *ps3, stand, refdata.PS3TitleID)
	fixtures.Materialize(t, filepath.Join(*out, "ps3_disc_folder"), ps3Files)
	must(isobuild.WriteISO9660(filepath.Join(*out, "ps3_decrypted.iso"), ps3Files, isobuild.ISO9660Options{}))

	// Encrypted-looking copy: same names and sizes, first 64 KiB random.
	var enc []isobuild.File
	for _, f := range ps3Files {
		if f.Src != "" {
			st, _ := os.Stat(f.Src)
			p := filepath.Join(stand, "enc_"+filepath.Base(f.Path))
			fh, err := os.Create(p)
			must(err)
			fh.Truncate(st.Size())
			b := make([]byte, min(65536, int(st.Size())))
			rand.Read(b)
			fh.WriteAt(b, 0)
			fh.Close()
			f.Src = p
		}
		enc = append(enc, f)
	}
	sys := make([]byte, 2048)
	binary.BigEndian.PutUint32(sys, 3)
	for i, v := range []uint32{0, 0x2000, 0x2001, 0x90000, 0x90001, 0x400000} {
		binary.BigEndian.PutUint32(sys[8+4*i:], v)
	}
	must(isobuild.WriteISO9660(filepath.Join(*out, "ps3_encrypted.iso"), enc, isobuild.ISO9660Options{SystemArea: sys}))

	xf := fixtures.X360Files(t, *x360, stand)
	fixtures.Materialize(t, filepath.Join(*out, "x360_folder"), xf)
	must(isobuild.WriteXDVDFS(filepath.Join(*out, "x360_xgd2.iso"), xf, 0x0FD90000))

	junk := make([]byte, 4<<20)
	rand.Read(junk)
	must(os.WriteFile(filepath.Join(*out, "junk.iso"), junk, 0o644))
	fmt.Println("fixtures written to", *out)
}
