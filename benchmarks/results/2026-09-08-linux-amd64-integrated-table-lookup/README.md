# Integrated table lookup assessment

The integrated lookup change is worth code review: all four complete programs improve, CBOR save improves modestly, and the earlier CBOR load regression does not recur in the predefined timing cohort. A callback control is repeatedly slower by 2.0–2.4%, about 1.2 ns per callback in its tight loop. This is a measured tradeoff, and the patch has not passed the literal no-regression condition. The PR remains a draft while the callback tradeoff is reviewed.

The measured runtime candidate is `22ad3f1e7b751031360705eea9338b5f0428a1e5`, based on main `5fc51e449a6661340056544e995aae4fdf89dcb2`. [Issue #17](https://github.com/mmcdole/lunar/issues/17) describes the indexed-table performance problem.

## What changed

PUC dispatches table lookup by key type, tries an integer array location, and continues a miss through the selected numeric lookup. Lunar's candidate applies that structure to shared raw reads and dynamic Lua writes. Reads no longer prepare insertion-specific normalization results; writes retain the array location and its presence status through the existing replacement/insertion decision. Main already avoids hashing array hits.

The change is confined to two runtime functions (61 added and 13 removed lines). It applies to ordinary dynamic keys, all supported array sizes and stored value types. It adds no cache, storage layout, public API or workload-specific threshold. Numeric edge cases, nil slots, metamethods, deletion and weak references retain their existing behavior. The [source rationale](source-rationale.md), [semantic review](integrated/review.md) and [patch](integrated/candidate.patch) give the details.

## Complete programs

| Program | Main | Candidate | Time change |
| --- | ---: | ---: | ---: |
| binary-trees | 135.539 ms | 131.692 ms | −2.84% |
| fannkuch-redux | 18.230 ms | 14.463 ms | −20.66% |
| n-body | 43.098 ms | 42.798 ms | −0.70% |
| spectral-norm | 44.637 ms | 42.096 ms | −5.69% |

Each row has 15 samples per runtime; all four differences have benchstat p<.001. Negative time changes mean less elapsed time. These are the existing complete Lua benchmark programs at the repository's documented inputs, not a claim about all Lua applications. The independent six-sample pilot is retained separately and is not pooled with this cohort.

The [full Go assessment](integrated/program-gate.md) reports all 13 Lunar rows, including interpreter and embedding controls. Reusing a Go-built table takes 13.93% less time, and building then reading a table takes 6.64% less. The four interpreter controls have no statistically resolved difference. Scalar calls, the callback loop and string echo are slower in this full cohort.

Allocation-count medians are unchanged. Fannkuch's measured allocation traffic rises from 1,168 to 1,190 bytes per operation, with 15 allocations in both binaries; this is not a retained-heap result. Other byte differences are either identical or statistically unresolved.

## Host-call repeat

The full comparison prompted one predefined repeat of its three slower embedding cases, using the exact same binaries and 15 fresh samples per row. Both cohorts remain visible:

| Control | Full comparison time change | Repeat main | Repeat candidate | Repeat time change |
| --- | ---: | ---: | ---: | ---: |
| Go calls Lua with scalar arguments | +1.26% | 53.45 ns | 53.23 ns | −0.41% |
| Lua calls Go 1,000 times | +2.03% | 49.750 µs | 50.956 µs | +2.42% |
| Lua echoes a 128-byte Go string | +1.71% | 78.10 ns | 74.47 ns | −4.65% |

The callback regression repeats (p<.001), including both execution orders. The scalar repeat is unresolved (p=.072), and echo reverses (p<.001). Neither scalar nor echo supports a consistent regression claim across these cohorts. All three controls allocate zero in every sample. [Repeat review](integrated/focused-repeat-review.md).

The callback case runs a Lua loop that calls a very small Go addition function. Its repeat difference is 1.206 µs per 1,000-call operation, alongside loop and interpreter work. This may matter to programs dominated by similarly small callbacks. It does not establish a 2.42% slowdown in ordinary Lua applications, and its cause has not been isolated.

## CBOR application

| Operation | Main median | Candidate median | Time change | 95% paired-bootstrap interval |
| --- | ---: | ---: | ---: | ---: |
| Load | 1755.430 ms | 1763.344 ms | +0.45% | −0.03% to +0.96% |
| Save | 1597.531 ms | 1575.076 ms | −1.41% | −1.90% to −0.21% |

Each timing cohort contains 15 adjacent baseline/candidate pairs in alternating order. Load is unresolved; that is not proof of equivalence. Save shows a modest improvement. All output oracles pass for the 9 MB graph containing 183,513 tables and 938,452 entries. These cohorts were explicitly exploratory because the earlier full Go control gate failed. Successful evidence validation is not a passed speed gate.

The separate three-pair retained-memory cohorts have identical retained-heap delta medians: 75,721,136 bytes after load and 7,283,304 bytes after save. They add heap stabilization before the operation, so their elapsed values are kept separate: load is 2.46% slower and save 2.13% faster in those small cohorts. Those values are descriptive, not replacements for the predefined timing cohorts. Allocation traffic is not entirely identical: the timing save median increases by 54,592 bytes and four allocations. The [full CBOR review](integrated/cbor-final-review.md) preserves all timings, memory metrics, conditions and identity checks.

## Review decision

The complete-program gains justify reviewing this bounded change. The roughly 1.2 ns callback-control cost is small in absolute terms, but remains relevant to programs dominated by trivial callbacks. The original no-regression gate remains failed. Review should weigh that cost against the measured application gains before merging.

The PR updates the existing Lunar/GopherLua/go-lua README tables from the complete 15-round comparison, including the slower host-call figures from that cohort. It does not substitute the more favorable repeat figures. PUC timing columns are not added, and the earlier M3 retained-memory tables retain their original provenance.

## Measurement and validation

Go 1.26.0, Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D. All timed executions are serialized on CPU 2 with GOMAXPROCS=1, GOGC=100 and GOMEMLIMIT=off. The full Go comparison rotates four runtime lanes through 15 rounds, with a fixed 500 ms target per row. It contains 60 successful processes, 44 rows and 660 samples, including fresh GopherLua and go-lua results. No PUC timing is added to the root README.

The host's power, frequency and external load are uncontrolled. Results describe these binaries on this machine; small changes should not be treated as a general runtime ranking. The [compiler review](integrated/compiler-review.md) records larger helper frames and removal of the redundant direct-array bounds check. Those facts do not establish the timing mechanism. The scalar and echo Lua bodies do not use tables, and the callback uses the unchanged constant-string lookup. Code placement has not been established as the cause of their timing changes.

Root tests, the Lua conformance suite, vet, race checks, benchmark-module tests/vet and both CBOR module configurations pass. New numeric boundary, metamethod and reference-release tests also pass when overlaid onto unchanged main. The conversion proof covers both Go int widths; execution checks for this candidate ran on amd64. [Validation manifest](integrated/validation-manifest.json).

The paired profiles and operation counts under `attribution/` describe the previously rejected shared-array candidate. They motivated this integrated experiment and do not measure its speed or isolate the previous slowdown. No previous candidate samples are pooled here. See the [evidence index](EVIDENCE.md) and [archive manifest](archive-manifest.json) for provenance.
