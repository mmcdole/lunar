# Are We Fast Yet measurements — 2026-09-25

This collection adds the [Are We Fast Yet](https://github.com/smarr/are-we-fast-yet)
Lua suite to the comparison and supplies the root README timing tables. It
measured clean commit `ca3e5d1fab99391e81d0b5ac94b2a3e19e3bba9b`: main
`2e0416c` plus the suite, with no runtime changes.

On Are We Fast Yet, Lunar is the fastest of the three Go runtimes on all 13
programs: GopherLua is 2.42× slower and go-lua 3.85× slower by geometric
mean. PUC Lua 5.1.5 is 1.87× faster than Lunar, with the largest gaps on
object- and string-heavy programs (Json, CD, Storage, Richards) and the
smallest on arithmetic kernels (Mandelbrot, NBody).

## Are We Fast Yet

Medians of 15 samples; ± is benchstat's 95% interval around the median. Every
difference is significant at p < .001.

| Program | Inner iterations | Lunar | ± | GopherLua | go-lua |
| --- | ---: | ---: | ---: | ---: | ---: |
| Richards | 5 | 877.9 ms | 1% | 1,677.6 ms | 1,990.4 ms |
| DeltaBlue | 1,000 | 141.7 ms | 1% | 326.1 ms | 9,503.5 ms |
| Json | 25 | 937.6 ms | 1% | 1,681.7 ms | 1,981.0 ms |
| CD | 100 | 2,755 ms | 1% | 6,321 ms | 6,623 ms |
| Bounce | 200 | 927.3 ms | 0% | 2,211.0 ms | 2,854.5 ms |
| List | 500 | 604.2 ms | 1% | 1,258.7 ms | 1,397.7 ms |
| Mandelbrot | 500 | 489.8 ms | 0% | 1,697.5 ms | 2,566.3 ms |
| NBody | 250,000 | 1,539 ms | 1% | 5,730 ms | 8,165 ms |
| Permute | 250 | 536.1 ms | 1% | 1,398.0 ms | 1,725.3 ms |
| Queens | 400 | 504.8 ms | 1% | 1,183.9 ms | 1,537.2 ms |
| Sieve | 1,000 | 721.2 ms | 1% | 1,893.1 ms | 2,058.3 ms |
| Storage | 30 | 1,011 ms | 1% | 2,456 ms | 2,812 ms |
| Towers | 200 | 886.2 ms | 1% | 1,899.5 ms | 3,000.7 ms |

go-lua's DeltaBlue time grows quadratically with the problem size (0.35 s at
500, 1.6 s at 1,000, 9.9 s at 2,000, 38 s at 4,000 in local checks), while
Lunar and GopherLua scale linearly; the size was chosen to keep collection
practical.

### PUC Lua 5.1.5 comparison

A separate collection of the same commit with `run-puc-comparison.sh`:

| Program | Lunar | PUC Lua 5.1 | Lunar ÷ PUC |
| --- | ---: | ---: | ---: |
| Richards | 884.7 ms | 383.0 ms | 2.31× |
| DeltaBlue | 142.69 ms | 64.99 ms | 2.20× |
| Json | 956.9 ms | 343.8 ms | 2.78× |
| CD | 2,795 ms | 1,166 ms | 2.40× |
| Bounce | 939.7 ms | 460.5 ms | 2.04× |
| List | 613.7 ms | 305.4 ms | 2.01× |
| Mandelbrot | 479.8 ms | 407.0 ms | 1.18× |
| NBody | 1,523 ms | 1,165 ms | 1.31× |
| Permute | 547.0 ms | 385.3 ms | 1.42× |
| Queens | 519.5 ms | 365.5 ms | 1.42× |
| Sieve | 723.9 ms | 391.0 ms | 1.85× |
| Storage | 1,015.8 ms | 424.3 ms | 2.39× |
| Towers | 914.6 ms | 516.9 ms | 1.77× |
| Geometric mean | 757.8 ms | 405.6 ms | 1.87× |

PUC was built from the verified `lua-5.1.5.tar.gz` with `make posix`
(`-O2 -Wall`, gcc 16.2.1) and runs in-process through cgo, one transition per
operation.

## Benchmarks Game programs

