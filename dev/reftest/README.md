# reftest — differential testing harness (development only)

This directory contains a **C reference driver** used to validate the Go
PPMd port byte-for-byte against the original 7-Zip implementation. It is
NOT part of the Go library and requires a C compiler (MSVC on Windows:
run from a "x64 Native Tools" prompt, or invoke `vcvars64.bat` first).

## Files

- `refdec.c` — reads a PPMd property byte pair + compressed stream from a
  file at an offset, decodes N symbols with the original C code, and can
  print a per-symbol state trace: symbol, order fall, model contexts,
  range coder state (code/range/low) and an FNV-1a hash over the live
  arena regions (text, context tail) plus the See/BinSumm tables, plus
  the allocator free lists. This lets a Go test compare the full model
  state after every decoded symbol.

## Build

Linux / macOS (any recent gcc or clang; fetches the 7-Zip C sources
sparsely on first run):

```sh
./build.sh                 # produces ./refdec next to this README
./build.sh /path/to/7zip   # or point at an existing 7-Zip checkout
```

Windows (MSVC — run from a "x64 Native Tools Command Prompt", or after
`vcvars64.bat`):

```bat
build.bat
```

Equivalent manual invocation, if you prefer:

```sh
git clone --depth 1 --filter=blob:none --sparse https://github.com/ip7z/7zip 7zip-src
cd 7zip-src && git sparse-checkout set C && cd ..
cc -O2 -I 7zip-src/C refdec.c 7zip-src/C/Ppmd8.c 7zip-src/C/Ppmd8Dec.c -o refdec
```

## Usage

```
refdec <file> <offset> <count> <memSize> [hashfrom hashto [stride]]
```

- `<file> <offset>`: the archive file and the offset of the entry's
  compressed stream (local header data start, i.e. *including* the 2
  property bytes).
- `<count>`: number of symbols to decode.
- `<memSize>`: model arena size in bytes (must match the entry, e.g.
  134217728 for 128 MB). The property bytes are read from the stream
  itself; memSize is the allocation size passed to `Ppmd8_Alloc`.
- `[hashfrom hashto [stride]]`: print the `HASH ...` state line for symbol
  indices in `[hashfrom, hashto]`, every `stride` symbols (default 1).
  Use a large stride to bisect the first diverging symbol quickly.

The Go side (harness used during bring-up, not committed) decoded the same
entry, printed the identical `HASH ...` line format (mind: print
`runLength` as `uint32`, and strip any trailing `err=<nil>` before
comparing), and the two outputs were diffed. Once the first diverging
symbol index is known, re-run both sides with traces enabled around it
(per-iteration BIN/NUM/ESC dumps in the decoder, allocator traces in the
model) to find the exact porting bug.

## History

This methodology caught four subtle porting bugs during the Ppmd8 bring-up
(see AGENTS.md "Subtle C idioms"): a missing `*32` row stride in the SEE
index, the `I2U(--i)` decrement in `SplitBlock`, a double `+4` when writing
through `&node->Next` link slots, and the `RESTORE_MODEL(c, x)` argument
semantics.
