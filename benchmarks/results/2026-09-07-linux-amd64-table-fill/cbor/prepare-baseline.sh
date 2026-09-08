#!/usr/bin/env bash
set -euo pipefail
export GOCACHE=/tmp/lunar-puc-assessment/go-cache
export GOMAXPROCS=2 GOGC=100 GOMEMLIMIT=off
cd /tmp/lunar-table-fill/baseline/benchmarks/cbor
taskset -c 6,7 go test ./...
taskset -c 6,7 go vet ./...
taskset -c 6,7 go build -trimpath -o /tmp/lunar-table-fill/cbor/bin/baseline-worker ./cmd/workload
taskset -c 6,7 go build -trimpath -o /tmp/lunar-table-fill/cbor/bin/baseline-shapes ./cmd/shapes
taskset -c 6,7 go build -trimpath -o /tmp/lunar-table-fill/cbor/bin/generate ./cmd/generate
taskset -c 6,7 go build -trimpath -o /tmp/lunar-table-fill/cbor/bin/run ./cmd/run
taskset -c 6,7 go build -trimpath -o /tmp/lunar-table-fill/cbor/bin/compare ./cmd/compare
taskset -c 6,7 /tmp/lunar-table-fill/cbor/bin/generate -preset small -fixture /tmp/lunar-table-fill/cbor/fixture -output /tmp/lunar-table-fill/cbor/small.cbor
taskset -c 6,7 /tmp/lunar-table-fill/cbor/bin/generate -preset large -fixture /tmp/lunar-table-fill/cbor/fixture -output /tmp/lunar-table-fill/cbor/large.cbor
export GOMAXPROCS=1
taskset -c 6 /tmp/lunar-table-fill/cbor/bin/baseline-worker -preset small -mode load -measurement retained -fixture /tmp/lunar-table-fill/cbor/fixture -data /tmp/lunar-table-fill/cbor/small.cbor -format jsonl > /tmp/lunar-table-fill/cbor/baseline-small-load-smoke.jsonl
taskset -c 6 /tmp/lunar-table-fill/cbor/bin/baseline-worker -preset small -mode save -measurement retained -fixture /tmp/lunar-table-fill/cbor/fixture -data /tmp/lunar-table-fill/cbor/small.cbor -format jsonl > /tmp/lunar-table-fill/cbor/baseline-small-save-smoke.jsonl
