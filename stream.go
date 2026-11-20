package zipppmd

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// StreamReader reads the entries of a ZIP archive sequentially from an
// io.Reader, without requiring seek support. This works for archives read
// from pipes, network connections, or other non-seekable sources.
//
// Entries must be consumed in order: after Next returns a file, call Open
// to obtain its decompressed content and read it fully before calling Next
// again. If the previous entry was not fully read, Next drains the
// remaining data automatically.
type StreamReader struct {
	r       *bufio.Reader
	cur     *File
	openRC  io.ReadCloser
	raw     io.Reader // the decompressor, for draining
	counted *countingReader
	err     error
}

// NewStreamReader creates a StreamReader reading from r.
func NewStreamReader(r io.Reader) *StreamReader {
	return &StreamReader{r: bufio.NewReaderSize(r, 64*1024)}
}

// Next advances to the next entry in the archive, returning its header. The
// returned File is only valid until the next call to Next.
func (s *StreamReader) Next() (*File, error) {
	if s.err != nil {
		return nil, s.err
	}
	// Drain the previous entry if the caller did not consume it, then pull
	// the decompressor to the end of the entry so that the shared stream is
	// positioned exactly after the entry's compressed data.
	if s.openRC != nil {
		if _, err := io.Copy(io.Discard, s.openRC); err != nil && s.err == nil {
			s.err = err
		}
		s.openRC.Close()
		s.openRC = nil
	}
	if s.err == nil && s.raw != nil {
		// Swallow drain errors: the caller-visible read already succeeded;
		// the drain only re-synchronizes the stream.
		io.Copy(io.Discard, s.raw)
		s.raw = nil
	}
	if s.err == nil && s.counted != nil && s.cur != nil {
		if rem := int64(s.cur.CompressedSize) - s.counted.n; rem > 0 {
			if _, err := io.CopyN(io.Discard, s.r, rem); err != nil && s.err == nil {
				s.err = fmt.Errorf("%w: truncated entry %q", ErrCorrupt, s.cur.Name)
			}
		}
		s.counted = nil
	}
	if s.err != nil {
		return nil, s.err
	}

	hdr := make([]byte, 30)
	if _, err := io.ReadFull(s.r, hdr); err != nil {
		if err == io.EOF {
			s.err = io.EOF
		} else if err == io.ErrUnexpectedEOF {
			s.err = fmt.Errorf("%w: truncated local file header", ErrCorrupt)
		} else {
			s.err = err
		}
		return nil, s.err
	}
	sig := binary.LittleEndian.Uint32(hdr)
	if sig != localFileHeaderSig {
		if sig == centralDirHeaderSig {
			// Start of the central directory: no more entries.
			s.err = io.EOF
			return nil, s.err
		}
		s.err = fmt.Errorf("%w: bad local file header signature %08x", ErrCorrupt, sig)
		return nil, s.err
	}
	flags := binary.LittleEndian.Uint16(hdr[6:])
	method := binary.LittleEndian.Uint16(hdr[8:])
	modTime := binary.LittleEndian.Uint16(hdr[10:])
	modDate := binary.LittleEndian.Uint16(hdr[12:])
	crc := binary.LittleEndian.Uint32(hdr[14:])
	csize := binary.LittleEndian.Uint32(hdr[18:])
	usize := binary.LittleEndian.Uint32(hdr[22:])
	nameLen := int(binary.LittleEndian.Uint16(hdr[26:]))
	extraLen := int(binary.LittleEndian.Uint16(hdr[28:]))

	if flags&encryptedFlag != 0 {
		s.err = fmt.Errorf("zipppmd: entry %d: %w", len(hdr), ErrEncrypted)
		return nil, s.err
	}
	if flags&dataDescriptorFlag != 0 {
		s.err = fmt.Errorf("%w (bit 3 set)", ErrDataDescriptor)
		return nil, s.err
	}

	name := make([]byte, nameLen)
	if _, err := io.ReadFull(s.r, name); err != nil {
		s.err = fmt.Errorf("%w: reading entry name", ErrCorrupt)
		return nil, s.err
	}
	if extraLen > 0 {
		if _, err := io.ReadFull(s.r, make([]byte, extraLen)); err != nil {
			s.err = fmt.Errorf("%w: reading entry extra field", ErrCorrupt)
			return nil, s.err
		}
	}

	s.cur = &File{FileHeader: FileHeader{
		Name:             decodeName(name, flags),
		Method:           method,
		CompressedSize:   uint64(csize),
		UncompressedSize: uint64(usize),
		CRC32:            crc,
		Modified:         dosTimeToUnix(modTime, modDate),
	}}
	return s.cur, nil
}

// Open returns an io.ReadCloser that decompresses the entry returned by the
// last call to Next and verifies its CRC-32 checksum. The returned reader
// must be fully consumed (or drained by the next Next call) before Next is
// called again.
func (s *StreamReader) Open() (io.ReadCloser, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.cur == nil {
		return nil, ErrInvalidState
	}
	if s.openRC != nil {
		return nil, errors.New("zipppmd: entry already opened")
	}
	body := &countingReader{r: io.LimitReader(s.r, int64(s.cur.CompressedSize))}
	rc, raw, counted, err := openBody(s.cur.FileHeader, body)
	if err != nil {
		s.err = err
		return nil, err
	}
	s.openRC = rc
	s.raw = raw
	s.counted = counted
	return rc, nil
}
