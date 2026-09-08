# Native-call README measurements — 2026-09-07

This collection supplied the root README timing tables for the native-call
change in [PR #15](https://github.com/mmcdole/lunar/pull/15), subsequently
merged as `5fc51e449a6661340056544e995aae4fdf89dcb2`. It measured clean candidate
`a2f32740110096c4eee57e7ec90ede31b78236c6`, based on main
`d79e0a39f8e4bd14dbf581cf204431cf24b0d073`.

## Established Lua programs

| Program | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | 138.976 ms | 157.084 ms | 165.418 ms |
| fannkuch-redux | 18.491 ms | 29.652 ms | 33.288 ms |
| n-body | 43.977 ms | 161.435 ms | 176.444 ms |
| spectral-norm | 45.470 ms | 145.046 ms | 136.129 ms |

## Embedding boundary

| Case | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Go to Lua, two scalar arguments and results | 54.15 ns | 52.51 ns | 127.60 ns |
| Lua to Go scalar callback, 1,000 calls | 51.148 µs | 92.473 µs | 70.922 µs |
| Convert and echo a 128-byte Go string | 76.31 ns | 68.43 ns | 129.30 ns |
| Pass prebuilt table and checksum | 272.7 ns | 503.4 ns | 859.1 ns |
| Create, fill, pass and checksum table | 2.219 µs | 1.302 µs | 1.478 µs |

All 27 cells are medians of 15 samples, each with a 500 ms target. Three
fresh runtime processes rotated order across rounds, producing 405 samples.
Small differences on one machine should be read as near parity.

The same Lunar executable passed the separate
[13-case comparison against its main baseline](../2026-09-07-linux-amd64-native-entry-main/README.md).
That paired comparison found n-body −7.10% and callbacks −26.76%, with no
statistically significant slowdown among its program, interpreter and
embedding cases. Those percentages do not come from comparing successive
README tables.

Lunar's median allocation traffic/counts were 68,951,952 B / 1,009,045 for
binary-trees, 1,173 B / 15 for fannkuch, 2,799 B / 25 for n-body, and
28,701 B / 35 for spectral-norm. Table construction used 632 B / 6;
the other four embedding operations used 0 B / 0. Allocation traffic is not
retained memory. This change adds no storage; the README's Apple M3 retained
heap tables came from their separately identified historical collection.

## Collection controls

- Go 1.26.0, Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D.
- GopherLua v1.1.2; go-lua `v0.0.0-20250718183320-1e37f32ad7d0`.
- `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`, CPU 2 affinity.
  Timing workloads ran serially; host power, frequency and external load
  were uncontrolled.
- `B.Loop` timed the warm operation. Preparation, compilation, warmup and
  final validation were excluded; required host result consumption was timed.
- Benchstat `v0.0.0-20260709024250-82a0b07e230d`; no aggregate acceptance score.
- The executable used the validated PUC-enabled harness, but this collection
  timed only the three Go runtimes. Runtime/conformance, race, vet, benchmark
  and CBOR checks passed for the underlying change.

The [original collection records](https://github.com/mmcdole/lunar/tree/5fc51e449a6661340056544e995aae4fdf89dcb2/benchmarks/results/2026-09-07-linux-amd64-native-calls) remain available in Git history. This directory retains the result summary.
