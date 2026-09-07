These measurements refresh every timing cell in the root README for the
native-call change in [PR #15](https://github.com/mmcdole/lunar/pull/15).
The existing Lunar, GopherLua, and go-lua columns are retained.

All 27 cells are medians of 15 samples from this collection. Each sample
has a 500 ms target. Three fresh runtime processes rotate order across
rounds, covering all four programs and all five embedding operations.
[Program statistics](Programs.benchstat.txt),
[embedding statistics](Embedding.benchstat.txt), [raw samples](comparison.txt),
and [exact medians in nanoseconds](medians-ns.json) are included.
Small timing differences on one machine should be read as near parity.

The exact Lunar executable used here also passed the separate
[13-case comparison against merged main](../2026-09-07-linux-amd64-native-entry-main/).
That comparison showed 7.10% less n-body time and 26.76% less callback time,
with no statistically significant slowdown among its program, interpreter,
and embedding cases. Those before/after percentages come from that paired
collection, not from comparing the old and new README cells.

The runtime and benchmark source are unchanged from the validated candidate
`a2f32740110096c4eee57e7ec90ede31b78236c6`, based on merged main
`d79e0a39f8e4bd14dbf581cf204431cf24b0d073`. The
[manifest](manifest.json) identifies the exact binary, runtime versions, and
collection controls. Its linked build manifest records compiler flags,
validation commands, and the PUC library used by the benchmark executable.
Only the three Go runtimes are measured in this README collection.

Controls and scope:

- Go 1.26.0, Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D.
- GopherLua v1.1.2; go-lua `v0.0.0-20250718183320-1e37f32ad7d0`.
- `GOGC=100`, `GOMEMLIMIT=off`, `GOMAXPROCS=1`, `-cpu=1`, CPU 2 affinity.
- No concurrent benchmark workloads. Host power, frequency, and external
  load were not controlled.
- `B.Loop` times the warm operation. Preparation, compilation, warmup,
  and final validation are excluded. Required host result consumption
  in embedding operations is timed.
- Benchstat `v0.0.0-20260709024250-82a0b07e230d` provides medians,
  confidence intervals, and pairwise comparisons. No aggregate runtime
  ranking is used.

The root README's retained-memory table remains the separately identified
historical Apple M3 Pro / Go 1.25.1 measurement. The native-call change adds
no storage; this collection measures execution time and allocation traffic,
not retained memory.

[collect-readme.py](collect-readme.py) records the exact commands. To repeat,
build the recorded clean candidate from the original build directory,
`/tmp/lunar-native-main/native_no_clear`, with the recorded settings and output
`/tmp/lunar-native-main/no_clear.test`. Also provision the clean candidate at
`/tmp/lunar-native-pr/repo` for collection. The original build path matters
because the collector checks the exact executable hash, including embedded
source paths. It writes 405 samples across 27 rows and refuses to overwrite
existing raw output. Its `readme-manifest.json` and `readme-comparison.txt`
outputs are archived here as `manifest.json` and `comparison.txt`.
[SHA256SUMS](SHA256SUMS) covers the archived files.
