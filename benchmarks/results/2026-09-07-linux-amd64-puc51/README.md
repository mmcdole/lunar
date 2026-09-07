The strongest measured opportunities are removing repeated execution-state loads
from table access and bypassing repeated layout planning for ordinary native
calls. Both operate on normal interpreter paths without introducing caches.
The small string/POW changes did not materially improve the established programs.

This is an assessment with isolated candidate patches. The working checkout's
runtime implementation is unchanged; the reusable PUC comparison adapter,
diagnostic fixtures, raw measurements, profiles, and candidate patches are added.

The primary comparator is unmodified **PUC Lua 5.1.5**, built with GCC 15.2.0 at
`-O2`, default double numbers, and no JIT. This matches Lunar's Lua 5.1 semantics
more closely than later PUC releases with integer arithmetic. Results here are
not measurements of Lua 5.4 or 5.5.

The [official source archive](https://www.lua.org/ftp/) has SHA-256
`2640fc56a795f29d28ef15e13c34a47e223960b0240e8cb0a82d9b0738695333`.
The exact build output is retained in [puc-build.txt](puc-build.txt).

**Baseline program results.** Medians of 15 samples, 500 ms target per sample;
loading, compilation, library setup, one warmup, and output validation are outside
timing. Every row uses the canonical source, input, and output oracle from
[PROGRAMS.md](../../PROGRAMS.md).

| Program | Input | Lunar | PUC 5.1.5 | Lunar / PUC time |
| --- | ---: | ---: | ---: | ---: |
| Binary-trees | depth 12 | 141.03 ms | 59.30 ms | 2.38× |
| Fannkuch-redux | n = 8 | 20.22 ms | 11.91 ms | 1.70× |
| N-body | 20,000 steps | 53.39 ms | 23.95 ms | 2.23× |
| Spectral-norm | n = 150 | 45.29 ms | 24.81 ms | 1.83× |

The [raw comparison](comparison.txt) and
[program statistics](comparison-Programs.benchstat.txt) include confidence
intervals and allocation traffic. There is no aggregate runtime score.
These programs cover recursion/allocation, array mutation, record access/native
math calls, and numeric work/Lua calls; they do not establish the distribution of
work in an arbitrary embedding application.

**Whole-program experiments.** Each candidate starts from the same unchanged
runtime revision. Comparisons use fresh baseline samples collected alongside the
candidate, with rotated process order. Tiny changes are treated as near parity,
even when a statistical test labels a sub-1% difference significant.

**First choice: pass existing executor locals into all raw table helpers.**
[all_table_locals.patch](patches/all_table_locals.patch) removes activation copies
and repeated loads of the current values, function, and register base. It changes
neither lookup algorithms nor storage, and adds no cache. This is the strongest
small, broadly useful candidate: **8.1% less time in fannkuch and 10.3% less in
n-body**, with binary-trees and spectral-norm near parity.

**Second choice: avoid replanning fixed native calls that already fit.**
[native-entry.patch](patches/native-entry.patch) proves argument/result windows,
frame capacity, and current resource limits before using existing activation
publication. Misses take the unchanged checked path. Native execution and public
Frame checks remain intact. On its own it reduced n-body time **8.1%**; combined
with the table-helper change, n-body improves **16.4%** and fannkuch **6.4%**.
These are measured combinations, not added estimates.

| Program | Paired baseline | All table helpers use locals | Table locals + native entry |
| --- | ---: | ---: | ---: |
| Binary-trees | 139.5 ms | 137.4 ms; −1.5%, near parity | 139.8 ms; near parity |
| Fannkuch-redux | 20.28 ms | **18.64 ms; −8.1%** | **18.98 ms; −6.4%** |
| N-body | 53.67 ms | **48.13 ms; −10.3%** | **44.85 ms; −16.4%** |
| Spectral-norm | 45.76 ms | 45.72 ms; near parity | 45.58 ms; near parity |

The final comparison has 15 samples for every cell. See its
[raw data](combined_hot_paths-stack_clear-all_table-broad.txt),
[program statistics](final-Programs.benchstat.txt), and
[interpreter checks](final-Interpreter.benchstat.txt).
The baseline differs slightly from the earlier PUC comparison because this is a
new interleaved collection; use each candidate's contemporaneous baseline.
Allocation counts are effectively unchanged, and neither candidate introduces
additional retained storage.

The isolated native-entry build had an unresolved **6.95% slowdown** in the fixed
Lua-call microbenchmark, despite its n-body gain. The final combined build has
near-parity fixed Lua-call results and no material slowdown in the four programs.
[Disassembly inspection](native-entry-disassembly.json) found identical normalized
executor and Lua-call-helper instructions in the isolated baseline/native builds;
code addresses and the checked driver's frame differ. That is not evidence that
code placement caused the regression. Favor the table-helper patch first and
recheck the exact native-entry combination intended for deployment.

The other candidates did not qualify:

| Experiment | Whole-program finding | Decision |
| --- | --- | --- |
| String-field helpers only | N-body −10.33%; other programs near parity | Broadened mechanically to all table helpers, producing the first-choice patch |
| Separate ADD/SUB/MUL/DIV/MOD dispatch cases | Spectral-norm **+2.64% slower**; other programs near parity; fixed-call microbenchmark +16.82% | Reject |
| Conditional nil writes and bulk dead-stack clearing | Binary-trees **+3.10% slower**, spectral-norm **+3.09% slower**; n-body −2.19% | Reject |
| Small string/POW fixes together | Every program within 1% of baseline | Lower priority; not a demonstrated broad speed gain |

The [isolated program](isolated-Programs.benchstat.txt) and
[interpreter](isolated-Interpreter.benchstat.txt) statistics retain the rejected
results as well as the successful ones.

**What the real-program profiles show.** Profiles include setup and warmup as
well as approximately three seconds of timed work; percentages are approximate
attribution, not predicted attainable speedups. Cumulative paths can overlap.

- In [n-body](profiles/nbody.cpu.txt), string-field get/set helpers account for
  21.23% and 20.11% cumulative CPU; `pushFunctionCall` and `invokeNativeCall`
  account for 11.45% each. The narrow native-result-reservation function alone
  is only 0.84%, so optimizing that check is a poor first target.
- In [spectral-norm](profiles/spectralnorm.cpu.txt), the executor itself is
  48.20% flat CPU, with operand selection, array reads, and fixed Lua call/return
  machinery contributing further cost. Merely copying PUC's split arithmetic
  cases worsened this program, so the C implementation's code structure is not
  automatically a Go optimization.
- In [binary-trees](profiles/binarytrees.cpu.txt), Go write-barrier work is
  23.27% cumulative and allocation work 16.62%. Semantic heap measurement is
  only 1.66%. Its [allocation profile](profiles/binarytrees.alloc_objects.txt)
  attributes 67.55% of allocation count to table headers and 32.42% to array
  backing. The workload constructs 674,478 tables and 334,510 two-slot arrays:
  1,008,988 constructor allocations explain almost all of the roughly 1,009,044
  observed Go allocations per operation. Allocation traffic is about 65.7 MiB/op.

After the measured candidates, table construction and pointer-write traffic are
the strongest structural investigation for allocation-heavy programs. A table
header plus small-array allocation could remove a large fraction of constructor
allocations, but its speedup is unmeasured. It needs careful ownership work:
abandoned inline slots must release pointers after growth, trailing storage must
remain accounted, and weak-reference identity must survive. This is a subsequent
experiment, not an already-qualified small change. A narrower constructor path
could avoid initializing appended SETLIST slots to nil immediately before
writing their final values, provided holes and sparse-key migration remain correct.

**Why the diagnostic gaps are not the priority list.**
The [diagnostic statistics](comparison-Diagnostics.benchstat.txt) show large
Lunar/PUC gaps for native sqrt (5.33×), ipairs (4.04×), pairs (4.20×), Lua
iteration (3.96×), repeated short concat (2.60×), identity substring (3.62×),
and explicit live-graph collection (3.36×). These locate costs; they do not prove
that reducing those costs helps a normal program.

The tested small-fixes group combined a numeric POW fast path, short-concat
stack scratch before the existing string-pool lookup, and identity-preserving
`string.sub`. It reduced some allocation counts, but every full-program time
remained within 1% of baseline. The [15-sample result](small_fixes-Programs.benchstat.txt)
therefore disqualifies that group as a high-impact performance priority.
The identity substring still merits a separate allocation-accounting fix:
eight full-range calls on a 64 KiB string incorrectly add 512 KiB of debt while
retaining the original backing. That correctness/accounting observation is
separate from a demonstrated application speedup.

The diagnostic `call_only` row includes its Lua wrapper, globals, and `tostring`;
it is not an isolated cgo transition measurement. Its PUC time is roughly 147 ns
for the complete operation, which also bounds the single cgo transition's
contribution to the much longer benchmark rows. PUC allocation counters are
omitted: Go `B/op` and `allocs/op` cannot observe the C heap. No PUC retained-memory
ranking is asserted.

**PUC techniques assessed.** PUC keeps register state directly available in its
[interpreter](https://www.lua.org/source/5.1/lvm.c.html) and uses compact native
call setup in [luaD_precall](https://www.lua.org/source/5.1/ldo.c.html). The winning
experiments transfer those principles while retaining Lunar's checked fallbacks,
Frame validation, resource limits, cancellation, error handling, and collection
seams. PUC also separates numeric arithmetic by opcode; the experiment here shows
that this particular arrangement is not beneficial under this Go compiler.

Lunar already has chained-scatter tables, array density sizing, prehashed field
keys, missing-metamethod flags, cached executor locals, and fixed Lua call/return
fast paths. They are not missing techniques to add. The relevant PUC references
are its [table implementation](https://www.lua.org/source/5.1/ltable.c.html) and
[collector](https://www.lua.org/source/5.1/lgc.c.html).

**Collection and reproduction.**

- Runtime base: `4f7e8928939271bbdf700998c837da42e9b93544`.
- Clean harness snapshot: `ff0795d3cba05772b07633430d1d34bc0feb9850`, created
  in a private `/tmp` clone. [harness.patch](harness.patch) is its exact diff
  against the base. It contains no runtime implementation changes.
- Go 1.26.0, linux/amd64, AMD Ryzen 9 9950X3D exposed through WSL2.
- `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`; timing processes
  serialized and pinned to logical CPU 2. No concurrent benchmark workloads;
  some preparation tests/builds ran on other guest CPUs.
- Host power policy, frequency, and external host load were not controlled.
  This is one virtualized-machine assessment. Treat very small differences as
  parity and repeat the promising changes on target deployment hardware.
- Statistics use the repository-pinned benchstat
  `v0.0.0-20260709024250-82a0b07e230d`. Its raw reports contain automatically
  generated geomeans; those are not used as an acceptance criterion or ranking.

Use [run-puc-comparison.sh](../../run-puc-comparison.sh) with the documented cgo
and provenance environment variables. The archived harness records the collector
used for this run; the current script additionally checks cgo availability and
per-process row counts. Raw data was independently checked for all 15 samples of
all 19 rows in both runtimes. Across the comparison and seven candidate
configurations, the archive contains 1,770 measured samples. Run
[validate-archive.py](validate-archive.py) to recheck sample completeness and
recorded patch hashes; its output is [archived here](archive-validation.json).

[run-broad.py](run-broad.py) records the exact candidate build/validation and
rotated sampling commands. It uses `/tmp/lunar-puc-assessment`: provision PUC
there, make `baseline` a clean checkout of the base plus `harness.patch`, and
copy the archived [patches](patches/) into that temporary directory. Build
`compare.test` there from `baseline/benchmarks` with the same cgo flags as the
PUC collector. The three invocations used were:

```sh
python3 run-broad.py small_fixes
python3 run-broad.py arithmetic fields native_entry
python3 run-broad.py combined_hot_paths stack_clear all_table
```

Each writes its own `*-broad.txt` and `*-manifest.json`, with clean candidate
revision IDs, patch hashes, and executable hashes. Candidate names in raw output
replace the original `runtime=lunar` label; the runtime is Lunar in every
candidate experiment. `combined_hot_paths` means `all_table_locals.patch` plus
`native-entry.patch`; it includes neither the small-fixes group nor stack clearing.
The temporary clones use local commits for clean provenance; the user's working
branch is not committed or modified by those experiments.

All seven candidate configurations passed full runtime tests, official-suite
conformance, `go vet`, and the tagged benchmark adapter tests/vet before timing.
The [validation log](broad-validation.txt) preserves those results. The default
benchmark module also passes tests/vet without cgo. The report includes both
successful and rejected experiments; passing semantic tests alone does not make
an optimization a performance win.
