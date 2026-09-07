Fixed native-call entry is the strongest next measured change after merging
[PR #13](https://github.com/mmcdole/lunar/pull/13). The selected candidate
reduces **n-body time by 7.10%** and **Lua-to-Go callback time by 26.76%**
against merged main. No statistically significant slowdown appears in any
of the 13 program, interpreter, or embedding cases in this collection.

The candidate is preserved on local branch `perf/native-call-entry`, at
`a2f32740110096c4eee57e7ec90ede31b78236c6`. It has not been merged or used
to update the root README's timing tables.

| Program | Main | Selected candidate | Change in time |
| --- | ---: | ---: | ---: |
| binary-trees | 136.16 ms | 135.97 ms | −0.14%; near parity |
| fannkuch-redux | 18.38 ms | 18.39 ms | +0.06%; near parity |
| n-body | 47.01 ms | 43.67 ms | **−7.10%** |
| spectral-norm | 45.36 ms | 44.89 ms | −1.04% |

Callbacks fall from **70.16 µs to 51.39 µs** per 1,000 calls. The slightly
higher medians elsewhere are small and not significant: Go-to-Lua scalar
calls +0.47% (p=0.213), reused-table checksum +0.45% (p=0.140), fannkuch
+0.06% (p=0.870), and the table-access control +0.02% (p=0.775).
Ordinary Lua calls remain near parity. Allocation counts are effectively
unchanged; this change adds no cache or retained storage.

All cells have 15 samples with a 500 ms target. The
[program](main-Programs.benchstat.txt), [interpreter](main-Interpreter.benchstat.txt),
and [embedding](main-Embedding.benchstat.txt) reports contain every result,
confidence intervals, and allocation traffic. [Raw output](comparison.txt)
contains 585 samples: 39 rows across three implementations. Results describe
this machine and these workloads, not a universal absence of regressions.

The [selected patch](native-entry-no-clear.patch) reuses reserved stack and
frame capacity for direct native calls with fixed arguments and results.
Misses take the existing checked path. Native invocation, Frame validation,
limits, cancellation, panic/error handling, yielding, and result adjustment
retain their existing execution paths. PUC's early callee classification and
compact native call setup in
[luaD_precall](https://www.lua.org/source/5.1/ldo.c.html) motivated the change.

The selected version also omits redundant dead-stack clearing from this
specific entry path. Before a verified fixed CALL, `top <= frameExtent`,
and its argument window fits within the caller's register extent. Publishing
the native frame leaves the live extent unchanged, so there is nothing to
clear. Open results must immediately feed an open consumer; those consumers
retain the existing clearing. The generic call path is unchanged.

Four new integration cases cover excess return or coroutine-resume values
consumed by SETLIST or an open-argument call, followed by a fixed native call
that collects. They check both cleared suffix slots and surviving caller
locals above the native argument window. All four pass on the original and
simplified native candidates. Existing differential entry, atomic-miss,
limits, protected-call, cancellation, panic, and yield tests also pass.
Runtime/conformance tests, vet, race tests, and tagged benchmark tests/vet
passed before measurement; the [validation manifest](no-clear-manifest.json)
links their commands and logs.

The original [native-entry patch](native-entry.patch), without the clearing
refinement, reduces n-body time by 6.27% in this collection but also slows
scalar Go-to-Lua calls by 1.33% and reused-table checksums by 1.57%.
The selected version's [refinement comparison](refinement-Programs.benchstat.txt)
reduces n-body time by a further 0.88%. Acceptance here applies to the selected
version, not every native-entry experiment.

Fresh profiles support the application target. On
[main](baseline-nbody.cpu.txt), `pushFunctionCall` accounts for 11.14%
cumulative CPU in n-body, including 5.16% in layout planning. In the
[selected candidate](no-clear-nbody.cpu.txt), the new entry helper accounts
for 2.81%. These are separate sampled CPU distributions and include setup;
they are not an additional speedup calculation. Profile-run timings are
excluded from the benchmark comparison.

A further [frame-copy experiment](cold-frame.patch) moves the caller-frame
read into the fallback. Assembly confirms that successful calls avoid a
32-byte activation copy and an index spill: entry through the helper call
falls from 16 instructions to 9, and the driver frame from 376 to 368 bytes.
Its [five-sample screen](cold-pilot.benchstat.txt) shows another 4.80% reduction
in callback time but no n-body improvement (p=0.841). It passed tests and
race checks, but has not received the full 15-sample suite. It is not part
of the selected candidate. The [pilot](cold-pilot.txt) and
[manifest](cold-frame-manifest.json) preserve that distinction.

Reproduction and controls:

- Merged main: `d79e0a39f8e4bd14dbf581cf204431cf24b0d073`.
- Original native candidate: `a9d2f0ec4b4b70647072f40f4ed4ffb4196035d1`.
- Selected candidate: `a2f32740110096c4eee57e7ec90ede31b78236c6`.
- Go 1.26.0, Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D.
- `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`, affinity to CPU 2.
  Three fresh runtime processes rotate order over 15 rounds. No concurrent
  benchmark workloads; some preparation tests/builds ran on other guest CPUs.
  Host power, frequency, and external load were not controlled.
- Setup, compilation, warmup, and final validation are excluded. Required
  embedding API result consumption is timed.
- All binaries use the same PUC-enabled harness and cgo flags as the earlier
  assessment. Only Lunar implementations are timed here.
- Statistics use benchstat `v0.0.0-20260709024250-82a0b07e230d`.

The [build](build-manifest.json) and [timing](timing-manifest.json) manifests
record revisions, executable hashes, and compiler settings. Main and the
original native candidate were checked byte-for-byte against their previously
validated Go source and module files. The new refinement received fresh
validation. Build manifests' `timing_run` fields describe their preparation
phase; sampling is recorded separately.

[collect.py](collect.py) records the full run, and
[cold-pilot.py](cold-pilot.py) records the separate preliminary screen.
Provision the named clean checkouts and PUC build under `/tmp/lunar-native-main`
and `/tmp/lunar-puc-assessment`, using the recorded patches and build settings.
The [exact medians](medians-ns.json), [sample counts](sample-validation.json),
and [SHA256SUMS](SHA256SUMS) allow the archived results to be checked.
