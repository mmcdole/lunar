# Apple M3 Pro timing and long-string cache results — 2026-08-07

The canonical runtime comparison was collected from clean source revision
`d9c162801840b7f7091b2bb4d337ff1f358bf900` with Go 1.25.1 on
Darwin/arm64:

- MacBook Pro `Mac15,6`
- Apple M3 Pro, 12 cores (6 performance, 6 efficiency)
- 18 GB memory
- `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, and `-cpu=1`
- 15 samples with a 500 ms target per Go benchmark
- AC power reported and Low Power Mode off
- foreground collection with normal macOS services
- no CPU affinity or frequency control

GopherLua is v1.1.2. Shopify go-lua is pinned to
`v0.0.0-20250718183320-1e37f32ad7d0`.

Times below are medians from
`golang.org/x/perf/cmd/benchstat@v0.0.0-20260709024250-82a0b07e230d`
with its reported confidence interval. Lower is better. An interval shown as
`<1%` was rounded to `0%` by benchstat.

This revision evaluates the long-string identity cache before integration with
the later compiler-only change on `origin/main`. The cache A/B is causal
because its baseline is the cache commit's exact parent. Recollect the public
matrix from the final integrated revision before treating these numbers as a
release-wide snapshot.

## Established Lua programs

| Program | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | **162.0 ms ±1%** | 173.1 ms ±1% | 180.9 ms ±2% |
| fannkuch-redux | **24.18 ms ±1%** | 33.35 ms ±1% | 40.14 ms ±2% |
| n-body | **59.33 ms ±1%** | 192.63 ms ±1% | 199.87 ms ±2% |
| spectral-norm | **52.05 ms ±1%** | 161.44 ms ±2% | 153.64 ms ±3% |

Median allocation traffic and allocation counts:

| Program | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | **65.80 MiB / 1,009,051** | 72.13 MiB / 1,009,811 | 99.03 MiB / 2,533,642 |
| fannkuch-redux | **1.20 KiB / 15** | 384.42 KiB / 1,559 | 11.41 MiB / 1,494,687 |
| n-body | **2.86 KiB / 25** | 44.56 MiB / 376,439 | 70.81 MiB / 8,080,629 |
| spectral-norm | **27.89 KiB / 35** | 56.45 MiB / 231,170 | 75.75 MiB / 9,925,272 |

The programs use unchanged upstream source in a common wrapper and scaled local
inputs. These are not official Computer Language Benchmarks Game scores.

## Interpreter operations

| Case | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| numeric `for`, 10,000 iterations | **45.94 µs ±3%** | 277.01 µs ±2% | 336.55 µs ±3% |
| fixed Lua call, 1,000 calls | **22.59 µs ±1%** | 81.48 µs ±2% | 66.07 µs ±3% |
| table field get/set, 10,000 iterations | **224.7 µs ±1%** | 844.0 µs ±1% | 1.0344 ms ±4% |
| append 256 strings | **41.00 µs ±1%** | 42.02 µs ±2% | 60.67 µs ±1% |

Median allocation traffic and allocation counts for the same operations:

| Case | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| numeric `for` | **0 B / 0** | 232.3 KiB / 929 | 234.5 KiB / 30,006 |
| fixed Lua call | **0 B / 0** | 20.47 KiB / 81 | 23.61 KiB / 3,006 |
| table field get/set | **0 B / 0** | 231.4 KiB / 925 | 390.8 KiB / 40,007 |
| append strings | **271.7 KiB / 255** | 277.8 KiB / 520 | 295.8 KiB / 1,795 |

Allocation cells are `B/op / allocs/op`; KiB values use 1,024 bytes.

## Embedding boundary

| Case | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Go to Lua, two scalar arguments and results | 62.82 ns ±<1% | **61.93 ns ±1%** | 146.40 ns ±2% |
| Lua to Go scalar callback, 1,000 calls | **63.35 µs ±1%** | 100.33 µs ±<1% | 84.96 µs ±1% |
| Convert one reused 128-byte Go string and echo from Lua | **63.06 ns ±1%** | 83.18 ns ±1% | 144.20 ns ±2% |
| Pass prebuilt table and checksum in Lua | **318.3 ns ±1%** | 592.2 ns ±2% | 981.9 ns ±1% |
| Create, fill, pass, and checksum table | 2.228 µs ±4% | **1.408 µs ±4%** | 1.641 µs ±1% |

Median allocation traffic and allocation counts:

| Case | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Go to Lua scalars | **0 B / 0** | **0 B / 0** | 160 B / 5 |
| Lua to Go callback | **0 B / 0** | 29.17 KiB / 1,085 | 31.41 KiB / 4,005 |
| Convert and echo reused Go string | **0 B / 0** | 16 B / 1 | 176 B / 5 |
| Pass prebuilt table | **0 B / 0** | 40 B / 0 | 576 B / 57 |
| Create, fill, and pass table | **647 B / 6** | 1,384 B / 37 | 1,536 B / 89 |

The string row intentionally measures repeated conversion of one exact Go
backing. It is a useful public-boundary workload, but it is the cache's hot
case rather than a claim about fresh or equal-but-separately-built strings.

## Causal cache A/B

The cache commit `d9c1628` was compared with its exact parent `ecda50c` using
separately compiled test binaries. Variant order alternated each round. The
headline and prebuilt-Value cases use 20 one-second samples per variant; the
component, cold, and lifecycle cases use 15 samples with a 500 ms target.
Every timed steady-state case remains at 0 B/op and 0 allocs/op.
The exact revisions, build recipe, binary identities, runtime flags, and ABBA
schedule are recorded in the
[`cache A/B manifest`](cache-ab-manifest.md).

| Workload | Parent | Cache | Change |
| --- | ---: | ---: | ---: |
| Public echo, convert the same 128 B backing each call | 89.90 ns ±1% | **63.77 ns ±1%** | **−29.06%** |
| Public echo, reuse one prebuilt long `Value` | 63.02 ns ±<1% | **58.68 ns ±<1%** | **−6.87%** |
| String construction + ingress, same 128 B backing | 34.14 ns ±<1% | **10.94 ns ±<1%** | **−67.96%** |
| String construction + ingress, 4,096 unique backings | **37.60 ns ±1%** | 40.24 ns ±1% | +7.02% |
| String construction + ingress, 4,096 equal distinct backings | **36.79 ns ±1%** | 39.79 ns ±<1% | +8.15% |
| Public echo, 4,096 unique backings | **102.2 ns ±2%** | 107.2 ns ±1% | +4.89% |
| Public echo, 4,096 equal distinct backings | **97.41 ns ±1%** | 102.60 ns ±1% | +5.33% |

The cold public cases stop automatic semantic collection after admitting their
working set, isolating the recurring miss tax from collection scheduling.
Separately built equal strings miss by design, confirming that content equality
does not accidentally turn into an identity hit.

### First-use and collection-epoch overhead

| Lifecycle operation | Parent | Cache | Change |
| --- | ---: | ---: | ---: |
| Construct State, first long admission, close | **568.5 ns** | 599.3 ns | +5.42% |
| Allocated by that operation | **2,136 B / 11** | 2,216 B / 12 | **+80 B / +1** |
| Admit after each sweep to an empty attribution set | **333.8 ns** | 366.1 ns | +9.68% |
| Allocated by each empty-sweep/readmission epoch | **256 B / 2** | 336 B / 3 | **+80 B / +1** |

`runtimeState` shrinks from 352 to 344 structural bytes, but both occupy Go's
352-byte allocation class on this platform. The cache therefore has no
per-State physical saving to offset its lazy 80-byte
`attributedStringSet`. That allocation recurs when a completed sweep empties
the set and a later long string is admitted. The four recent slots do not grow
with string cardinality and duplicate roots already held by the authoritative
attribution map.

The evidence supports a bounded optimization for recurring host strings, not
a general claim that long-string ingress is faster. A cache-cold minimal echo
pays about 5%; real calls that perform more Lua work dilute that fixed few-
nanosecond tax. New caches elsewhere still need their own profile, natural
owner and lifetime, hit/miss workloads, and whole-operation benchmark.

## Memory suites

The cache does not change table layout or string content retention, so the
CBOR graph and controlled table-shape memory suites were not recollected here.
Their latest fresh-process results remain in the
[`2026-07-28`](../2026-07-28-darwin-arm64-m3-pro/) and
[`2026-08-05`](../2026-08-05-darwin-arm64-m3-pro/) archives.

## Raw evidence

| File | SHA-256 |
| --- | --- |
| [`go-benchmarks.txt`](go-benchmarks.txt) | `78cedf7a25989abba35aad688961b63e354cea3fd5c83e71fa6f95c1bb76efa6` |
| [`cache-echo-base.txt`](cache-echo-base.txt) | `872754cfa4de9933669c51ee5b5f9ec5d39f865ed0e674a43be51f91a2bc7bcd` |
| [`cache-echo-candidate.txt`](cache-echo-candidate.txt) | `b4542716b6ab42b84247b490aefdcf30625340349a45971da18dff327f3045b6` |
| [`cache-prebuilt-base.txt`](cache-prebuilt-base.txt) | `ac7336caa74b63fc42b6afa84f72bd7f0ac60d4ea5e81e3c7f930d0c7feffbc3` |
| [`cache-prebuilt-candidate.txt`](cache-prebuilt-candidate.txt) | `b86581d55f1e0ba676bd5f274c48ccfe1a116e0fcb3f696484865d3567fd8731` |
| [`cache-ingress-base.txt`](cache-ingress-base.txt) | `9774473f8b2a93e1bf40918fc2a5099d3b719fa47d5b1236692a3c5263f06eda` |
| [`cache-ingress-candidate.txt`](cache-ingress-candidate.txt) | `3cf76c3beb15589ded1a15a45733f09c2f6c84b179bd83ed6922c801105c0dc7` |
| [`cache-cold-embedding-base.txt`](cache-cold-embedding-base.txt) | `c4af87bfdc33e8bc0d48af9c53d2f05879a03786f413d25aef617fd9d3e7bd59` |
| [`cache-cold-embedding-candidate.txt`](cache-cold-embedding-candidate.txt) | `4af3d1ae050b09252069e959e32136c4dc80a4b9af17685afac0e4bb43d3aa51` |

The exact additional benchmark sources are retained as
[`cache-review-ingress-benchmark.go.txt`](cache-review-ingress-benchmark.go.txt),
[`cache-review-cold-embedding-benchmark.go.txt`](cache-review-cold-embedding-benchmark.go.txt),
and
[`cache-review-prebuilt-benchmark.go.txt`](cache-review-prebuilt-benchmark.go.txt).
The corresponding source SHA-256 values are, in that order,
`3ee552915897f4b0f3aa014b910c2f1846aff5ecc84942a116939f2824e15370`,
`6629fa862170246d36e32307ac1561c3bc47dff3ce2d5c434e5da2d4a5ec49bb`,
and `ef5131a7896897b614eac4255b3e7c89ee76c650cdc62c377097fecf9a026d0e`.

The canonical archive contains 15 observations for each of 39 program,
interpreter, and embedding runtime cells. The causal headline and prebuilt
files contain 20 observations per revision. The ingress files contain 15
observations for each of five component or lifecycle cases, and the cold
embedding files contain 15 observations for each of two cases.

Collection and analysis conventions are documented in
[`benchmarks/README.md`](../../README.md).
