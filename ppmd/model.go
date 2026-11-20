// Package ppmd implements a decompressor for the PPMd var.I (Ppmd8)
// compression algorithm as used by 7-Zip for ZIP archives (compression
// method 98) and for the "PPMd" method of 7z archives.
//
// The code is a Go port of the public domain reference implementation by
// Igor Pavlov (7-Zip), which is itself based on:
//
//	PPMd var.I (2002): Dmitry Shkarin : Public domain
//	Carryless rangecoder (1999): Dmitry Subbotin : Public domain
//
// Only decompression is implemented.
package ppmd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// SymEnd is returned by Decoder.DecodeSymbol when the end-of-payload
// marker of a stream is reached.
const SymEnd = -1

// Restore methods (Ppmd8_RestoreMethod in the 7-Zip sources).
const (
	RestoreRestart = 0
	RestoreCutOff  = 1
)

const (
	maxOrderConst = 16
	minMemSize    = 1 << 11
	maxMemSize    = 0xFFFFFFFF - 12*3
	maxFreq       = 124
	unitSize      = 12
	periodBits    = 7
	binScale      = 1 << (periodBits + periodBits)
	numIndexes    = 38 // PPMD_N1(4) + PPMD_N2(4) + PPMD_N3(4) + PPMD_N4(26)
	emptyNode     = 0xFFFFFFFF
	maxFreeUnit   = 128
)

// kInitBinEsc is PPMD8_kInitBinEsc from Ppmd8.c.
var kInitBinEsc = [8]uint16{0x3CDD, 0x1F3F, 0x59BF, 0x48F3, 0x64A1, 0x5ABC, 0x6632, 0x6051}

// kExpEscape is PPMD8_kExpEscape from Ppmd8.c.
var kExpEscape = [16]byte{25, 14, 9, 7, 5, 5, 4, 4, 4, 3, 3, 3, 2, 2, 2, 2}

const (
	flagRescaled  = 1 << 2
	flagPrevHigh  = 1 << 4
	glueCountInit = 1 << 13
)

// errCorrupt is returned when the compressed data is malformed.
var errCorrupt = errors.New("ppmd: corrupt data")

// Model is the PPMd var.I model state (CPpmd8 in the C sources).
// All pointers of the original C implementation are represented as uint32
// offsets into the base arena.
type Model struct {
	minContext, maxContext uint32
	foundState             uint32
	orderFall              uint32
	initEsc                uint32
	prevSuccess            uint32
	maxOrder               uint32
	restoreMethod          uint32
	runLength, initRL      int32

	size       uint32
	glueCount  uint32
	alignOfs   uint32
	base       []byte // arena; all offsets are relative to this slice
	loUnit     uint32
	hiUnit     uint32
	text       uint32
	unitsStart uint32

	range_, code, low uint32

	indx2Units [numIndexes + 2]byte
	units2Indx [maxFreeUnit]byte
	freeList   [numIndexes]uint32
	stamps     [numIndexes]uint32

	ns2bsIndx [256]byte
	ns2indx   [260]byte

	see      [24 * 32]seeCtx
	dummySee seeCtx
	binSumm  [25 * 64]uint16
}

// seeCtx mirrors CPpmd_See.
type seeCtx struct {
	summ  uint16
	shift byte
	count byte
}

// u2b converts a number of units to bytes (U2B macro).
func u2b(nu uint32) uint32 { return nu * unitSize }

// Construct initializes the static tables (Ppmd8_Construct).
func (p *Model) Construct() {
	var i, k, m uint32
	for i, k = 0, 0; i < numIndexes; i++ {
		step := (i >> 2) + 1
		if i >= 12 {
			step = 4
		}
		for {
			p.units2Indx[k] = byte(i)
			k++
			step--
			if step == 0 {
				break
			}
		}
		p.indx2Units[i] = byte(k)
	}

	p.ns2bsIndx[0] = 0 << 1
	p.ns2bsIndx[1] = 1 << 1
	for i = 2; i < 11; i++ {
		p.ns2bsIndx[i] = 2 << 1
	}
	for i = 11; i < 256; i++ {
		p.ns2bsIndx[i] = 3 << 1
	}

	for i = 0; i < 5; i++ {
		p.ns2indx[i] = byte(i)
	}
	for m, k = i, 1; i < 260; i++ {
		p.ns2indx[i] = byte(m)
		k--
		if k == 0 {
			m++
			k = m - 4
		}
	}
}

