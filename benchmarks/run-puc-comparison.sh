#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 1 ]]; then
  echo 'usage: ./run-puc-comparison.sh OUTPUT.txt' >&2
  exit 2
fi
output=$1
samples=${LUNAR_BENCH_SAMPLES:-15}
benchtime=${LUNAR_BENCH_TIME:-500ms}
: "${LUNAR_BENCH_POWER_POLICY:?describe power and background load controls}"
: "${LUNAR_PUC_SOURCE_SHA256:?record the reference source archive SHA-256}"
: "${LUNAR_PUC_LIBRARY:?path to the linked Lua 5.1.5 static library}"
if [[ "$(go env CGO_ENABLED)" != 1 ]]; then
  echo 'the PUC comparison requires CGO_ENABLED=1' >&2
  exit 2
fi
if [[ -e "$output" || ! "$samples" =~ ^[1-9][0-9]*$ ]]; then
  echo 'output must be new and sample count must be positive' >&2
  exit 2
fi
if [[ -n "$(git status --porcelain)" ]]; then
  echo 'benchmark collection requires a clean checkout' >&2
  exit 2
fi

staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT
go test -tags puc51 ./...
go vet -tags puc51 ./...
go test -tags puc51 -c -o "$staging/compare.test"

affinity=()
if [[ -n "${LUNAR_BENCH_CPU:-}" ]]; then
  affinity=(taskset -c "$LUNAR_BENCH_CPU")
fi
{
  echo "# revision: $(git rev-parse HEAD)"
  echo "# go: $(go version)"
  echo "# system: $(uname -srmv)"
  echo "# cpu_model: ${LUNAR_BENCH_CPU_MODEL:-$(awk -F ': ' '/model name/{print $2;exit}' /proc/cpuinfo)}"
  echo "# power_policy: $LUNAR_BENCH_POWER_POLICY"
  echo "# affinity: ${LUNAR_BENCH_CPU:-none}"
  echo "# PUC Lua: 5.1.5, non-JIT, default double lua_Number"
  echo "# puc_source_sha256: $LUNAR_PUC_SOURCE_SHA256"
  echo "# puc_library_sha256: $(sha256sum "$LUNAR_PUC_LIBRARY")"
  echo "# compiler: $(${CC:-cc} --version | head -1)"
  echo "# puc_build_flags: ${LUNAR_PUC_BUILD_FLAGS:-unrecorded}"
  echo "# CGO_CFLAGS: ${CGO_CFLAGS:-}"
  echo "# CGO_LDFLAGS: ${CGO_LDFLAGS:-}"
  echo "# test_binary_sha256: $(sha256sum "$staging/compare.test" | cut -d ' ' -f 1)"
  echo '# GOGC: 100; GOMEMLIMIT: off; GOMAXPROCS: 1; cpu: 1'
  echo "# samples: $samples; benchtime: $benchtime"
  echo '# timing: Go B.Loop wall time; cached function; protected call; one warmup; setup excluded'
  echo '# PUC includes one cgo transition per operation; no native allocation counters'
} > "$output"

expected_rows=0
for ((round=0; round<samples; round++)); do
  if ((round % 2 == 0)); then order=(lunar puc51); else order=(puc51 lunar); fi
  for engine in "${order[@]}"; do
    echo "round $((round+1))/$samples: $engine" >&2
    echo "# round: $((round+1)); runtime: $engine" >> "$output"
    GOGC=100 GOMEMLIMIT=off GOMAXPROCS=1 "${affinity[@]}" "$staging/compare.test" \
      -test.run '^$' \
      -test.bench "^(BenchmarkPrograms|BenchmarkInterpreter|BenchmarkDiagnostics)\$/.*\$/^runtime=${engine}\$" \
      -test.benchtime "$benchtime" -test.count 1 -test.cpu 1 > "$staging/sample.txt"
    rows=$(awk '/^Benchmark/ {n++} END {print n+0}' "$staging/sample.txt")
    if [[ "$rows" == 0 || ( "$expected_rows" != 0 && "$rows" != "$expected_rows" ) ]]; then
      cat "$staging/sample.txt" >> "$output"
      echo "incomplete benchmark process: expected $expected_rows rows, got $rows" >&2
      exit 1
    fi
    expected_rows=$rows
    cat "$staging/sample.txt" >> "$output"
  done
done
echo "# completed: $samples samples per runtime for each of $expected_rows rows" >> "$output"
