# Runtime comparisons

This module compares Lunar, GopherLua, and Shopify go-lua with three separate
benchmark groups:

| Benchmark | Timed work |
| --- | --- |
| `BenchmarkPrograms` | Four established Lua programs, invoked once from Go |
| `BenchmarkInterpreter` | Numeric loops, Lua calls, table access, and string construction inside Lua |
| `BenchmarkEmbedding` | Go-to-Lua calls, Lua-to-Go callbacks, string conversion, and new or reused host-built tables |

The large synthetic CBOR graph has a separate fresh-process harness under
[`cbor/`](cbor/README.md). Retained memory from that harness is not combined
with the Go benchmark allocation counters. The same module carries the
controlled table-shape suite, which decomposes the CBOR retained-memory
result into single-variable cases and is recollected whenever table storage
or string interning changes.

Published measurement summaries are indexed under [`results/`](results/).
Benchmark source, fixtures, licenses and reusable runners live outside that
directory. Working output belongs in the repository's ignored `.bench/`
directory or another local output directory.

## Runtime versions

The module pins:

- Lunar to the adjacent checkout;
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
- raw output available locally while checking and reviewing the result.

The embedding table cases separate passing an already-built table from
creating, filling, and passing a new table. This keeps table access distinct
from construction and public-handle publication.

Every current row remains in the canonical suite. Rows with similar times
cover different execution paths and can still differ in allocation traffic.
Small time differences on one machine are reported as near parity rather than
as a general runtime ranking.

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
LUNAR_BENCH_POWER_POLICY='AC power; low-power mode off; otherwise idle' \
  ./run-comparison.sh /tmp/lunar-benchmarks.txt
```

Defaults are 15 samples and a 500 ms target per benchmark. They can be changed
explicitly:

```sh
LUNAR_BENCH_POWER_POLICY='AC power; low-power mode off; otherwise idle' \
LUNAR_BENCH_SAMPLES=20 LUNAR_BENCH_TIME=1s \
  ./run-comparison.sh /tmp/lunar-benchmarks.txt
```

The local output begins with the Git revision, Go and comparator versions, platform,
machine and CPU models, power policy, collection policy, sample count, and
benchmark duration.

If automatic hardware detection is unavailable, set
`LUNAR_BENCH_MACHINE_MODEL` and `LUNAR_BENCH_CPU_MODEL` to explicit descriptions
before collection.

## Statistical summaries

Use the pinned `benchstat` release to compute medians, confidence intervals,
and pairwise significance:

```sh
BENCHSTAT=golang.org/x/perf/cmd/benchstat@v0.0.0-20260709024250-82a0b07e230d

go run "$BENCHSTAT" \
  -filter '.name:Programs' \
  -row /program -col '/runtime@(lunar gopherlua golua)' \
  /tmp/lunar-benchmarks.txt

go run "$BENCHSTAT" \
  -filter '.name:Interpreter' \
  -row /case -col '/runtime@(lunar gopherlua golua)' \
  /tmp/lunar-benchmarks.txt

go run "$BENCHSTAT" \
  -filter '.name:Embedding' \
  -row /case -col '/runtime@(lunar gopherlua golua)' \
  /tmp/lunar-benchmarks.txt
```

`ns/op` measures the warm timed operation. `B/op` and `allocs/op` measure Go
allocation traffic during that operation; they do not measure live or retained
heap. Report every program row separately. Do not combine the program,
embedding, interpreter, and CBOR results into one score.

## Publishing a summary

Commit one `README.md` per dated directory under `results/`. Include:

- the measured baseline and candidate revisions, toolchain and comparator versions;
- the machine, execution settings, sample counts and timed workload;
- the results and uncertainty for every compared workload, including regressions,
  inconclusive differences and any separate repeat;
- allocation or retained-memory findings, correctness checks and limitations; and
- the collection and analysis commands, using the maintained benchmark tools.

Keep raw samples, profiles, disassembly, compiler logs, intermediate statistical
files and experimental scripts in `.bench/` or another local working directory.
Use them to validate the measurements before publishing the summary. Do not
commit those files, copies of source, binaries, or generated corpora under
`results/`. The ignore rules allow only the index and dated README summaries.
Historical supporting files removed during cleanup remain in Git history.

Update the root README tables from a complete reviewed measurement set. Preserve
earlier measurements in their dated summaries; label corrections explicitly.

## Optional PUC Lua 5.1 comparison

The `puc51` build tag adds PUC Lua to `BenchmarkPrograms`,
`BenchmarkInterpreter`, `BenchmarkDiagnostics`, and their correctness checks.
It uses the canonical sources, inputs, output oracles, warmup, and protected
calls. Build an unmodified
PUC Lua 5.1.5 static library with position-independent code, then point cgo at its
headers and archive. For example, from this module with the source unpacked at
`/tmp/lunar-puc-assessment/lua-5.1.5`:

```sh
export CGO_ENABLED=1
export CGO_CFLAGS='-I/tmp/lunar-puc-assessment/lua-5.1.5/src'
export CGO_LDFLAGS='/tmp/lunar-puc-assessment/lua-5.1.5/src/liblua.a -lm -ldl'
go test -tags puc51 ./...
go vet -tags puc51 ./...

GOGC=100 GOMEMLIMIT=off GOMAXPROCS=1 go test -tags puc51 -run '^$' \
  -bench '^(BenchmarkPrograms|BenchmarkInterpreter)$/.*$/^runtime=(lunar|puc51)$' \
  -benchtime=500ms -count=15 -cpu=1
```

This last command is a quick collection example. For publication, use
`run-puc-comparison.sh`: it checks a clean checkout, runs tests and vet, builds
once, and alternates runtime order in separate processes. Keep the cgo exports
above and supply the reference build metadata:

```sh
export LUNAR_PUC_SOURCE_SHA256='<SHA-256 of the Lua 5.1.5 source archive>'
export LUNAR_PUC_LIBRARY='/tmp/lunar-puc-assessment/lua-5.1.5/src/liblua.a'
export LUNAR_PUC_BUILD_FLAGS='<exact compiler and make flags used>'
LUNAR_BENCH_POWER_POLICY='AC power; otherwise idle' \
  ./run-puc-comparison.sh /tmp/lunar-puc-benchmarks.txt
```

The script includes the diagnostic probes, whose results must stay separate
from the canonical programs. It records the source release, archive and library
checksums, C compiler, build flags, and benchmark binary checksum alongside
the environment metadata. `LUNAR_BENCH_CPU` optionally pins execution to a
Linux CPU with `taskset`. The existing `run-comparison.sh` selects only the
three Go runtimes.

Each timed PUC operation pays one Go-to-C transition, a cached registry lookup,
and `lua_pcall(0, 0, 0)`; the complete Lua workload executes in C. Compilation,
library setup, result conversion, and cleanup remain outside timing. PUC keeps
its default garbage collector settings and native allocator. The forced Go GC
before timing only clears Go garbage; it does not collect PUC's heap.

Omit `-benchmem` for this comparison. The Go engines request their own allocation
metrics, while PUC omits them because Go's allocation counters cannot observe
C allocations. This adapter does not measure PUC live memory or allocation
traffic. Without `-tags puc51`, the benchmark module has no C dependency.

The [2026-09-07 PUC assessment](results/2026-09-07-linux-amd64-puc51/README.md)
summarizes the whole-program comparisons, candidate experiments and resulting
optimization priorities.
