#!/usr/bin/env bash
set -euo pipefail
export GOCACHE=/tmp/lunar-puc-assessment/go-cache
export GOMAXPROCS=2 GOGC=100 GOMEMLIMIT=off
cd /tmp/lunar-table-fill/candidate/benchmarks/cbor
if [[ -n "$(git status --porcelain)" ]]; then
  echo 'candidate checkout must be committed and clean before building' >&2
  exit 1
fi
taskset -c 6,7 go test ./...
taskset -c 6,7 go vet ./...
taskset -c 6,7 go build -trimpath -o /tmp/lunar-table-fill/cbor/bin/candidate-worker ./cmd/workload
taskset -c 6,7 go build -trimpath -o /tmp/lunar-table-fill/cbor/bin/candidate-shapes ./cmd/shapes
export GOMAXPROCS=1
taskset -c 6 /tmp/lunar-table-fill/cbor/bin/candidate-worker -preset small -mode load -measurement retained -fixture /tmp/lunar-table-fill/cbor/fixture -data /tmp/lunar-table-fill/cbor/small.cbor -format jsonl > /tmp/lunar-table-fill/cbor/candidate-small-load-smoke.jsonl
taskset -c 6 /tmp/lunar-table-fill/cbor/bin/candidate-worker -preset small -mode save -measurement retained -fixture /tmp/lunar-table-fill/cbor/fixture -data /tmp/lunar-table-fill/cbor/small.cbor -format jsonl > /tmp/lunar-table-fill/cbor/candidate-small-save-smoke.jsonl
python3 /tmp/lunar-table-fill/cbor/write-manifest.py candidate
