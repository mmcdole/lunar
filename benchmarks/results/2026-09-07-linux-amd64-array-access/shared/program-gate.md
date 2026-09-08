# Shared array lookup: completed program and embedding assessment

The shared-read candidate delivers measured whole-program improvements in fannkuch-redux (20.90%) and spectral-norm (4.89%). Binary-trees and n-body show no significant difference. The broader comparison does **not** establish a no-regression result: string echo is 4.86% slower, and the callback median remains 0.74% slower with p=0.061. CBOR measurements are still pending and will add workload tradeoff evidence; they cannot erase these observed controls.

Assessment status: **FAILED no-regression gate because string echo is 4.86% slower (p<0.001).** Whole-program benefits are demonstrated, and the callback remains a concern. CBOR is exploratory evidence for the final tradeoff assessment; it cannot qualify this as a no-regret patch. No additional variant or timing repeat is proposed.

## Validation and source identities

The strict summarizer completed successfully on CPU 8. It validated all 60 process blocks, 44 runtime/case rows and 660 samples: 15 samples per row, including the 13 Lunar cases for baseline and candidate plus nine cases each for GopherLua and go-lua. Checks include exact case inventory and rotated run order, raw and collector SHA-256, reconstructed original per-process output SHA-256, passing processes, metric validity, manifest counts, source/build agreement and matching harness hashes.

| Artifact | Identity |
| --- | --- |
| Baseline source | `5fc51e449a6661340056544e995aae4fdf89dcb2` |
| Candidate source | `a98a15738f29dbf4684e7ee51213ab8f811673df` |
| Baseline binary SHA-256 | `9753e1de4e8e3971adf7fd66de55d6254328958859af4569bd3585e71905b74a` |
| Candidate binary SHA-256 | `bece74b5966a6acaf0dc105f5333094243b41f5793cf1662bebcf1baa12a06d5` |
| Full raw SHA-256 | `90cf8b5d59921537fc7d55109a453cb26c3e5be17141030887fedd64df29109f` |
| Full manifest SHA-256 | `6a327a05adf3da5c129180af89f85aca7866c8fecff7b527ea0f1e5521fc9e74` |

Timings were collected serially on CPU 2, Go 1.26.0, Linux/amd64 under WSL2, AMD Ryzen 9 9950X3D, `GOMAXPROCS=1`, `GOGC=100`, `GOMEMLIMIT=off`, 500 ms per benchmark process case. Lane order rotates each round. WSL2 host frequency, power and external load are uncontrolled. This review ran no workload, build or benchmark; it summarized the completed records.

## All 13 Lunar timing rows

These are medians from 15 samples in each lane. Changes use unrounded nanosecond medians. Positive changes are slower. P-values are benchstat's default comparisons; “no significant difference” is not proof of equal speed. Its confidence ranges and allocation tables remain in the linked reports.

| Group | Case | Baseline | Candidate | Change | p | Observed result |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| Program | binary-trees | 136.435 ms | 136.560 ms | +0.09% | 0.775 | No significant difference |
| Program | fannkuch-redux | 18.488 ms | 14.623 ms | −20.90% | <0.001 | Faster |
| Program | n-body | 44.108 ms | 44.378 ms | +0.61% | 0.624 | No significant difference |
| Program | spectral-norm | 45.258 ms | 43.044 ms | −4.89% | <0.001 | Faster |
| Interpreter | numeric_for_10000 | 37.658 µs | 37.948 µs | +0.77% | 0.148 | No significant difference |
| Interpreter | fixed_lua_calls_1000 | 25.097 µs | 25.260 µs | +0.65% | 0.361 | No significant difference |
| Interpreter | table_field_get_set_10000 | 187.148 µs | 189.178 µs | +1.08% | 0.436 | No significant difference |
| Interpreter | string_append_256 | 35.643 µs | 36.193 µs | +1.54% | 0.081 | No significant difference |
| Embedding | go_to_lua_scalars | 54.23 ns | 54.55 ns | +0.59% | 0.480 | No significant difference |
| Embedding | lua_to_go_scalar_1000 | 51.677 µs | 52.060 µs | +0.74% | 0.061 | Slower median; inconclusive test |
| Embedding | go_string_echo_128B | 76.21 ns | 79.91 ns | +4.86% | <0.001 | Slower |
| Embedding | prebuilt_go_table_16_4_to_lua | 270.6 ns | 234.4 ns | −13.38% | <0.001 | Faster |
| Embedding | create_fill_go_table_16_4_to_lua | 2.355 µs | 2.333 µs | −0.93% | 0.943 | No significant difference |

No Lunar row shows a significant change in measured Go allocation count or byte traffic. These allocation measurements are not retained-memory measurements.

Reports: [programs](full-programs.benchstat.txt), [interpreter](full-interpreter.benchstat.txt), [embedding](full-embedding.benchstat.txt), and [exact medians with provenance](full-medians.json). The separately produced README comparisons are [programs](readme-programs.benchstat.txt) and [embedding](readme-embedding.benchstat.txt). No README updater has been run by this review.

## Interpretation for the proposed change

The benefits have normal-program relevance. Fannkuch-redux and spectral-norm exercise existing numeric array reads and writes; the optimization applies to all eligible table sizes and numeric keys. The shared placement also serves raw table access outside the instruction helper. It is neither a special case for these programs nor a result based only on isolated operation benchmarks.

Binary-trees' earlier pilot benefit did not persist in this complete collection. It should be reported from the complete run as +0.09%, without claiming the pilot's 2.38% improvement. The final n-body estimate is similarly +0.61%, with no significant difference; previous initial-candidate cohorts changed direction, as documented in [the control review](control-review.md).

The initial full comparison and this shared full comparison are separate cohorts measuring different candidate revisions. Their samples are not pooled, and the initial or pilot results do not replace the complete shared results above.

The callback remains relevant even though p=0.061 exceeds 0.05. Its candidate median is slower in this full run, the shared pilot, the unsigned pilot, and both initial-candidate collections. Those cohorts are kept separate because they measure different code or different collection periods. The source trace in the control review shows that `host_add` uses the unchanged constant-string global lookup and native-call machinery; it never executes the numeric predicate. That limits the causal explanation available from source, but it does not justify suppressing the measured delta or declaring the control fixed.

The string-echo regression is 3.70 ns per operation in this run. The operation returns its string argument and performs public string/result handling; it does not execute the new numeric array lookup either. Its measured 4.86% cost must remain visible. No alignment, register-pressure or other binary mechanism is inferred without evidence.

CBOR load/save results should be attached for the same candidate runtime before making the final workload assessment. If those complete, checked workloads improve, the result would support accepting a documented tradeoff for workloads that benefit. The current evidence alone does not support labeling the change as having no speed regressions.
