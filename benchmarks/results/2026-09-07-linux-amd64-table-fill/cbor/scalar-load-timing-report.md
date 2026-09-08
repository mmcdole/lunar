| Metric | Baseline median | Candidate median | Reduction |
|---|---:|---:|---:|
| Elapsed | 1749.885 ms | 1728.755 ms | 1.21% (95% bootstrap 0.71%..2.30%) |
| Allocated | 107475832 B | 107475832 B | 0.00% |
| Allocations | 664877 | 664877 | 0.00% |
| Retained delta | 0 B | 0 B | 0.00% |

Samples: 15 baseline / 15 candidate; timing CV: 0.81% / 0.67%.

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
| Report output | /tmp/lunar-table-fill/cbor/scalar-load-timing-report.md |
| Report overwrite allowed | false |
| Report format | markdown |

Qualification: false

| Gate | Enabled | Policy | Actual | Result |
|---|---:|---:|---:|---:|
| baseline timing CV | false | disabled | 0.0081 | - |
| candidate timing CV | false | disabled | 0.0067 | - |
| median speedup | false | disabled | 0.0121 | - |
| elapsed ratio | false | disabled | 0.9879 | - |
| allocated-byte ratio | false | disabled | 1.0000 | - |
| malloc ratio | false | disabled | 1.0000 | - |
| excess malloc removal | false | disabled | - | - |
