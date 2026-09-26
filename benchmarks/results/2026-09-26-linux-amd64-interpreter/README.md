# Interpreter improvements — 2026-09-26

This collection supplies the root README timing tables after four interpreter
changes: direct calls for small library builtins
([#48](https://github.com/mmcdole/lunar/pull/48)), `__index` chains and string
equality inside the interpreter loop
([#50](https://github.com/mmcdole/lunar/pull/50)), pointer-free nil and
boolean slots ([#52](https://github.com/mmcdole/lunar/pull/52)), and keeping
the loop's hot state in registers
([#54](https://github.com/mmcdole/lunar/pull/54)). It measured clean main
`ddf12734ee7e96c2143a8d9bcf1a9f84e881810b` on the machine and protocol of the
[previous collection](../2026-09-25-linux-amd64-are-we-fast-yet/), which
measured `ca3e5d1` before these changes.

Lunar's Are We Fast Yet geometric mean fell from 757.8 ms to 592.4 ms against
the PUC Lua 5.1.5 reference (−22%), and its gap to PUC narrowed from 1.87× to
1.46×. GopherLua is now 3.0× and go-lua 4.8× slower than Lunar by geometric
mean on the same suite.

## Are We Fast Yet

Medians of 15 samples. Every difference is significant at p < .001.

| Program | Lunar | ± | GopherLua | go-lua | Lunar at ca3e5d1 |
| --- | ---: | ---: | ---: | ---: | ---: |
| Richards | 595.0 ms | 1% | 1,677.2 ms | 1,990.8 ms | 877.9 ms |
| DeltaBlue | 124.5 ms | 1% | 325.6 ms | 9,569.5 ms | 141.7 ms |
| Json | 651.0 ms | 2% | 1,670.2 ms | 1,982.6 ms | 937.6 ms |
| CD | 2,541 ms | 1% | 6,314 ms | 6,645 ms | 2,755 ms |
| Bounce | 542.7 ms | 1% | 2,143.7 ms | 2,874.0 ms | 927.3 ms |
| List | 528.1 ms | 2% | 1,247.2 ms | 1,401.1 ms | 604.2 ms |
| Mandelbrot | 367.7 ms | 1% | 1,681.1 ms | 2,563.6 ms | 489.8 ms |
| NBody | 1,363 ms | 0% | 5,631 ms | 8,151 ms | 1,539 ms |
| Permute | 586.5 ms | 1% | 1,387.6 ms | 1,740.5 ms | 536.1 ms |
| Queens | 467.9 ms | 1% | 1,187.8 ms | 1,527.9 ms | 504.8 ms |
| Sieve | 561.5 ms | 1% | 1,899.3 ms | 2,064.2 ms | 721.2 ms |
| Storage | 519.3 ms | 2% | 2,440.1 ms | 2,801.2 ms | 1,011 ms |
| Towers | 899.0 ms | 1% | 1,925.1 ms | 3,050.1 ms | 886.2 ms |

Permute is 9% slower than at `ca3e5d1` and Towers 1% slower. #48 and #50
enlarged `runInstructions`, which raised spills across the whole dispatch
loop; #54 recovered most of that but not all of it for Permute, whose time
goes mostly to calls and returns.

### PUC Lua 5.1.5 comparison

A separate collection of the same commit with `run-puc-comparison.sh`:

| Program | Lunar | PUC Lua 5.1 | Lunar ÷ PUC | At ca3e5d1 |
| --- | ---: | ---: | ---: | ---: |
| Richards | 593.9 ms | 382.1 ms | 1.55× | 2.31× |
| DeltaBlue | 125.36 ms | 64.48 ms | 1.94× | 2.20× |
| Json | 647.7 ms | 343.7 ms | 1.88× | 2.78× |
| CD | 2,528 ms | 1,154 ms | 2.19× | 2.40× |
| Bounce | 533.0 ms | 461.3 ms | 1.16× | 2.04× |
| List | 538.9 ms | 306.0 ms | 1.76× | 2.01× |
| Mandelbrot | 371.0 ms | 399.8 ms | 0.93× | 1.18× |
| NBody | 1,356 ms | 1,176 ms | 1.15× | 1.31× |
| Permute | 532.4 ms | 387.2 ms | 1.38× | 1.42× |
| Queens | 459.7 ms | 365.0 ms | 1.26× | 1.42× |
| Sieve | 556.8 ms | 386.2 ms | 1.44× | 1.85× |
| Storage | 509.0 ms | 423.8 ms | 1.20× | 2.39× |
| Towers | 905.2 ms | 538.6 ms | 1.68× | 1.77× |
| Geometric mean | 592.4 ms | 405.8 ms | 1.46× | 1.87× |

Lunar is now faster than PUC Lua on Mandelbrot, and on the Benchmarks Game
fannkuch-redux and n-body below. The largest remaining gaps
are CD, DeltaBlue and Json, which allocate many small tables and make many
Lua-to-Lua calls.

## Benchmarks Game programs

| Program | Lunar | ± | GopherLua | go-lua | Lunar ÷ PUC |
| --- | ---: | ---: | ---: | ---: | ---: |
| binary-trees | 250.5 ms | 1% | 322.2 ms | 344.0 ms | 2.10× |
| fannkuch-redux | 26.57 ms | 1% | 62.56 ms | 73.84 ms | 0.97× |
| n-body | 63.94 ms | 1% | 335.20 ms | 444.98 ms | 0.87× |
| spectral-norm | 66.49 ms | 1% | 312.13 ms | 344.02 ms | 1.13× |

The ratios to PUC come from the PUC collection's own Lunar medians.

## Embedding and interpreter controls

| Case | Lunar | GopherLua | go-lua | PUC Lua 5.1 |
| --- | ---: | ---: | ---: | ---: |
| Go to Lua, two scalar arguments and results | 144.4 ns | 123.5 ns | 289.5 ns | |
| Lua to Go scalar callback, 1,000 calls | 113.2 µs | 201.6 µs | 183.8 µs | |
| Convert and echo a 128-byte Go string | 198.3 ns | 127.0 ns | 281.1 ns | |
| Pass prebuilt table and checksum | 475.6 ns | 948.6 ns | 1,946.0 ns | |
| Create, fill, pass and checksum table | 3.789 µs | 2.941 µs | 3.444 µs | |
| Numeric loop, 10,000 iterations | 60.32 µs | 530.20 µs | 625.76 µs | 48.14 µs |
| Fixed Lua calls, 1,000 calls | 35.38 µs | 135.28 µs | 133.35 µs | 30.86 µs |
| Table field get/set, 10,000 iterations | 242.0 µs | 1,524.2 µs | 2,149.0 µs | 247.4 µs |
| String append, 256 iterations | 85.53 µs | 96.26 µs | 139.53 µs | 76.76 µs |

The PUC column comes from the separate PUC collection.

## Allocation

Allocation counts per operation match `ca3e5d1` on every program to within
0.1%; none of these changes allocates. Lunar / GopherLua / go-lua:
Richards 8,211 / 1,475,587 / 10,568,801; Json 474.6 k / 3.263 M / 8.923 M;
CD 5.121 M / 25.79 M / 24.84 M; Storage 368.7 k / 6.457 M / 26.40 M;
binary-trees 1.009 M / 1.010 M / 2.534 M.

## Collection controls

- Go 1.27.1 (`X:nodwarf5`), Linux/amd64 (CachyOS, kernel 7.2.5), MINIPC PN50,
  AMD Ryzen 7 4800U.
- GopherLua v1.1.2; go-lua `v0.0.0-20250718183320-1e37f32ad7d0`; PUC Lua
  5.1.5 from the verified source archive, `make posix` (`-O2 -Wall`, gcc
  16.2.1), linked in-process through cgo.
- `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`, 500 ms per
  benchmark, no CPU affinity; AC power (no battery), `schedutil` governor, no
  other benchmark load.
- 15 rounds with rotating runtime order in separate `go test` processes:
  1,170 samples for the Go runtimes and 960 for the PUC comparison.

```sh
cd benchmarks
LUNAR_BENCH_POWER_POLICY='AC power (no battery); schedutil governor; no other benchmark load' \
  ./run-comparison.sh ~/lunar-perf/go-ddf1273.txt

BENCHSTAT=golang.org/x/perf/cmd/benchstat@v0.0.0-20260709024250-82a0b07e230d
go run "$BENCHSTAT" -filter '.name:AWFY' -row /program \
  -col '/runtime@(lunar gopherlua golua)' ~/lunar-perf/go-ddf1273.txt
```
