package ppmd

import (
	"bytes"
	"testing"
)

// FuzzDecoder feeds arbitrary bytes through the Ppmd8 stream decoder. The
// decoder is a port of pointer-heavy C code operating on a raw arena, so it
// must never panic on hostile input; malformed streams are reported as
// errors (ErrCorrupt, io.ErrUnexpectedEOF) instead.
func FuzzDecoder(f *testing.F) {
	// Valid props (order 6, 1 MiB, restart) and invalid ones (restore 3),
	// both with truncated range coder init bytes.
	f.Add([]byte{0x05, 0x00, 0, 0, 0, 0})
	f.Add([]byte{0xF9, 0x37, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 2 {
			return
		}
		// Same 2-byte property layout as ZIP method 98.
		val := uint32(data[0]) | uint32(data[1])<<8
		order := int(val&0xF) + 1
		// Cap the arena at 1..4 MiB so the fuzzer can run many inputs
		// quickly; the sub-allocator logic is size-independent.
		memSize := uint32((val>>4)&0xFF)%4 + 1
		restore := int(val >> 12)
		d, err := NewDecoder(bytes.NewReader(data[2:]), order, memSize<<20, restore)
		if err != nil {
			return
		}
		buf := make([]byte, 4096)
		for {
			if _, err := d.Read(buf); err != nil {
				return
			}
		}
	})
}
