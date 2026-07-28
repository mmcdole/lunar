# CBOR graph benchmark

This nested module compares Lugo with stock GopherLua v1.1.2 on the same
deterministic CBOR load and save workload. The graph stresses large string maps,
dense numeric lists, mixed numeric/string keys, repeated record layouts, table
iteration, file I/O, and recursive codec calls.

The graph is reported as a large-table memory workload. General interpreter and
embedding results come from the benchmark module above it. The Lugo worker uses
Lugo's owned public API, explicitly opens the standard libraries required by
the fixture, and uses operation-scoped contexts for guarded calls. The stock
worker is selected with the `gopherlua_reference` build tag and uses the pinned
dependency in `stock.mod`; both builds otherwise compile the same generator,
workload, validation, and reporting code.

Published paired retained-memory records and summaries are in the
[`2026-07-28 Apple M3 Pro archive`](../results/2026-07-28-darwin-arm64-m3-pro/).

## Deterministic synthetic fixtures

`testdata/cbor.lua` is lua-cbor 1.0.0 with one documented patch. Its license and
source details are in `testdata/LICENSE.lua-cbor` and
`testdata/PROVENANCE.md`. `testdata/workload.lua` is project-owned.

The generator contains no captured user or application data. Names, IDs,
coordinates, descriptions, timestamps, and graph relationships are synthetic
values derived only from deterministic counters. Generated corpora are not
checked in: the small file is 8,011 bytes and the large file is 9,208,046
bytes.

From this directory, generate the public fixtures:

```sh
go run ./cmd/generate \
  -preset small -output /tmp/lugo-cbor-small.cbor

go run ./cmd/generate \
  -preset large -output /tmp/lugo-cbor-large.cbor
```

The expected current checksums are:

| Preset | Bytes | File SHA-256 | Structural SHA-256 |
|---|---:|---|---|
| small | 8,011 | `5a840e6955b60c49832742a9e279c0d92163abceb96a22d79e7ce22c98d4b633` | `7bfe6d54697c9c0954bacc2b141d373e2d87ff419ab5f868359db0e673bcadfd` |
| large | 9,208,046 | `65c43f4abd104fb629f22aee7801d3b458a93e24e6a6ec6dffb4c4b02252ab7c` | `dfced0fa169e1abb659ef54220f2395c7c3b5757c1b8ae129b3487c7a489eead` |

Verify the file hashes with:

```sh
shasum -a 256 \
  /tmp/lugo-cbor-small.cbor \
  /tmp/lugo-cbor-large.cbor
```

Worker output validates the structural digest after decoding.

Each preset also has an independently calculated structural expectation:

| Preset | Areas | Rooms | Exits | Tables | Entries |
|---|---:|---:|---:|---:|---:|
| small | 3 | 32 | 96 | 172 | 871 |
| large | 341 | 36,705 | 109,742 | 183,513 | 938,452 |

## Build and smoke-test both runtimes

Build one fresh-process worker for each implementation:

```sh
go build -trimpath \
  -o /tmp/lugo-cbor-lugo ./cmd/workload

go build -trimpath -tags gopherlua_reference -modfile=stock.mod \
  -o /tmp/lugo-cbor-gopherlua ./cmd/workload
```

Run the small fixture through both load and save paths:

```sh
/tmp/lugo-cbor-lugo \
  -preset small -mode load -measurement retained \
  -fixture testdata -data /tmp/lugo-cbor-small.cbor
/tmp/lugo-cbor-lugo \
  -preset small -mode save -measurement retained \
  -fixture testdata -data /tmp/lugo-cbor-small.cbor

/tmp/lugo-cbor-gopherlua \
  -preset small -mode load -measurement retained \
  -fixture testdata -data /tmp/lugo-cbor-small.cbor
/tmp/lugo-cbor-gopherlua \
  -preset small -mode save -measurement retained \
  -fixture testdata -data /tmp/lugo-cbor-small.cbor
```

Every invocation copies the shared input into a private writable directory.
After the measured operation, it validates the decoded graph counts, round-trips
the graph, compares a canonical structural SHA-256 digest, and verifies that the
original input file did not change. The result also records the input, codec,
workload, runtime, Go, platform, and VCS identities.

