# Native-call entry against merged main — 2026-09-07

The selected fixed native-call entry candidate reduced n-body time by 7.10%
and callback time by 26.76% against the main revision containing PR #13.
No statistically significant slowdown appeared in any of the 13 program,
interpreter or embedding cases. It was subsequently included in
[PR #15](https://github.com/mmcdole/lunar/pull/15), merged as
`5fc51e449a6661340056544e995aae4fdf89dcb2`.

| Program | Main | Selected candidate | Time change |
| --- | ---: | ---: | ---: |
| binary-trees | 136.16 ms | 135.97 ms | −0.14%; near parity |
| fannkuch-redux | 18.38 ms | 18.39 ms | +0.06%; near parity |
| n-body | 47.01 ms | 43.67 ms | −7.10% |
| spectral-norm | 45.36 ms | 44.89 ms | −1.04% |

Callbacks fell from 70.16 µs to 51.39 µs per 1,000 calls. Higher medians
elsewhere were small and unresolved: scalar Go-to-Lua calls +0.47% (p=.213),
reused-table checksum +0.45% (p=.140), fannkuch +0.06% (p=.870) and the
table-access control +0.02% (p=.775). Ordinary Lua calls remained near parity.
Allocation counts were effectively unchanged; the change added no retained
storage. These results describe one machine and workload set, not a universal
absence of regressions.

The selected version reused reserved stack and frame capacity for fixed
native calls, with misses taking the existing checked path. Native invocation,
Frame validation, limits, cancellation, panic/error handling, yielding and
result adjustment retained their existing paths. PUC's compact call setup in
[luaD_precall](https://www.lua.org/source/5.1/ldo.c.html) motivated the change.
It also omitted redundant dead-stack clearing from this verified entry path:
its native frame leaves the live extent unchanged. Generic and open-result
paths retained their clearing.

## Other versions and profile evidence

The original native candidate improved n-body by 6.27%, but slowed scalar
Go-to-Lua calls by 1.33% and reused-table checksums by 1.57%. Removing redundant
clearing improved n-body by a further 0.88% and produced the selected results
above. Acceptance applied to that selected version.

Separate n-body profiles attributed 11.14% cumulative CPU on main to
`pushFunctionCall`, including 5.16% in layout planning. The selected direct
entry helper accounted for 2.81%. These sampled distributions included setup,
overlapped other paths and were not additional speedup measurements.

A further frame-copy experiment removed a 32-byte activation copy and an
index spill. Its five-sample screen reduced callback time another 4.80% but
showed no n-body improvement (p=.841). It passed tests and race checks but
never received the full 15-sample suite; it was excluded from the selected
candidate.

## Collection and validation

- Main: `d79e0a39f8e4bd14dbf581cf204431cf24b0d073`.
  Original candidate: `a9d2f0ec4b4b70647072f40f4ed4ffb4196035d1`.
  Selected candidate: `a2f32740110096c4eee57e7ec90ede31b78236c6`.
- Fifteen samples per cell with a 500 ms target; 585 samples across 39 rows.
  Three fresh Lunar processes rotated leading position over 15 rounds.
- Go 1.26.0, Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D;
  `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`, CPU 2 affinity.
  Timing ran serially; host power, frequency and external load were uncontrolled.
- Binaries used the same PUC-enabled harness and cgo flags. Only Lunar
  implementations were timed. Setup, compilation, warmup and final validation
  were excluded; required embedding result consumption was timed.
- Statistics used benchstat `v0.0.0-20260709024250-82a0b07e230d`.
  The five-sample frame-copy screen and profile runs were kept separate.
- Runtime/conformance, vet, race and tagged benchmark tests/vet passed.
  Four new integration tests checked open return/resume values consumed by
  SETLIST or open calls, followed by a collecting fixed native call. They
  verified both cleared suffix slots and surviving caller locals. Existing
  differential-entry, atomic-miss, limit, protected-call, panic and yield tests
  also passed.

The subsequent [README timing collection](../2026-09-07-linux-amd64-native-calls/README.md)
compared the selected executable with GopherLua and go-lua.

The [original collection records](https://github.com/mmcdole/lunar/tree/5fc51e449a6661340056544e995aae4fdf89dcb2/benchmarks/results/2026-09-07-linux-amd64-native-entry-main) remain available in Git history. This directory retains the result summary.
