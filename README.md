# go-zip-ppmd

Pure Go (CGO=0) library and CLI for streaming work with **ZIP archives
compressed with PPMd** (compression method 98, as produced by 7-Zip), plus
plain store/deflate archives.

The PPMd decoder is a faithful port of the public domain **Ppmd8**
implementation from the [7-Zip](https://7-zip.org) project (PPMd var.I by
Dmitry Shkarin, carryless range coder by Dmitry Subbotin). It has been
verified byte-for-byte against the C reference on gigabytes of real-world
data (all 8130 entries of the test corpora pass CRC-32).

## Features

- **Reading** with two access modes:
  - `Reader` — random access over `io.ReaderAt` (e.g. `*os.File`); parses
    the central directory, like `archive/zip`.
  - `StreamReader` — sequential access over a plain `io.Reader` (stdin,
    pipes, network); reads local file headers one by one, no seeking.
- Methods: **PPMd (98)**, store (0), deflate (8). CRC-32 is verified for
  every entry.
- PPMd parameters (order, memory size, restore method) are read from the
  2-byte property block 7-Zip prepends to each compressed stream.
- Pure Go, zero external dependencies.

Not implemented yet: **writing** (`NewWriter` is a stub that fails with
`ErrNotImplemented`). PPMd compression is planned.

## Install

```sh
go get github.com/MrOzean/go-zip-ppmd
```

## Library usage

Random access:

```go
f, _ := os.Open("books.zip")
st, _ := f.Stat()
r, err := zipppmd.NewReader(f, st.Size())
if err != nil { ... }
for _, e := range r.File {
    if e.Method != zipppmd.MethodPPMd { continue }
    rc, err := e.Open()          // decompresses and verifies CRC-32
    if err != nil { ... }
    body, err := io.ReadAll(rc)  // 64–128 MB are allocated for the PPMd model
    rc.Close()
    _ = body
}
```

Streaming (stdin / pipes):

```go
sr := zipppmd.NewStreamReader(os.Stdin)
for {
    e, err := sr.Next()
    if err == io.EOF { break }
    if err != nil { ... }
    rc, err := sr.Open()
    if err != nil { ... }
    _, err = io.Copy(dst, rc) // must be consumed before the next Next()
    rc.Close()
    if err != nil { ... }
}
```

## CLI

```sh
# list entries
zipppmd list -v books.zip

# extract everything
zipppmd extract -d out books.zip

# extract selected entries, streaming the archive from stdin
zipppmd extract -d out - '*.fb2' < books.zip
```

## Format notes (forensics)

ZIP method 98 stores a 2-byte PPMd property block at the start of the
compressed stream (little-endian 16-bit value `v`):

| bits    | meaning                        |
|---------|--------------------------------|
| 0–3     | `order - 1` (order 2…16)       |
| 4–11    | `memorySizeMB - 1` (1…256 MB)  |
| 12–15   | restore method (0=restart, 1=cutoff, 2=freeze — unsupported) |

### Format history (verified against 7-Zip sources)

7-Zip introduced ZIP method 98 (PPMd) support in **version 9.11 (2009)**
— directly on **PPMd var.I (Ppmd8)**, with the packed 2-byte property
layout shown above (verified in the 9.11 sources: identical parsing and
decoder logic to current 7-Zip). Earlier 7-Zip versions did not support
zip+ppmd at all. The older PPMd var.H (Ppmd7) decoder that existed in the
same era serves the **7z** container's PPMd method (5-byte properties), not
ZIP — no mainstream tool is known to produce Ppmd7-based method 98
archives, so this package does not implement it.

One historical caveat: Ppmd8 received rare edge-case fixes over the years
(e.g. the 20.01 "divide by zero" correction, `PPMD8_CORRECT_SUM_RANGE`).
This port follows the current (fixed) decoder, matching modern 7-Zip.

## Testing

```sh
go test ./...
```

Small fixture archives in `testdata/` were produced with 7-Zip (PPMd,
restart/cutoff modes). To additionally regression-test against a large
real-world archive (e.g. the 60 MB+ archives with text files):

```sh
set ZIPPPMD_TEST_ARCHIVE=f.fb2-183066-183652.zip   # PowerShell: $env:...=
go test -run TestLargeArchive -v .
```

## License

MIT (see [LICENSE](LICENSE)). The ported PPMd code is in the public domain
(Igor Pavlov / Dmitry Shkarin / Dmitry Subbotin).
