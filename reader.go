package zipppmd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	localFileHeaderSig   = 0x04034b50
	centralDirHeaderSig  = 0x02014b50
	endOfCentralDirSig   = 0x06054b50
	eocd64Sig            = 0x06064b50
	eocd64LocatorSig     = 0x07064b50
	zip64ExtraID         = 0x0001
	utf8Flag             = 0x0800
	dataDescriptorFlag   = 0x0008
	encryptedFlag        = 0x0001
	maxCommentLen        = 0xFFFF
	eocdFixedLen         = 22
	fileHeaderFixedLen   = 46
	directoryHeaderFixed = 46
)

// Reader provides random access to the entries of a ZIP archive via an
// io.ReaderAt. The central directory is parsed by NewReader.
type Reader struct {
	r    io.ReaderAt
	size int64
	File []*File
}

// NewReader reads the central directory of a ZIP archive and returns a
// Reader. size must be the full size of the archive.
func NewReader(r io.ReaderAt, size int64) (*Reader, error) {
	z := &Reader{r: r, size: size}
	if err := z.init(); err != nil {
		return nil, err
	}
	return z, nil
}

func (z *Reader) init() error {
	// Locate the end of central directory record by scanning the last
	// 64 KiB (plus the fixed EOCD length) of the file.
	lookupLen := int64(maxCommentLen + eocdFixedLen)
	if lookupLen > z.size {
		lookupLen = z.size
	}
	buf := make([]byte, lookupLen)
	if _, err := z.r.ReadAt(buf, z.size-lookupLen); err != nil {
		return fmt.Errorf("zipppmd: reading end of central directory: %w", err)
	}
	off := int64(-1)
	for i := int64(len(buf) - eocdFixedLen); i >= 0; i-- {
		if binary.LittleEndian.Uint32(buf[i:]) == endOfCentralDirSig {
			off = i
			break
		}
	}
	if off < 0 {
		return fmt.Errorf("%w: end of central directory not found", ErrCorrupt)
	}
	eocd := buf[off:]

	diskNo := binary.LittleEndian.Uint16(eocd[4:])
	cdDisk := binary.LittleEndian.Uint16(eocd[6:])
	if diskNo != 0 || cdDisk != 0 {
		return ErrMultiDisk
	}
	cdCount := uint64(binary.LittleEndian.Uint16(eocd[10:]))
	cdSize := uint64(binary.LittleEndian.Uint32(eocd[12:]))
	cdOffset := uint64(binary.LittleEndian.Uint32(eocd[16:]))

	if cdCount == 0xFFFF || cdSize == 0xFFFFFFFF || cdOffset == 0xFFFFFFFF {
		// ZIP64: locate the EOCD64 locator just before the EOCD record.
		locOff := z.size - lookupLen + off - 20
		if locOff < 0 {
			return fmt.Errorf("%w: truncated ZIP64 locator", ErrCorrupt)
		}
		loc := make([]byte, 20)
		if _, err := z.r.ReadAt(loc, locOff); err != nil {
			return fmt.Errorf("zipppmd: reading ZIP64 locator: %w", err)
		}
		if binary.LittleEndian.Uint32(loc) != eocd64LocatorSig {
			return fmt.Errorf("%w: bad ZIP64 locator signature", ErrCorrupt)
		}
		e64Off := int64(binary.LittleEndian.Uint64(loc[8:]))
		e64 := make([]byte, 56)
		if _, err := z.r.ReadAt(e64, e64Off); err != nil {
			return fmt.Errorf("zipppmd: reading ZIP64 end of central directory: %w", err)
		}
		if binary.LittleEndian.Uint32(e64) != eocd64Sig {
			return fmt.Errorf("%w: bad ZIP64 end of central directory signature", ErrCorrupt)
		}
		cdCount = binary.LittleEndian.Uint64(e64[32:])
		cdSize = binary.LittleEndian.Uint64(e64[40:])
		cdOffset = binary.LittleEndian.Uint64(e64[48:])
	}

	if cdOffset+cdSize > uint64(z.size) {
		return fmt.Errorf("%w: central directory out of bounds", ErrCorrupt)
	}

	// Read the central directory.
	cd := make([]byte, cdSize)
	if _, err := z.r.ReadAt(cd, int64(cdOffset)); err != nil {
		return fmt.Errorf("zipppmd: reading central directory: %w", err)
	}

	z.File = make([]*File, 0, cdCount)
	pos := uint64(0)
	for i := uint64(0); i < cdCount; i++ {
		if pos+fileHeaderFixedLen > cdSize ||
			binary.LittleEndian.Uint32(cd[pos:]) != centralDirHeaderSig {
			return fmt.Errorf("%w: bad central directory entry %d", ErrCorrupt, i)
		}
		h := cd[pos:]
		flags := binary.LittleEndian.Uint16(h[8:])
		method := binary.LittleEndian.Uint16(h[10:])
		modTime := binary.LittleEndian.Uint16(h[12:])
		modDate := binary.LittleEndian.Uint16(h[14:])
		crc := binary.LittleEndian.Uint32(h[16:])
		csize := binary.LittleEndian.Uint32(h[20:])
		usize := binary.LittleEndian.Uint32(h[24:])
		nameLen := int(binary.LittleEndian.Uint16(h[28:]))
		extraLen := int(binary.LittleEndian.Uint16(h[30:]))
		commentLen := int(binary.LittleEndian.Uint16(h[32:]))
		lhOffset := binary.LittleEndian.Uint32(h[42:])
		end := pos + fileHeaderFixedLen + uint64(nameLen+extraLen+commentLen)
		if end > cdSize {
			return fmt.Errorf("%w: truncated central directory entry %d", ErrCorrupt, i)
		}
		nameBytes := h[46 : 46+nameLen]
		extra := h[46+nameLen : 46+nameLen+extraLen]

		// ZIP64 extended information.
		if csize == 0xFFFFFFFF || usize == 0xFFFFFFFF || lhOffset == 0xFFFFFFFF {
			p := extra
			for len(p) >= 4 {
				tag := binary.LittleEndian.Uint16(p)
				sz := int(binary.LittleEndian.Uint16(p[2:]))
				p = p[4:]
				if sz > len(p) {
					break
				}
				if tag == zip64ExtraID {
					q := p
					if usize == 0xFFFFFFFF && len(q) >= 8 {
						usize = binary.LittleEndian.Uint32(q)
						q = q[8:]
					}
					if csize == 0xFFFFFFFF && len(q) >= 8 {
						csize = binary.LittleEndian.Uint32(q)
						q = q[8:]
					}
					if lhOffset == 0xFFFFFFFF && len(q) >= 8 {
						lhOffset = binary.LittleEndian.Uint32(q)
					}
					break
				}
				p = p[sz:]
			}
		}

		name := decodeName(nameBytes, flags)
		f := &File{
			FileHeader: FileHeader{
				Name:             name,
				Method:           method,
				CompressedSize:   uint64(csize),
				UncompressedSize: uint64(usize),
				CRC32:            crc,
				Modified:         dosTimeToUnix(modTime, modDate),
			},
			zip: z,
		}
		if err := f.findDataOffset(int64(lhOffset)); err != nil {
			return fmt.Errorf("zipppmd: entry %q: %w", name, err)
		}
		z.File = append(z.File, f)
		pos = end
	}
	return nil
}

