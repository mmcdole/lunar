# Integrated table lookup assessment

The measured change improves all four complete programs and CBOR save, with an unresolved CBOR load difference. A trivial-callback control is repeatedly 2.0–2.4% slower, about 1.2 ns per callback. The result supports review as a tradeoff; it fails the literal no-regression condition.

Baseline: `5fc51e449a6661340056544e995aae4fdf89dcb2`. Candidate: `22ad3f1e7b751031360705eea9338b5f0428a1e5`. Related: [issue #17](https://github.com/mmcdole/lunar/issues/17), [PR #18](https://github.com/mmcdole/lunar/pull/18), [rejected predecessor](../2026-09-07-linux-amd64-array-access/).

## Scope

Following PUC Lua 5.1.5's type-directed lookup, shared raw reads select the numeric array/hash path directly; dynamic Lua writes retain the resolved array location for replacement/insertion handling. Main already avoided hashing array hits. The change covers ordinary dynamic keys, all array sizes and value types, in two runtime functions (61 additions, 13 deletions), without caches, storage changes or public API changes.

Diagnostics on the predecessor found no existing-value array-write hits during CBOR load, versus 99.9972% in fannkuch; CBOR save's 2,387,931 dynamic reads all used string keys. Five paired load profiles sampled 8.86 s baseline and 8.96 s candidate. These observations motivated integrated dispatch but did not isolate the predecessor's slowdown. They do not measure this candidate's speed.

## Complete Go comparison

Medians of 15 samples per row; positive changes mean slower execution.

| Workload | Baseline | Candidate | Time change | benchstat p |
| --- | ---: | ---: | ---: | ---: |
| binary-trees | 135.539 ms | 131.692 ms | −2.84% | <.001 |
| fannkuch-redux | 18.230 ms | 14.463 ms | −20.66% | <.001 |
| n-body | 43.098 ms | 42.798 ms | −0.70% | <.001 |
| spectral-norm | 44.637 ms | 42.096 ms | −5.69% | <.001 |
| Numeric loop, 10,000 iterations | 36.996 µs | 37.023 µs | +0.07% | .775 |
| Fixed Lua calls, 1,000 calls | 24.511 µs | 24.559 µs | +0.20% | .631 |
| Table field get/set, 10,000 iterations | 184.345 µs | 184.202 µs | −0.08% | .412 |
| String append, 256 iterations | 34.881 µs | 34.664 µs | −0.62% | .389 |
| Go calls Lua with scalar arguments | 53.25 ns | 53.92 ns | +1.26% | <.001 |
| Lua calls Go 1,000 times | 50.572 µs | 51.600 µs | +2.03% | <.001 |
| Lua echoes a 128-byte Go string | 76.48 ns | 77.79 ns | +1.71% | .001 |
| Lua checksums a reused Go-built table | 267.0 ns | 229.8 ns | −13.93% | <.001 |
| Build a table in Go, then checksum it in Lua | 2.318 µs | 2.164 µs | −6.64% | .013 |

The interpreter differences are unresolved, not proven equivalent. All allocation-count medians are unchanged. Fannkuch increases from 1,168 to 1,190 allocated bytes per operation, with 15 allocations; other byte differences are identical or unresolved. Allocation traffic is not retained heap.

One predefined repeat used the same binaries and 15 fresh samples per row:

| Control | Full time change | Repeat baseline | Repeat candidate | Repeat time change |
| --- | ---: | ---: | ---: | ---: |
| Scalar arguments | +1.26% | 53.45 ns | 53.23 ns | −0.41% |
| 1,000 callbacks | +2.03% | 49.750 µs | 50.956 µs | +2.42% |
| String echo | +1.71% | 78.10 ns | 74.47 ns | −4.65% |

Callbacks regress again (p<.001), in both execution orders. Scalar calls are unresolved (p=.072); echo reverses (p<.001). All three allocate zero. The callback cost may matter to programs dominated by tiny Go calls; its cause was not isolated. Scalar/echo bodies do not access tables, and callbacks use the unchanged constant-string lookup. No code-placement mechanism is established.

## CBOR application

Each timing cohort contains 15 adjacent pairs in alternating order, following two warmup pairs. Intervals use 10,000 paired-bootstrap resamples, seed 1.

| Operation | Baseline | Candidate | Time change | 95% interval |
| --- | ---: | ---: | ---: | ---: |
| Load | 1755.430 ms | 1763.344 ms | +0.45% | −0.03% to +0.96% |
| Save | 1597.531 ms | 1575.076 ms | −1.41% | −1.90% to −0.21% |

Load is unresolved; this does not prove equivalence. The predecessor's 3.75% load regression did not recur in this timing cohort. Save improves modestly. These collections were exploratory because the Go control gate failed; successful validation does not qualify speed.

Separate three-pair retained cohorts stabilize heap before execution and before reading retained heap:

| Operation | Baseline heap increase | Candidate heap increase | Baseline elapsed | Candidate elapsed | Descriptive change |
| --- | ---: | ---: | ---: | ---: | ---: |
| Load | 75,721,136 B | 75,721,136 B | 1745.610 ms | 1788.586 ms | +2.46% |
| Save | 7,283,304 B | 7,283,304 B | 1617.595 ms | 1583.095 ms | −2.13% |

These elapsed values remain separate from speed qualification; save heap increase is above its loaded graph. Timing-save allocation medians rise from 317,001,568 to 317,056,160 bytes (+54,592) and 3,257,733 to 3,257,737 allocations. Retained-save allocation medians rise from 316,947,008 to 317,383,696 bytes and 3,257,729 to 3,257,761 allocations. Timing-load bytes are 107,475,832 versus 107,475,816, with 664,877 allocations each; retained-load allocation medians are identical at 107,474,712 bytes and 664,873 allocations.

The separate six-sample program pilot measured binary-trees −3.17%, fannkuch −20.78%, n-body −1.17% and spectral-norm −4.90%. Its six-pair CBOR pilot found load −0.002% (interval −0.95% to +0.48%) and save −1.44% (−2.96% to +1.91%), both unresolved. No cohorts are pooled.

## Published README timing values

These are the complete cohort's candidate/GopherLua/go-lua medians, including its slower host-call results rather than favorable repeat values. GopherLua v1.1.2; go-lua `1e37f32ad7d0`. The historical M3 retained-memory tables were not recollected.

| Workload | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | 131.69 ms | 153.78 ms | 159.24 ms |
| fannkuch-redux | 14.46 ms | 29.25 ms | 32.59 ms |
| n-body | 42.80 ms | 161.24 ms | 170.93 ms |
| spectral-norm | 42.10 ms | 144.71 ms | 133.44 ms |
| Go calls Lua with scalar arguments | 53.92 ns | 51.01 ns | 123.50 ns |
| Lua calls Go 1,000 times | 51.60 µs | 89.80 µs | 67.53 µs |
| Lua echoes a 128-byte Go string | 77.79 ns | 66.06 ns | 126.30 ns |
| Lua checksums a reused Go-built table | 229.8 ns | 519.7 ns | 846.7 ns |
| Build a table in Go, then checksum it in Lua | 2.164 µs | 1.250 µs | 1.453 µs |

## Conditions and validation

Go 1.26.0, Linux/amd64 under WSL2, Ryzen 9 9950X3D; serialized CPU 2 execution, GOMAXPROCS=1, GOGC=100, GOMEMLIMIT=off, test CPU=1. The Go comparison rotates four runtime lanes through 15 rounds: 60 processes, 44 rows, 660 samples, targeting 500 ms per row. No PUC lane is timed. Host power, frequency and external load were uncontrolled.

Setup, compilation and final checks are outside timing. CBOR times complete operations including file IO/traversal, excluding setup, save preload and verification. All 72 recorded CBOR processes passed the oracle for 9,208,046 bytes, 183,513 tables and 938,452 entries. Source/build/harness identities, sample inventories, order, hashes and statistics were independently validated.

Root tests, Lua conformance, vet, race, benchmark-module checks and both CBOR configurations passed. Numeric boundary, metamethod and reference-release tests also passed on unchanged main. Candidate execution checks ran on amd64. Compiler review found larger helper frames and removal of redundant direct-array bounds checks, without new local heap escapes; this does not establish the timing mechanism.

The [original supporting files](https://github.com/mmcdole/lunar/tree/56ed803906dd3d1e19119736e3c49780b798dbb1/benchmarks/results/2026-09-08-linux-amd64-integrated-table-lookup) preserve raw samples, exact hashes, patches, diagnostics and validation in Git history.
