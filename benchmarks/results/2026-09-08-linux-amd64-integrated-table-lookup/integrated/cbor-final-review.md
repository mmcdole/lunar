# Integrated lookup CBOR assessment

The full CBOR evidence shows a modest save improvement and an unresolved load slowdown. It does not qualify the patch under the no-regression condition: the full Go comparison already found three embedding regressions. All four CBOR cohorts were explicitly collected as exploratory evidence, with `qualification=false`. The earlier six-pair pilot remains a separate cohort.

The predefined timing cohorts each contain 15 baseline/candidate pairs. Positive time changes mean slower execution.

| Operation | Main median | Candidate median | Time change | 95% paired-bootstrap interval |
| --- | ---: | ---: | ---: | ---: |
| Load | 1755.430 ms | 1763.344 ms | +0.45% | −0.03% to +0.96% |
| Save | 1597.531 ms | 1575.076 ms | −1.41% | −1.90% to −0.21% |

Save improves in this cohort. Load's interval narrowly spans zero; its positive median should be reported, and the interval does not establish equivalence. The 3.75% load regression measured with the earlier shared lookup variant is not reproduced here. These are different candidates and cohorts, so that comparison does not isolate a causal mechanism and their samples are not pooled.

Timing-run allocation medians are 107,475,832 versus 107,475,816 bytes for load, with 664,877 allocations in both lanes. Save increases from 317,001,568 to 317,056,160 bytes (+54,592 bytes, approximately 0.0172%) and from 3,257,733 to 3,257,737 allocations. The patch does not reduce application allocation counts in these measurements.

The retained-memory cohorts each contain only three pairs. They call `stabilizeHeap` before the operation and again before reading retained heap; these are different measurement conditions from the 15-pair timing cohorts. Their elapsed fields describe the operation after the extra initial heap stabilization, and are not used for speed acceptance.

| Retained cohort | Main retained-heap delta | Candidate retained-heap delta | Main elapsed median | Candidate elapsed median | Descriptive time change |
| --- | ---: | ---: | ---: | ---: | ---: |
| Load | 75,721,136 B | 75,721,136 B | 1745.610 ms | 1788.586 ms | +2.46% |
| Save | 7,283,304 B | 7,283,304 B | 1617.595 ms | 1583.095 ms | −2.13% |

Retained-heap delta medians are identical in both lanes for each operation. This is a measurement of retained heap, not a claim that every memory metric is identical: retained-run save allocation medians are 316,947,008 versus 317,383,696 bytes and 3,257,729 versus 3,257,761 allocations. Retained-run load allocation medians are identical at 107,474,712 bytes and 664,873 allocations. The differing elapsed results are preserved without treating these small cohorts as equivalent to, or replacements for, the predefined timing cohorts.

Independent validation passed for all 72 recorded worker processes: 30 load-timing records, 30 save-timing records and six records in each retained cohort. Each cohort also had two discarded warmup pairs, totaling 16 separate warmup processes; these are excluded from the recorded sample counts and statistics. The collector completed successfully, and each finalized manifest reports collection and comparison exit code zero.

The checked baseline is clean main `5fc51e449a6661340056544e995aae4fdf89dcb2`; the candidate is clean `22ad3f1e7b751031360705eea9338b5f0428a1e5`. Both worker hashes, the byte-identical candidate worker reuse record, original build records, unchanged CBOR harness, pinned inputs, artifact hashes and the immutable failed program-gate evidence match their manifests. The wrapper's complete paired order, per-record identities, runtime and execution settings, fixtures and structural oracles also pass. Recomputing all medians and re-running the pinned 10,000-resample paired-bootstrap comparison reproduced all four reports exactly apart from their output destination.

Every recorded process returned the expected 341 areas, 36,705 rooms, 109,742 exits, 183,513 tables, 938,452 entries and 9,208,046 encoded bytes, with digest `dfced0fa169e1abb659ef54220f2395c7c3b5757c1b8ae129b3487c7a489eead`. The reports' `strict_evidence=false` describes their non-qualifying comparison mode; the independent wrapper checks above still passed. Exact source, worker, manifest and report hashes are preserved in [cbor-full-validation.json](cbor-full-validation.json), with reproducible checks in [validate-cbor.py](validate-cbor.py). This review ran no workloads or builds and changed no hashed collection artifacts.
