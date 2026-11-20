package ppmd

// This file is a Go port of the model update machinery from Ppmd8.c
// (7-Zip, public domain). See model.go for the porting notes.

// hiBitsFlag3/4 are PPMD8_HiBitsFlag_3/4.
func hiBitsFlag3(sym byte) byte { return byte((uint32(sym) + 0xC0) >> 5 & 8) }
func hiBitsFlag4(sym byte) byte { return byte((uint32(sym) + 0xC0) >> 4 & 16) }

// hiBitsPrepare is HiBits_Prepare.
func hiBitsPrepare(sym byte) uint32 { return uint32(sym) + 0xC0 }

// hiBitsConvert3 is HiBits_Convert_3.
func hiBitsConvert3(flags uint32) byte { return byte(flags >> 5 & 8) }

// swapStates is SWAP_STATES for two 6-byte state records.
func swapStates(b []byte, t1, t2 uint32) {
	for i := uint32(0); i < 6; i++ {
		b[t1+i], b[t2+i] = b[t2+i], b[t1+i]
	}
}

// copyState copies a 6-byte state record.
func copyState(b []byte, dst, src uint32) {
	tmp0, tmp1, tmp2, tmp3, tmp4, tmp5 := b[src], b[src+1], b[src+2], b[src+3], b[src+4], b[src+5]
	b[dst], b[dst+1], b[dst+2], b[dst+3], b[dst+4], b[dst+5] = tmp0, tmp1, tmp2, tmp3, tmp4, tmp5
}

// binSummIndex computes the flat index into p.binSumm (Ppmd8_GetBinSumm).
// The second return value is false when the model state is corrupt (an
// out-of-arena pointer or an out-of-range index); the C original reads
// out of bounds in that case.
func (p *Model) binSummIndex() (uint32, bool) {
	b := p.base
	mc := p.minContext
	if !p.inArena(mc, 4) {
		return 0, false
	}
	oneStateFreq := uint32(b[mc+3]) // ONE_STATE(mc)->Freq
	if oneStateFreq == 0 {
		return 0, false
	}
	suf := ctxSuffix(b, mc)
	if !p.inArena(suf, 1) {
		return 0, false
	}
	suffixNumStats := uint32(b[suf])
	var runLenBit uint32
	if p.runLength>>26&0x20 != 0 {
		runLenBit = 0x20
	}
	idx := uint32(p.ns2indx[oneStateFreq-1])*64 +
		p.prevSuccess + runLenBit +
		uint32(p.ns2bsIndx[suffixNumStats]) +
		uint32(b[mc+1]) // Flags
	if idx >= uint32(len(p.binSumm)) {
		return 0, false
	}
	return idx, true
}

// getUsedMemory is GetUsedMemory.
func (p *Model) getUsedMemory() uint32 {
	var v uint32
	for i := uint32(0); i < numIndexes; i++ {
		v += p.stamps[i] * p.i2u(i)
	}
	return p.size - (p.hiUnit - p.loUnit) - (p.unitsStart - p.text) - u2b(v)
}

// refresh is Refresh.
func (p *Model) refresh(ctx uint32, oldNU, scale uint32) {
	b := p.base
	i := uint32(b[ctx])
	stats := p.shrinkUnits(ctxStats(b, ctx), oldNU, (i+2)>>1)
	setCtxStats(b, ctx, stats)
	if ctxSummFreq(b, ctx) >= 1<<15 {
		scale |= 1
	}
	flags := hiBitsPrepare(b[stats])
	freq := uint32(b[stats+1])
	escFreq := uint32(ctxSummFreq(b, ctx)) - freq
	freq = (freq + scale) >> scale
	sumFreq := freq
	b[stats+1] = byte(freq)
	for {
		stats += 6
		freq = uint32(b[stats+1])
		escFreq -= freq
		freq = (freq + scale) >> scale
		sumFreq += freq
		b[stats+1] = byte(freq)
		flags |= hiBitsPrepare(b[stats])
		i--
		if i == 0 {
			break
		}
	}
	setCtxSummFreq(b, ctx, uint16(sumFreq+((escFreq+scale)>>scale)))
	b[ctx+1] = (b[ctx+1] & (flagPrevHigh + flagRescaled*byte(scale))) + hiBitsConvert3(flags)
}

