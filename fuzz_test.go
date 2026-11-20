package zipppmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// FuzzReader feeds arbitrary bytes through the central-directory parser and
// the entry decoders. It must never panic; malformed input is reported as
// errors (mostly ErrCorrupt) instead.
func FuzzReader(f *testing.F) {
	for _, name := range []string{"sample_ppmd_restart.zip", "sample_ppmd_cutoff.zip"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		sr := NewStreamReader(bytes.NewReader(data))
		for _, file := range r.File {
			// Keep the fuzz run fast: skip entries that claim to decode
			// to more than 1 MiB.
			if file.UncompressedSize > 1<<20 {
				continue
			}
			rc, err := file.Open()
			if err == nil {
				io.Copy(io.Discard, rc) //nolint:errcheck
				rc.Close()
			}
		}
		// Walk the same archive with the StreamReader; it stops at the
		// first error, which is fine.
		for {
			hdr, err := sr.Next()
			if err != nil {
				return
			}
			if hdr.UncompressedSize > 1<<20 {
				continue
			}
			rc, err := sr.Open()
			if err != nil {
				return
			}
			io.Copy(io.Discard, rc) //nolint:errcheck
			rc.Close()
		}
	})
}