// Alloc allocates the model memory arena (Ppmd8_Alloc).
func (p *Model) Alloc(size uint32) {
	if p.base != nil && p.size == size {
		return
	}
	p.alignOfs = (4 - size) & 3
	p.base = make([]byte, p.alignOfs+size)
	p.size = size
}

// i2u returns the number of units for a free-list index (I2U macro).
func (p *Model) i2u(indx uint32) uint32 { return uint32(p.indx2Units[indx]) }

// u2i returns the free-list index for a number of units (U2I macro).
func (p *Model) u2i(nu uint32) uint32 { return uint32(p.units2Indx[nu-1]) }

// --- arena field accessors -------------------------------------------------

// Context record (12 bytes): NumStats(1) Flags(1) SummFreq(2) Stats(4) Suffix(4).
func ctxSummFreq(b []byte, o uint32) uint16 {
	return binary.LittleEndian.Uint16(b[o+2:])
}
func ctxStats(b []byte, o uint32) uint32 {
	return binary.LittleEndian.Uint32(b[o+4:])
}
func ctxSuffix(b []byte, o uint32) uint32 {
	return binary.LittleEndian.Uint32(b[o+8:])
}
func setCtxSummFreq(b []byte, o uint32, v uint16) {
	binary.LittleEndian.PutUint16(b[o+2:], v)
}
func setCtxStats(b []byte, o uint32, v uint32) {
	binary.LittleEndian.PutUint32(b[o+4:], v)
}
func setCtxSuffix(b []byte, o uint32, v uint32) {
	binary.LittleEndian.PutUint32(b[o+8:], v)
}

// State record (6 bytes): Symbol(1) Freq(1) Successor(4).
func stSuccessor(b []byte, o uint32) uint32 {
	return binary.LittleEndian.Uint32(b[o+2:])
}
func setStFreq(b []byte, o uint32, v byte) { b[o+1] = v }
func setStSuccessor(b []byte, o uint32, v uint32) {
	binary.LittleEndian.PutUint32(b[o+2:], v)
}

// Free-list node record (12 bytes): Stamp(4) Next(4) NU(4).
func nodeStamp(b []byte, o uint32) uint32 {
	return binary.LittleEndian.Uint32(b[o:])
}
func nodeNext(b []byte, o uint32) uint32 {
	return binary.LittleEndian.Uint32(b[o+4:])
}
func nodeNU(b []byte, o uint32) uint32 {
	return binary.LittleEndian.Uint32(b[o+8:])
}
func setNodeStampEmpty(b []byte, o uint32) {
	binary.LittleEndian.PutUint32(b[o:], emptyNode)
}
func setNodeNext(b []byte, o uint32, v uint32) {
	binary.LittleEndian.PutUint32(b[o+4:], v)
}
func setNodeNU(b []byte, o uint32, v uint32) {
	binary.LittleEndian.PutUint32(b[o+8:], v)
}

// copyUnits copies nu 12-byte units (MEM_12_CPY).
func copyUnits(b []byte, dst, src, nu uint32) {
	copy(b[dst:dst+u2b(nu)], b[src:src+u2b(nu)])
}

// --- sub-allocator (Ppmd8.c) ------------------------------------------------

func (p *Model) insertNode(node, indx uint32) {
	b := p.base
	setNodeStampEmpty(b, node)
	setNodeNext(b, node, p.freeList[indx])
	setNodeNU(b, node, p.i2u(indx))
	p.freeList[indx] = node
	p.stamps[indx]++
}

func (p *Model) removeNode(indx uint32) uint32 {
	node := p.freeList[indx]
	p.freeList[indx] = nodeNext(p.base, node)
	p.stamps[indx]--
	return node
}

func (p *Model) splitBlock(ptr, oldIndx, newIndx uint32) {
	nu := p.i2u(oldIndx) - p.i2u(newIndx)
	ptr += u2b(p.i2u(newIndx))
	i := p.u2i(nu)
	if p.i2u(i) != nu {
		// Note: C decrements i here and the main block below is
		// inserted with the decremented index.
		i--
		k := p.i2u(i)
		p.insertNode(ptr+u2b(k), nu-k-1)
	}
	p.insertNode(ptr, i)
}

