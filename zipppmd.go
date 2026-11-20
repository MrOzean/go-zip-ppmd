// Package zipppmd provides streaming read access to ZIP archives that use
// PPMd compression (method 98, as produced by 7-Zip), in addition to the
// standard store and deflate methods.
//
// The package offers two reading modes:
//
//   - Reader: random access over an io.ReaderAt (e.g. an *os.File), with
//     the central directory parsed up front, similar to archive/zip.
//   - StreamReader: sequential access over an io.Reader (e.g. stdin or a
//     network connection), reading local file headers one by one without
//     requiring seek support.
//
// Writing (PPMd compression) is not implemented yet; NewWriter returns a
// stub whose methods fail with ErrNotImplemented.
//
// # PPMd variant support
//
// 7-Zip has used PPMd var.I (Ppmd8) for ZIP method 98 since it introduced
// zip+ppmd support in version 9.11 (2009), always with the same 2-byte
// packed property format this package parses, so all 7-Zip zip+ppmd
// archives are supported. No mainstream tool is known to produce method 98
// archives with the older PPMd var.H (Ppmd7); that variant only appears in
// the 7z container's PPMd method, which is out of scope here. Property
// blocks that fail the Ppmd8 validation (the same check 7-Zip itself
// applies: restore method must be 0..2) are rejected with
// ErrInvalidPPMdProps.
//
// The PPMd decoder is a pure Go port of the public domain Ppmd8 decoder
// from the 7-Zip project (PPMd var.I, carryless range coder by
// D. Shkarin / D. Subbotin); see the ppmd subpackage for details.
package zipppmd

import (
	"compress/flate"
	"errors"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/MrOzean/go-zip-ppmd/ppmd"
)

// Compression methods supported by this package.
const (
	MethodStore   uint16 = 0
	MethodDeflate uint16 = 8
	MethodPPMd    uint16 = 98
)

// Errors returned by the package.
var (
	ErrNotImplemented     = errors.New("zipppmd: writing archives is not implemented yet")
	ErrUnsupportedMethod  = errors.New("zipppmd: unsupported compression method")
	ErrEncrypted          = errors.New("zipppmd: encrypted entries are not supported")
	ErrMultiDisk          = errors.New("zipppmd: multi-disk archives are not supported")
	ErrDataDescriptor     = errors.New("zipppmd: entries with data descriptors are not supported in streaming mode")
	ErrCorrupt            = errors.New("zipppmd: corrupt archive")
	ErrChecksum           = errors.New("zipppmd: checksum error")
	ErrInvalidPPMdProps   = errors.New("zipppmd: invalid PPMd properties")
	ErrUnsupportedRestore = errors.New("zipppmd: PPMd restore method 2 (freeze) is not supported")
	ErrModelMemoryLimit   = errors.New("zipppmd: PPMd model memory limit exceeded")
	ErrInvalidState       = errors.New("zipppmd: Open called before Next")
	errDirectorySignature = errors.New("zipppmd: expected local file header")
)

// FileHeader describes a single file or directory entry in an archive.
type FileHeader struct {
	Name             string // entry name (path separator is '/')
	Method           uint16 // compression method (0, 8 or 98)
	CompressedSize   uint64 // compressed size in bytes
	UncompressedSize uint64 // uncompressed size in bytes
	CRC32            uint32 // CRC-32 (IEEE) of the uncompressed data
	Modified         int64  // modification time as Unix timestamp (0 if unknown)
}

// IsDir reports whether the entry is a directory.
func (fh *FileHeader) IsDir() bool {
	return len(fh.Name) > 0 && fh.Name[len(fh.Name)-1] == '/'
}

// File represents a single entry in an archive.
type File struct {
	FileHeader
	// dataOffset is valid for files obtained from a Reader.
	dataOffset int64
	zip        *Reader
	flags      uint16 // general purpose bit flags from the central directory
}

// Open returns an io.ReadCloser that decompresses the entry and verifies
// its CRC-32 checksum. It is only valid for files obtained from a Reader.
func (f *File) Open() (io.ReadCloser, error) {
	if f.zip == nil {
		return nil, errors.New("zipppmd: Open is only valid on files from a Reader; use StreamReader.Open")
	}
	if f.flags&encryptedFlag != 0 {
		return nil, fmt.Errorf("%w: %q", ErrEncrypted, f.Name)
	}
	body := io.NewSectionReader(f.zip.r, f.dataOffset, int64(f.CompressedSize))
	rc, _, _, err := openBody(f.FileHeader, body, f.zip.maxModelMem)
	return rc, err
}