// cutOff is CutOff.
func (p *Model) cutOff(ctx uint32, order uint32) uint32 {
	b := p.base
	ns := int32(b[ctx])
	if ns == 0 {
		s := ctx + 2 // ONE_STATE(ctx)
		successor := stSuccessor(b, s)
		if successor >= p.unitsStart {
			if order < p.maxOrder {
				successor = p.cutOff(successor, order+1)
			} else {
				successor = 0
			}
			setStSuccessor(b, s, successor)
			if successor != 0 || order <= 9 { // O_BOUND
				return ctx
			}
		}
		p.specialFreeUnit(ctx)
		return 0
	}

	nu := uint32(ns+2) >> 1
	stats := ctxStats(b, ctx)
	{
		indx := p.u2i(nu)
		if stats-p.unitsStart <= 1<<14 && ctxStats(b, ctx) <= p.freeList[indx] {
			ptr := p.removeNode(indx)
			setCtxStats(b, ctx, ptr)
			copyUnits(b, ptr, stats, nu)
			if stats != p.unitsStart {
				p.insertNode(stats, indx)
			} else {
				p.unitsStart += u2b(p.i2u(indx))
			}
			stats = ptr
		}
	}

	{
		s := stats + uint32(ns)*6
		for {
			successor := stSuccessor(b, s)
			if successor < p.unitsStart {
				ns--
				s2 := stats + uint32(ns)*6
				if order != 0 {
					if s != s2 {
						copyState(b, s, s2)
					}
				} else {
					swapStates(b, s, s2)
					setStSuccessor(b, s2, 0)
				}
			} else {
				if order < p.maxOrder {
					setStSuccessor(b, s, p.cutOff(successor, order+1))
				} else {
					setStSuccessor(b, s, 0)
				}
			}
			if s == stats {
				break
			}
			s -= 6
		}
	}

	if int32(ns) != int32(b[ctx]) && order != 0 {
		if ns < 0 {
			p.freeUnits(stats, nu)
			p.specialFreeUnit(ctx)
			return 0
		}
		b[ctx] = byte(ns)
		if ns == 0 {
			sym := b[stats]
			b[ctx+1] = (b[ctx+1] & flagPrevHigh) + hiBitsFlag3(sym)
			b[ctx+2] = sym // State2.Symbol
			b[ctx+3] = byte((uint32(b[stats+1]) + 11) >> 3)
			setStSuccessor(b, ctx+2, stSuccessor(b, stats))
			p.freeUnits(stats, nu)
		} else {
			var scale uint32
			if uint32(ctxSummFreq(b, ctx)) > 16*uint32(ns) {
				scale = 1
			}
			p.refresh(ctx, nu, scale)
		}
	}
	return ctx
}

// restoreModel is RestoreModel (with FREEZE support disabled, matching the
// 7-Zip default build).
func (p *Model) restoreModel(ctxError uint32) {
	b := p.base
	p.text = p.alignOfs

	// Remove the last symbol from each of the contexts
	// [MaxContext, ctxError).
	c := p.maxContext
	for ; c != ctxError; c = ctxSuffix(b, c) {
		b[c]-- // --NumStats
		if b[c] == 0 {
			s := ctxStats(b, c)
			b[c+1] = (b[c+1] & flagPrevHigh) + hiBitsFlag3(b[s])
			b[c+2] = b[s]
			b[c+3] = byte((uint32(b[s+1]) + 11) >> 3)
			setStSuccessor(b, c+2, stSuccessor(b, s))
			p.specialFreeUnit(s)
		} else {
			p.refresh(c, (uint32(b[c])+3)>>1, 0)
		}
	}

	// Increase escape freq for contexts [ctxError, MinContext).
	for ; c != p.minContext; c = ctxSuffix(b, c) {
		if b[c] == 0 {
			b[c+3] = byte((uint32(b[c+3]) + 1) >> 1) // ONE_STATE(c)->Freq
		} else {
			sf := uint32(ctxSummFreq(b, c)) + 4
			setCtxSummFreq(b, c, uint16(sf))
			if sf > 128+4*uint32(b[c]) {
				p.refresh(c, (uint32(b[c])+2)>>1, 1)
			}
		}
	}

	if p.restoreMethod == RestoreRestart || p.getUsedMemory() < p.size>>1 {
		p.restartModel()
	} else {
		for ctxSuffix(b, p.maxContext) != 0 {
			p.maxContext = ctxSuffix(b, p.maxContext)
		}
		for {
			p.cutOff(p.maxContext, 0)
			p.expandTextArea()
			if p.getUsedMemory() <= 3*(p.size>>2) {
				break
			}
		}
		p.glueCount = 0
		p.orderFall = p.maxOrder
	}
	p.minContext = p.maxContext
}

