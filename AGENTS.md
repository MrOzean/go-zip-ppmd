# AGENTS.md

## Project overview

Pure Go (CGO=0) library + CLI for reading ZIP archives with PPMd
compression (method 98), store and deflate. Module:
`github.com/MrOzean/go-zip-ppmd`.

Target use case: streaming extraction of large (60 MB+) archives with
text files. Test corpora live in `test_arch/` (gitignored, not committed).

## Layout

- `zipppmd.go` — package doc, `FileHeader`, `File`, `Open`, CRC-32 checking
  reader, `openBody` (method dispatch: store/deflate/ppmd), exported
  errors, `WithMaxModelMemory` option types.
- `reader.go` — `Reader` (io.ReaderAt): EOCD (+ZIP64) scan, central
  directory parsing.
- `stream.go` — `StreamReader` (io.Reader): sequential local-header walk.
- `writer.go` — stub; PPMd compression not implemented (returns
  `ErrNotImplemented`).
- `version.go` — `const Version` (semver without "v"; git tag is "v"+Version).
- `ppmd/` — self-contained PPMd var.I (Ppmd8) decoder, a Go port of
  `C/Ppmd8.c` + `C/Ppmd8Dec.c` from the 7-Zip sources. Only decompression.
- `cmd/zipppmd/` — CLI (`list`, `extract`; `create` is a stub).
- `testdata/` — small fixture zips (NanaZip/7-Zip generated) + reference
  file.

## Key implementation facts

- PPMd stream format for ZIP method 98: 2 property bytes, then the
  carryless range coder stream. `val = b0 | b1<<8`: order = (val&0xF)+1,
  memMB = ((val>>4)&0xFF)+1, restore = val>>12 (only 0=restart, 1=cutoff;
  2=freeze is unsupported, matching 7-Zip default builds). **Format history
  (verified in 7-Zip sources via github.com/mcmilk/7-Zip history): 7-Zip
  introduced zip+ppmd in 9.11 (2009) directly on Ppmd8 with this exact
  packed props format; versions <= 9.10 had no zip+ppmd at all. "Version
  made by" = 63 (`0x003F`) is 7-Zip's `kMadeByProgramVersion`. PPMd var.H
  (Ppmd7) was never used for zip method 98 by any 7-Zip release — it only
  serves the 7z container's PPMd method (5-byte props). See "Ppmd7 port
  context" below.
- The `ppmd.Model` uses one `[]byte` arena; all C pointers are `uint32`
  offsets into it. Context = 12 bytes, State = 6 bytes, free Node = 12
  bytes. See `ppmd/model.go` for the accessor functions.
- Ported from the 2023+ 7-Zip Ppmd8 (var.I). 7-Zip ≥ 23 uses Ppmd8 for ZIP
  method 98; older 7-Zip used Ppmd7 (var.H) — NOT supported here.
- Subtle C idioms that must be preserved (bugs bit us here before):
  - `SplitBlock` / glue fill loop decrement `i` (`I2U(--i)`) before the
    main insert: the main block is inserted with the *decremented* class.
  - `prev = &node->Next` slots are node addresses; writes go through
    `setNodeNext(b, node, v)` (+4 inside). Don't add +4 again.
  - `RESTORE_MODEL(c1, x)` ignores the second argument: the error context
    is always the first one.
  - **Go byte arithmetic wraps**: `b[c]+1` on a `byte(255)` yields `0`.
    Always widen first: `(uint32(b[c]) + 1)`. (This broke valid decoding
    once when added to the state-list scan bound.)
- `makeEscFreq` indexes `see[row*32 + col]`; don't forget the row stride.
- Streaming (`StreamReader`) positions the shared stream after each entry
  by: drain `openRC`, drain the raw decompressor (ppmd/flate read ahead),
  then `Discard(csize - counted)`.
- CRC-32 (IEEE) is verified per entry; `checksumReader` errors with
  `ErrChecksum`.

## Robustness / resource limits 

- `WithMaxModelMemory(n)` (`NewReader`/`NewStreamReader` option) caps the
  PPMd model memory one entry may request (props allow 1..256 MiB); entries
  over the limit fail with `ErrModelMemoryLimit` **before** the arena is
  allocated — important for servers that open entries concurrently.
