#!/usr/bin/env bash
set -euo pipefail
if [[ $# -lt 2 || $# -gt 3 ]]; then
  echo 'usage: bash collect-pair.sh MODE MEASUREMENT [RUNS=15]' >&2
  exit 2
fi
mode=$1
measurement=$2
runs=${3:-15}
case "$mode/$measurement" in load/timing|save/timing|load/retained|save/retained) ;; *) exit 2;; esac
export GOMAXPROCS=1 GOGC=100 GOMEMLIMIT=off
root=/tmp/lunar-table-fill/cbor
cpu=${LUNAR_CBOR_CPU:-2}
export LUNAR_CBOR_CANDIDATE=${LUNAR_CBOR_CANDIDATE:-candidate}
case "$LUNAR_CBOR_CANDIDATE" in candidate|scalar) ;; *) echo 'invalid candidate label' >&2; exit 2;; esac
python3 - <<'PY'
from pathlib import Path
import hashlib, json, os
root=Path('/tmp/lunar-table-fill/cbor')
for label in ('baseline', os.environ['LUNAR_CBOR_CANDIDATE']):
    manifest=json.loads((root/(label+'-manifest.json')).read_text())
    for name, info in manifest['binaries'].items():
        if hashlib.sha256((root/'bin'/name).read_bytes()).hexdigest()!=info['sha256']:
            raise SystemExit('binary changed: '+name)
    for name, info in manifest['fixtures'].items():
        if hashlib.sha256((root/name).read_bytes()).hexdigest()!=info['sha256']:
            raise SystemExit('fixture changed: '+name)
PY
baseline_sha=$(python3 -c 'import json; print(json.load(open("/tmp/lunar-table-fill/cbor/baseline-manifest.json"))["binaries"]["baseline-worker"]["sha256"])')
baseline_revision=5fc51e449a6661340056544e995aae4fdf89dcb2
default_prefix="$mode-$measurement"
if [[ "$LUNAR_CBOR_CANDIDATE" != candidate ]]; then
  default_prefix="$LUNAR_CBOR_CANDIDATE-$default_prefix"
fi
prefix="$root/${LUNAR_CBOR_PREFIX:-$default_prefix}"
for suffix in baseline.jsonl candidate.jsonl collection.log report.md; do
  if [[ -e "$prefix-$suffix" ]]; then
    echo "refusing to overwrite $prefix-$suffix" >&2
    exit 2
  fi
done
# Root coordinates this script with all other timing processes. Workers inherit CPU affinity.
taskset -c "$cpu" "$root/bin/run" \
  -baseline "$root/bin/baseline-worker" -candidate "$root/bin/$LUNAR_CBOR_CANDIDATE-worker" \
  -expect-baseline-sha256 "$baseline_sha" -expect-baseline-revision "$baseline_revision" -require-clean \
  -comparison-mode implementations -preset large -mode "$mode" -measurement "$measurement" \
  -fixture "$root/fixture" -data "$root/large.cbor" \
  -runs "$runs" -warmups "${LUNAR_CBOR_WARMUPS:-2}" -seed 1 \
  -baseline-output "$prefix-baseline.jsonl" -candidate-output "$prefix-candidate.jsonl" \
  > "$prefix-collection.log" 2>&1
"$root/bin/compare" \
  -baseline "$prefix-baseline.jsonl" -candidate "$prefix-candidate.jsonl" \
  -expect-baseline-sha256 "$baseline_sha" -expect-baseline-revision "$baseline_revision" -require-clean \
  -comparison-mode implementations -min-samples "$runs" -bootstrap 10000 -seed 1 \
  -output "$prefix-report.md"
