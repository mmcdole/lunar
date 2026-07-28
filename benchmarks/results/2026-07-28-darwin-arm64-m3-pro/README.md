# Apple M3 Pro results — 2026-07-28

These results were collected from clean source revision
`1d43aec413fa32e8db7eec1bb185c91b551e0028` with Go 1.25.1 on
Darwin/arm64:

- MacBook Pro `Mac15,6`
- Apple M3 Pro, 12 cores (6 performance, 6 efficiency)
- 18 GB memory
- `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, and `-cpu=1`
- 15 samples with a 500 ms target per Go benchmark
- AC power reported and Low Power Mode off
- no CPU affinity or frequency control

GopherLua is v1.1.2. Shopify go-lua is pinned to
`v0.0.0-20250718183320-1e37f32ad7d0`.

Times below are medians from
`golang.org/x/perf/cmd/benchstat@v0.0.0-20260709024250-82a0b07e230d`
with its reported confidence interval. Lower is better. An interval shown as
`<1%` was rounded to `0%` by benchstat.

## Established Lua programs

| Program | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | **162.2 ms ±<1%** | 172.4 ms ±<1% | 179.8 ms ±1% |
| fannkuch-redux | **23.63 ms ±1%** | 33.17 ms ±1% | 40.14 ms ±1% |
| n-body | **59.03 ms ±<1%** | 193.39 ms ±1% | 197.77 ms ±1% |
| spectral-norm | **52.82 ms ±<1%** | 160.14 ms ±1% | 153.66 ms ±1% |

Median allocation traffic and allocation counts:

| Program | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | **65.80 MiB / 1,009,051** | 72.13 MiB / 1,009,811 | 99.03 MiB / 2,533,642 |
| fannkuch-redux | **1.19 KiB / 15** | 384.42 KiB / 1,559 | 11.41 MiB / 1,494,687 |
| n-body | **2.86 KiB / 25** | 44.56 MiB / 376,439 | 70.81 MiB / 8,080,629 |
| spectral-norm | **27.89 KiB / 35** | 56.45 MiB / 231,170 | 75.75 MiB / 9,925,272 |

The programs use unchanged upstream source in a common wrapper and scaled local
inputs. These are not official Computer Language Benchmarks Game scores.

## Interpreter operations

| Case | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| numeric `for`, 10,000 iterations | **45.86 µs ±<1%** | 267.33 µs ±<1% | 326.86 µs ±1% |
| fixed Lua call, 1,000 calls | **22.89 µs ±<1%** | 80.65 µs ±1% | 64.69 µs ±1% |
| table field get/set, 10,000 iterations | **224.9 µs ±<1%** | 847.1 µs ±1% | 981.8 µs ±1% |
| append 256 strings | **41.09 µs ±<1%** | 42.15 µs ±1% | 60.25 µs ±<1% |

Median allocation traffic and allocation counts for the same operations:

| Case | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| numeric `for` | **0 B / 0** | 232.3 KiB / 929 | 234.5 KiB / 30,006 |
| fixed Lua call | **0 B / 0** | 20.47 KiB / 81 | 23.61 KiB / 3,006 |
| table field get/set | **0 B / 0** | 231.4 KiB / 925 | 390.8 KiB / 40,007 |
| append strings | **271.7 KiB / 255** | 277.8 KiB / 520 | 295.8 KiB / 1,795 |

Allocation cells are `B/op / allocs/op`; KiB values use 1,024 bytes.

## Embedding boundary

| Case | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Go to Lua, two scalar arguments and results | **61.21 ns ±<1%** | 61.51 ns ±<1% | 146.80 ns ±1% |
| Lua to Go scalar callback, 1,000 calls | **62.49 µs ±<1%** | 99.58 µs ±<1% | 84.37 µs ±<1% |
| Convert 128-byte Go string and echo from Lua | 86.20 ns ±<1% | **81.41 ns ±1%** | 144.00 ns ±<1% |
| Pass prebuilt table and checksum in Lua | **312.4 ns ±<1%** | 578.3 ns ±2% | 972.9 ns ±1% |
| Create, fill, pass, and checksum table | 2.230 µs ±4% | **1.405 µs ±<1%** | 1.645 µs ±<1% |

Median allocation traffic and allocation counts:

| Case | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Go to Lua scalars | **0 B / 0** | **0 B / 0** | 160 B / 5 |
| Lua to Go callback | **0 B / 0** | 29.17 KiB / 1,085 | 31.41 KiB / 4,005 |
| Convert and echo Go string | **0 B / 0** | 16 B / 1 | 176 B / 5 |
| Pass prebuilt table | **0 B / 0** | 40 B / 0 | 576 B / 57 |
| Create, fill, and pass table | **650 B / 6** | 1,384 B / 37 | 1,536 B / 89 |

The prebuilt and create/fill rows separate steady-state table use from table
construction and public-handle publication.

## CBOR graph memory

The CBOR load uses a deterministic 9,208,046-byte input containing 183,513
tables and 938,452 entries. Each runtime ran in 15 fresh processes in
randomized paired order. Two forced Go collections precede the baseline and
follow the measured load.

| Metric | Lugo median | GopherLua median | Reduction |
| --- | ---: | ---: | ---: |
| Allocation traffic during load | **107.5 MB** | 784.6 MB | 86.30% |
| Allocations during load | **664,906** | 12,733,959 | 94.78% |
| Baseline-subtracted retained heap increase | **72.24 MiB** | 542.26 MiB | 86.68% |
| Absolute live Go heap after load and GC | **72.53 MiB** | 543.83 MiB | 86.66% |

MB uses 1,000,000 bytes. MiB uses 1,048,576 bytes. The retained increase and
absolute live heap are separate metrics.

Shopify go-lua is not in this table because its pinned standard IO library
does not implement `file:read("*a")`; the identical file workload cannot run
without a host shim.

## Raw evidence

| File | SHA-256 |
| --- | --- |
| [`go-benchmarks.txt`](go-benchmarks.txt) | `631f713367bba647018c558d5bd5a9c33f510e12ad8c5caa15b3a3aac82c2376` |
| [`cbor-lugo-retained.jsonl`](cbor-lugo-retained.jsonl) | `e55d351d3ef8dbb47e885e3d778ec0348dae528fc14eec6670309e445ba4c849` |
| [`cbor-gopherlua-retained.jsonl`](cbor-gopherlua-retained.jsonl) | `ad6c49a1a9b14df18cdedd50e0073fe1a81c98c011c35c3e1997d23fcb0c3778` |

The Go archive contains 15 observations for each of 39 program, interpreter,
and embedding runtime cells. Both CBOR files use collection ID
`7089ec87ebeeb04e504562f55a9b37d8`; each contains 15 records with the same
fixture, codec, workload, source revision, and structural oracle.

The collection and analysis commands are in
[`benchmarks/README.md`](../../README.md) and
[`benchmarks/cbor/README.md`](../../cbor/README.md).
