The callback slowdown repeated, so this candidate **does not pass a strict no-regression gate**. The scalar-call and string-echo slowdowns from the full comparison did not repeat. Advancing a draft PR would require reviewing the measured tradeoff.

| Case | Main median | Candidate median | Repeat change | p | Full change |
| --- | ---: | ---: | ---: | ---: | ---: |
| Go-to-Lua scalar call | 53.45 ns | 53.23 ns | −0.41% | 0.072 | +1.26% |
| Lua-to-Go callbacks, 1,000 calls | 49.750 µs | 50.956 µs | +2.42% | <0.001 | +2.03% |
| 128-byte string echo | 78.10 ns | 74.47 ns | −4.65% | <0.001 | +1.71% |

The callback difference is 1.206 µs per 1,000-call batch, or about 1.206 ns per callback amortized across this benchmark. Callback medians are slower in both orders: +2.54% across eight main-first pairs and +2.37% across seven candidate-first pairs. These subsets are descriptive, not separate significance tests.

All 90 samples report zero bytes and zero allocations per operation. The [benchstat report](focused-repeat.benchstat.txt) includes all three metrics; the [validation record](focused-repeat-validation.json) retains exact medians, samples and provenance.

Validation confirmed all 30 fresh processes, six lane/case rows and 90 samples; exact alternating order and filters; raw/per-process hashes; source/binary identities; clean sources; matching harness/platform headers; passing processes; and manifest counts. Both cohorts used identical binaries from main `5fc51e449a6661340056544e995aae4fdf89dcb2` and candidate `22ad3f1e7b751031360705eea9338b5f0428a1e5`. Collection used CPU 2, Go 1.26.0, 500 ms per case, `GOMAXPROCS=1`, `GOGC=100`, and `GOMEMLIMIT=off`. Pinned benchstat ran on CPU 8.

The cohorts remain separate. Direction reversals limit claims about the first scalar/echo results; they do not erase them. The callback benchmark does not use the changed dynamic numeric lookup, so this repeat establishes a measured difference without identifying its mechanism. The [full embedding results](full-embedding.benchstat.txt), gate assessment and measurement manifests remain unchanged. This review ran no workloads or builds.
