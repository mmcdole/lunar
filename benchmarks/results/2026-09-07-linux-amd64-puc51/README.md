# PUC Lua comparison — 2026-09-07

This assessment identified repeated execution-state loads in table helpers
and repeated layout planning for ordinary native calls as useful targets.
Both affected complete programs without introducing caches. Small string/POW
changes did not materially improve those programs. Later implementation and
acceptance results are recorded in the [table-access summary](../2026-09-07-linux-amd64-table-access/README.md)
and [native-entry summary](../2026-09-07-linux-amd64-native-entry-main/README.md).

The comparator was unmodified PUC Lua 5.1.5, GCC 15.2.0 `-O2`, default double
numbers and no JIT. This matches Lunar's Lua 5.1 numeric semantics more closely
than later integer-capable releases. These are not Lua 5.4/5.5 measurements.
The [official source archive](https://www.lua.org/ftp/lua-5.1.5.tar.gz) SHA-256 is
`2640fc56a795f29d28ef15e13c34a47e223960b0240e8cb0a82d9b0738695333`.

## Baseline complete programs

| Program | Input | Lunar | PUC 5.1.5 | Lunar / PUC time |
| --- | ---: | ---: | ---: | ---: |
| binary-trees | depth 12 | 141.03 ms | 59.30 ms | 2.38× |
| fannkuch-redux | n = 8 | 20.22 ms | 11.91 ms | 1.70× |
| n-body | 20,000 steps | 53.39 ms | 23.95 ms | 2.23× |
| spectral-norm | n = 150 | 45.29 ms | 24.81 ms | 1.83× |

These are medians of 15 samples, each with a 500 ms target, using the
canonical sources, inputs and oracles in [PROGRAMS.md](../../PROGRAMS.md).
They cover recursion/allocation, array mutation, record/native math calls and
numeric/Lua-call work; they do not establish the mix in arbitrary applications.

## Candidate comparisons

Candidates started from the same runtime revision and used fresh,
contemporaneous baseline samples in rotated process order. The following
collection had 15 samples per cell:

| Program | Paired baseline | Table helpers use executor locals | Table locals + native entry |
| --- | ---: | ---: | ---: |
| binary-trees | 139.5 ms | 137.4 ms; −1.5%, near parity | 139.8 ms; near parity |
| fannkuch-redux | 20.28 ms | 18.64 ms; −8.1% | 18.98 ms; −6.4% |
| n-body | 53.67 ms | 48.13 ms; −10.3% | 44.85 ms; −16.4% |
| spectral-norm | 45.76 ms | 45.72 ms; near parity | 45.58 ms; near parity |

Passing executor locals into all raw table helpers removed activation copies
and repeated state loads without changing lookup algorithms or storage.
Fixed native entry reused existing argument/result windows, frame capacity
and limits; misses retained the checked path. The isolated native change
reduced n-body by 8.1%. The combined percentages above are measured results,
not sums of separate gains. Allocation counts were effectively unchanged,
and neither candidate added retained storage.

The isolated native build also slowed the fixed Lua-call control by 6.95%.
The combined build had near-parity fixed Lua calls and no material slowdown
in the four programs. Normalized executor and Lua-call instructions matched
between the isolated builds, but this did not establish code placement as
the cause. The exact intended implementation still required a full acceptance
comparison, including embedding controls.

| Other experiment | Finding | Decision at collection |
| --- | --- | --- |
| String-field helpers only | n-body −10.33%; other programs near parity | Broaden to all raw table helpers |
| Separate ADD/SUB/MUL/DIV/MOD dispatch | spectral-norm +2.64%; fixed-call control +16.82%; other programs near parity | Reject |
| Conditional nil writes and bulk dead-stack clearing | binary-trees +3.10%; spectral-norm +3.09%; n-body −2.19% | Reject |
| Numeric POW, short-concat scratch and identity substring together | Every complete program within 1% of baseline | No demonstrated broad speed gain |

Sub-1% changes were treated as near parity even where statistically resolved.
No aggregate runtime score determined acceptance.

## Source and profile findings

PUC keeps register state available in its
[interpreter](https://www.lua.org/source/5.1/lvm.c.html) and uses compact native
setup in [luaD_precall](https://www.lua.org/source/5.1/ldo.c.html). Those principles
motivated the successful candidates while retaining Lunar's validation,
resource limits, cancellation, errors and collection seams. Copying PUC's
arithmetic dispatch structure was not beneficial under this Go compiler.

Lunar already had chained-scatter tables, array density sizing, prehashed field
keys, missing-metamethod flags, cached executor locals and fixed Lua call/return
paths. PUC's [tables](https://www.lua.org/source/5.1/ltable.c.html) and
[collector](https://www.lua.org/source/5.1/lgc.c.html) supplied comparison points,
not a list of missing features.

Profiles included setup/warmup and approximately three seconds of timed work.
Cumulative paths overlap; their shares are attribution, not attainable speedups:

- N-body's string-field get/set helpers accounted for 21.23%/20.11%
  cumulative CPU; `pushFunctionCall` and `invokeNativeCall` each 11.45%.
  Native-result reservation alone was only 0.84%.
- Spectral-norm's executor accounted for 48.20% flat CPU, with operand
  selection, array reads and fixed Lua call/return paths contributing further.
- Binary-trees had 23.27% cumulative write-barrier CPU, 16.62% allocation CPU
  and 1.66% semantic heap measurement. Table headers/backing accounted for
  67.55%/32.42% of allocation count. Its 674,478 table headers and 334,510
  two-slot arrays explained nearly all 1,009,044 allocations and 65.7 MiB/op.
  Reducing constructor allocations was an unmeasured structural opportunity,
  requiring ownership, weak-reference and heap-accounting work.

Diagnostics showed Lunar/PUC ratios of 5.33× for native sqrt, 4.04× for
ipairs, 4.20× for pairs, 3.96× for Lua iteration, 2.60× for short concat,
3.62× for identity substring and 3.36× for explicit live-graph collection.
Those gaps located costs without proving normal-program benefit. The small
string/POW group reduced some allocations but did not improve the complete
programs materially. Identity substring separately exposed allocation debt:
eight full-range calls on 64 KiB added 512 KiB of debt while retaining the
original backing. That was an accounting observation, not a measured speedup.

The approximately 147 ns PUC `call_only` diagnostic included its Lua wrapper,
globals and `tostring`; it did not isolate cgo transition time. Go allocation
counters cannot observe PUC's C heap; no PUC retained-memory ranking was made.

## Collection and validation

- Runtime baseline: `4f7e8928939271bbdf700998c837da42e9b93544`.
  Identical benchmark harness snapshot: `ff0795d3cba05772b07633430d1d34bc0feb9850`.
- Go 1.26.0, Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D;
  `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`, CPU 2 affinity.
  Timing ran serially; some preparation ran on other guest CPUs. Host power,
  frequency and external load were uncontrolled. Small differences are
  specific to this virtualized machine.
- Loading, compilation, library setup, one warmup and output checks were
  excluded. One cgo call per PUC operation was included. Statistics used
  benchstat `v0.0.0-20260709024250-82a0b07e230d`.
- The PUC comparison included all 19 rows in both runtimes at 15 samples each.
  That comparison plus seven candidate configurations totaled 1,770 samples.
- All seven candidates passed runtime/conformance tests, vet and tagged
  benchmark tests/vet before measurement. The default benchmark module also
  passed tests/vet without cgo. Semantic correctness did not qualify a
  candidate as a performance improvement.

The maintained collector is [run-puc-comparison.sh](../../run-puc-comparison.sh),
with build/provenance settings documented in [the benchmark guide](../../README.md).
The [original collection records](https://github.com/mmcdole/lunar/tree/5fc51e449a6661340056544e995aae4fdf89dcb2/benchmarks/results/2026-09-07-linux-amd64-puc51)
remain available in Git history. This directory retains the result summary.
