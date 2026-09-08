# Existing-array access investigation

This experiment was rejected: array-heavy programs improved, but CBOR loading became 3.75% slower. String echo also regressed. These findings did not meet the requested no-regression condition, so this version did not advance to a runtime PR. The [integrated lookup follow-up](../2026-09-08-linux-amd64-integrated-table-lookup/) records the subsequent experiment for [issue #17](https://github.com/mmcdole/lunar/issues/17).

## Scope and provenance

Baseline: `5fc51e449a6661340056544e995aae4fdf89dcb2`. Final shared-read candidate: `a98a15738f29dbf4684e7ee51213ab8f811673df`.

PUC Lua 5.1.5 checks exact numeric indices before reaching an existing array slot; PUC 5.5.1 makes early array read/write selection explicit. The candidate adds an existing-array check ahead of Lunar's general key classification. Main already avoided hashing positive integer array hits, so hash elimination does not explain the gains.

Shared reads cover VM indexing, Lua rawget, public Table.RawGet and native Frame indexing. Writes shortcut existing non-nil array values, retaining replacement/deletion bookkeeping. Nil slots, insertion, exceptional keys and metamethods keep their established paths. The change supports all array sizes and value types without caches or storage changes.

## Complete Go comparison

Each row is the median of 15 samples. Positive changes mean slower execution; an unresolved comparison does not establish equivalence.

| Workload | Baseline | Candidate | Time change | benchstat p |
| --- | ---: | ---: | ---: | ---: |
| binary-trees | 136.435 ms | 136.560 ms | +0.09% | .775 |
| fannkuch-redux | 18.488 ms | 14.623 ms | −20.90% | <.001 |
| n-body | 44.108 ms | 44.378 ms | +0.61% | .624 |
| spectral-norm | 45.258 ms | 43.044 ms | −4.89% | <.001 |
| Numeric loop, 10,000 iterations | 37.658 µs | 37.948 µs | +0.77% | .148 |
| Fixed Lua calls, 1,000 calls | 25.097 µs | 25.260 µs | +0.65% | .361 |
| Table field get/set, 10,000 iterations | 187.148 µs | 189.178 µs | +1.08% | .436 |
| String append, 256 iterations | 35.643 µs | 36.193 µs | +1.54% | .081 |
| Go calls Lua with scalar arguments | 54.23 ns | 54.55 ns | +0.59% | .480 |
| Lua calls Go 1,000 times | 51.677 µs | 52.060 µs | +0.74% | .061 |
| Lua echoes a 128-byte Go string | 76.21 ns | 79.91 ns | +4.86% | <.001 |
| Lua checksums a reused Go-built table | 270.6 ns | 234.4 ns | −13.38% | <.001 |
| Build a table in Go, then checksum it in Lua | 2.355 µs | 2.333 µs | −0.93% | .943 |

No row shows a statistically resolved change in allocation count or byte traffic. These are allocation measurements, not retained-memory measurements. The callback's slower median remains concerning despite its inconclusive test. Its constant-string lookup and native-call paths are unchanged; string echo does not execute the new numeric predicate either. Their timing mechanisms were not established.

## CBOR application

The unchanged codec processes a 9,208,046-byte graph containing 183,513 tables and 938,452 entries. Timing covers complete load/save operations, including existing file IO and graph traversal. Setup, save-mode preload and structural verification are outside timing. Each mode has 15 randomized adjacent baseline/candidate pairs after two discarded warmup pairs. Intervals use 10,000 paired-bootstrap resamples, seed 1.

| Operation | Baseline | Candidate | Time change | 95% interval |
| --- | ---: | ---: | ---: | ---: |
| Load | 1740.668 ms | 1805.956 ms | +3.75% | +2.97% to +4.67% |
| Save | 1598.034 ms | 1580.729 ms | −1.08% | −2.29% to +0.79% |

Load's 65.29 ms regression rejects this candidate. Save's difference is unresolved. Timing allocation medians for load are identical: 107,475,832 bytes and 664,877 allocations. Save changes from 317,165,344 to 317,001,584 bytes and 3,257,745 to 3,257,733 allocations; no broad memory benefit is claimed.

Separate three-pair retained-memory cohorts have identical median heap increases: 75,721,136 bytes for load and 7,283,304 bytes for save. Save's increase is above its already loaded graph. Those cohorts' elapsed values are not speed qualification. All 72 recorded processes passed structural oracles and provenance checks. Collection was explicitly exploratory; successful collection did not mean the speed gate passed.

## Earlier attempts

Cohorts were kept separate, without pooling samples or selecting favorable repeats:

- Initial VM-read shortcut (`cd44f54`), 15 samples: fannkuch −23.81%, spectral-norm −6.04%, binary-trees −2.14%, n-body +1.52%, callbacks +1.34%. A separate 15-sample repeat reversed n-body's result but repeated the callback regression at +1.26%.
- Unsigned-bound variant (`c013ee6`), five-sample pilot: array-program gains remained, with a +0.98% callback median. It did not establish a regression fix.
- Shared-read variant (`a98a157`): its pilot's 2.38% binary-trees improvement did not persist in the full results above.

The source review found repeated classification after shortcut misses. That motivated the follow-up; it did not establish the cause of the entire CBOR regression. No binary-placement variants were attempted.

## Conditions and validation

Go 1.26.0, Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D; serialized CPU 2 execution, GOMAXPROCS=1, GOGC=100, GOMEMLIMIT=off, test CPU=1. Go samples target 500 ms. The full collection rotates baseline Lunar, candidate Lunar, GopherLua and go-lua: 60 processes, 44 rows, 660 samples. Setup/compilation and final output checks are outside timing. No PUC timing lane was measured. Host power, frequency and external load were uncontrolled.

Root tests, Lua conformance, vet, race, benchmark-module tests/vet and both CBOR configurations passed. Added tests cover numeric distinctions, nil/metamethod behavior, deletion, false/reference values and weak-reference release; they also passed on unchanged main. The initial 386 binary compiled, but the host stopped execution with `signal: bad system call`; no 386 assertions ran.

The [original supporting files](https://github.com/mmcdole/lunar/tree/56ed803906dd3d1e19119736e3c49780b798dbb1/benchmarks/results/2026-09-07-linux-amd64-array-access) preserve raw samples, exact source and binary hashes, patches, profiles, validation and statistical output in Git history. This summary retains the decision and material results.
