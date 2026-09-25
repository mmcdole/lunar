# Numeric operand specialization pilot

Park this prototype. It passes correctness checks, but two exploratory cohorts
do not establish a general complete-program improvement. The repeat contains
slower program and table-construction results. CBOR load provides an encouraging
signal with substantial timing variation; it does not qualify the change.

Baseline: main `99aacff48447e29a85a043d0b257e3cca12315b8`.
Candidate: local experimental commit `d8867c86b145dedac2b2332aa1707cf6bd64b9eb`,
available in `.bench/numeric-operands/candidate`. No runtime change is promoted
to main, and no root README timing table is updated from these pilots.

## Source comparison

PUC 5.5's [compiler](https://www.lua.org/source/5.5/lcode.c.html) selects
constant-operand forms; its [interpreter](https://www.lua.org/source/5.5/lvm.c.html)
loads operands from known locations. Lunar's prototype adds ten internal forms
for ADD/SUB/MUL/DIV/MOD with one numeric constant on either side. Sealing selects
the forms, verification proves their operands, and handlers avoid generic RK
selection and the constant's numeric-type check. Generic arithmetic is unchanged.
Operand order and the existing cold coercion/metamethod path are preserved;
dumping lowers the instructions to Lua 5.1 bytecode.

The compiled `runInstructions` symbol grows from 6,885 to 8,184 bytes, 18.9%.
Code growth is a possible tradeoff, not an established cause of the timings.
This tests a specific implementation; it does not rule out other ways of
reducing operand-handling work. No PUC timing lane was collected here.

## Complete programs and controls

Each cohort contains six samples per cell, 156 samples across 12 processes.
The repeat was specified after the initial cohort showed substantial variation;
it uses identical binaries, all 13 cases, and the same measurement conditions.
No samples are excluded and cohorts are not pooled. Positive changes mean slower.

| Workload | Pilot baseline | Pilot candidate | Change | Repeat baseline | Repeat candidate | Change |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Binary-trees | 138.551 ms | 136.290 ms | -1.63% | 135.736 ms | 138.595 ms | +2.11% |
| Fannkuch-redux | 14.730 ms | 14.602 ms | -0.87% | 14.574 ms | 14.451 ms | -0.84% |
| N-body | 43.637 ms | 44.091 ms | +1.04% | 43.577 ms | 43.667 ms | +0.21% |
| Spectral-norm | 43.269 ms | 43.760 ms | +1.13% | 42.733 ms | 44.340 ms | +3.76% |
| Numeric loop, 10,000 iterations | 37.831 µs | 38.401 µs | +1.51% | 37.681 µs | 37.837 µs | +0.41% |
| Fixed Lua calls, 1,000 calls | 25.489 µs | 24.948 µs | -2.12% | 25.012 µs | 24.418 µs | -2.38% |
| Table field get/set, 10,000 iterations | 188.179 µs | 190.060 µs | +1.00% | 187.667 µs | 187.346 µs | -0.17% |
| String append, 256 iterations | 35.742 µs | 36.771 µs | +2.88% | 35.931 µs | 35.858 µs | -0.20% |
| Go calls Lua with scalar arguments | 65.445 ns | 54.630 ns | -16.53% | 54.460 ns | 54.755 ns | +0.54% |
| Lua calls Go 1,000 times | 51.501 µs | 51.393 µs | -0.21% | 51.536 µs | 50.759 µs | -1.51% |
| Lua echoes a 128-byte Go string | 85.275 ns | 81.140 ns | -4.85% | 79.740 ns | 79.350 ns | -0.49% |
| Lua checksums a reused Go-built table | 241.800 ns | 238.950 ns | -1.18% | 234.800 ns | 234.550 ns | -0.11% |
| Build a table in Go, then checksum in Lua | 2.515 µs | 2.443 µs | -2.88% | 2.264 µs | 2.453 µs | +8.35% |

Benchstat resolves no timing differences in the initial cohort. In the repeat,
binary-trees is slower (p=.041), spectral-norm slower (p=.002), table construction
slower (p=.026), and callbacks faster (p=.009). All other timing differences are
unresolved. These are exploratory per-row tests without multiplicity correction;
the slower results are not independently reproduced regression estimates.

Variation remains substantial in both cohorts. The initial candidate's second
process slows all four programs together; unrelated controls also fluctuate.
The cause is unresolved. Allocation-count medians are unchanged in every Go row.
Allocated-byte medians differ only in table construction: 643.5/644.5 B/op in the
pilot and 638/639 B/op in the repeat, both unresolved. This measures allocation
traffic, not retained heap.

## CBOR application

Six fresh-process pairs per mode after two discarded warmup pairs, using the
unchanged large fixture. The maintained comparator computes 95% paired-bootstrap
intervals with 10,000 resamples and seed 1. These cohorts are separate from the
Go cohorts and were collected between them.

| Operation | Baseline | Candidate | Time change | 95% interval | Baseline/candidate CV |
| --- | ---: | ---: | ---: | ---: | ---: |
| Load | 1835.342 ms | 1782.325 ms | -2.89% | -6.65% to -0.78% | 18.32% / 14.79% |
| Save | 1705.217 ms | 1651.125 ms | -3.17% | -4.66% to +3.65% | 1.66% / 3.22% |

Load has an exploratory improvement signal; save remains unresolved. Load's
allocated-byte medians are 107,475,832/107,475,824, with 664,877 allocations in
both. Save's are 317,083,456/316,892,384 bytes and 3,257,739/3,257,725 allocations.
Retained memory was not measured. Both reports explicitly disable qualification.

## Conditions, validation and commands

Go 1.26.0, Linux/amd64 under WSL2, Ryzen 9 9950X3D. Timed processes ran serially
on CPU 2 with GOMAXPROCS=1, GOGC=100, GOMEMLIMIT=off, and Go test CPU=1. Host
power, frequency and external load were uncontrolled, including during the
repeat. Go samples target 500 ms; runtime order alternates each round. CBOR order
uses the maintained runner's randomization with seed 1.

Go timing excludes compilation, setup, warmup and output checks. CBOR timing
covers the complete operation, including I/O, but excludes setup, save preload
and verification. Every recorded CBOR result passes the established oracle:
9,208,046 bytes, 183,513 tables, 938,452 entries and the canonical graph digest.

Root tests, conformance, vet, benchmark-module tests and CBOR-module tests passed.
Candidate tests cover numeric strings, operand order, number/table metamethod
precedence, signed zero, NaN, overlapping destinations, malformed instructions,
extra SETLIST words and private-opcode rejection. Dumped arithmetic executes in
PUC 5.1.5. Source, executable, fixture and raw-output identities and the complete
sample inventories were independently checked. Cross-platform CI, a full
15-sample qualification and retained-memory checks were not performed.

Both checkouts build the maintained Go harness with `go test -c` in `benchmarks`.
Each sample invokes its saved binary with:

```sh
GOGC=100 GOMEMLIMIT=off GOMAXPROCS=1 taskset -c 2 "$LUNAR_TEST_BINARY" \
  -test.run='^$' \
  -test.bench='^(BenchmarkPrograms|BenchmarkEmbedding|BenchmarkInterpreter)$/.*$/^runtime=lunar$' \
  -test.benchtime=500ms -test.count=1 -test.cpu=1 -test.benchmem
```

CBOR workers use `go build -trimpath ./cmd/workload`. The maintained
[`cmd/run`](../../cbor/cmd/run) and [`cmd/compare`](../../cbor/cmd/compare) commands
use `-preset large -measurement timing`, each mode, `-runs 6 -warmups 2 -seed 1`,
and `-min-samples 6 -bootstrap 10000 -seed 1`, respectively. Both commands pin
the baseline revision and binary hash and require clean builds. Exact invocations,
raw records, binaries and analysis remain in `.bench/numeric-operands/`.

Use the [investigation note](../../INVESTIGATIONS.md) for the next priorities.
