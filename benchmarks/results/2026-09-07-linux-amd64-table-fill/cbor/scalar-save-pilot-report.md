| Metric | Baseline median | Candidate median | Reduction |
|---|---:|---:|---:|
| Elapsed | 1599.343 ms | 1588.641 ms | 0.67% (95% bootstrap -1.94%..2.23%) |
| Allocated | 316946992 B | 316892384 B | 0.02% |
| Allocations | 3257729 | 3257725 | 0.00% |
| Retained delta | 0 B | 0 B | 0.00% |

Samples: 3 baseline / 3 candidate; timing CV: 1.23% / 0.92%.

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
| Report output | /tmp/lunar-table-fill/cbor/scalar-save-pilot-report.md |
| Report overwrite allowed | false |
| Report format | markdown |

Qualification: false

| Gate | Enabled | Policy | Actual | Result |
|---|---:|---:|---:|---:|
| baseline timing CV | false | disabled | 0.0123 | - |
| candidate timing CV | false | disabled | 0.0092 | - |
| median speedup | false | disabled | 0.0067 | - |
| elapsed ratio | false | disabled | 0.9933 | - |
| allocated-byte ratio | false | disabled | 0.9998 | - |
| malloc ratio | false | disabled | 1.0000 | - |
| excess malloc removal | false | disabled | - | - |
