#!/usr/bin/env bash
# Collects retained-heap samples for the controlled table-shape suite.
# Each sample is one fresh worker process; the two implementations alternate
# leading position every round. Evidence lands in OUTPUT_DIR as one JSONL
# file per case and implementation, plus a median summary.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: scripts/shapes.sh OUTPUT_DIR" >&2
  exit 2
fi

output=$1
runs=${LUNAR_SHAPE_RUNS:-15}
root=$(cd "$(dirname "$0")/.." && pwd)

if [[ -e "$output" ]]; then
  echo "refusing to overwrite $output" >&2
  exit 2
fi
mkdir -p "$output"

cd "$root"
mkdir -p bin
go build -trimpath -o bin/shapes-lunar ./cmd/shapes
go build -trimpath -tags gopherlua_reference -modfile=stock.mod \
  -o bin/shapes-gopherlua ./cmd/shapes

cases=$(./bin/shapes-lunar -list | awk '{print $1}')

median_of() {
  grep -o '"heap_delta":[0-9-]*' "$1" | cut -d: -f2 | sort -n |
    awk '{ values[NR] = $1 } END {
      if (NR == 0) { exit 1 }
      if (NR % 2 == 1) { print values[(NR + 1) / 2] }
      else { printf "%d\n", (values[NR / 2] + values[NR / 2 + 1]) / 2 }
    }'
}

for round in $(seq 1 "$runs"); do
  if (( round % 2 == 1 )); then
    order="lunar gopherlua"
  else
    order="gopherlua lunar"
  fi
  for name in $cases; do
    for impl in $order; do
      "./bin/shapes-$impl" -case "$name" -format jsonl \
        >> "$output/$name-$impl.jsonl"
    done
  done
  echo "round $round/$runs complete" >&2
done

summary="$output/summary.txt"
{
  printf '%-20s %14s %14s %8s\n' case lunar_delta gopher_delta ratio
  for name in $cases; do
    lunar_median=$(median_of "$output/$name-lunar.jsonl")
    gopher_median=$(median_of "$output/$name-gopherlua.jsonl")
    ratio=$(awk -v g="$gopher_median" -v l="$lunar_median" \
      'BEGIN { printf "%.2f", g / l }')
    printf '%-20s %14d %14d %8s\n' \
      "$name" "$lunar_median" "$gopher_median" "$ratio"
  done
} > "$summary"
cat "$summary"
