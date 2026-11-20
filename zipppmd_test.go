package zipppmd

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// readAllEntries decodes every entry of the archive at path using the
// Reader API and returns the decompressed contents keyed by name.
func readAllEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]byte)
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %q: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %q: %v", f.Name, err)
		}
		out[f.Name] = body
	}
	return out
}

func TestReaderPPMdRestart(t *testing.T) {
	entries := readAllEntries(t, filepath.Join("testdata", "sample_ppmd_restart.zip"))
	got, ok := entries["sample.txt"]
	if !ok {
		t.Fatal("sample.txt not found in archive")
	}
	want, err := os.ReadFile(filepath.Join("testdata", "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}

func TestReaderPPMdCutOff(t *testing.T) {
	entries := readAllEntries(t, filepath.Join("testdata", "sample_ppmd_cutoff.zip"))
	got := entries["sample.txt"]
	want, err := os.ReadFile(filepath.Join("testdata", "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}

func TestStreamReaderPPMd(t *testing.T) {
	for _, name := range []string{"sample_ppmd_restart.zip", "sample_ppmd_cutoff.zip"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", "sample.txt"))
			if err != nil {
				t.Fatal(err)
			}
			sr := NewStreamReader(bytes.NewReader(data))
			f, err := sr.Next()
			if err != nil {
				t.Fatal(err)
			}
			if f.Name != "sample.txt" {
				t.Fatalf("unexpected entry name %q", f.Name)
			}
			rc, err := sr.Open()
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("content mismatch: got %d bytes, want %d bytes", len(got), len(want))
			}
			if _, err := sr.Next(); err != io.EOF {
				t.Fatalf("expected io.EOF, got %v", err)
			}
		})
	}
}

// buildZip builds a minimal single-entry ZIP archive in memory using the
// given method. body is the stored (method 0) or raw-deflated (method 8)
// payload; crc is the checksum of the uncompressed data.
func buildZip(name string, method uint16, body []byte, crc uint32) []byte {
	var buf bytes.Buffer

	// Local file header.
	var lh []byte
	lh = binary.LittleEndian.AppendUint32(lh, localFileHeaderSig)
	lh = binary.LittleEndian.AppendUint16(lh, 20) // version needed
	lh = binary.LittleEndian.AppendUint16(lh, 0)  // flags
	lh = binary.LittleEndian.AppendUint16(lh, method)
	lh = binary.LittleEndian.AppendUint16(lh, 0)    // time
	lh = binary.LittleEndian.AppendUint16(lh, 0x21) // date: 1980-01-01
	lh = binary.LittleEndian.AppendUint32(lh, crc)  // crc
	lh = binary.LittleEndian.AppendUint32(lh, uint32(len(body)))
	lh = binary.LittleEndian.AppendUint32(lh, uint32(len(body)))
	lh = binary.LittleEndian.AppendUint16(lh, uint16(len(name)))
	lh = binary.LittleEndian.AppendUint16(lh, 0) // extra len
	buf.Write(lh)
	buf.WriteString(name)
	dataStart := buf.Len()
	buf.Write(body)

	// Central directory.
	var cd []byte
	cd = binary.LittleEndian.AppendUint32(cd, centralDirHeaderSig)
	cd = binary.LittleEndian.AppendUint16(cd, 20)
	cd = binary.LittleEndian.AppendUint16(cd, 20)
	cd = binary.LittleEndian.AppendUint16(cd, 0)
	cd = binary.LittleEndian.AppendUint16(cd, method)
	cd = binary.LittleEndian.AppendUint16(cd, 0)
	cd = binary.LittleEndian.AppendUint16(cd, 0x21)
	cd = binary.LittleEndian.AppendUint32(cd, crc)
	cd = binary.LittleEndian.AppendUint32(cd, uint32(len(body)))
	cd = binary.LittleEndian.AppendUint32(cd, uint32(len(body)))
	cd = binary.LittleEndian.AppendUint16(cd, uint16(len(name)))
	cd = binary.LittleEndian.AppendUint16(cd, 0)
	cd = binary.LittleEndian.AppendUint16(cd, 0)
	cd = binary.LittleEndian.AppendUint16(cd, 0)
	cd = binary.LittleEndian.AppendUint16(cd, 0)
	cd = binary.LittleEndian.AppendUint16(cd, 0)
	cd = binary.LittleEndian.AppendUint32(cd, 0) // external attributes
	cd = binary.LittleEndian.AppendUint32(cd, 0) // local header offset
	cd = append(cd, name...)
	buf.Write(cd)

	// End of central directory.
	var eocd []byte
	eocd = binary.LittleEndian.AppendUint32(eocd, endOfCentralDirSig)
	eocd = binary.LittleEndian.AppendUint16(eocd, 0)
	eocd = binary.LittleEndian.AppendUint16(eocd, 0)
	eocd = binary.LittleEndian.AppendUint16(eocd, 1)
	eocd = binary.LittleEndian.AppendUint16(eocd, 1)
	eocd = binary.LittleEndian.AppendUint32(eocd, uint32(len(cd)))
	eocd = binary.LittleEndian.AppendUint32(eocd, uint32(dataStart+len(body)))
	eocd = binary.LittleEndian.AppendUint16(eocd, 0)
	buf.Write(eocd)

	return buf.Bytes()
}

func TestReaderStore(t *testing.T) {
	payload := []byte("hello store method")
	archive := buildZip("hello.txt", MethodStore, payload, crc32.ChecksumIEEE(payload))
	r, err := NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.File) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(r.File))
	}
	rc, err := r.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestReaderDeflate(t *testing.T) {
	payload := []byte("hello deflate method, hello deflate method, hello!")
	var deflated bytes.Buffer
	w, _ := flate.NewWriter(&deflated, flate.DefaultCompression)
	w.Write(payload)
	w.Close()
	archive := buildZip("hello.txt", MethodDeflate, deflated.Bytes(), crc32.ChecksumIEEE(payload))

	r, err := NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := r.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestStreamReaderPartialRead(t *testing.T) {
	// Exercise the drain path where the caller does not read an entry to
	// completion: read only part of the entry, then call Next and verify a
	// clean EOF.
	data, err := os.ReadFile(filepath.Join("testdata", "sample_ppmd_restart.zip"))
	if err != nil {
		t.Fatal(err)
	}
	sr := NewStreamReader(bytes.NewReader(data))
	if _, err := sr.Next(); err != nil {
		t.Fatal(err)
	}
	rc, err := sr.Open()
	if err != nil {
		t.Fatal(err)
	}
	partial := make([]byte, 1000)
	if _, err := io.ReadFull(rc, partial); err != nil {
		t.Fatal(err)
	}
	rc.Close()
	// Next must drain the unread data and then report EOF.
	if _, err := sr.Next(); err != io.EOF {
		t.Fatalf("expected io.EOF after drain, got %v", err)
	}
}

func TestPPMdOldVariantRejected(t *testing.T) {
	// A property block with restore method 3 (val = 0x37F9) fails the Ppmd8
	// validation that 7-Zip itself applies (restore must be 0..2). Such
	// entries cannot come from any 7-Zip release (all of them write
	// Ppmd8-compatible props for method 98) and must be rejected with
	// ErrInvalidPPMdProps before any decoding happens.
	body := []byte{0xF9, 0x37, 0x00, 0x00, 0x00, 0x00}
	archive := buildZip("old.fb2", MethodPPMd, body, 0)
	r, err := NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.File[0].Open()
	if !errors.Is(err, ErrInvalidPPMdProps) {
		t.Fatalf("expected ErrInvalidPPMdProps, got %v", err)
	}
}

func TestCRCError(t *testing.T) {
	payload := []byte("hello store method")
	archive := buildZip("hello.txt", MethodStore, payload, crc32.ChecksumIEEE(payload))
	// Corrupt the CRC in the central directory.
	for i := 0; i+4 <= len(archive); i++ {
		if binary.LittleEndian.Uint32(archive[i:]) == centralDirHeaderSig {
			binary.LittleEndian.PutUint32(archive[i+16:], 0xDEADBEEF)
			break
		}
	}
	r, err := NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := r.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(rc)
	if err == nil {
		t.Fatal("expected checksum error")
	}
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("expected ErrChecksum, got %v", err)
	}
}

func TestUnsupportedMethod(t *testing.T) {
	archive := buildZip("hello.txt", 99, []byte("x"), crc32.ChecksumIEEE([]byte("x")))
	r, err := NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.File[0].Open()
	if !errors.Is(err, ErrUnsupportedMethod) {
		t.Fatalf("expected ErrUnsupportedMethod, got %v", err)
	}
}

func TestWriterStub(t *testing.T) {
	w := NewWriter(io.Discard)
	if _, err := w.Create(&FileHeader{Name: "x"}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented from Create, got %v", err)
	}
	if err := w.Close(); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented from Close, got %v", err)
	}
}

// TestLargeArchive decodes a real multi-hundred-megabyte archive when the
// ZIPPPMD_TEST_ARCHIVE environment variable points to one. It is skipped by
// default; it exists to regression-test against the big
// archives this library was built for.
func TestLargeArchive(t *testing.T) {
	path := os.Getenv("ZIPPPMD_TEST_ARCHIVE")
	if path == "" {
		t.Skip("ZIPPPMD_TEST_ARCHIVE not set")
	}
	entries := readAllEntries(t, path)
	t.Logf("decoded %d entries", len(entries))
}

func BenchmarkDecodePPMd(b *testing.B) {
	data, err := os.ReadFile(filepath.Join("testdata", "sample_ppmd_cutoff.zip"))
	if err != nil {
		b.Fatal(err)
	}
	r, err := NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		b.Fatal(err)
	}
	f := r.File[0]
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