// inArena reports whether the byte range [o, o+n) lies inside the arena.
func (p *Model) inArena(o, n uint32) bool {
	l := uint32(len(p.base))
	return o <= l && n <= l-o
}

// findState scans the state list of the context c for the symbol sym.
// The scan is unbounded in the C original (a valid stream guarantees the
// symbol is present); on corrupt input it can run past the arena, so it is
// bounded by the context's declared state count. Returns 0 if not found.
// (uint32 conversions matter: byte(255)+1 wraps to 0.)
func (p *Model) findState(c uint32, sym byte) uint32 {
	b := p.base
	s := ctxStats(b, c)
	n := (uint32(b[c]) + 1) * 6 // NumStats+1 states, 6 bytes each
	if !p.inArena(s, n) {
		return 0
	}
	end := s + n
	for s < end && b[s] != sym {
		s += 6
	}
	if s >= end {
		return 0
	}
	return s
}

// createSuccessors is Ppmd8_CreateSuccessors. Returns 0 on allocation
// failure or corrupt state.
func (p *Model) createSuccessors(skip bool, s1 uint32, c uint32) uint32 {
	b := p.base
	upBranch := stSuccessor(b, p.foundState)
	var ps [maxOrderConst + 1]uint32
	numPs := 0

	if !skip {
		ps[numPs] = p.foundState
		numPs++
	}

	for ctxSuffix(b, c) != 0 {
		var s uint32
		c = ctxSuffix(b, c)
		if !p.inArena(c, unitSize) {
			return 0
		}
		if numPs >= len(ps) {
			// Corrupt suffix chain: valid models stay within maxOrder.
			return 0
		}
		if s1 != 0 {
			s = s1
			s1 = 0
		} else if b[c] != 0 { // NumStats != 0
			sym := b[p.foundState]
			s = p.findState(c, sym)
			if s == 0 {
				return 0
			}
			if b[s+1] < maxFreq-9 {
				b[s+1]++
				setCtxSummFreq(b, c, ctxSummFreq(b, c)+1)
			}
		} else {
			temp := uint32(0)
			suf := ctxSuffix(b, c)
			if !p.inArena(suf, 1) {
				return 0
			}
			if b[suf] == 0 {
				temp = 1
			}
			s = c + 2 // ONE_STATE(c)
			if temp&boolToUint32(b[s+1] < 24) != 0 {
				b[s+1]++
			}
		}
		successor := stSuccessor(b, s)
		if successor != upBranch {
			c = successor
			if !p.inArena(c, unitSize) {
				return 0
			}
			if numPs == 0 {
				return c
			}
			break
		}
		ps[numPs] = s
		numPs++
	}

	if !p.inArena(upBranch, 1) || !p.inArena(c, unitSize) {
		return 0
	}
	newSym := b[upBranch]
	upBranch++
	flags := hiBitsFlag4(b[p.foundState]) + hiBitsFlag3(newSym)

	var newFreq byte
	if b[c] == 0 {
		newFreq = b[c+3] // State2.Freq
	} else {
		s := p.findState(c, newSym)
		if s == 0 {
			return 0
		}
		cf := uint32(b[s+1]) - 1
		s0 := uint32(ctxSummFreq(b, c)) - uint32(b[c]) - cf
		if s0 == 0 {
			// Corrupt state guard (division below); unreachable on valid
			// streams.
			return 0
		}
		// max(newFreq) = (s->Freq - 1), when (s0 == 1)
		if 2*cf <= s0 {
			var v uint32
			if 5*cf > s0 {
				v = 1
			}
			newFreq = byte(1 + v)
		} else {
			newFreq = byte(1 + (cf+2*s0-3)/s0)
		}
	}

	if numPs <= 0 {
		// Only reachable on corrupt state (C's do-while would underflow
		// the counter and write out of bounds).
		return 0
	}
	for {
		// AllocContext(p)
		var c1 uint32
		if p.hiUnit != p.loUnit {
			p.hiUnit -= unitSize
			c1 = p.hiUnit
		} else if p.freeList[0] != 0 {
			c1 = p.removeNode(0)
		} else {
			c1 = p.allocUnitsRare(0)
			if c1 == 0 {
				return 0
			}
		}
		b[c1+1] = flags
		b[c1] = 0 // NumStats
		b[c1+2] = newSym
		b[c1+3] = newFreq
		setStSuccessor(b, c1+2, upBranch)
		setCtxSuffix(b, c1, c)
		numPs--
		setStSuccessor(b, ps[numPs], c1)
		c = c1
		if numPs == 0 {
			break
		}
	}
	return c
}

