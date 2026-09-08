| Metric | Baseline median | Candidate median | Reduction |
|---|---:|---:|---:|
| Elapsed | 1597.531 ms | 1575.076 ms | 1.41% (95% bootstrap 0.21%..1.90%) |
| Allocated | 317001568 B | 317056160 B | -0.02% |
| Allocations | 3257733 | 3257737 | -0.00% |
| Retained delta | 0 B | 0 B | 0.00% |

Samples: 15 baseline / 15 candidate; timing CV: 1.52% / 1.34%.

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
| Report output | /tmp/lunar-array-followup/integrated/cbor-full/save-timing-report.md |
| Report overwrite allowed | false |
| Report format | markdown |

Qualification: false

| Gate | Enabled | Policy | Actual | Result |
|---|---:|---:|---:|---:|
| baseline timing CV | false | disabled | 0.0152 | - |
| candidate timing CV | false | disabled | 0.0134 | - |
| median speedup | false | disabled | 0.0141 | - |
| elapsed ratio | false | disabled | 0.9859 | - |
| allocated-byte ratio | false | disabled | 1.0002 | - |
| malloc ratio | false | disabled | 1.0000 | - |
| excess malloc removal | false | disabled | - | - |
