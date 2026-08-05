# Apple M3 Pro table-shape results — 2026-08-05

This collection covers only the controlled table-shape suite. The program,
interpreter, embedding, and CBOR graph results remain published under
[`results/2026-07-28-darwin-arm64-m3-pro/`](../2026-07-28-darwin-arm64-m3-pro/).

These results were collected from clean source revision
`87cc98d4a023f7a29e601008b176d1936b3d1c87` with Go 1.25.1 on Darwin/arm64:

- MacBook Pro `Mac15,6`
- Apple M3 Pro, 12 cores (6 performance, 6 efficiency)
- 18 GB memory
- AC power and Low Power Mode off
- Go runtime defaults (`GOGC=100`, no `GOMEMLIMIT`)
- no CPU affinity or frequency control

GopherLua is v1.1.2. Shopify go-lua is not part of this suite; the shape
worker builds tables through the two-engine benchmark bridge.

## Protocol

Each sample is one fresh worker process that builds a single table shape
through the engine bridge, anchors it in a Lua global, and reports the live
Go heap added between two stabilization points, each preceded by two forced
collections. The two engines alternate leading position every round; each
case has 15 samples per engine. Every process verifies the built shape —
table count, entry count, and total key bytes — after its measured samples
are captured.

Repeated keys are freshly cloned before every insertion, so retained sharing
can come only from the runtime. Unique keys derive from chained SHA-256
blocks, so the distinguishing bytes span the whole key rather than a counter
suffix. Tables are built without capacity hints, matching a dynamic decoder.
Values are boolean `true` throughout, keeping boxed-number allocation out of
every case.

## Retained heap added, median of 15

| Shape | Lunar | GopherLua | Ratio |
| --- | ---: | ---: | ---: |
| 25,000 four-field tables, four repeated 16 B keys | **7.26 MiB** | 72.01 MiB | 9.92× |
| 25,000 four-field tables, unique 16 B keys | **8.76 MiB** | 72.01 MiB | 8.22× |
| 25,000 four-field tables, four repeated 80 B keys | **14.89 MiB** | 78.11 MiB | 5.25× |
| One table, 100,000 unique 16 B keys | **6.54 MiB** | 14.75 MiB | 2.26× |
| One table, 100,000 unique 64 B keys | **11.11 MiB** | 19.33 MiB | 1.74× |
| One table, 100,000 unique 256 B keys | **29.42 MiB** | 37.64 MiB | 1.28× |
| One table, 100,000 unique 1,024 B keys | **102.67 MiB** | 110.88 MiB | 1.08× |

MiB uses 1,048,576 bytes. The widest min-to-max spread across any 15-sample
series is 0.23%, so no confidence intervals are shown.

Observations the rows support directly:

- GopherLua's retained heap is identical for repeated and unique 16-byte
  keys; it shares nothing. Lunar retains 1.57 MiB less with repeated keys,
  matching the 1.6 MB of raw key bytes its string reuse avoids.
- Repeated 80-byte keys sit past Lunar's 64-byte reuse limit and cost it
  exactly the raw key payload (8.0 MiB over the repeated-16 case), while the
  many-small-tables ratio remains 5.25× because per-table overhead still
  dominates.
- With one large table the ratio falls from 2.26× at 16-byte keys to 1.08×
  at 1,024-byte keys as raw key bytes dominate both heaps.

## Raw evidence

| File | SHA-256 |
| --- | --- |
| [`shapes-lunar-retained.jsonl`](shapes-lunar-retained.jsonl) | `4bcdc0d9332f2e736adb499e34b631b4c1c85d98eab7bee03f9feebc6867cb06` |
| [`shapes-gopherlua-retained.jsonl`](shapes-gopherlua-retained.jsonl) | `466b50d7bbb12012a861b0ead4c22093eaa42bd12d39ab529a823192b1d5255e` |

Each file holds 105 records: 15 per case, ordered by case then round. Every
record reports the same clean source revision and its shape parameters.

The collection command is `scripts/shapes.sh` in
[`benchmarks/cbor/`](../../cbor/README.md).
