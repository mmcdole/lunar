# Retained memory on a Ryzen 7 4800U — 2026-09-25

This collection reruns the CBOR graph and the controlled table-shape suite on
the machine that supplies the other README tables, replacing the Apple M3 Pro
figures ([CBOR](../2026-07-28-darwin-arm64-m3-pro/),
[shapes](../2026-08-05-darwin-arm64-m3-pro/)) in the root README. It measured
clean main `c0340bcbc84f42e4919c9706553adca3ff9c8e0f`.

The results match the M3 Pro collections closely: retained heap is a property
of the data layout, not the machine.

## CBOR graph

The deterministic 9,208,046-byte input (SHA-256 `65c43f4a…a9baacbd` matches
the published fixture) decodes to 183,513 tables and 938,452 entries. Each
runtime ran in 15 fresh processes in randomized paired order after 2 warmups.

| Metric | Lunar | GopherLua | Reduction |
| --- | ---: | ---: | ---: |
| Retained heap increase | **72.26 MiB** | 542.27 MiB | 86.67% |
| Absolute live heap after load | **72.69 MiB** | 543.98 MiB | 86.64% |
| Allocation traffic during load | **107.5 MB** | 754.5 MB | 85.75% |
| Allocations during load | **664,928** | 12,733,970 | 94.78% |
| Load time | **3,272 ms** | 3,883 ms | 15.73% |

The load-time reduction's 95% bootstrap interval is 14.79–16.44%; timing CV
was under 1% for both runtimes. Shopify go-lua is not measured because its
standard IO library does not implement `file:read("*a")`.

## Table shapes

Retained heap added, median of 15 fresh processes per case and runtime, with
the leading runtime alternating each round.

| Shape | Lunar | GopherLua | Ratio |
| --- | ---: | ---: | ---: |
| 25,000 four-field tables, four repeated 16 B keys | **7.41 MiB** | 72.03 MiB | 9.72× |
| 25,000 four-field tables, unique 16 B keys | **8.87 MiB** | 72.03 MiB | 8.12× |
| 25,000 four-field tables, four repeated 80 B keys | **15.03 MiB** | 78.13 MiB | 5.20× |
| One table, 100,000 unique 16 B keys | **6.54 MiB** | 14.76 MiB | 2.26× |
| One table, 100,000 unique 64 B keys | **11.12 MiB** | 19.34 MiB | 1.74× |
| One table, 100,000 unique 256 B keys | **29.43 MiB** | 37.65 MiB | 1.28× |
| One table, 100,000 unique 1,024 B keys | **102.67 MiB** | 110.89 MiB | 1.08× |

The widest min-to-max spread across any 15-sample series is 0.9%, so no
confidence intervals are shown. MiB is 1,048,576 bytes; MB is 1,000,000.

## Collection controls

- Go 1.27.1 (`X:nodwarf5`), Linux/amd64 (CachyOS, kernel 7.2.5), MINIPC PN50,
  AMD Ryzen 7 4800U.
- GopherLua v1.1.2.
- `GOGC=100`, `GOMEMLIMIT=off`, Go runtime default `GOMAXPROCS`; AC power,
  `schedutil` governor, no other benchmark load.
- Two forced collections precede each baseline and follow each measured
  operation.

```sh
cd benchmarks/cbor
go run ./cmd/generate -preset large -output "$out/large.cbor"
go build -trimpath -o "$out/cbor-lunar" ./cmd/workload
go build -trimpath -tags gopherlua_reference -modfile=stock.mod \
  -o "$out/cbor-gopherlua" ./cmd/workload
go run ./cmd/run -baseline "$out/cbor-gopherlua" -candidate "$out/cbor-lunar" \
  -preset large -mode load -measurement retained \
  -fixture testdata -data "$out/large.cbor" \
  -runs 15 -warmups 2 -seed 1 \
  -baseline-output "$out/cbor-gopherlua-retained.jsonl" \
  -candidate-output "$out/cbor-lunar-retained.jsonl"
go run ./cmd/compare -baseline "$out/cbor-gopherlua-retained.jsonl" \
  -candidate "$out/cbor-lunar-retained.jsonl" \
  -min-samples 15 -bootstrap 10000 -seed 1
scripts/shapes.sh "$out/shapes"
```
