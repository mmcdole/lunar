The table-access change reduces fannkuch-redux time by **8.14%** and n-body
time by **8.76%** against current main. It does not yet meet the no-regression
merge condition: Lua-to-Go callbacks take **1.34% more time** in the full run
and **1.61% more** in an independent repeat. PR #13 remains open.

The runtime change passes existing executor locals to the four raw table
helpers. A full slice expression keeps unused capacity out of dispatch's
live state. Lookup rules, table storage, and metamethod fallbacks are unchanged.

| Program | Baseline | Candidate | Change in time |
| --- | ---: | ---: | ---: |
| binary-trees | 136.30 ms | 137.51 ms | +0.89%; p=0.067 |
| fannkuch-redux | 19.98 ms | 18.36 ms | **−8.14%**; p<0.001 |
| n-body | 51.61 ms | 47.09 ms | **−8.76%**; p<0.001 |
| spectral-norm | 45.11 ms | 45.34 ms | +0.51%; p<0.001 |

All cells are medians of 15 samples, with a 500 ms target per sample. The
[full program statistics](final-Programs.benchstat.txt) retain confidence
intervals and allocation traffic. There is no aggregate acceptance score.

The [interpreter checks](final-Interpreter.benchstat.txt) show numeric loops
at −0.84%, ordinary Lua calls at −0.31%, table field access at −10.29%, and
string appends at −0.41%. The [embedding checks](final-Embedding.benchstat.txt)
show scalar Go-to-Lua calls at −0.39%, callbacks at +1.34%, string echo at
−1.19%, reused-table checksum at −8.58%, and table construction/checksum at
−2.68% (the last difference is not significant). Allocation counts are
effectively unchanged. No retained-memory improvement is claimed.

The callback and spectral-norm differences were repeated with the same
executables, alternating process order, and 15 samples at a 1 s target:

| Repeat | Baseline | Candidate | Change in time |
| --- | ---: | ---: | ---: |
| Lua calls Go 1,000 times | 68.64 µs | 69.74 µs | +1.61% |
| Captured native sqrt, 10,000 calls | 618.3 µs | 625.3 µs | +1.14% |
| spectral-norm | 45.14 ms | 45.38 ms | +0.55% |

All three differences have p<0.001. The captured-function diagnostic avoids
repeating a global lookup. Inspection found no added successful-path work in
the native-call driver, so the cause of this smaller regression remains
unresolved. The [repeat statistics](focused-repeat.benchstat.txt) and
[raw samples](focused-repeat.txt) preserve it; the program gains do not erase it.

The first candidate, `81bb654`, also slowed numeric loops by 2.03%. Passing
the register slice into helpers kept its unused capacity live, adding three
stack reloads per numeric iteration. Candidate `f890f74` removes those loads
by limiting the local slice capacity to its length. The numeric-loop regression
is absent from the full revised run. The initial [samples](initial-comparison.txt)
and [statistics](initial-Interpreter.benchstat.txt) are retained, along with
the five-sample [preliminary check](capped-pilot.txt). That preliminary check
is not used for the published tables or merge decision.

The root README retains its existing Lunar, GopherLua, and go-lua columns.
Every timing cell comes from this new collection; the older Apple M3 Pro
retained-memory tables have separate provenance. The PUC comparison is only
in the detailed [program report](published-Programs.benchstat.txt).
[Published embedding statistics](published-Embedding.benchstat.txt) and
[exact medians in nanoseconds](medians-ns.json) accompany the README values.

Collection details:

- Main runtime baseline: `86d27109cb8fffef8f439353716e139516a89ace`.
- Clean baseline plus identical benchmark harness:
  `c2ab522e77a16b44cb4fe41fab0685ad03b529c4`.
- Initial candidate: `81bb654556c36b5017e5764ad2ea9ce8d858d626`.
- Revised candidate: `f890f7450d729d848c0c3355587b71ed251bc156`.
- Go 1.26.0, Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D.
- `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`, affinity to CPU 2.
  Timing workloads ran serially. Host frequency and power controls were
  unavailable. These are results from one virtualized machine.
- Each full collection rotates five runtime processes across 15 rounds:
  baseline, candidate, GopherLua, go-lua, and PUC. Each Go runtime has all
  13 program/interpreter/embedding rows; PUC has the four program rows.
  Both full collections contain 840 samples.
- Setup, compilation, warmup, and final validation are outside timing.
  Embedding result consumption required by each Go API is timed.
- GopherLua v1.1.2; go-lua `v0.0.0-20250718183320-1e37f32ad7d0`.
- PUC Lua 5.1.5, GCC 15.2 at `-O2`, default double, no JIT. One cgo call
  per operation is included. Both Go benchmark executables use identical
  cgo flags; PUC's C heap is not reported as Go allocations.
- Statistics use benchstat `v0.0.0-20260709024250-82a0b07e230d`.

The [initial manifest](initial-manifest.json) and
[revised manifest](final-manifest.json) record source revisions, binary hashes,
PUC source/library hashes, and exact build settings. Reproduction uses
[collect.py](collect.py): provision the PUC build described in the
[benchmark protocol](../../README.md), create clean `baseline` and `repo`
checkouts at the recorded revisions under `/tmp/lunar-table-pr`, then run
`python3 collect.py final`. Use a different stage name for another run;
the collector refuses to overwrite raw samples. The initial collection
predates the stage filename prefix but used the same build and timing commands.
[focus-repeat.py](focus-repeat.py) records the independent repeat commands.

Runtime/conformance tests, race tests, and vet passed. The benchmark module
passed tests/vet both with and without PUC, and the CBOR module passed its
default and GopherLua reference checks. CI also passed Linux, macOS, Windows,
benchmark smoke, and CBOR smoke for the measured revision:
[workflow run](https://github.com/mmcdole/lunar/actions/runs/34165526105).
Passing these checks does not resolve the measured speed regressions.

[SHA256SUMS](SHA256SUMS) records the archive's files. The earlier
[PUC assessment](../2026-09-07-linux-amd64-puc51/) contains profiles and
the separate native-entry candidate for the next investigation.
