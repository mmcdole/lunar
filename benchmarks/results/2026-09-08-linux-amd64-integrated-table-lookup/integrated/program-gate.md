# Integrated lookup full Go comparison

The no-regression gate fails. All four complete programs improve, but three embedding workloads regress: scalar calls by 1.26%, the 1,000-callback loop by 2.03%, and string echo by 1.71%. The patch has measured benefits and costs; it is not qualified as a no-regret change. The full CBOR comparison should remain exploratory.

This is candidate `22ad3f1e7b751031360705eea9338b5f0428a1e5` against main `5fc51e449a6661340056544e995aae4fdf89dcb2`. The full cohort contains 15 samples per runtime row, collected in rotating lane order on CPU 2 with GOMAXPROCS=1, GOGC=100, GOMEMLIMIT=off and 500 ms per benchmark. Pilot results and earlier variants are separate cohorts and are not pooled.

| Workload | Main median | Candidate median | Time change | benchstat p |
| --- | ---: | ---: | ---: | ---: |
| binarytrees | 135.539 ms | 131.692 ms | −2.84% | <.001 |
| fannkuchredux | 18.230 ms | 14.463 ms | −20.66% | <.001 |
| nbody | 43.098 ms | 42.798 ms | −0.70% | <.001 |
| spectralnorm | 44.637 ms | 42.096 ms | −5.69% | <.001 |
| numeric_for_10000 | 36.996 µs | 37.023 µs | +0.07% | .775 |
| fixed_lua_calls_1000 | 24.511 µs | 24.559 µs | +0.20% | .631 |
| table_field_get_set_10000 | 184.345 µs | 184.202 µs | −0.08% | .412 |
| string_append_256 | 34.881 µs | 34.664 µs | −0.62% | .389 |
| go_to_lua_scalars | 53.25 ns | 53.92 ns | +1.26% | <.001 |
| lua_to_go_scalar_1000 | 50.572 µs | 51.600 µs | +2.03% | <.001 |
| go_string_echo_128B | 76.48 ns | 77.79 ns | +1.71% | .001 |
| prebuilt_go_table_16_4_to_lua | 267.0 ns | 229.8 ns | −13.93% | <.001 |
| create_fill_go_table_16_4_to_lua | 2.318 µs | 2.164 µs | −6.64% | .013 |

Negative time changes mean faster execution. The four interpreter controls have no statistically resolved timing differences in this cohort; this does not prove equivalence. The callback increase is 1.028 µs per 1,000-callback benchmark operation, alongside its loop and interpreter work. The scalar and string echo differences are 0.67 ns and 1.31 ns per operation. Their small absolute size does not erase the regressions under the stated acceptance condition.

All 13 Lunar allocation-count medians are unchanged. Binarytrees remains at 1,009,045 allocations, fannkuch at 15, nbody at 25 and spectralnorm at 35; string append remains at 255 and create/fill table at six. All other controls remain at zero. Fannkuch's allocated-byte median increases from 1,168 to 1,190 bytes (+1.88%, p<.001). Binarytrees decreases from 68,994,128 to 68,935,440 bytes (p=.340), and create/fill table from 661 to 657 bytes (p=.505); neither difference is statistically resolved. All other byte medians are identical. These are per-operation allocation measurements, not retained-memory measurements.

Strict evidence validation passed: 60 successful processes, 44 runtime rows and 660 samples, with exactly 15 observations per row. The raw file, collector, per-process output hashes, commands, rotation order, complete row inventory, metrics and platform headers match the manifest. Independent checks also confirm both clean source revisions, both actual binary hashes, all recorded runtime and harness hashes, identical harnesses and build environments, and the linked PUC library hash. See [full-validation.json](full-validation.json) and [full-medians.json](full-medians.json); the three `full-*.benchstat.txt` files contain the statistical reports.

One focused repeat of the same three embedding workloads with the exact existing binaries is reasonable to assess reproducibility after CBOR finishes. It is not needed to establish that this full cohort fails the gate, and it cannot establish the mechanism behind a slowdown. Its sample count, workload inventory and lane rotation should be fixed before collection. Report it separately alongside this full cohort, including any disagreement; do not select the more favorable result or infer an unmeasured code-placement cause. No additional runtime variant is justified by these measurements alone.