func boolToUint32(v bool) uint32 {
	if v {
		return 1
	}
	return 0
}

// reduceOrder is ReduceOrder (with FREEZE support disabled).
func (p *Model) reduceOrder(s1 uint32, c uint32) uint32 {
	b := p.base
	var s uint32
	c1 := c
	upBranch := p.text

	setStSuccessor(b, p.foundState, upBranch)
	p.orderFall++

	for {
		if s1 != 0 {
			c = ctxSuffix(b, c)
			s = s1
			s1 = 0
		} else {
			if ctxSuffix(b, c) == 0 {
				return c
			}
			c = ctxSuffix(b, c)
			if !p.inArena(c, unitSize) {
				return 0
			}
			if b[c] != 0 { // NumStats
				s = p.findState(c, b[p.foundState])
				if s == 0 {
					return 0
				}
				if b[s+1] < maxFreq-9 {
					b[s+1] += 2
					setCtxSummFreq(b, c, ctxSummFreq(b, c)+2)
				}
			} else {
				s = c + 2 // ONE_STATE
				if b[s+1] < 32 {
					b[s+1]++
				}
			}
		}
		if stSuccessor(b, s) != 0 {
			break
		}
		setStSuccessor(b, s, upBranch)
		p.orderFall++
	}

	if stSuccessor(b, s) <= upBranch {
		s2 := p.foundState
		p.foundState = s
		successor := p.createSuccessors(false, 0, c)
		if successor == 0 {
			setStSuccessor(b, s, 0)
		} else {
			setStSuccessor(b, s, successor)
		}
		p.foundState = s2
	}

	successor := stSuccessor(b, s)
	if p.orderFall == 1 && c1 == p.maxContext {
		setStSuccessor(b, p.foundState, successor)
		p.text--
	}
	if successor == 0 {
		return 0
	}
	return successor
}