- Error taxonomy (all `errors.Is`-friendly): `ErrInvalidPPMdProps` (restore
  > 2, same check as 7-Zip), `ErrUnsupportedRestore` (restore == 2,
  freeze — valid Ppmd8 but unsupported, like 7-Zip default builds),
  `ErrEncrypted` (checked in both Reader and StreamReader), `ErrCorrupt`,
  `ErrChecksum`, `ppmd.ErrCorrupt`.
- Decoder termination: when the compressed input is exhausted,
  `decodeSymbol` returns corrupt-data (`io.ErrUnexpectedEOF` via
  `Decoder.Read`), and `rcNorm` stops feeding phantom zero bytes (the
  original port spun forever / emitted an endless phantom-symbol stream on
  truncated input — found by fuzzing). `rcGetThreshold` maps a
  denormalized zero range to the largest count so the corrupt-data path is
  taken instead of dividing by zero. Note: a valid entry's stream decodes
  to a few *extra* symbols past `UncompressedSize` (the encoder's
  range-coder flush); the zip layer discards them via
  `io.LimitReader(decoder, UncompressedSize)` in `openBody`, so the
  exhaustion checks must stay behind that limit.
- The state-list scans in `createSuccessors` are bounded by the context's
  declared `NumStats+1` entries (`findState`) instead of running unbounded
  like the C original — on corrupt input the C scan reads past the arena
  (Go panics; found by fuzzing). The same bound applies in `updateModel`
  and `reduceOrder`. The suffix chain walk is likewise bounded
  (`ps` overflow, out-of-arena suffix/successor pointers in
  `createSuccessors` and `decodeSymbol`, and `binSummIndex` validates its
  pointers and result index, returning `(idx, false)` on corrupt state).
  Only triggers on corrupt data; valid streams unaffected. Beware:
  `b[ctxSuffix(b,c)] == 0` reads the *byte at* the suffix context (its
  NumStats), it does NOT compare the pointer to zero — don't "simplify"
  it.
- Fuzz tests: `FuzzReader` (zip parsing + decoding) and `ppmd.FuzzDecoder`.
  Run: `go test -fuzz=FuzzReader -fuzztime=60s .` and
  `go test -fuzz=FuzzDecoder -fuzztime=60s ./ppmd/`. FuzzDecoder caps the
  model at 1..4 MiB per input so the fuzzer stays fast. Crashers found by
  fuzzing are kept as regression seeds in `ppmd/testdata/fuzz/FuzzDecoder/`.

## Ppmd7 (var.H) — out of scope, archives do not exist

**Verified 2026-11-20:** 7-Zip introduced ZIP method 98 (PPMd) support in
version **9.11 (2009)** — directly on PPMd var.I (Ppmd8), with the same
packed 2-byte property format this package parses (source-verified in
github.com/mcmilk/7-Zip history, commit 6eb2240 "7-Zip Version 9.11";
≤ 9.10 had no zip+ppmd at all). Therefore **zip+ppmd7 archives were never
produced by any 7-Zip release** and are not known to exist from any
mainstream tool; no Ppmd7 port is planned. PPMd var.H (Ppmd7, C/Ppmd7.c)
only serves the **7z** container's PPMd method (5-byte properties) — a
different container, out of scope here (use bodgit/sevenzip for 7z).

## Verification status / how to re-verify

The decoder was validated byte-for-byte (FNV state hashes per symbol)
against the C reference (`refdec` harness, not committed) and by CRC-32 on
all entries of the test corpora: 8130/8130 entries OK (~3.7 GB decoded).
`fresh.zip` in `test_arch/` (8 MB PPMd settings) exercises the
`glueFreeBlocks`/`expandTextArea` paths — keep testing against it.

## Commands

```sh
go build ./...
go vet ./...
go test ./...
go test -bench=DecodePPMd -benchtime=3x -run=NONE .   # needs test_arch/fresh.zip for the fresh-entry bench (skips otherwise)
```

CLI smoke test:

```sh
go run ./cmd/zipppmd list -v testdata/sample_ppmd_cutoff.zip
go run ./cmd/zipppmd extract -d /tmp/out testdata/sample_ppmd_restart.zip
```

## Style

- Standard library only; no external deps (vendored philosophy).
- Follow the existing code: small funcs mirroring the C originals with a
  comment naming the C function (e.g. `// cutOff is CutOff.`).
- Go 1.23+.