## Fresh-process paired sampling

Timing and allocation noise should be sampled with `cmd/run`. It starts a new
worker process for every sample, randomizes implementation order within each
round, stages immutable copies of both binaries, and writes paired JSONL
evidence:

```sh
go run ./cmd/run \
  -baseline /tmp/lugo-cbor-gopherlua \
  -candidate /tmp/lugo-cbor-lugo \
  -preset large -mode load -measurement timing \
  -fixture testdata -data /tmp/lugo-cbor-large.cbor \
  -runs 15 -warmups 2 -seed 1 \
  -baseline-output /tmp/cbor-gopherlua-load.jsonl \
  -candidate-output /tmp/cbor-lugo-load.jsonl

go run ./cmd/compare \
  -baseline /tmp/cbor-gopherlua-load.jsonl \
  -candidate /tmp/cbor-lugo-load.jsonl \
  -min-samples 15 -bootstrap 10000 -seed 1
```

Use the same commands with `-mode save` and separate output files for save
measurements. Setup for save loads the graph before the measured interval.

The comparator reports medians, MAD, p95, mean, coefficient of variation,
bootstrap timing intervals, allocation ratios, and malloc ratios. Its numeric
gates are disabled by default, so the command above is descriptive. Optional
gates include `-max-cv`, `-max-elapsed-ratio`,
`-max-total-alloc-ratio`, and `-max-malloc-ratio`. Enabling any gate switches
the comparison to strict qualification: use at least 15 timing samples, pin the
baseline binary SHA-256 and VCS revision in both collection and comparison,
require a clean build with `-require-clean`, and write an exclusive report with
`-output`.

## Retained-memory measurements

Collect retained-memory results through the same randomized paired fresh-process
runner:

```sh
go run ./cmd/run \
  -baseline /tmp/lugo-cbor-gopherlua \
  -candidate /tmp/lugo-cbor-lugo \
  -preset large -mode load -measurement retained \
  -fixture testdata -data /tmp/lugo-cbor-large.cbor \
  -runs 15 -warmups 2 -seed 1 \
  -baseline-output /tmp/cbor-gopherlua-retained.jsonl \
  -candidate-output /tmp/cbor-lugo-retained.jsonl

go run ./cmd/compare \
  -baseline /tmp/cbor-gopherlua-retained.jsonl \
  -candidate /tmp/cbor-lugo-retained.jsonl \
  -min-samples 15 -bootstrap 10000 -seed 1
```

The memory fields have distinct meanings. Heap and allocation fields are
bytes, `mallocs_delta` is a count, and JSON elapsed values are nanoseconds:

- `heap_before` is absolute live Go heap after setup and two forced garbage
  collections, immediately before the operation.
- `heap_retained` is absolute live Go heap after the operation and two forced
  garbage collections.
- `heap_delta` is signed `heap_retained - heap_before`; it is the
  baseline-subtracted retained change, not an absolute footprint.
- `total_alloc_delta` and `mallocs_delta` are cumulative allocation changes
  across only the measured operation.

For load, the pre-operation baseline includes the initialized runtime and loaded
fixture code, while the retained heap additionally contains the decoded graph.
For save, setup preloads the graph before `heap_before`, so `heap_delta`
describes what encoding and writing retain beyond that graph. Report
`heap_retained` and `heap_delta` separately; neither should be substituted for
the other.

For a one-process smoke check, invoke either worker directly with
`-measurement retained -format jsonl`. Do not use that single result as a
published comparison.

## Native timing references

The optional [native runner](cmd/native/README.md) measures the same CBOR
workload in separately provisioned PUC Lua 5.1.5 and LuaJIT 2.1 processes.
Those results are timing references only; their allocator measurements are not
combined with Go heap metrics.

## Verification

Run both module variants:

```sh
go test ./...
go vet ./...

go test -tags gopherlua_reference -modfile=stock.mod ./...
go vet -tags gopherlua_reference -modfile=stock.mod ./...
```
