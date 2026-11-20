package zipppmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkDecodeFreshEntry(b *testing.B) {
	data, err := os.ReadFile(filepath.Join("test_arch", "fresh.zip"))
	if err != nil {
		b.Skip("fresh.zip not available")
	}
	r, err := NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		b.Fatal(err)
	}
	var f *File
	for _, ff := range r.File {
		if ff.Name == "183067.fb2" {
			f = ff
			break
		}
	}
	if f == nil {
		b.Skip("entry not found")
	}
	b.SetBytes(int64(f.UncompressedSize))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rc, err := f.Open()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			b.Fatal(err)
		}
		rc.Close()
	}
}
