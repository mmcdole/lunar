#!/usr/bin/env python3
"""Validate the completed array-access comparison and summarize its samples."""

import argparse
import collections
import hashlib
import json
import math
from pathlib import Path
import re
import statistics
import subprocess


ROOT = Path(__file__).resolve().parent
GROUPS = {
    "programs": ("BenchmarkPrograms", "program", (
        "binarytrees", "fannkuchredux", "nbody", "spectralnorm")),
    "interpreter": ("BenchmarkInterpreter", "case", (
        "numeric_for_10000", "fixed_lua_calls_1000",
        "table_field_get_set_10000", "string_append_256")),
    "embedding": ("BenchmarkEmbedding", "case", (
        "go_to_lua_scalars", "lua_to_go_scalar_1000", "go_string_echo_128B",
        "prebuilt_go_table_16_4_to_lua", "create_fill_go_table_16_4_to_lua")),
}
LANES = (
    ("baseline", "baseline", "lunar"),
    ("candidate", "candidate", "lunar"),
    ("gopherlua", "candidate", "gopherlua"),
    ("golua", "candidate", "golua"),
)
MARKER = re.compile(r"# round=(\d+) lane=(\w+)\n\Z")
METRICS = {"ns/op": "ns_per_op", "B/op": "bytes_per_op", "allocs/op": "allocs_per_op"}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def expected_rows():
    rows = {}
    for lane, _, runtime in LANES:
        rows[lane] = {
            f"{benchmark}/{key}={case}/runtime={runtime}"
            for group, (benchmark, key, cases) in GROUPS.items()
            if group != "interpreter" or runtime == "lunar"
            for case in cases
        }
    return rows


