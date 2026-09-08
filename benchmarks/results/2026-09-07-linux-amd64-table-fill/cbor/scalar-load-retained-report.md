| Metric | Baseline median | Candidate median | Reduction |
|---|---:|---:|---:|
| Elapsed | 1777.201 ms | 1773.217 ms | 0.22% (95% bootstrap -1.13%..0.53%) |
| Allocated | 107474712 B | 107474696 B | 0.00% |
| Allocations | 664873 | 664873 | 0.00% |
| Retained delta | 75721136 B | 75721136 B | 0.00% |

Samples: 3 baseline / 3 candidate; timing CV: 0.41% / 0.31%.

| Qualification policy | Value |
|---|---|
| Comparison mode | implementations |
| Baseline runtime | Lunar (Lua 5.1) |
| Candidate runtime | Lunar (Lua 5.1) |
| Minimum samples | 3 |
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
| Report output | /tmp/lunar-table-fill/cbor/scalar-load-retained-report.md |
| Report overwrite allowed | false |
| Report format | markdown |

Qualification: false

| Gate | Enabled | Policy | Actual | Result |
|---|---:|---:|---:|---:|
| baseline timing CV | false | disabled | 0.0041 | - |
| candidate timing CV | false | disabled | 0.0031 | - |
| median speedup | false | disabled | 0.0022 | - |
| elapsed ratio | false | disabled | 0.9978 | - |
| allocated-byte ratio | false | disabled | 1.0000 | - |
| malloc ratio | false | disabled | 1.0000 | - |
| excess malloc removal | false | disabled | - | - |
