package ppmd

// This file is a Go port of Ppmd8Dec.c (7-Zip, public domain): the
// carryless range decoder and Ppmd8_DecodeSymbol.

const (
	kTop = uint32(1) << 24
	kBot = uint32(1) << 15
)

// rcNorm is the RC_NORM macro.
func (p *Model) rcNorm(br *byteReader) {
	for {
		if (p.low ^ (p.low + p.range_)) < kTop {
			// fall through to normalization below
		} else if p.range_ < kBot {
			p.range_ = (0 - p.low) & (kBot - 1)
		} else {
			break
		}
		p.code = p.code<<8 | uint32(br.readByte())
		p.range_ <<= 8
		p.low <<= 8
	}
}

// rcDecode is Ppmd8_RD_Decode.
func (p *Model) rcDecode(start, size uint32) {
	start *= p.range_
	p.low += start
	p.code -= start
	p.range_ *= size
}

// rcDecodeFinal is RC_DecodeFinal (decode + remote normalization).
func (p *Model) rcDecodeFinal(br *byteReader, start, size uint32) {
	p.rcDecode(start, size)
	p.rcNorm(br)
}

// rcGetThreshold is RC_GetThreshold.
func (p *Model) rcGetThreshold(total uint32) uint32 {
	p.range_ /= total
	return p.code / p.range_
}

// getMean is PPMD_GET_MEAN.
func getMean(summ uint32) uint32 { return (summ + (1 << (periodBits - 2))) >> periodBits }

// updateProb1 is PPMD_UPDATE_PROB_1.
func updateProb1(prob uint32) uint32 { return prob - getMean(prob) }

// allBitsMask is PPMD_SetAllBitsIn256Bytes.
var allBitsMask = [256]byte{
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
}

// setAllBits sets all bytes of the mask to 0xFF (PPMD_SetAllBitsIn256Bytes).
func setAllBits(mask *[256]byte) {
	*mask = allBitsMask
}

// decodeSymbol is Ppmd8_DecodeSymbol. It returns a symbol [0, 255], or
// SymEnd (-1) at the end-of-payload marker, or -2 on corrupt data.
func (p *Model) decodeSymbol(br *byteReader) int {
	b := p.base
	var charMask [256]byte

	if b[p.minContext] != 0 { // NumStats != 0
		stats := ctxStats(b, p.minContext)
		summFreq := uint32(ctxSummFreq(b, p.minContext))
		if summFreq > p.range_ { // PPMD8_CORRECT_SUM_RANGE
			summFreq = p.range_
		}

		count := p.rcGetThreshold(summFreq)
		hiCnt := count
		s := stats
		count -= uint32(b[s+1])
		if int32(count) < 0 {
			freq := uint32(b[s+1])
			p.rcDecodeFinal(br, 0, freq)
			p.foundState = s
			sym := b[s]
			p.update1_0()
			return int(sym)
		}

		p.prevSuccess = 0
		i := uint32(b[p.minContext])
		for {
			s += 6
			count -= uint32(b[s+1])
			if int32(count) < 0 {
				freq := uint32(b[s+1])
				p.rcDecodeFinal(br, (hiCnt-count)-freq, freq)
				p.foundState = s
				sym := b[s]
				p.update1()
				return int(sym)
			}
			i--
			if i == 0 {
				break
			}
		}

		if hiCnt >= summFreq {
			return -2
		}
		hiCnt -= count
		p.rcDecode(hiCnt, summFreq-hiCnt)

		setAllBits(&charMask)
		charMask[b[s]] = 0
		s2 := stats
		for {
			sym0 := b[s2]
			sym1 := b[s2+6]
			s2 += 12
			charMask[sym0] = 0
			charMask[sym1] = 0
			if s2 >= s {
				break
			}
		}
	} else {
		s := p.minContext + 2 // ONE_STATE
		probIdx := p.binSummIndex()
		pr := uint32(p.binSumm[probIdx])
		size0 := (p.range_ >> 14) * pr
		pr = updateProb1(pr)

		if p.code < size0 {
			p.binSumm[probIdx] = uint16(pr + (1 << periodBits))
			p.range_ = size0
			p.rcNorm(br)

			c := stSuccessor(b, s)
			sym := b[s]
			p.foundState = s
			p.prevSuccess = 1
			p.runLength++
			freq := uint32(b[s+1])
			if freq < 196 {
				b[s+1] = byte(freq + 1)
			}
			if p.orderFall == 0 && c >= p.unitsStart {
				p.maxContext = c
				p.minContext = c
			} else {
				p.updateModel()
			}
			return int(sym)
		}

		p.binSumm[probIdx] = uint16(pr)
		p.initEsc = uint32(kExpEscape[pr>>10])

		p.low += size0
		p.code -= size0
		p.range_ = (p.range_ &^ (binScale - 1)) - size0
		// RC_NORM_LOCAL is a no-op here by design.

		setAllBits(&charMask)
		charMask[b[s]] = 0
		p.prevSuccess = 0
	}

	for {
		if br.failed {
			return -2
		}
		p.rcNorm(br)
		mc := p.minContext
		numMasked := uint32(b[mc])

		for {
			p.orderFall++
			suf := ctxSuffix(b, mc)
			if suf == 0 {
				return -1 // PPMD8_SYM_END
			}
			mc = suf
			if uint32(b[mc]) != numMasked {
				break
			}
		}

		s := ctxStats(b, mc)
		num := uint32(b[mc]) + 1
		num2 := num / 2
		num &= 1
		var hiCnt uint32
		if num == 1 {
			hiCnt = uint32(b[s+1]) & uint32(charMask[b[s]])
			s += 6
		}
		p.minContext = mc

		for {
			sym0 := b[s]
			sym1 := b[s+6]
			f0 := uint32(b[s+1])
			f1 := uint32(b[s+7])
			s += 12
			hiCnt += f0 & uint32(charMask[sym0])
			hiCnt += f1 & uint32(charMask[sym1])
			num2--
			if num2 == 0 {
				break
			}
		}

		var escFreq uint32
		see := p.makeEscFreq(numMasked, &escFreq)
		freqSum := escFreq + hiCnt
		freqSum2 := freqSum
		if freqSum2 > p.range_ { // PPMD8_CORRECT_SUM_RANGE
			freqSum2 = p.range_
		}

		count := p.rcGetThreshold(freqSum2)

		if count < hiCnt {
			s = ctxStats(b, p.minContext)
			hiCnt = count
			for {
				count -= uint32(b[s+1]) & uint32(charMask[b[s]])
				s += 6
				if int32(count) < 0 {
					break
				}
			}
			s -= 6
			freq := uint32(b[s+1])
			p.rcDecodeFinal(br, (hiCnt-count)-freq, freq)

			// Ppmd_See_UPDATE; the new see->Summ can overflow 16 bits in
			// rare cases, matching the C behavior.
			if see.shift < periodBits {
				see.count--
				if see.count == 0 {
					see.summ <<= 1
					see.count = 3 << see.shift
					see.shift++
				}
			}
			p.foundState = s
			sym := b[s]
			p.update2()
			return int(sym)
		}

		if count >= freqSum2 {
			return -2
		}

		p.rcDecode(hiCnt, freqSum2-hiCnt)

		// Increase see->Summ by the sum of freqs of all non-masked symbols.
		see.summ = uint16(uint32(see.summ) + freqSum)

		s = ctxStats(b, p.minContext)
		s2 := s + (uint32(b[p.minContext])+1)*6
		for {
			charMask[b[s]] = 0
			s += 6
			if s == s2 {
				break
			}
		}
	}
}