// findDataOffset parses the local file header at the given offset and
// records where the compressed data starts.
func (f *File) findDataOffset(lhOffset int64) error {
	if f.zip == nil {
		return errors.New("zipppmd: file is not attached to a reader")
	}
	buf := make([]byte, 30)
	if _, err := f.zip.r.ReadAt(buf, lhOffset); err != nil {
		return fmt.Errorf("reading local file header: %w", err)
	}
	if binary.LittleEndian.Uint32(buf) != localFileHeaderSig {
		return fmt.Errorf("%w at offset %d", errDirectorySignature, lhOffset)
	}
	nameLen := int(binary.LittleEndian.Uint16(buf[26:]))
	extraLen := int(binary.LittleEndian.Uint16(buf[28:]))
	f.dataOffset = lhOffset + 30 + int64(nameLen+extraLen)
	return nil
}

// decodeName converts the raw name bytes to a string, honoring the UTF-8
// flag (entries without the flag commonly use CP437; for the PPMd archives
// targeted by this package names are plain ASCII, so the raw bytes are used
// unless the UTF-8 flag is set).
func decodeName(b []byte, flags uint16) string {
	if flags&utf8Flag != 0 {
		return string(b)
	}
	return string(b)
}

// dosTimeToUnix converts the DOS date/time fields of a ZIP entry to a Unix
// timestamp. Returns 0 for out-of-range values.
func dosTimeToUnix(t, d uint16) int64 {
	if d == 0 {
		return 0
	}
	year := int(d>>9) + 1980
	month := int((d >> 5) & 0xF)
	day := int(d & 0x1F)
	hour := int(t >> 11)
	min := int((t >> 5) & 0x3F)
	sec := int(t&0x1F) * 2
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return 0
	}
	return unixTime(year, month, day, hour, min, sec)
}

// unixTime is a small helper to avoid importing time for a single
// conversion; it computes the Unix timestamp for a UTC civil time.
func unixTime(year, month, day, hour, min, sec int) int64 {
	// Days since epoch via Howard Hinnant's algorithm.
	days := daysFromCivil(year, month, day)
	return int64(days)*86400 + int64(hour*3600+min*60+sec)
}

func daysFromCivil(y, m, d int) int64 {
	if m <= 2 {
		y--
	}
	era := divFloor(y, 400)
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return int64(era*146097 + doe - 719468)
}

func divFloor(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}
