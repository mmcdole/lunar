# Measurement summaries

Each dated directory contains one summary of the measured revisions, workload,
conditions, results and limitations. Dates and machine names identify historical
measurements; they do not imply that a result describes the current runtime.

| Measurement | Scope |
| --- | --- |
| [2026-07-27, Apple M3 Pro](2026-07-27-darwin-arm64-m3-pro/) | Initial runtime and CBOR memory comparison |
| [2026-07-28, Apple M3 Pro](2026-07-28-darwin-arm64-m3-pro/) | Complete programs, embedding and CBOR memory |
| [2026-08-05, Apple M3 Pro](2026-08-05-darwin-arm64-m3-pro/) | Controlled table-shape memory comparison |
| [2026-09-07, PUC comparison](2026-09-07-linux-amd64-puc51/) | PUC-inspired optimization assessment |
| [2026-09-07, table access](2026-09-07-linux-amd64-table-access/) | Constant-string table access comparison |
| [2026-09-07, native entry](2026-09-07-linux-amd64-native-entry/) | Initial native-call entry experiment |
| [2026-09-07, native entry against updated main](2026-09-07-linux-amd64-native-entry-main/) | Native-call and frame-copy experiments |
| [2026-09-07, native calls](2026-09-07-linux-amd64-native-calls/) | README comparison after the native-call change |
| [2026-09-07, array access](2026-09-07-linux-amd64-array-access/) | Rejected array-access experiments |
| [2026-09-08, integrated table lookup](2026-09-08-linux-amd64-integrated-table-lookup/) | General indexed-access improvement and callback tradeoff |

The [benchmark protocol](../README.md) describes collection, analysis and the
summary-only publication policy. Workloads, fixtures and reusable tools live in
the maintained benchmark modules. Raw output and investigation files belong in
the ignored `.bench/` working directory; previously tracked files remain in Git
history.
