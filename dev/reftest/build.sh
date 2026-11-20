#!/bin/sh
# Build the reftest reference decoder on Linux / macOS.
#
# Usage:
#   ./build.sh                 # fetch 7-Zip C sources (sparse) and build ./refdec
#   ./build.sh /path/to/7zip   # use an existing 7-Zip source checkout instead
#
# Honors CC/CFLAGS from the environment.
set -eu

cd "$(dirname "$0")"

if [ $# -ge 1 ]; then
    SRC="$1"
else
    SRC=7zip-src
    if [ ! -d "$SRC/C" ]; then
        echo ">> cloning 7-Zip C sources (sparse) into $SRC" >&2
        rm -rf "$SRC"
        git clone --quiet --depth 1 --filter=blob:none --sparse https://github.com/ip7z/7zip "$SRC"
        git -C "$SRC" sparse-checkout set C
    fi
fi

if [ ! -f "$SRC/C/Ppmd8.c" ]; then
    echo "error: $SRC/C/Ppmd8.c not found (bad 7-Zip source path?)" >&2
    exit 1
fi

: "${CC:=cc}"
: "${CFLAGS:=-O2}"

echo ">> building ./refdec with $CC" >&2
# shellcheck disable=SC2086
"$CC" $CFLAGS -I "$SRC/C" \
    refdec.c \
    "$SRC/C/Ppmd8.c" \
    "$SRC/C/Ppmd8Dec.c" \
    -o refdec

cat >&2 <<'EOF'
>> done. Example:

   # decode 100 symbols of a PPMd zip entry whose compressed stream
   # (2 property bytes + data) starts at offset 40 of the archive,
   # printing the state hash for symbols 0..99:
   ./refdec archive.zip 40 100 4194304 0 99 1

   # <memSize> is the model arena in bytes (props give the MB value,
   # e.g. 4194304 for 4 MB, 134217728 for 128 MB).
EOF
