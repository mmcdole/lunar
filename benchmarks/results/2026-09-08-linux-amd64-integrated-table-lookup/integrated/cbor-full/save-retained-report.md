| Metric | Baseline median | Candidate median | Reduction |
|---|---:|---:|---:|
| Elapsed | 1617.595 ms | 1583.095 ms | 2.13% (95% bootstrap 0.07%..3.31%) |
| Allocated | 316947008 B | 317383696 B | -0.14% |
| Allocations | 3257729 | 3257761 | -0.00% |
| Retained delta | 7283304 B | 7283304 B | 0.00% |

Samples: 3 baseline / 3 candidate; timing CV: 1.19% / 0.29%.

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
| Report output | /tmp/lunar-array-followup/integrated/cbor-full/save-retained-report.md |
| Report overwrite allowed | false |
| Report format | markdown |

Qualification: false

| Gate | Enabled | Policy | Actual | Result |
|---|---:|---:|---:|---:|
| baseline timing CV | false | disabled | 0.0119 | - |
| candidate timing CV | false | disabled | 0.0029 | - |
| median speedup | false | disabled | 0.0213 | - |
| elapsed ratio | false | disabled | 0.9787 | - |
| allocated-byte ratio | false | disabled | 1.0014 | - |
| malloc ratio | false | disabled | 1.0000 | - |
| excess malloc removal | false | disabled | - | - |
