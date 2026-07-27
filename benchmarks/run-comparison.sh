#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: ./run-comparison.sh OUTPUT.txt" >&2
  exit 2
fi

output=$1
samples=${LUGO_BENCH_SAMPLES:-15}
benchtime=${LUGO_BENCH_TIME:-500ms}
power_policy=${LUGO_BENCH_POWER_POLICY:-}
cpu_model=${LUGO_BENCH_CPU_MODEL:-}
machine_model=${LUGO_BENCH_MACHINE_MODEL:-}

if [[ -e "$output" ]]; then
  echo "refusing to overwrite $output" >&2
  exit 2
fi
if [[ ! "$samples" =~ ^[1-9][0-9]*$ ]]; then
  echo "LUGO_BENCH_SAMPLES must be a positive integer" >&2
  exit 2
fi
if [[ -z "$power_policy" ]]; then
  echo "LUGO_BENCH_POWER_POLICY must describe power and background-load controls" >&2
  exit 2
fi
if [[ "$power_policy" == *$'\n'* || "$power_policy" == *$'\r'* ]]; then
  echo "LUGO_BENCH_POWER_POLICY must be one line" >&2
  exit 2
fi
if [[ -n "$(git status --porcelain)" ]]; then
  echo "benchmark collection requires a clean checkout" >&2
  exit 2
fi

detect_cpu_model() {
  local value=
  case "$(uname -s)" in
    Darwin)
      value=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || true)
      ;;
    Linux)
      if [[ -r /proc/cpuinfo ]]; then
        value=$(awk -F: '
          /^(model name|Hardware|Processor)[[:space:]]*:/ {
            sub(/^[[:space:]]+/, "", $2)
            print $2
            exit
          }
        ' /proc/cpuinfo)
      fi
      ;;
  esac
  if [[ -z "$value" ]]; then
    value=$(uname -m)
  fi
  printf '%s' "$value"
}

detect_machine_model() {
  local value=
  case "$(uname -s)" in
    Darwin)
      value=$(sysctl -n hw.model 2>/dev/null || true)
      ;;
    Linux)
      if [[ -r /sys/devices/virtual/dmi/id/product_name ]]; then
        value=$(< /sys/devices/virtual/dmi/id/product_name)
      fi
      ;;
  esac
  if [[ -z "$value" ]]; then
    value=unavailable
  fi
  printf '%s' "$value"
}

revision=$(git rev-parse HEAD)
gopherlua_version=$(go list -m -f '{{.Version}}' github.com/yuin/gopher-lua)
golua_version=$(go list -m -f '{{.Version}}' github.com/Shopify/go-lua)
if [[ -z "$cpu_model" ]]; then
  cpu_model=$(detect_cpu_model)
fi
if [[ -z "$machine_model" ]]; then
  machine_model=$(detect_machine_model)
fi
{
  echo "# revision: $revision"
  echo "# go: $(go version)"
  echo "# gopherlua: $gopherlua_version"
  echo "# golua: $golua_version"
  echo "# platform: $(go env GOOS)/$(go env GOARCH)"
  echo "# system: $(uname -srmv)"
  echo "# machine_model: $machine_model"
  echo "# cpu_model: $cpu_model"
  echo "# power_policy: $power_policy"
  echo "# GOGC: 100"
  echo "# GOMEMLIMIT: off"
  echo "# GOMAXPROCS: 1"
  echo "# samples: $samples"
  echo "# benchtime: $benchtime"
} >"$output"

for ((round = 0; round < samples; round++)); do
  case $((round % 3)) in
    0) order=(lugo gopherlua golua) ;;
    1) order=(gopherlua golua lugo) ;;
    2) order=(golua lugo gopherlua) ;;
  esac

  for runtime_name in "${order[@]}"; do
    echo "round $((round + 1))/$samples: $runtime_name" >&2
    GOGC=100 GOMEMLIMIT=off GOMAXPROCS=1 \
      go test -run '^$' \
      -bench "^(BenchmarkPrograms|BenchmarkInterpreter|BenchmarkEmbedding)\$/.*\$/^runtime=${runtime_name}\$" \
      -benchmem -benchtime="$benchtime" -count=1 -cpu=1 |
      tee -a "$output"
  done
done
