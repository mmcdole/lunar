The native-call entry experiment reduces n-body time by **7.68%** and
Lua-to-Go callback time by **26.28%** compared with the table-access candidate.
This confirms a useful next target in a complete program. It adds no cache.

This is an incremental comparison over the unmerged table change in
[PR #13](https://github.com/mmcdole/lunar/pull/13), not a comparison against
main. The native change is isolated in a private experimental checkout;
neither runtime change has been merged. The root README's timing tables
in PR #13 continue to describe that PR's table-only implementation.

| Program | Table candidate | Table + native entry | Change in time |
| --- | ---: | ---: | ---: |
| binary-trees | 137.95 ms | 137.38 ms | −0.41%; p=0.116 |
| fannkuch-redux | 18.45 ms | 18.36 ms | −0.45%; p=0.089 |
| n-body | 47.08 ms | 43.47 ms | **−7.68%**; p<0.001 |
| spectral-norm | 45.37 ms | 44.94 ms | −0.94%; p<0.001 |

The native callback row falls from **69.54 µs to 51.27 µs** per 1,000 calls.
Ordinary Lua calls remain near parity, unlike the earlier isolated native
experiment on the older baseline. Go-to-Lua scalar calls increase from
53.58 ns to 53.91 ns (**+0.62%, p=0.001**). That small measured difference
is retained here; this run does not establish an absence of regressions.
The remaining embedding rows and interpreter controls are all within 1%.
Allocation counts are effectively unchanged.

The [program](Programs.benchstat.txt), [embedding](Embedding.benchstat.txt),
and [interpreter](Interpreter.benchstat.txt) reports contain every row,
confidence intervals, and allocation traffic. Each cell has 15 samples,
with a 500 ms target. There are 390 samples across 26 rows in the
[raw output](comparison.txt). No aggregate score is used.

The [patch](native-entry.patch) enters a direct native call when its fixed
argument/result windows, existing stack capacity, and current limits already
permit it. It otherwise takes the existing checked path. This follows PUC's
early callee classification and compact call setup in
[luaD_precall](https://www.lua.org/source/5.1/ldo.c.html).
Lunar's native invocation, Frame validation, cancellation, panic/error
handling, yielding, and result adjustment stay in their existing paths.

The test patch compares successful entry with the existing checked call
across argument/result shapes, and verifies that failed attempts do not
mutate execution state. Existing tests cover cancellation, callback panics,
nested calls, yielding, collection roots, and temporary xpcall limit headroom.
Runtime/conformance tests, vet, race tests, and tagged benchmark tests/vet
passed before measurement. Their exact commands and logs are recorded in
the [build manifest](manifest.json).

The [timing manifest](timing-manifest.json) records the source and executable
identities:

- Table baseline: `f890f7450d729d848c0c3355587b71ed251bc156`.
- Native candidate: `5ca2ec4337f46fc2786d29a1bf5655c3bfe68eca`.
- Go 1.26.0, Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D.
- Identical cgo flags and PUC 5.1.5 library as the table-access comparison.
  Only Lunar is timed in this incremental run.
- `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`, CPU 2 affinity.
  Fresh processes alternate order each round; no concurrent timing workloads.
  Host power and frequency controls are unavailable.
- Setup, compilation, warmup, and final validation are excluded. API result
  consumption in embedding rows is timed.
- Benchstat `v0.0.0-20260709024250-82a0b07e230d`.

[collect.py](collect.py) records the sampling commands and paths. The table
binary is the exact executable from PR #13's revised full collection. To
recreate the native candidate, apply the archived patch to the table baseline
and use the recorded build settings. [SHA256SUMS](SHA256SUMS) covers the
archive, including the patch and [exact medians](medians-ns.json).

The next acceptance comparison needs the intended combined implementation
against current main, including all program, interpreter, and embedding rows.
Do not add percentage gains from separate collections or use this incremental
run to waive PR #13's independently confirmed callback regression.
