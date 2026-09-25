# README measurements on a Ryzen 7 4800U — 2026-09-25

This collection supplied the root README timing tables after the interpreter
changes in [PR #37](https://github.com/mmcdole/lunar/pull/37),
[PR #38](https://github.com/mmcdole/lunar/pull/38) and
[PR #39](https://github.com/mmcdole/lunar/pull/39). It measured clean main
`186cb17292350990c3e072e082ed3ab5e9752c35`.

It also moves the README to a different machine. The previous tables came
from an AMD Ryzen 9 9950X3D under WSL2
([2026-09-08 summary](../2026-09-08-linux-amd64-integrated-table-lookup/)).
This machine is roughly half as fast, so the two sets of tables are not
comparable to each other. The effect of each change was measured separately
in its PR against the same main commit on this machine.

## Established Lua programs

Medians of 15 samples; the ± column is benchstat's 95% interval around the
Lunar median.

| Program | Lunar | ± | GopherLua | go-lua |
| --- | ---: | ---: | ---: | ---: |
| binary-trees | 268.9 ms | 1% | 316.6 ms | 333.6 ms |
| fannkuch-redux | 26.72 ms | 1% | 60.42 ms | 72.42 ms |
| n-body | 98.13 ms | 2% | 339.40 ms | 432.98 ms |
| spectral-norm | 73.15 ms | 2% | 310.65 ms | 314.38 ms |

## Embedding boundary

| Case | Lunar | ± | GopherLua | go-lua |
| --- | ---: | ---: | ---: | ---: |
| Go to Lua, two scalar arguments and results | 154.9 ns | 1% | 122.8 ns | 278.3 ns |
| Lua to Go scalar callback, 1,000 calls | 105.9 µs | 0% | 201.0 µs | 186.6 µs |
| Convert and echo a 128-byte Go string | 198.3 ns | 1% | 133.4 ns | 270.8 ns |
| Pass prebuilt table and checksum | 488.7 ns | 1% | 942.4 ns | 1.918 µs |
| Create, fill, pass and checksum table | 3.755 µs | 5% | 2.846 µs | 3.293 µs |

GopherLua remains faster for Go-to-Lua calls, string echo, and building a
table in Go. Every difference in both tables is significant at p < .001.

## Interpreter controls

These rows are not in the root README.

| Case | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Numeric loop, 10,000 iterations | 62.11 µs | 538.59 µs | 585.77 µs |
| Fixed Lua calls, 1,000 calls | 36.90 µs | 136.09 µs | 126.21 µs |
| Table field get/set, 10,000 iterations | 356.4 µs | 1,576.1 µs | 2,106.0 µs |
| String append, 256 iterations | 80.92 µs | 91.88 µs | 128.98 µs |

## Allocation

Lunar's median allocation traffic and counts per operation:

- binary-trees: 65.80 MiB, about 1.009 million.
- fannkuch-redux: 1.230 KiB, 15.
- n-body: 2.883 KiB, 25.
- spectral-norm: 27.95 KiB, 35.
- Table construction embedding case: 648 B, 6.
- String append: 271.7 KiB, 255.
- Every other embedding and interpreter case: 0 B, 0.

The program allocation counts match the previous collection. Table
construction was 632 B and 6 allocations there. Allocation traffic is not retained memory. The README's Apple M3
retained-heap tables come from their separately identified historical
collections.

## Collection controls

- Go 1.27.1 (`X:nodwarf5`), Linux/amd64 (CachyOS, kernel 7.2.5), MINIPC PN50,
  AMD Ryzen 7 4800U.
- GopherLua v1.1.2; go-lua `v0.0.0-20250718183320-1e37f32ad7d0`.
- `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`, 500 ms per
  benchmark, no CPU affinity.
- AC power (no battery), `schedutil` governor, no other benchmark load.
  Frequency was not fixed.
- Runtime order rotated across the 15 rounds in separate `go test` processes,
  producing 585 samples.
- The benchmark module's tests and vet passed on this revision before
  collection.

Collection and analysis:

```sh
cd benchmarks
LUNAR_BENCH_POWER_POLICY='AC power (no battery); schedutil governor; no other benchmark load' \
  ./run-comparison.sh ~/lunar-perf/readme-2026-09-25.txt

BENCHSTAT=golang.org/x/perf/cmd/benchstat@v0.0.0-20260709024250-82a0b07e230d
go run "$BENCHSTAT" -filter '.name:Programs' -row /program \
  -col '/runtime@(lunar gopherlua golua)' ~/lunar-perf/readme-2026-09-25.txt
go run "$BENCHSTAT" -filter '.name:Embedding' -row /case \
  -col '/runtime@(lunar gopherlua golua)' ~/lunar-perf/readme-2026-09-25.txt
go run "$BENCHSTAT" -filter '.name:Interpreter' -row /case \
  -col '/runtime@(lunar gopherlua golua)' ~/lunar-perf/readme-2026-09-25.txt
```
