# Table-access comparison — 2026-09-07

Passing existing executor locals to four raw table helpers reduced fannkuch
and n-body time. The measured candidate also repeatedly slowed callbacks;
at collection it did not meet the strict no-regression condition.
[PR #13](https://github.com/mmcdole/lunar/pull/13) was subsequently merged
as `d79e0a39f8e4bd14dbf581cf204431cf24b0d073`.

Lookup rules, table storage and metamethod fallbacks were unchanged. A full
slice expression kept unused register capacity out of dispatch's live state.

| Program | Baseline | Candidate | Time change |
| --- | ---: | ---: | ---: |
| binary-trees | 136.30 ms | 137.51 ms | +0.89%; p=.067 |
| fannkuch-redux | 19.98 ms | 18.36 ms | −8.14%; p<.001 |
| n-body | 51.61 ms | 47.09 ms | −8.76%; p<.001 |
| spectral-norm | 45.11 ms | 45.34 ms | +0.51%; p<.001 |

Medians use 15 samples per cell, with a 500 ms target. Positive changes mean
more elapsed time. The interpreter controls changed by −0.84% for numeric
loops, −0.31% for ordinary Lua calls, −10.29% for table field access and
−0.41% for string appends. Embedding controls changed by −0.39% for scalar
Go-to-Lua calls, +1.34% for callbacks, −1.19% for string echo, −8.58% for
reused-table checksum and −2.68% for table construction/checksum (the last
was not significant). Allocation counts were effectively unchanged; no
retained-memory improvement was claimed.

## Independent repeat

The same executables ran in alternating order for 15 samples per cell with
a 1 s target:

| Case | Baseline | Candidate | Time change |
| --- | ---: | ---: | ---: |
| Lua calls Go 1,000 times | 68.64 µs | 69.74 µs | +1.61% |
| Captured native sqrt, 10,000 calls | 618.3 µs | 625.3 µs | +1.14% |
| spectral-norm | 45.14 ms | 45.38 ms | +0.55% |

All three differences had p<.001. The captured-function diagnostic avoided
repeating a global lookup. Inspection found no added successful-path work in
the native-call driver; the cause of its smaller regression was unresolved.
The complete-program gains did not erase these measured costs.

The initial candidate `81bb654556c36b5017e5764ad2ea9ce8d858d626` also slowed
numeric loops by 2.03%. Keeping unused slice capacity live added three stack
reloads per numeric iteration. Revised candidate
`f890f7450d729d848c0c3355587b71ed251bc156` removed those loads by limiting
local capacity to length; its full run no longer showed that regression.
A five-sample preliminary check was not pooled with the final results.

## Collection and validation

- Runtime baseline: `86d27109cb8fffef8f439353716e139516a89ace`;
  baseline with the identical harness: `c2ab522e77a16b44cb4fe41fab0685ad03b529c4`.
- Go 1.26.0, Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D.
  `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`, CPU 2 affinity.
  Timing ran serially; host power, frequency and external load were uncontrolled.
- Each initial/final collection rotated five runtime processes over 15 rounds:
  baseline, candidate, GopherLua, go-lua and PUC. Each Go runtime had 13 rows;
  PUC had four program rows. Each full collection contained 840 samples.
- GopherLua v1.1.2; go-lua `v0.0.0-20250718183320-1e37f32ad7d0`;
  PUC Lua 5.1.5, GCC 15.2 `-O2`, default double numbers, no JIT. The benchmark
  executables used identical cgo flags; one cgo call per PUC operation was timed.
  Go allocation counters do not measure PUC's C heap.
- Setup, compilation, warmup and final validation were excluded. Required
  embedding API result consumption was timed. Benchstat
  `v0.0.0-20260709024250-82a0b07e230d` supplied statistics; no aggregate score
  was used for acceptance.
- Runtime/conformance, race, vet, benchmark tests/vet with and without PUC,
  and default/GopherLua CBOR checks passed. Linux, macOS, Windows and both
  benchmark smoke CI jobs passed for the measured revision.

The README update associated with this collection retained its Lunar,
GopherLua and go-lua columns. Its older M3 retained-memory tables had separate
provenance. The subsequent native-call investigation is summarized in
[the incremental comparison](../2026-09-07-linux-amd64-native-entry/README.md).

The [original collection records](https://github.com/mmcdole/lunar/tree/5fc51e449a6661340056544e995aae4fdf89dcb2/benchmarks/results/2026-09-07-linux-amd64-table-access) remain available in Git history. This directory retains the result summary.