// glueFreeBlocks is Ppmd8_GlueFreeBlocks.
func (p *Model) glueFreeBlocks() {
	b := p.base
	p.glueCount = glueCountInit
	for i := range p.stamps {
		p.stamps[i] = 0
	}

	// guard node at loUnit
	if p.loUnit != p.hiUnit {
		b[p.loUnit] = 0
		b[p.loUnit+1] = 0
		b[p.loUnit+2] = 0
		b[p.loUnit+3] = 0
	}

	// Build a temporary list of free blocks, coalescing adjacent ones.
	// prevSlot models the C (CPpmd8_Node_Ref *prev) pointer: it either
	// addresses the local head variable or a node's Next field in the arena.
	var head uint32
	prevInArena := false
	var prevSlot uint32

	for i := uint32(0); i < numIndexes; i++ {
		next := p.freeList[i]
		p.freeList[i] = 0
		for next != 0 {
			node := next
			nu := nodeNU(b, node)
			if prevInArena {
				setNodeNext(b, prevSlot, node)
			} else {
				head = node
			}
			next = nodeNext(b, node)
			if nu != 0 {
				prevInArena = true
				prevSlot = node // the node's Next field is the link slot
				for {
					node2 := node + u2b(nu)
					if nodeStamp(b, node2) != emptyNode {
						break
					}
					nu += nodeNU(b, node2)
					setNodeNU(b, node2, 0)
					setNodeNU(b, node, nu)
				}
			}
		}
	}
	if prevInArena {
		setNodeNext(b, prevSlot, 0)
	} else {
		head = 0
	}

	// Fill the free lists again.
	for n := head; n != 0; {
		node := n
		nu := nodeNU(b, node)
		n = nodeNext(b, node)
		if nu == 0 {
			continue
		}
		for ; nu > maxFreeUnit; nu -= maxFreeUnit {
			p.insertNode(node, numIndexes-1)
			node += maxFreeUnit * unitSize
		}
		i := p.u2i(nu)
		if p.i2u(i) != nu {
			// Note: C decrements i here and the main block below is
			// inserted with the decremented index.
			i--
			k := p.i2u(i)
			p.insertNode(node+u2b(k), nu-k-1)
		}
		p.insertNode(node, i)
	}
}

// allocUnitsRare is Ppmd8_AllocUnitsRare. Returns 0 on failure.
func (p *Model) allocUnitsRare(indx uint32) uint32 {
	if p.glueCount == 0 {
		p.glueFreeBlocks()
		if p.freeList[indx] != 0 {
			return p.removeNode(indx)
		}
	}

	i := indx
	for {
		i++
		if i == numIndexes {
			numBytes := u2b(p.i2u(indx))
			us := p.unitsStart
			p.glueCount--
			if us-p.text > numBytes {
				p.unitsStart = us - numBytes
				return p.unitsStart
			}
			return 0
		}
		if p.freeList[i] != 0 {
			break
		}
	}
	block := p.removeNode(i)
	p.splitBlock(block, i, indx)
	return block
}

// allocUnits is Ppmd8_AllocUnits. Returns 0 on failure.
func (p *Model) allocUnits(indx uint32) uint32 {
	if p.freeList[indx] != 0 {
		return p.removeNode(indx)
	}
	numBytes := u2b(p.i2u(indx))
	if p.hiUnit-p.loUnit >= numBytes {
		lo := p.loUnit
		p.loUnit = lo + numBytes
		return lo
	}
	return p.allocUnitsRare(indx)
}

// shrinkUnits is ShrinkUnits.
func (p *Model) shrinkUnits(oldPtr, oldNU, newNU uint32) uint32 {
	i0 := p.u2i(oldNU)
	i1 := p.u2i(newNU)
	if i0 == i1 {
		return oldPtr
	}
	if p.freeList[i1] != 0 {
		ptr := p.removeNode(i1)
		copyUnits(p.base, ptr, oldPtr, newNU)
		p.insertNode(oldPtr, i0)
		return ptr
	}
	p.splitBlock(oldPtr, i0, i1)
	return oldPtr
}

