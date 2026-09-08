# Incremental native-call entry comparison — 2026-09-07

Direct native-call entry reduced n-body time by 7.68% and callback time by
26.28% on top of the table-access candidate. This was an incremental
experiment over the then-unmerged [PR #13](https://github.com/mmcdole/lunar/pull/13),
not a comparison against main. A later
[comparison against merged main](../2026-09-07-linux-amd64-native-entry-main/README.md)
assessed the selected implementation.

| Program | Table candidate | Table + native entry | Time change |
| --- | ---: | ---: | ---: |
| binary-trees | 137.95 ms | 137.38 ms | −0.41%; p=.116 |
| fannkuch-redux | 18.45 ms | 18.36 ms | −0.45%; p=.089 |
| n-body | 47.08 ms | 43.47 ms | −7.68%; p<.001 |
| spectral-norm | 45.37 ms | 44.94 ms | −0.94%; p<.001 |

The callback row fell from 69.54 µs to 51.27 µs per 1,000 calls. Ordinary
Lua calls stayed near parity. Scalar Go-to-Lua calls rose from 53.58 ns to
53.91 ns (+0.62%, p=.001); this run did not establish an absence of
regressions. Other embedding and interpreter controls stayed within 1%.
Allocation counts were effectively unchanged, and the change added no cache.

The candidate entered fixed native calls directly when their argument/result
windows, reserved stack/frame capacity and current limits already permitted
it; misses took the existing checked path. PUC's early callee classification
and compact setup in [luaD_precall](https://www.lua.org/source/5.1/ldo.c.html)
motivated it. Native invocation, Frame validation, cancellation, panic/error
handling, yielding and result adjustment retained their existing paths.

## Collection and validation

- Table baseline: `f890f7450d729d848c0c3355587b71ed251bc156`.
  Native candidate: `5ca2ec4337f46fc2786d29a1bf5655c3bfe68eca`.
- Fifteen samples per cell, 500 ms target, 390 samples across 26 rows.
  Two fresh processes alternated leading position. Only Lunar was timed.
- Go 1.26.0, Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D;
  `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`, CPU 2 affinity.
  Timing ran serially; host power, frequency and external load were uncontrolled.
- The table baseline reused the exact PR #13 executable. Both binaries had
  the same PUC 5.1.5 library and cgo flags as the table comparison.
- Setup, compilation, warmup and final validation were excluded. Required
  embedding result consumption was timed. Statistics used benchstat
  `v0.0.0-20260709024250-82a0b07e230d`, with no aggregate acceptance score.
- Runtime/conformance, vet, race and tagged benchmark tests/vet passed.
  Added tests compared direct and checked entry across call shapes and
  verified unsuccessful attempts left execution state unchanged. Existing
  tests covered limits, protected calls, panic, nested calls, yield and GC roots.

Do not add gains from separate collections or use this incremental run to
waive the table candidate's independently confirmed callback regression.

The [original collection records](https://github.com/mmcdole/lunar/tree/5fc51e449a6661340056544e995aae4fdf89dcb2/benchmarks/results/2026-09-07-linux-amd64-native-entry) remain available in Git history. This directory retains the result summary.
