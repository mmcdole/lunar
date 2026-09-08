# CBOR: final independent assessment

The shared array-lookup candidate **fails the no-regression requirement on a complete application workload**: CBOR loading is 3.75% slower, with the entire 95% paired-bootstrap interval above zero. Saving has a 1.08% faster median, but its interval spans zero. Retained-memory medians are unchanged. These results do not qualify the change as a no-regret optimization.

## Complete-operation timing

Each timing cohort has 15 recorded baseline/candidate pairs after two discarded warmup pairs. Positive changes below mean slower execution. Intervals use 10,000 paired bootstrap resamples, seed 1.

| Operation | Baseline median | Candidate median | Time change | 95% interval for time change |
| --- | ---: | ---: | ---: | ---: |
| Load | 1740.668 ms | 1805.956 ms | +3.75% | +2.97% to +4.67% |
| Save | 1598.034 ms | 1580.729 ms | −1.08% | −2.29% to +0.79% |

Loading takes 65.288 ms more per operation. Its allocation medians remain identical: 107,475,832 bytes and 664,877 allocations. Saving's timing interval does not demonstrate a reliable speed improvement. The measured operations are complete graph loads and saves, with structural roundtrip validation outside their timed regions.

## Retained memory

These separate cohorts contain three recorded pairs per operation after two discarded warmup pairs. The figures are measured post-collection heap deltas, in bytes. They are descriptive memory results; elapsed times from the retained protocol are not used for speed acceptance.

| Operation | Baseline median | Candidate median |
| --- | ---: | ---: |
| Load | 75,721,136 B | 75,721,136 B |
| Save | 7,283,304 B | 7,283,304 B |

The medians show no retained-memory benefit. This does not assert equality of every sample. Zero heap fields in the separate timing reports are defaults for unmeasured fields, not additional retained-memory observations.

## Evidence validation

All four cohorts completed successfully. Independent checks cover all 72 recorded samples: 30 load-timing, 30 save-timing, six load-retained and six save-retained. They confirm complete adjacent pair order, sample counts, collection identity, runtime metadata, clean source revisions and worker hashes. Artifact, input, fixture, build-manifest and orchestrator hashes match; the pinned `control-review.md` launch evidence remains unchanged. Report medians were recomputed directly from the records; retained deltas also equal `heap_retained - heap_before`. The load report's paired-bootstrap output was reproduced exactly using the pinned comparison tool. No workload, build or benchmark was run for this review.

Every sample matches the expected input and structural oracle: 9,208,046 encoded bytes, 341 areas, 36,705 rooms, 109,742 exits, 183,513 tables and 938,452 entries. The structural digest is `dfced0fa169e1abb659ef54220f2395c7c3b5757c1b8ae129b3487c7a489eead`.

Baseline revision: `5fc51e449a6661340056544e995aae4fdf89dcb2`. Candidate revision: `a98a15738f29dbf4684e7ee51213ab8f811673df`. All collections used the same pinned worker identities, inputs and fixtures, serially on CPU 2 under the recorded Go 1.26.0 / Linux amd64 / WSL2 environment.

The reports intentionally have qualification disabled because the collection launched as exploratory while the program gate was unresolved. The wrapper's identity, oracle and pairing checks remained mandatory and passed. A successful report exit is not a speed-gate pass; the completed evidence now establishes a failed gate in the real load application.

Reports: [load timing](cbor/load-timing-report.json), [save timing](cbor/save-timing-report.json), [load retained](cbor/load-retained-report.json), [save retained](cbor/save-retained-report.json).

The final workload tradeoff is clear: fannkuch-redux and spectral-norm improve, while CBOR loading regresses. The string-echo regression and slower callback median also remain in the [program assessment](program-gate.md). These are separate per-workload findings, with no pooled cohorts or aggregate acceptance score. The optimization has useful array-access benefits, but this version should not be promoted as a generally regression-free change.