func (p *Model) freeUnits(ptr, nu uint32) {
	p.insertNode(ptr, p.u2i(nu))
}

func (p *Model) specialFreeUnit(ptr uint32) {
	if ptr != p.unitsStart {
		p.insertNode(ptr, 0)
	} else {
		p.unitsStart += unitSize
	}
}

// expandTextArea is ExpandTextArea.
func (p *Model) expandTextArea() {
	b := p.base
	var count [numIndexes]uint32

	if p.loUnit != p.hiUnit {
		b[p.loUnit] = 0
		b[p.loUnit+1] = 0
		b[p.loUnit+2] = 0
		b[p.loUnit+3] = 0
	}

	node := p.unitsStart
	for nodeStamp(b, node) == emptyNode {
		nu := nodeNU(b, node)
		b[node] = 0
		b[node+1] = 0
		b[node+2] = 0
		b[node+3] = 0
		count[p.u2i(nu)]++
		node += u2b(nu)
	}
	p.unitsStart = node

	for i := uint32(0); i < numIndexes; i++ {
		cnt := count[i]
		if cnt == 0 {
			continue
		}
		p.stamps[i] -= cnt
		// Walk the free list, unlinking nodes whose Stamp became 0
		// (they were merged into the text area).
		prevInHead := true
		var prevSlot uint32
		for n := p.freeList[i]; ; {
			node := n
			n = nodeNext(b, node)
			if nodeStamp(b, node) != 0 {
				prevInHead = false
				prevSlot = node // the node's Next field is the link slot
				continue
			}
			// unlink this node: *prev = n
			if prevInHead {
				p.freeList[i] = n
			} else {
				setNodeNext(b, prevSlot, n)
			}
			cnt--
			if cnt == 0 {
				break
			}
		}
	}
}

// restartModel is Ppmd8_RestartModel.
func (p *Model) restartModel() {
	b := p.base
	for i := range p.freeList {
		p.freeList[i] = 0
	}
	for i := range p.stamps {
		p.stamps[i] = 0
	}
	p.text = p.alignOfs
	p.hiUnit = p.text + p.size
	p.loUnit = p.hiUnit - p.size/8/unitSize*7*unitSize
	p.unitsStart = p.loUnit
	p.glueCount = 0

	p.orderFall = p.maxOrder
	maxOrder := int32(p.maxOrder)
	if maxOrder > 12 {
		maxOrder = 12
	}
	p.runLength = -maxOrder - 1
	p.initRL = p.runLength
	p.prevSuccess = 0

	// order-0 context at the top of the arena; 256 states from loUnit
	mc := p.hiUnit - unitSize
	p.hiUnit = mc
	s := p.loUnit
	p.loUnit += u2b(256 / 2)
	p.maxContext = mc
	p.minContext = mc
	p.foundState = s
	b[mc+1] = 0 // Flags
	b[mc] = 256 - 1
	setCtxSummFreq(b, mc, 256+1)
	setCtxStats(b, mc, s)
	setCtxSuffix(b, mc, 0)
	for i := uint32(0); i < 256; i++ {
		o := s + i*6
		b[o] = byte(i) // Symbol
		b[o+1] = 1     // Freq
		setStSuccessor(b, o, 0)
	}

	// BinSumm
	var i, m, k uint32
	for i, m = 0, 0; m < 25; m++ {
		for p.ns2indx[i] == byte(m) {
			i++
		}
		for k = 0; k < 8; k++ {
			val := uint16(binScale - uint32(kInitBinEsc[k])/(i+1))
			for r := uint32(0); r < 64; r += 8 {
				p.binSumm[m*64+k+r] = val
			}
		}
	}

	// See
	for i, m = 0, 0; m < 24; m++ {
		for p.ns2indx[i+3] == byte(m+3) {
			i++
		}
		summ := uint16((2*i + 5) << (periodBits - 4))
		for k = 0; k < 32; k++ {
			sc := &p.see[m*32+k]
			sc.summ = summ
			sc.shift = periodBits - 4
			sc.count = 7
		}
	}

	p.dummySee.summ = 0
	p.dummySee.shift = periodBits
	p.dummySee.count = 64
}

