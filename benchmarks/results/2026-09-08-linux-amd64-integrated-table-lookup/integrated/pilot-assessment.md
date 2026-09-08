# Integrated lookup pilot assessment

The six-sample pilot supports completing the full comparison. It does not qualify the patch for the no-regression gate. The candidate is `22ad3f1e7b751031360705eea9338b5f0428a1e5`; the baseline is unchanged main `5fc51e449a6661340056544e995aae4fdf89dcb2`.

The Go pilot contains four complete programs, with six samples per program per lane. Negative time changes mean faster execution.

| Program | Main median | Candidate median | Time change | benchstat p |
| --- | ---: | ---: | ---: | ---: |
| binarytrees | 134.925 ms | 130.645 ms | −3.17% | .002 |
| fannkuchredux | 18.284 ms | 14.484 ms | −20.78% | .002 |
| nbody | 43.377 ms | 42.869 ms | −1.17% | .004 |
| spectralnorm | 44.552 ms | 42.368 ms | −4.90% | .002 |

These results are encouraging across the four programs. This pilot does not include the embedding callback or string echo controls that raised concerns in the earlier shared lookup variant. Their results must be assessed in the full comparison. Allocation counts show no significant changes in this pilot; fannkuch's allocated-byte median increases by 1.88% (p=.002).

The CBOR pilot uses six recorded pairs per operation, following two discarded warmup pairs. It measures the complete load or save operation with the existing fixture and structural oracle.

| Operation | Main median | Candidate median | Time change | 95% interval for time change |
| --- | ---: | ---: | ---: | ---: |
| Load | 1757.030 ms | 1756.998 ms | −0.002% | −0.95% to +0.48% |
| Save | 1614.738 ms | 1591.448 ms | −1.44% | −2.96% to +1.91% |

Both intervals span zero. The load regression observed with the earlier shared variant is absent from this pilot, but six pairs cannot establish equivalence or rule out a smaller regression. The apparent save improvement is also unresolved. These are separate candidates and cohorts; their samples are not pooled.

Load allocation medians are identical at 107,475,832 bytes and 664,877 allocations. Save medians are 317,028,880 versus 317,056,168 bytes and 3,257,735 versus 3,257,737 allocations. No retained-memory measurement is included in this pilot.

Independent validation passed. The Go evidence contains exactly 48 samples across eight runtime rows and 12 processes, with the expected alternating lane order. Source revisions, clean worktrees, runtime and harness hashes, binary identities, collector and raw hashes, per-process output hashes, row inventory and metric fields match the manifests. Re-running the pinned statistical tool reproduced the program report byte for byte.

The CBOR wrapper checks passed for both operations despite their explicitly exploratory, non-qualifying report mode. Worker and source identities, input and fixture hashes, build and artifact manifests, paired sample order, scope, metadata and all structural oracles match. The pinned paired-bootstrap comparison reproduced the reported results. Every operation returned the expected 341 areas, 36,705 rooms, 109,742 exits, 183,513 tables, 938,452 entries and 9,208,046 encoded bytes, with digest `dfced0fa169e1abb659ef54220f2395c7c3b5757c1b8ae129b3487c7a489eead`.

Validation details and input hashes are recorded in [pilot-validation.json](pilot-validation.json). No workloads or builds were run for this review. The acceptance decision remains open until the full program, embedding and application evidence is complete.
