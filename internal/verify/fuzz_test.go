package verify

import "testing"

func FuzzSFO(f *testing.F) {
	f.Add([]byte("\x00PSF\x01\x01\x00\x00\x14\x00\x00\x00\x2c\x00\x00\x00\x01\x00\x00\x00TITLE_ID\x00BLUS30003\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		ParseSFO(b)
		LooksLikeEncryptedPS3Header(b, int64(len(b)))
	})
}