// Init initializes the model (Ppmd8_Init).
func (p *Model) Init(maxOrd, restoreMethod uint32) error {
	if maxOrd < 2 || maxOrd > maxOrderConst {
		return fmt.Errorf("ppmd: order %d out of range [2, %d]", maxOrd, maxOrderConst)
	}
	p.maxOrder = maxOrd
	p.restoreMethod = restoreMethod
	p.restartModel()
	return nil
}

// --- decoder-facing API ------------------------------------------------------

// byteReader is a buffered byte source with sticky error.
type byteReader struct {
	r      io.Reader
	buf    []byte
	pos    int
	err    error // underlying read error (sticky)
	failed bool  // true once a byte was requested but none was available
}

func newByteReader(r io.Reader) *byteReader {
	return &byteReader{r: r, buf: make([]byte, 0, 64<<10)}
}

func (b *byteReader) refill() {
	if b.err != nil {
		return
	}
	n, err := io.ReadFull(b.r, b.buf[:cap(b.buf)])
	b.buf = b.buf[:n]
	b.pos = 0
	if err != nil {
		b.err = err
	}
}

// readByte returns the next byte; when the input is exhausted it returns 0
// and sets the failed flag.
func (b *byteReader) readByte() byte {
	if b.pos >= len(b.buf) {
		b.refill()
		if b.pos >= len(b.buf) {
			b.failed = true
			return 0
		}
	}
	c := b.buf[b.pos]
	b.pos++
	return c
}

// Decoder is a PPMd var.I stream decoder.
type Decoder struct {
	m  Model
	br *byteReader
}

// NewDecoder creates a PPMd decoder reading the compressed stream from r.
// order must be in [2, 16], memorySize is the model arena size in bytes
// (at least 2 KiB), restore is one of the Restore constants. The range
// coder initialization bytes are consumed before NewDecoder returns.
func NewDecoder(r io.Reader, order int, memorySize uint32, restore int) (*Decoder, error) {
	if order < 2 || order > maxOrderConst {
		return nil, fmt.Errorf("ppmd: order %d out of range [2, %d]", order, maxOrderConst)
	}
	if memorySize < minMemSize || memorySize > maxMemSize {
		return nil, fmt.Errorf("ppmd: memory size %d out of range [%d, %d]", memorySize, minMemSize, maxMemSize)
	}
	if restore != RestoreRestart && restore != RestoreCutOff {
		return nil, fmt.Errorf("ppmd: unsupported restore method %d", restore)
	}
	d := &Decoder{br: newByteReader(r)}
	d.m.Construct()
	d.m.Alloc(memorySize)
	if !d.initRangeDec() {
		return nil, errCorrupt
	}
	if err := d.m.Init(uint32(order), uint32(restore)); err != nil {
		return nil, err
	}
	return d, nil
}

// initRangeDec is Ppmd8_Init_RangeDec.
func (d *Decoder) initRangeDec() bool {
	p := &d.m
	p.code = 0
	p.range_ = 0xFFFFFFFF
	p.low = 0
	for i := 0; i < 4; i++ {
		p.code = (p.code << 8) | uint32(d.br.readByte())
	}
	return p.code < 0xFFFFFFFF
}

// DecodeSymbol decodes a single symbol: a byte value [0, 255], or SymEnd
// when the end-of-payload marker is reached.
func (d *Decoder) DecodeSymbol() (int, error) {
	sym := d.m.decodeSymbol(d.br)
	if sym == -2 {
		if d.br.failed {
			return 0, io.ErrUnexpectedEOF
		}
		return 0, errCorrupt
	}
	return sym, nil
}

// Read implements io.Reader over the decoded byte stream. It returns io.EOF
// after the end-of-payload marker (or when the compressed input is fully
// consumed).
func (d *Decoder) Read(buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		sym := d.m.decodeSymbol(d.br)
		if sym == -2 {
			if d.br.failed {
				return n, io.ErrUnexpectedEOF
			}
			return n, errCorrupt
		}
		if sym == -1 {
			if n > 0 {
				return n, nil
			}
			return 0, io.EOF
		}
		buf[n] = byte(sym)
		n++
	}
	return n, nil
}