// updateModel is Ppmd8_UpdateModel.
func (p *Model) updateModel() {
	b := p.base
	fFreq := uint32(b[p.foundState+1])
	fSymbol := b[p.foundState]
	var c uint32

	var s uint32
	if b[p.foundState+1] < maxFreq/4 && ctxSuffix(b, p.minContext) != 0 {
		// Update freqs in the suffix context.
		c = ctxSuffix(b, p.minContext)
		if !p.inArena(c, unitSize) {
			return
		}
		if b[c] == 0 {
			s = c + 2
			if b[s+1] < 32 {
				b[s+1]++
			}
		} else {
			sym := b[p.foundState]
			stats := ctxStats(b, c)
			s = p.findState(c, sym)
			if s == 0 {
				return
			}
			// Sort by freq: swap towards the front while the predecessor
			// has an equal or higher freq (matches the C scan-then-swap).
			if s != stats && b[s+1] >= b[s-5] {
				swapStates(b, s, s-6)
				s -= 6
			}
			if b[s+1] < maxFreq-9 {
				b[s+1] += 2
				setCtxSummFreq(b, c, ctxSummFreq(b, c)+2)
			}
		}
	}

	c = p.maxContext
	minSuccessor := stSuccessor(b, p.foundState)

	if p.orderFall == 0 && minSuccessor != 0 {
		cs := p.createSuccessors(true, s, p.minContext)
		if cs == 0 {
			setStSuccessor(b, p.foundState, 0)
			p.restoreModel(c)
			return
		}
		setStSuccessor(b, p.foundState, cs)
		p.minContext = cs
		p.maxContext = cs
		return
	}

	var maxSuccessor uint32
	{
		text := p.text
		b[text] = fSymbol
		text++
		p.text = text
		if text >= p.unitsStart {
			p.restoreModel(c)
			return
		}
		maxSuccessor = text
	}

	if minSuccessor == 0 {
		cs := p.reduceOrder(s, p.minContext)
		if cs == 0 {
			p.restoreModel(c)
			return
		}
		minSuccessor = cs
	} else if minSuccessor < p.unitsStart {
		cs := p.createSuccessors(false, s, p.minContext)
		if cs == 0 {
			p.restoreModel(c)
			return
		}
		minSuccessor = cs
	}

	p.orderFall--
	if p.orderFall == 0 {
		maxSuccessor = minSuccessor
		if p.maxContext != p.minContext {
			p.text--
		}
	}

	flag := hiBitsFlag3(fSymbol)
	ns := uint32(b[p.minContext])
	s0 := uint32(ctxSummFreq(b, p.minContext)) - ns - fFreq

	for ; c != p.minContext; c = ctxSuffix(b, c) {
		var sum uint32
		ns1 := uint32(b[c])
		if ns1 != 0 {
			if ns1&1 != 0 {
				// Expand for one UNIT.
				oldNU := (ns1 + 1) >> 1
				i := p.u2i(oldNU)
				if i != p.u2i(oldNU+1) {
					ptr := p.allocUnits(i + 1)
					if ptr == 0 {
						p.restoreModel(c)
						return
					}
					oldPtr := ctxStats(b, c)
					copyUnits(b, ptr, oldPtr, oldNU)
					p.insertNode(oldPtr, i)
					setCtxStats(b, c, ptr)
				}
			}
			sum = uint32(ctxSummFreq(b, c))
			// Max increase of escape freq is 1 here; an average
			// increase is 1/3 per symbol.
			if 3*ns1+1 < ns {
				sum++
			}
		} else {
			s = p.allocUnits(0)
			if s == 0 {
				p.restoreModel(c)
				return
			}
			freq := uint32(b[c+3]) // State2.Freq
			b[s] = b[c+2]          // Symbol
			setStSuccessor(b, s, stSuccessor(b, c+2))
			setCtxStats(b, c, s)
			if freq < maxFreq/4-1 {
				freq <<= 1
			} else {
				freq = maxFreq - 4
			}
			b[s+1] = byte(freq)
			sum = freq + p.initEsc
			if ns > 2 {
				sum++
			}
		}

		s = ctxStats(b, c) + (ns1+1)*6
		cf := 2 * (sum + 6) * fFreq
		sf := s0 + sum
		b[s] = fSymbol
		b[c] = byte(ns1 + 1)
		setStSuccessor(b, s, maxSuccessor)
		b[c+1] |= flag
		if cf < 6*sf {
			cf = 1 + boolToUint32(cf > sf) + boolToUint32(cf >= 4*sf)
			sum += 4
			// Adds (1, 2, 3) to the escape freq.
		} else {
			cf = 4 + boolToUint32(cf > 9*sf) + boolToUint32(cf > 12*sf) + boolToUint32(cf > 15*sf)
			sum += cf
		}
		setCtxSummFreq(b, c, uint16(sum))
		b[s+1] = byte(cf)
	}
	p.maxContext = minSuccessor
	p.minContext = minSuccessor
}

