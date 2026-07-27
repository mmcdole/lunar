# Runtime comparisons

This module compares Lugo, GopherLua, and Shopify go-lua with three separate
benchmark groups:

| Benchmark | Timed work |
| --- | --- |
| `BenchmarkPrograms` | Four established Lua programs, invoked once from Go |
| `BenchmarkInterpreter` | Numeric loops, Lua calls, table access, and string construction inside Lua |
| `BenchmarkEmbedding` | Go-to-Lua calls, Lua-to-Go callbacks, string exchange, and host-built tables |

The large synthetic CBOR graph has a separate fresh-process harness under
[`cbor/`](cbor/README.md). Retained memory from that harness is not combined
with the Go benchmark allocation counters.

## Runtime versions

The module pins:

- Lugo to the adjacent checkout;
- GopherLua to `v1.1.2`; and
- Shopify go-lua to
  `v0.0.0-20250718183320-1e37f32ad7d0`.

go-lua targets Lua 5.2. Every comparison fixture stays within the shared Lua
5.1/5.2 language subset.

## Measurement rules

Each runtime executes the same Lua source for a given row. The harness uses
protected calls and the closest equivalent public API in each runtime.
Compilation, source loading, library setup, and one warmup run occur before
timing. A forced Go garbage collection clears garbage from earlier
subbenchmarks before the timed loop. Results are consumed as required by the
host API and checked after both warmup and timing.

Each Go benchmark instance creates a new runtime state and reuses it during
`testing.B.Loop`. `B.Loop` excludes setup and cleanup from timing. Published
comparisons use:

- `GOGC=100`;
- `GOMEMLIMIT=off`;
- `GOMAXPROCS=1` and `-cpu=1`;
- at least 15 samples;
- a fixed benchmark duration;
- a clean, recorded Git revision; and
- raw Go benchmark output retained with the result.

The four program sources, local input sizes, output oracles, exact upstream
commit, file hashes, and license are recorded in
[`PROGRAMS.md`](PROGRAMS.md). The local inputs are scaled for Go interpreters;
the resulting numbers are not official Computer Language Benchmarks Game
scores.

## Collecting samples

Run correctness checks first:

```sh
cd benchmarks
go test ./...
go vet ./...
```

The collection script requires a clean checkout. It runs each runtime in a
separate `go test` process and rotates runtime order across rounds. Describe
the machine's power and background-load policy explicitly:

```sh
LUGO_BENCH_POWER_POLICY='AC power; low-power mode off; otherwise idle' \
  ./run-comparison.sh /tmp/lugo-benchmarks.txt
```

Defaults are 15 samples and a 500 ms target per benchmark. They can be changed
explicitly:

```sh
LUGO_BENCH_POWER_POLICY='AC power; low-power mode off; otherwise idle' \
LUGO_BENCH_SAMPLES=20 LUGO_BENCH_TIME=1s \
  ./run-comparison.sh /tmp/lugo-benchmarks.txt
```

The output begins with the Git revision, Go and comparator versions, platform,
machine and CPU models, power policy, collection policy, sample count, and
benchmark duration.

If automatic hardware detection is unavailable, set
`LUGO_BENCH_MACHINE_MODEL` and `LUGO_BENCH_CPU_MODEL` to explicit descriptions
before collection.

## Statistical summaries

Use the pinned `benchstat` release to compute medians, confidence intervals,
and pairwise significance:

```sh
BENCHSTAT=golang.org/x/perf/cmd/benchstat@v0.0.0-20260709024250-82a0b07e230d

go run "$BENCHSTAT" \
  -filter '.name:Programs' \
  -row /program -col '/runtime@(lugo gopherlua golua)' \
  /tmp/lugo-benchmarks.txt

go run "$BENCHSTAT" \
  -filter '.name:Interpreter' \
  -row /case -col '/runtime@(lugo gopherlua golua)' \
  /tmp/lugo-benchmarks.txt

go run "$BENCHSTAT" \
  -filter '.name:Embedding' \
  -row /case -col '/runtime@(lugo gopherlua golua)' \
  /tmp/lugo-benchmarks.txt
```

`ns/op` measures the warm timed operation. `B/op` and `allocs/op` measure Go
allocation traffic during that operation; they do not measure live or retained
heap. Report every program row separately. Do not combine the program,
embedding, interpreter, and CBOR results into one score.