def validate(raw_path, manifest_path, collector_path):
    raw_bytes = raw_path.read_bytes()
    manifest_bytes = manifest_path.read_bytes()
    manifest = json.loads(manifest_bytes)
    require(manifest.get("status") == "complete", "collection is not complete")
    require(manifest.get("samples_per_row") == 15, "expected 15 samples per row")
    require(manifest.get("lanes") == [list(lane) for lane in LANES], "unexpected lane definitions")
    require(manifest.get("samples") == 660 and manifest.get("unique_rows") == 44,
            "expected 660 samples across 44 rows")
    require(manifest.get("raw_sha256") == digest(raw_bytes), "raw SHA-256 mismatch")
    require(manifest.get("collector_sha256") == digest(collector_path.read_bytes()),
            "collector SHA-256 mismatch")
    require(manifest.get("environment") == {"GOGC": "100", "GOMEMLIMIT": "off", "GOMAXPROCS": "1"},
            "unexpected measurement environment")
    require(manifest.get("cpu_affinity") == 2 and manifest.get("test_cpu") == 1,
            "unexpected benchmark CPU settings")
    require(manifest.get("benchtime") == "500ms", "unexpected measurement duration")
    rows = expected_rows()
    require(manifest.get("expected_rows") == {lane: sorted(names) for lane, names in rows.items()},
            "manifest row definitions differ from the complete comparison")
    for lane in ("baseline", "candidate"):
        source = manifest["sources"][lane]
        build = manifest["build_manifest"][lane]
        require({key: value for key, value in source.items() if key != "repo"} == build,
                f"source/build identity differs for {lane}")
        require(re.fullmatch(r"[0-9a-f]{40}", source["revision"]) is not None,
                f"invalid {lane} revision")
        require(re.fullmatch(r"[0-9a-f]{64}", source["sha256"]) is not None,
                f"invalid {lane} binary digest")
    require(manifest["sources"]["baseline"]["harness_sha256"] ==
            manifest["sources"]["candidate"]["harness_sha256"], "harness differs between builds")

    blocks = []
    body = None
    lines = raw_bytes.decode().splitlines(keepends=True)
    require(lines and lines[0] == f"# Source and measurement settings: {manifest_path.name}\n",
            "raw file does not identify the supplied manifest")
    for line in lines[1:]:
        match = MARKER.fullmatch(line)
        if match:
            body = []
            blocks.append((int(match[1]), match[2], body))
        else:
            require(body is not None, "unexpected content before first process block")
            body.append(line)
    order = []
    for round_number in range(1, 16):
        shift = (round_number - 1) % len(LANES)
        order.extend((round_number, lane[0]) for lane in LANES[shift:] + LANES[:shift])
    require([(round_number, lane) for round_number, lane, _ in blocks] == order,
            "raw process coverage/order differs from the 15-round rotation")
    processes = manifest["processes"]
    require([(p["round"], p["lane"]) for p in processes] == order,
            "manifest process coverage/order differs from raw data")

    samples = collections.defaultdict(list)
    inputs = collections.defaultdict(list)
    counts = collections.Counter()
    configs = set()
    lane_info = {lane: (binary, runtime) for lane, binary, runtime in LANES}
    row_info = {f"{benchmark}/{key}={case}": (group, case)
                for group, (benchmark, key, cases) in GROUPS.items() for case in cases}
    for (round_number, lane, body), process in zip(blocks, processes):
        output = "".join(body)
        binary, runtime = lane_info[lane]
        restored = output.replace(f"/runtime={lane}", f"/runtime={runtime}")
        require(digest(restored.encode()) == process["stdout_sha256"],
                f"process output digest mismatch: round {round_number} {lane}")
        require(output.splitlines().count("PASS") == 1 and not any(
            line.startswith(("FAIL", "--- FAIL:", "panic:")) for line in output.splitlines()),
            f"process did not pass: round {round_number} {lane}")
        command = process["command"]
        require(len(command) == 10 and command[:3] == ["taskset", "-c", "2"]
                and Path(command[3]).name == binary + ".test"
                and command[4] == "-test.run=^$"
                and command[6:] == ["-test.benchtime=500ms", "-test.count=1", "-test.cpu=1", "-test.benchmem"],
                f"unexpected command: round {round_number} {lane}")
        groups = ["BenchmarkPrograms", "BenchmarkEmbedding"]
        if runtime == "lunar":
            groups.append("BenchmarkInterpreter")
        require(command[5] == "-test.bench=^(" + "|".join(groups) + ")$/.*$/^runtime=" + runtime + "$",
                f"unexpected benchmark filter: round {round_number} {lane}")
        config = tuple(line for line in body if line.startswith(("goos:", "goarch:", "pkg:", "cpu:")))
        require(len(config) == 4, f"missing platform header: round {round_number} {lane}")
        configs.add(config)
        found = []
        by_group = collections.defaultdict(list)
        for line in body:
            if not line.startswith("Benchmark"):
                continue
            fields = line.split()
            require(len(fields) == 8 and fields[1].isdigit() and int(fields[1]) > 0,
                    f"invalid benchmark line: {line.rstrip()}")
            name = re.sub(r"-\d+$", "", fields[0])
            suffix = f"/runtime={lane}"
            require(name.endswith(suffix), f"unexpected runtime in {name}")
            short_name = name[:-len(suffix)]
            require(short_name in row_info, f"unexpected benchmark {name}")
            group, case = row_info[short_name]
            values = {}
            for number, unit in zip(fields[2::2], fields[3::2]):
                require(unit in METRICS and METRICS[unit] not in values, f"unexpected unit in {name}")
                value = float(number)
                require(math.isfinite(value) and value >= 0, f"invalid measurement in {name}")
                values[METRICS[unit]] = value
            require(values["ns_per_op"] > 0, f"non-positive timing in {name}")
            samples[(group, case, lane)].append(values)
            original_name = short_name + f"/runtime={runtime}"
            found.append(original_name)
            counts[(lane, original_name)] += 1
            by_group[group].append(short_name + "\t" + "\t".join(fields[1:]) + "\n")
        require(len(found) == len(set(found)) and set(found) == rows[lane],
                f"missing/duplicate/unexpected rows: round {round_number} {lane}")
        for group, benchmark_lines in by_group.items():
            inputs[(group, lane)].extend(config + ("\n",) + tuple(benchmark_lines))
    require(len(configs) == 1, "platform/package headers differ between processes")
    expected_counts = {(lane, row): 15 for lane, names in rows.items() for row in names}
    require(dict(counts) == expected_counts, "raw sample counts differ from 15 per row")
    require(manifest.get("counts") == [
        {"lane": lane, "row": row, "samples": count}
        for (lane, row), count in sorted(expected_counts.items())], "manifest sample counts differ from raw data")
    return manifest, samples, inputs, digest(manifest_bytes)


