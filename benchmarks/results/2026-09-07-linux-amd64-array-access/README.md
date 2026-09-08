# Existing-array access investigation

Shortening existing-array access produces whole-program gains, but this experiment has not produced a patch that meets the no-regression requirement. The final shared-read candidate reduces fannkuch-redux time by 20.90% and spectral-norm by 4.89%. CBOR load is 3.75% slower, a regression in a complete application workload. String echo is also 4.86% slower, and the callback median remains 0.74% slower with an inconclusive test.

[Issue #17](https://github.com/mmcdole/lunar/issues/17) records the general performance problem. The runtime experiment is preserved in [the candidate patch](shared/candidate.patch); it has not been merged. The root README continues to report the existing main measurements. Fresh candidate/GopherLua/go-lua comparisons are retained with the evidence, but have not replaced those published tables.

## PUC comparison and scope

PUC Lua 5.1 checks exact numeric indices and reaches an existing array slot through a short lookup. PUC 5.5 makes the early array read/write decision explicit. Lunar's main path first produces a general table-key classification. The experiment moves an existing-array check ahead of that work, retaining Lua 5.1 double-valued numbers. Main already avoids numeric hashing for positive integer keys, so eliminating array-hit hashing is not the source of the gain.

The shared read applies to VM indexing, Lua rawget, public Table.RawGet, and native Frame indexing. It accepts any supported array size and any stored value type, including references, strings, functions and false. The write shortcut covers currently non-nil array slots and uses existing replacement/deletion bookkeeping. Nil slots, insertion, exceptional keys and metamethod handling retain their established paths. No caches, value-layout changes, or workload-specific thresholds are added.

The [source rationale](source-rationale.md) pairs the Lunar and PUC paths, identifies the official source archives and their hashes, and explains the numeric-index proof. The [semantic review](review.md) covers public/raw callers, missing entries, ownership and reference lifetime.

## Complete-program comparison

The comparison base is merged main `5fc51e449a6661340056544e995aae4fdf89dcb2`. The final candidate is `a98a15738f29dbf4684e7ee51213ab8f811673df`. Each cell below is the median of 15 samples; positive changes are slower.

| Program | Main | Candidate | Time change | Comparison |
| --- | ---: | ---: | ---: | --- |
| binary-trees | 136.435 ms | 136.560 ms | +0.09% | No significant difference, p=.775 |
| fannkuch-redux | 18.488 ms | 14.623 ms | -20.90% | p<.001 |
| n-body | 44.108 ms | 44.378 ms | +0.61% | No significant difference, p=.624 |
| spectral-norm | 45.258 ms | 43.044 ms | -4.89% | p<.001 |

The [complete 13-case assessment](shared/program-gate.md) includes every interpreter and embedding control. Reusing a Go-built table improves 13.38%. String echo increases from 76.21 to 79.91 ns (+4.86%, p<.001); 1,000 callbacks increase from 51.677 to 52.060 microseconds (+0.74%, p=.061). There is no significant difference in measured allocation counts or byte traffic in the final cohort. Allocation traffic does not measure retained memory.

The gains establish relevance to array-heavy programs. They do not imply that all Lua programs improve: record fields, strings, native calls, insertion and collection have different cost distributions. No aggregate runtime score is used for acceptance.

## CBOR application workload

The unchanged vendored codec loads or saves the established 9,208,046-byte graph. Every sample passed the structural oracle: 183,513 tables, 938,452 entries and the expected digest. Timing covers the complete loadBenchmarkGraph or saveBenchmarkGraph operation, including its existing file IO and graph traversal. Fixture setup, save-mode preload, roundtrip verification and digest checks are outside timing.

There are 15 randomized adjacent main/candidate pairs per mode after two discarded warmup pairs. The intervals use 10,000 paired bootstrap resamples with seed 1. Positive changes are slower.

| Operation | Main | Candidate | Time change | 95% interval for change |
| --- | ---: | ---: | ---: | ---: |
| CBOR load | 1740.668 ms | 1805.956 ms | +3.75% | +2.97% to +4.67% |
| CBOR save | 1598.034 ms | 1580.729 ms | -1.08% | -2.29% to +0.79% |

Load regresses by 65.29 ms, with an interval entirely on the slower side. Save shows no clear change. This application result is sufficient to reject the patch under the requested no-regression requirement. The two algorithm gains do not cancel the codec regression.

The separate retained-memory cohorts contain three recorded pairs per mode, also after two warmup pairs. Median heap increase after collection is identical in each lane: 75,721,136 bytes for load and 7,283,304 bytes for save. Save's increase is relative to its already loaded graph. These observations show no retained-memory cost in the measured workloads; the cohorts' elapsed times are not used to qualify speed.

Load's timing allocation medians are exactly equal: 107,475,832 bytes and 664,877 allocations. Save's medians differ by about 0.05% in bytes and 12 allocations out of roughly 3.26 million; no broad memory improvement is claimed. Timing-record heap zeroes are absent-field defaults, not retained-memory observations.

The [independent CBOR review](shared/cbor-final-review.md) validates all 72 recorded processes.

[Load timing](shared/cbor/load-timing-report.md), [save timing](shared/cbor/save-timing-report.md), [load retention](shared/cbor/load-retained-report.md), and [save retention](shared/cbor/save-retained-report.md) retain every result. Collections were explicitly exploratory because the earlier Go-suite gate was unresolved at launch and subsequently failed. The compare tool's qualification gates were disabled; the wrapper still required and verified exact identities, input hashes, clean revisions, complete adjacent pairs and all output oracles. A successful collection exit is not a passed speed gate.

## Earlier variants

All attempted timing cohorts are retained separately; samples from different revisions or collection periods are not pooled.

- `initial/candidate.patch` (`cd44f54`) puts the read shortcut in the VM helper and bounds the number before integer conversion. Its complete 15-sample comparison shows fannkuch -23.81%, spectral-norm -6.04%, binary-trees -2.14%, n-body +1.52%, and callbacks +1.34%. A separate 15-sample repeat reverses the n-body result but repeats the callback slowdown at +1.26%. That version was not qualified.
- `unsigned/candidate.patch` (`c013ee6`) combines an unsigned array bound with the exact numeric round trip. Its five-sample pilot retains the array-program gains and a +0.98% callback median. It did not establish a regression fix.
- `shared/candidate.patch` (`a98a157`) puts reads in the shared raw lookup, broadening API coverage. The full results above supersede its pilot. In particular, the binary-trees pilot gain did not persist in the full collection.

The [control review](control-review.md) traces the callback through the unchanged constant-string lookup and native-call paths. The measured slowdowns have no demonstrated source-level cause; no claim is made about alignment, caches or register pressure. Three source-based variants were measured; no binary-placement changes were attempted.

## Measurement and correctness

Go timing used Go 1.26.0 on Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D, CPU 2 affinity, GOMAXPROCS=1, GOGC=100, GOMEMLIMIT=off and test CPU=1. Each sample targets 500 ms. The full collection rotates four process lanes: baseline Lunar, candidate Lunar, GopherLua and go-lua. It contains 660 samples across 44 rows, with output checks, source/harness/binary hashes and run order validated. Benchmark workloads ran serially; preparation could use other guest CPUs. Host power, frequency and external load were uncontrolled. Small differences are specific to this environment.

The full programs and embedding operations use B.Loop. Loading, compilation, setup, warmup and final output checks are outside timing; required host result consumption is timed. The benchmark executable includes the PUC adapter, but no PUC lane is measured in these candidate comparisons. The separately labeled main-profile evidence supports source attribution and is not a candidate speed measurement.

Root tests, the official Lua conformance suite, vet and race checks pass. Benchmark module tests/vet pass, including the PUC adapter. CBOR module tests/vet and the stock GopherLua CI configuration pass. New behavior tests cover numeric-key distinctions, nil/metamethod behavior, deletion, false/reference-valued entries and reference release through weak tables. Those tests also passed on unchanged main through an overlay. The initial 386 binary compiled, but this host stopped its execution with `signal: bad system call`; no 386 assertions ran.

## Next investigation

The [fallback review](shared/fallback-review.md) identifies a concrete difference from PUC. The candidate attempts an array shortcut and then restarts general classification on a miss. PUC places array selection inside its type-directed lookup and continues an integer array miss directly to numeric hashing. Dynamic nonnumeric keys, array growth, and writes to nil slots expose different kinds of repeated work in Lunar's candidate.

The next useful evidence is the frequency and sampled cost of those paths in CBOR, using separate diagnostic instrumentation and paired main/candidate profiles. The source establishes extra work; it does not attribute the entire 3.75% slowdown to it. That attribution should precede another implementation variant.

No runtime PR was opened from this unqualified experiment. The [evidence index](EVIDENCE.md) identifies all cohorts, source snapshots, provenance and collection tools. [SHA256SUMS](SHA256SUMS) covers this report and the archived files.
