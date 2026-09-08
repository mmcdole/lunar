| Metric | Baseline median | Candidate median | Reduction |
|---|---:|---:|---:|
| Elapsed | 1756.261 ms | 1812.597 ms | -3.21% (95% bootstrap -4.68%..-2.82%) |
| Allocated | 107474712 B | 107474696 B | 0.00% |
| Allocations | 664873 | 664873 | 0.00% |
| Retained delta | 75721136 B | 75721136 B | 0.00% |

Samples: 3 baseline / 3 candidate; timing CV: 0.57% / 0.20%.

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
| Report output | /tmp/lunar-array-access/shared/cbor/load-retained-report.md |
| Report overwrite allowed | false |
| Report format | markdown |

Qualification: false

| Gate | Enabled | Policy | Actual | Result |
|---|---:|---:|---:|---:|
| baseline timing CV | false | disabled | 0.0057 | - |
| candidate timing CV | false | disabled | 0.0020 | - |
| median speedup | false | disabled | -0.0321 | - |
| elapsed ratio | false | disabled | 1.0321 | - |
| allocated-byte ratio | false | disabled | 1.0000 | - |
| malloc ratio | false | disabled | 1.0000 | - |
| excess malloc removal | false | disabled | - | - |
