| Metric | Baseline median | Candidate median | Reduction |
|---|---:|---:|---:|
| Elapsed | 1755.430 ms | 1763.344 ms | -0.45% (95% bootstrap -0.96%..0.03%) |
| Allocated | 107475832 B | 107475816 B | 0.00% |
| Allocations | 664877 | 664877 | 0.00% |
| Retained delta | 0 B | 0 B | 0.00% |

Samples: 15 baseline / 15 candidate; timing CV: 0.59% / 0.49%.

| Qualification policy | Value |
|---|---|
| Comparison mode | implementations |
| Baseline runtime | Lunar (Lua 5.1) |
| Candidate runtime | Lunar (Lua 5.1) |
| Minimum samples | 15 |
| Bootstrap resamples | 10000 |
| Bootstrap seed | 1 |
| Strict evidence validation | false |
| Collection metadata required | false |
| Required measurement | any |
| Expected baseline SHA-256 | 3c29592b0328af4a52689884d46f90e49fcb05001a8a23f340a6de028d5fc5b0 |
| Expected baseline revision | 5fc51e449a6661340056544e995aae4fdf89dcb2 |
| Expected pre-tranche SHA-256 | not required |
| Expected pre-tranche revision | not required |
| Clean builds required | true |
| Report output | /tmp/lunar-array-followup/integrated/cbor-full/load-timing-report.md |
| Report overwrite allowed | false |
| Report format | markdown |

Qualification: false

| Gate | Enabled | Policy | Actual | Result |
|---|---:|---:|---:|---:|
| baseline timing CV | false | disabled | 0.0059 | - |
| candidate timing CV | false | disabled | 0.0049 | - |
| median speedup | false | disabled | -0.0045 | - |
| elapsed ratio | false | disabled | 1.0045 | - |
| allocated-byte ratio | false | disabled | 1.0000 | - |
| malloc ratio | false | disabled | 1.0000 | - |
| excess malloc removal | false | disabled | - | - |