// rescale is Ppmd8_Rescale.
func (p *Model) rescale() {
	b := p.base
	stats := ctxStats(b, p.minContext)
	s := p.foundState

	// Sort the list by freq: move FoundState to the front.
	if s != stats {
		var tmp [6]byte
		copy(tmp[:], b[s:s+6])
		for {
			copyState(b, s, s-6)
			s -= 6
			if s == stats {
				break
			}
		}
		copy(b[s:s+6], tmp[:])
	}

	sumFreq := uint32(b[s+1])
	escFreq := uint32(ctxSummFreq(b, p.minContext)) - sumFreq

	adder := uint32(0)
	if p.orderFall != 0 {
		adder = 1
	}

	sumFreq = (sumFreq + 4 + adder) >> 1
	i := uint32(b[p.minContext])
	b[s+1] = byte(sumFreq)

	for {
		s += 6
		freq := uint32(b[s+1])
		escFreq -= freq
		freq = (freq + adder) >> 1
		sumFreq += freq
		b[s+1] = byte(freq)
		if freq > uint32(b[s-5]) {
			var tmp [6]byte
			copy(tmp[:], b[s:s+6])
			s1 := s
			for {
				copyState(b, s1, s1-6)
				s1 -= 6
				if s1 == stats || freq <= uint32(b[s1-5]) {
					break
				}
			}
			copy(b[s1:s1+6], tmp[:])
		}
		i--
		if i == 0 {
			break
		}
	}

	if b[s+1] == 0 {
		// Remove all states with Freq == 0.
		i = 0
		for {
			s -= 6
			i++
			if b[s+1] != 0 {
				break
			}
		}

		escFreq += i
		mc := p.minContext
		numStats := uint32(b[mc])
		numStatsNew := numStats - i
		b[mc] = byte(numStatsNew)
		n0 := (numStats + 2) >> 1

		if numStatsNew == 0 {
			freq := (2*uint32(b[stats+1]) + escFreq - 1) / escFreq
			if freq > maxFreq/3 {
				freq = maxFreq / 3
			}
			b[mc+1] = (b[mc+1] & flagPrevHigh) + hiBitsFlag3(b[stats])
			s = mc + 2 // ONE_STATE(mc)
			copyState(b, s, stats)
			b[s+1] = byte(freq)
			p.foundState = s
			p.insertNode(stats, p.u2i(n0))
			return
		}

		n1 := (numStatsNew + 2) >> 1
		if n0 != n1 {
			setCtxStats(b, mc, p.shrinkUnits(stats, n0, n1))
		}
	}

	mc := p.minContext
	setCtxSummFreq(b, mc, uint16(sumFreq+escFreq-(escFreq>>1)))
	b[mc+1] |= flagRescaled
	p.foundState = ctxStats(b, mc)
}

// makeEscFreq is Ppmd8_MakeEscFreq.
func (p *Model) makeEscFreq(numMasked1 uint32, escFreq *uint32) *seeCtx {
	b := p.base
	mc := p.minContext
	numStats := uint32(b[mc])
	if numStats != 0xFF {
		suffixNumStats := uint32(b[ctxSuffix(b, mc)])
		idx := (uint32(p.ns2indx[numStats+2]) - 3) * 32
		idx += boolToUint32(uint32(ctxSummFreq(b, mc)) > 11*(numStats+1))
		if 2*numStats < suffixNumStats+numMasked1 {
			idx += 2
		}
		idx += uint32(b[mc+1]) // Flags
		see := &p.see[idx]
		summ := uint32(see.summ)
		r := summ >> see.shift
		see.summ = uint16(summ - r)
		*escFreq = r + boolToUint32(r == 0)
		return see
	}
	*escFreq = 1
	return &p.dummySee
}

// nextContext is Ppmd8_NextContext.
func (p *Model) nextContext() {
	c := stSuccessor(p.base, p.foundState)
	if p.orderFall == 0 && c >= p.unitsStart {
		p.maxContext = c
		p.minContext = c
	} else {
		p.updateModel()
	}
}

// update1 is Ppmd8_Update1.
func (p *Model) update1() {
	b := p.base
	s := p.foundState
	freq := uint32(b[s+1]) + 4
	setCtxSummFreq(b, p.minContext, ctxSummFreq(b, p.minContext)+4)
	b[s+1] = byte(freq)
	if freq > uint32(b[s-5]) {
		swapStates(b, s, s-6)
		s -= 6
		p.foundState = s
		if freq > maxFreq {
			p.rescale()
		}
	}
	p.nextContext()
}

// update1_0 is Ppmd8_Update1_0.
func (p *Model) update1_0() {
	b := p.base
	s := p.foundState
	freq := uint32(b[s+1])
	summFreq := uint32(ctxSummFreq(b, p.minContext))
	if 2*freq >= summFreq {
		p.prevSuccess = 1
	} else {
		p.prevSuccess = 0
	}
	p.runLength += int32(p.prevSuccess)
	setCtxSummFreq(b, p.minContext, uint16(summFreq+4))
	freq += 4
	b[s+1] = byte(freq)
	if freq > maxFreq {
		p.rescale()
	}
	p.nextContext()
}

// update2 is Ppmd8_Update2.
func (p *Model) update2() {
	b := p.base
	s := p.foundState
	freq := uint32(b[s+1]) + 4
	p.runLength = p.initRL
	setCtxSummFreq(b, p.minContext, ctxSummFreq(b, p.minContext)+4)
	b[s+1] = byte(freq)
	if freq > maxFreq {
		p.rescale()
	}
	p.updateModel()
}
