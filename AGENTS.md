# AGENTS.md

## Project overview

Pure Go (CGO=0) library + CLI for reading ZIP archives with PPMd
compression (method 98), store and deflate. Module:
`github.com/MrOzean/go-zip-ppmd`.

Target use case: streaming extraction of large (60 MB+) archives with
text files. Test corpora live in `test_arch/` (gitignored, not committed).

## Layout

- `zipppmd.go` — package doc, `FileHeader`, `File`, `Open`, CRC-32 checking
  reader, `openBody` (method dispatch: store/deflate/ppmd).
- `reader.go` — `Reader` (io.ReaderAt): EOCD (+ZIP64) scan, central
  directory parsing.
- `stream.go` — `StreamReader` (io.Reader): sequential local-header walk.
- `writer.go` — stub; PPMd compression not implemented (returns
  `ErrNotImplemented`).
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
- `makeEscFreq` indexes `see[row*32 + col]`; don't forget the row stride.
- Streaming (`StreamReader`) positions the shared stream after each entry
  by: drain `openRC`, drain the raw decompressor (ppmd/flate read ahead),
  then `Discard(csize - counted)`.
- CRC-32 (IEEE) is verified per entry; `checksumReader` errors with
  `ErrChecksum`.

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
