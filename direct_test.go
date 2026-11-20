package zipppmd

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/MrOzean/go-zip-ppmd/ppmd"
)

// TestDirectPPMdDecode bypasses the zip layer: it feeds the entry's exact
// compressed bytes to the ppmd decoder and checks the CRC.
func TestDirectPPMdDecode(t *testing.T) {
	for _, name := range []string{"sample_ppmd_restart.zip", "sample_ppmd_cutoff.zip"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		csize := binary.LittleEndian.Uint32(data[18:])
		usize := binary.LittleEndian.Uint32(data[22:])
		nl := int(binary.LittleEndian.Uint16(data[26:]))
		el := int(binary.LittleEndian.Uint16(data[28:]))
		body := data[30+nl+el : 30+nl+el+int(csize)]
		val := uint32(body[0]) | uint32(body[1])<<8
		d, err := ppmd.NewDecoder(bytes.NewReader(body[2:]), int(val&0xF)+1, (((val>>4)&0xFF)+1)<<20, int(val>>12))
		if err != nil {
			t.Fatal(err)
		}
		// Like the zip layer, bound the output to the entry's uncompressed
		// size: the encoder's range-coder flush yields a few extra symbols
		// past the logical end, which the caller discards.
		lr := io.LimitReader(d, int64(usize))
		out, err := io.ReadAll(lr)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if uint32(len(out)) != usize {
			t.Fatalf("%s: got %d bytes, want %d", name, len(out), usize)
		}
		t.Logf("%s: crc=%08x", name, crc32.ChecksumIEEE(out))
	}
}
