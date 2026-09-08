| Metric | Baseline median | Candidate median | Reduction |
|---|---:|---:|---:|
| Elapsed | 1618.463 ms | 1580.223 ms | 2.36% (95% bootstrap 0.47%..2.68%) |
| Allocated | 316946976 B | 317001584 B | -0.02% |
| Allocations | 3257729 | 3257733 | -0.00% |
| Retained delta | 7283304 B | 7283304 B | 0.00% |

Samples: 3 baseline / 3 candidate; timing CV: 0.22% / 0.80%.

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
| Report output | /tmp/lunar-array-access/shared/cbor/save-retained-report.md |
| Report overwrite allowed | false |
| Report format | markdown |

Qualification: false

| Gate | Enabled | Policy | Actual | Result |
|---|---:|---:|---:|---:|
| baseline timing CV | false | disabled | 0.0022 | - |
| candidate timing CV | false | disabled | 0.0080 | - |
| median speedup | false | disabled | 0.0236 | - |
| elapsed ratio | false | disabled | 0.9764 | - |
| allocated-byte ratio | false | disabled | 1.0002 | - |
| malloc ratio | false | disabled | 1.0000 | - |
| excess malloc removal | false | disabled | - | - |
