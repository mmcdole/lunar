# Apple M3 Pro results — 2026-07-27

These results were collected from clean source revision
`1a50139f103c79f83b08745504dcb544620ba484` with Go 1.25.1 on
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
with its reported confidence interval. Lower is better.

## Established Lua programs

| Program | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | 172.9 ms ±1% | 176.5 ms ±1% | 186.0 ms ±0% |
| fannkuch-redux | 24.25 ms ±0% | 33.67 ms ±1% | 41.33 ms ±1% |
| n-body | 61.30 ms ±0% | 195.59 ms ±1% | 204.94 ms ±1% |
| spectral-norm | 55.63 ms ±1% | 166.21 ms ±1% | 157.80 ms ±1% |

Median allocation traffic and allocation counts:

| Program | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | 79.14 MiB / 1,009,169 | 72.13 MiB / 1,009,811 | 99.03 MiB / 2,533,642 |
| fannkuch-redux | 1.20 KiB / 15 | 384.42 KiB / 1,559 | 11.41 MiB / 1,494,687 |
| n-body | 2.86 KiB / 25 | 44.56 MiB / 376,439 | 70.81 MiB / 8,080,629 |
| spectral-norm | 27.89 KiB / 35 | 56.45 MiB / 231,170 | 75.75 MiB / 9,925,272 |

The programs use unchanged upstream source in a common wrapper and scaled local
inputs. These are not official Computer Language Benchmarks Game scores.

## Interpreter operations

| Case | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| numeric `for`, 10,000 iterations | 45.97 µs ±0% | 270.84 µs ±1% | 334.16 µs ±1% |
| fixed Lua call, 1,000 calls | 23.57 µs ±0% | 84.17 µs ±1% | 67.41 µs ±1% |
| table field get/set, 10,000 iterations | 231.4 µs ±0% | 858.7 µs ±0% | 1,013.9 µs ±1% |
| append 256 strings | 41.87 µs ±0% | 42.96 µs ±0% | 61.60 µs ±0% |

Median allocation traffic and allocation counts for the same operations:

| Case | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| numeric `for` | 0 B / 0 | 232.3 KiB / 929 | 234.5 KiB / 30,006 |
| fixed Lua call | 0 B / 0 | 20.47 KiB / 81 | 23.61 KiB / 3,006 |
| table field get/set | 0 B / 0 | 231.4 KiB / 925 | 390.8 KiB / 40,007 |
| append strings | 271.7 KiB / 255 | 277.8 KiB / 520 | 295.8 KiB / 1,795 |

Allocation cells are `B/op / allocs/op`; KiB values use 1,024 bytes.

## Embedding boundary

| Case | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Go to Lua, two scalar arguments and results | 64.25 ns ±0% | 63.00 ns ±0% | 153.90 ns ±0% |
| Lua to Go scalar callback, 1,000 calls | 63.65 µs ±0% | 103.13 µs ±1% | 86.96 µs ±1% |
| Go string echo, 128 bytes | 89.04 ns ±0% | 85.51 ns ±1% | 148.50 ns ±1% |
| Go-built table, 16 array and 4 record fields | 2.327 µs ±2% | 1.434 µs ±0% | 1.679 µs ±0% |

Median allocation traffic and allocation counts:

| Case | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Go to Lua scalars | 0 B / 0 | 0 B / 0 | 160 B / 5 |
| Lua to Go callback | 0 B / 0 | 29.17 KiB / 1,085 | 31.41 KiB / 4,005 |
| Go string echo | 0 B / 0 | 16 B / 1 | 176 B / 5 |
| Go-built table | 651 B / 6 | 1,384 B / 37 | 1,536 B / 89 |

## CBOR graph memory

The CBOR load uses a deterministic 9,208,046-byte input containing 183,513
tables and 938,452 entries. Each runtime ran in 15 fresh processes in
randomized paired order. Two forced Go collections precede the baseline and
follow the measured load.

| Metric | Lugo median | GopherLua median | Reduction |
| --- | ---: | ---: | ---: |
| Allocation traffic during load | 107.5 MB | 784.6 MB | 86.30% |
| Allocations during load | 664,905 | 12,733,958 | 94.78% |
| Baseline-subtracted retained heap increase | 72.24 MiB | 542.26 MiB | 86.68% |
| Absolute live Go heap after load and GC | 72.53 MiB | 543.83 MiB | 86.66% |

MB uses 1,000,000 bytes. MiB uses 1,048,576 bytes. The retained increase and
absolute live heap are separate metrics.

Shopify go-lua is not in this table because its pinned standard IO library
does not implement `file:read("*a")`; the identical file workload cannot run
without a host shim.

Collection protocols are documented in
[the benchmark guide](../../README.md) and
[the CBOR benchmark guide](../../cbor/README.md).

The [original collection records](https://github.com/mmcdole/lunar/tree/5fc51e449a6661340056544e995aae4fdf89dcb2/benchmarks/results/2026-07-27-darwin-arm64-m3-pro) remain available in Git history. This directory retains the result summary.