| Program | Lunar | ± | GopherLua | go-lua |
| --- | ---: | ---: | ---: | ---: |
| binary-trees | 271.1 ms | 1% | 323.7 ms | 345.1 ms |
| fannkuch-redux | 27.73 ms | 1% | 60.38 ms | 73.63 ms |
| n-body | 86.26 ms | 0% | 337.07 ms | 440.58 ms |
| spectral-norm | 81.22 ms | 1% | 311.41 ms | 348.86 ms |

Spectral-norm measured 73.15 ms in the morning collection of `186cb17`
([summary](../2026-09-25-linux-amd64-hosaka/)). The interleaved checks of the
changes merged in between showed no spectral-norm effect, and this run uses a
different test binary, so the difference most likely reflects code placement,
which moves these benchmarks by up to 10–15% on this machine. It was not
investigated further.

## Embedding and interpreter controls

These rows are no longer in the root README; they remain in the harness.

| Case | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Go to Lua, two scalar arguments and results | 151.5 ns | 125.2 ns | 287.1 ns |
| Lua to Go scalar callback, 1,000 calls | 104.0 µs | 202.0 µs | 185.8 µs |
| Convert and echo a 128-byte Go string | 196.5 ns | 137.7 ns | 279.4 ns |
| Pass prebuilt table and checksum | 501.9 ns | 937.1 ns | 1,959.0 ns |
| Create, fill, pass and checksum table | 3.946 µs | 2.914 µs | 3.439 µs |
| Numeric loop, 10,000 iterations | 61.31 µs | 536.61 µs | 623.26 µs |
| Fixed Lua calls, 1,000 calls | 40.65 µs | 132.38 µs | 134.79 µs |
| Table field get/set, 10,000 iterations | 278.1 µs | 1,542.4 µs | 2,165.0 µs |
| String append, 256 iterations | 85.47 µs | 96.82 µs | 138.89 µs |

## Allocation

Lunar allocates far less than either Go runtime on every Are We Fast Yet
program, from 1.8× fewer allocations (Queens, against GopherLua) to none at
all (Mandelbrot). Median allocations per operation, Lunar / GopherLua /
go-lua: Richards 8,211 / 1,475,587 / 10,568,801; CD 5.121 M / 25.79 M /
24.84 M; Json 474.5 k / 3.263 M / 8.923 M; Storage 368.7 k / 6.457 M /
26.40 M. Allocation traffic is not retained memory.

## Method and limitations

- The 13 programs are vendored unchanged from upstream commit
  `74306fec151070fd07157cefeacf19e7e0bcdc89`; provenance is in
  [`PROGRAMS.md`](../../PROGRAMS.md).
- Lua 5.1 has no bitwise operators, so `benchmarks/awfy/bit.lua`, a pure-Lua
  stand-in for LuaJIT's `bit` module, supplies them. Every runtime runs the
  same stand-in; Richards and the `som` hash functions pay for it.
- Each program's modules are bundled into one chunk with a local `require`,
  so all runtimes load identical code with only the base, string and math
  libraries. Each timed operation calls `inner_benchmark_loop` once and
  checks the program's own verification.
- Havlak is omitted: its smallest verified size takes about 6 s on PUC Lua.
- Inner iteration counts are scaled for interpreters, so these numbers are
  not comparable to published Are We Fast Yet results.

## Collection controls

- Go 1.27.1 (`X:nodwarf5`), Linux/amd64 (CachyOS, kernel 7.2.5), MINIPC PN50,
  AMD Ryzen 7 4800U.
- GopherLua v1.1.2; go-lua `v0.0.0-20250718183320-1e37f32ad7d0`.
- `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`, 500 ms per
  benchmark, no CPU affinity; AC power (no battery), `schedutil` governor, no
  other benchmark load.
- 15 rounds with rotating runtime order in separate `go test` processes:
  1,170 samples for the Go runtimes and 960 for the PUC comparison.
- All 13 programs passed their verification on all four runtimes before
  collection.

```sh
cd benchmarks
LUNAR_BENCH_POWER_POLICY='AC power (no battery); schedutil governor; no other benchmark load' \
  ./run-comparison.sh ~/lunar-perf/awfy-go-2026-09-25.txt

BENCHSTAT=golang.org/x/perf/cmd/benchstat@v0.0.0-20260709024250-82a0b07e230d
go run "$BENCHSTAT" -filter '.name:AWFY' -row /program \
  -col '/runtime@(lunar gopherlua golua)' ~/lunar-perf/awfy-go-2026-09-25.txt
```