def summarize(raw_path, manifest_path, collector_path, out_dir, benchstat, cpu):
    manifest, samples, inputs, manifest_sha = validate(raw_path, manifest_path, collector_path)
    benchstat_sha = digest(benchstat.read_bytes())
    medians = {}
    comparisons = []
    for group, (_, _, cases) in GROUPS.items():
        medians[group] = {}
        for case in cases:
            lane_medians = {}
            for lane, _, _ in LANES:
                observations = samples.get((group, case, lane))
                if observations is None:
                    continue
                lane_medians[lane] = {"samples": len(observations), **{
                    metric: statistics.median(sample[metric] for sample in observations)
                    for metric in METRICS.values()}}
            medians[group][case] = lane_medians
            before = lane_medians["baseline"]["ns_per_op"]
            after = lane_medians["candidate"]["ns_per_op"]
            comparisons.append({"group": group, "case": case,
                                "baseline_ns": before, "candidate_ns": after,
                                "change_percent": 100 * (after / before - 1)})

    input_dir = out_dir / "stats-inputs"
    input_dir.mkdir(parents=True, exist_ok=True)
    for (group, lane), lines in inputs.items():
        (input_dir / f"{group}-{lane}.txt").write_text("".join(lines))
    commands = []
    for group in GROUPS:
        cohorts = [("full", ("baseline", "candidate"))]
        if group != "interpreter":
            cohorts.append(("readme", ("candidate", "gopherlua", "golua")))
        for prefix, lanes in cohorts:
            command = ["taskset", "-c", str(cpu), str(benchstat), *(
                f"{lane}={input_dir / (group + '-' + lane + '.txt')}" for lane in lanes)]
            result = subprocess.run(command, capture_output=True, text=True, check=True)
            report = out_dir / f"{prefix}-{group}.benchstat.txt"
            report.write_text(result.stdout + result.stderr)
            commands.append({"output": report.name, "command": command})

    summary = {
        "schema_version": 1,
        "validation": {"status": "complete", "processes": len(manifest["processes"]),
                       "samples_per_row": 15, "unique_rows": 44, "samples": 660},
        "provenance": {
            "raw_file": raw_path.name, "raw_sha256": manifest["raw_sha256"],
            "manifest_file": manifest_path.name, "manifest_sha256": manifest_sha,
            "collector_sha256": manifest["collector_sha256"],
            "summarizer_sha256": digest(Path(__file__).read_bytes()),
            "sources": {lane: {"revision": source["revision"], "binary_sha256": source["sha256"]}
                        for lane, source in manifest["sources"].items()},
            "environment": manifest["environment"], "cpu_affinity": manifest["cpu_affinity"],
            "benchtime": manifest["benchtime"], "platform": manifest["platform"], "go": manifest["go"],
            "benchstat": {"path": str(benchstat), "sha256": benchstat_sha, "commands": commands},
        },
        "medians": medians, "baseline_candidate_comparisons": comparisons,
    }
    destination = out_dir / "full-medians.json"
    destination.write_text(json.dumps(summary, indent=2) + "\n")
    print(f"Validated 60 processes, 44 rows, 660 samples; wrote {destination}")
    for comparison in comparisons:
        print(f"{comparison['group']}/{comparison['case']}: {comparison['change_percent']:+.2f}%")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--raw", type=Path, default=ROOT / "full-comparison.txt")
    parser.add_argument("--manifest", type=Path, default=ROOT / "full-comparison-manifest.json")
    parser.add_argument("--collector", type=Path, default=ROOT / "collect.py")
    parser.add_argument("--out-dir", type=Path, default=ROOT)
    parser.add_argument("--benchstat", type=Path, default=Path("/tmp/lunar-puc-assessment/bin/benchstat"))
    parser.add_argument("--cpu", type=int, default=8)
    args = parser.parse_args()
    summarize(args.raw, args.manifest, args.collector, args.out_dir, args.benchstat, args.cpu)


if __name__ == "__main__":
    main()
