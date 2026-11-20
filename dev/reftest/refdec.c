// Reference driver for the original 7-Zip Ppmd8 decoder.
// Reads: props (2 bytes) + compressed stream from a file, decodes N symbols
// with a per-symbol trace identical in spirit to the Go port.
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "Ppmd8.h"

static Byte g_buf[1 << 20];
static size_t g_pos, g_len;

static uint64_t fnv64(const unsigned char *p, size_t n, uint64_t h) {
  for (size_t i = 0; i < n; i++) { h ^= p[i]; h *= 1099511628211ULL; }
  return h;
}

static Byte readByte(void *p) {
  (void)p;
  if (g_pos >= g_len) return 0;
  return g_buf[g_pos++];
}

static IByteIn g_byteIn = { readByte };

static void *SzAlloc(const ISzAlloc *a, size_t size) { (void)a; return calloc(size, 1); }
static void SzFree(const ISzAlloc *a, void *ptr) { (void)a; free(ptr); }
static const ISzAlloc g_alloc = { SzAlloc, SzFree };

int main(int argc, char **argv) {
  if (argc < 5) {
    fprintf(stderr, "usage: refdec <file> <offset> <count> <memsize> [hashfrom hashto]\n");
    return 2;
  }
  const char *path = argv[1];
  long offset = atol(argv[2]);
  int count = atoi(argv[3]);
  UInt32 memSize = (UInt32)atol(argv[4]);
  int hashFrom = (argc > 5) ? atoi(argv[5]) : -1;
  int hashTo = (argc > 6) ? atoi(argv[6]) : -1;
  int hashStride = (argc > 7) ? atoi(argv[7]) : 1;

  FILE *f = fopen(path, "rb");
  if (!f) { perror("open"); return 1; }
  fseek(f, 0, SEEK_END);
  long sz = ftell(f);
  fseek(f, offset, SEEK_SET);
  g_len = (size_t)(sz - offset);
  if (g_len > sizeof(g_buf)) g_len = sizeof(g_buf);
  if (fread(g_buf, 1, g_len, f) != g_len) { perror("read"); return 1; }
  fclose(f);

  Byte props[2] = { g_buf[0], g_buf[1] };
  g_pos = 2;
  const UInt32 val = (UInt32)props[0] | ((UInt32)props[1] << 8);
  const unsigned order = (val & 0xF) + 1;
  const UInt32 mem = ((val >> 4) & 0xFF) + 1;
  const unsigned restor = (val >> 12);
  fprintf(stderr, "props: order=%u mem=%uMB restor=%u (alloc %u)\n", order, mem, restor, memSize);

  CPpmd8 ppmd;
  memset(&ppmd, 0, sizeof(ppmd));
  Ppmd8_Construct(&ppmd);
  if (!Ppmd8_Alloc(&ppmd, memSize, &g_alloc)) { fprintf(stderr, "alloc fail\n"); return 1; }
  ppmd.Stream.In = &g_byteIn;
  if (!Ppmd8_Init_RangeDec(&ppmd)) { fprintf(stderr, "init fail\n"); return 1; }
  Ppmd8_Init(&ppmd, order, restor);

  CPpmd8 *p = &ppmd;
  for (int i = 0; i < count; i++) {
    int sym = Ppmd8_DecodeSymbol(p);
    if (i >= hashFrom && i <= hashTo && (i % hashStride) == 0) {
      uint64_t h = 14695981039346656037ULL;
      h = fnv64(p->Base, (size_t)(p->Text - p->Base), h);
      h = fnv64(p->HiUnit, p->AlignOffset + p->Size - (UInt32)(p->HiUnit - p->Base), h);
      h = fnv64((const unsigned char*)p->See, sizeof(p->See), h);
      h = fnv64((const unsigned char*)p->BinSumm, sizeof(p->BinSumm), h);
      fprintf(stderr, "HASH %d sym=%d of=%u mc=%x maxc=%x fs=%x code=%08x range=%08x low=%08x h=%016llx text=%x us=%x lu=%x hu=%x gc=%x fl=",
          i, sym, p->OrderFall, (unsigned)((Byte*)p->MinContext - p->Base),
          (unsigned)((Byte*)p->MaxContext - p->Base),
          p->FoundState ? (unsigned)((Byte*)p->FoundState - p->Base) : 0,
          p->Code, p->Range, p->Low, (unsigned long long)h,
          (unsigned)(p->Text - p->Base), (unsigned)(p->UnitsStart - p->Base),
          (unsigned)(p->LoUnit - p->Base), (unsigned)(p->HiUnit - p->Base), p->GlueCount);
      for (int k = 0; k < PPMD_NUM_INDEXES; k++) fprintf(stderr, "%x,", (unsigned)p->FreeList[k]);
      fprintf(stderr, "\n");
    }
    if (sym < 0) break;
  }
  return 0;
}