// openBody wraps a compressed entry stream with the appropriate decoder and
// the CRC-32 checker. maxModelMem is the PPMd model memory limit in bytes
// (0 means unlimited). It also returns the raw decompressor (for draining in
// streaming mode) and the counted reader wrapping body (or nil when body is
// not a countingReader), so the caller can position the stream exactly after
// the entry's compressed data.
func openBody(fh FileHeader, body io.Reader, maxModelMem uint64) (io.ReadCloser, io.Reader, *countingReader, error) {
	var counted *countingReader
	if cr, ok := body.(*countingReader); ok {
		counted = cr
	}
	if fh.UncompressedSize == 0 && fh.CompressedSize == 0 && !fh.IsDir() {
		// Empty file: nothing to decode.
		return &checksumReader{want: fh.CRC32, done: true}, nil, counted, nil
	}
	var r io.Reader
	switch fh.Method {
	case MethodStore:
		r = body
	case MethodDeflate:
		r = flate.NewReader(body)
	case MethodPPMd:
		// The 2-byte property block (little-endian value `val`) is the
		// format 7-Zip has used for ZIP method 98 since version 9.11
		// (2009), where zip+ppmd support was introduced directly on
		// PPMd var.I (Ppmd8):
		//
		//	order   = (val & 0xF) + 1          (2..16)
		//	memMB   = ((val >> 4) & 0xFF) + 1  (1..256)
		//	restore = val >> 12                (0=restart, 1=cutoff, 2=freeze)
		//
		// This matches 7-Zip's own validation (`order < 2 || restor > 2` is
		// rejected). restore == 2 (freeze) needs PPMD8_FREEZE_SUPPORT, which
		// neither 7-Zip default builds nor this package enable. No
		// mainstream tool is known to write other layouts for method 98.
		props := make([]byte, 2)
		if _, err := io.ReadFull(body, props); err != nil {
			return nil, nil, counted, fmt.Errorf("zipppmd: reading PPMd properties: %w", err)
		}
		val := uint32(props[0]) | uint32(props[1])<<8
		order := int((val & 0xF) + 1)
		memSize := uint64(((val >> 4) & 0xFF) + 1)
		restore := int(val >> 12)
		if restore > 2 {
			return nil, nil, counted, fmt.Errorf("%w: restore method %d (valid Ppmd8 props use 0..2)", ErrInvalidPPMdProps, restore)
		}
		if restore == 2 {
			return nil, nil, counted, fmt.Errorf("%w: entry %q (7-Zip default builds reject it too)", ErrUnsupportedRestore, fh.Name)
		}
		// Reject oversized model requests before allocating the arena: a
		// hostile or corrupt archive can ask for up to 256 MiB per entry,
		// and entries may be opened concurrently.
		if maxModelMem > 0 && memSize<<20 > maxModelMem {
			return nil, nil, counted, fmt.Errorf("%w: entry %q requests %d MiB, limit is %d MiB", ErrModelMemoryLimit, fh.Name, memSize, maxModelMem>>20)
		}
		d, err := ppmd.NewDecoder(body, order, uint32(memSize<<20), restore)
		if err != nil {
			return nil, nil, counted, fmt.Errorf("zipppmd: initializing PPMd decoder (order=%d mem=%dMB): %w", order, memSize, err)
		}
		r = d
	default:
		return nil, nil, counted, fmt.Errorf("%w: %d", ErrUnsupportedMethod, fh.Method)
	}
	return &checksumReader{
		r:    io.LimitReader(r, int64(fh.UncompressedSize)),
		hash: crc32.NewIEEE(),
		want: fh.CRC32,
	}, r, counted, nil
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// checksumReader verifies the CRC-32 of the decompressed data when the
// expected number of bytes has been read.
type checksumReader struct {
	r    io.Reader // may be nil for empty entries
	hash interface {
		io.Writer
		Sum32() uint32
	}
	want    uint32
	read    uint64
	checked bool
	done    bool
	err     error
}

func (c *checksumReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if c.done {
		return 0, io.EOF
	}
	n, err := c.r.Read(p)
	if n > 0 {
		c.hash.Write(p[:n])
		c.read += uint64(n)
	}
	if err == io.EOF {
		c.done = true
		if c.hash.Sum32() != c.want {
			c.err = fmt.Errorf("%w: got %08x, want %08x", ErrChecksum, c.hash.Sum32(), c.want)
			return n, c.err
		}
		return n, io.EOF
	}
	if err != nil {
		c.err = err
	}
	return n, err
}

func (c *checksumReader) Close() error {
	if c.err != nil {
		return c.err
	}
	return nil
}
