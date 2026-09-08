#!/usr/bin/env python3
"""Update the existing README timing cells from a validated comparison."""

import argparse
import difflib
import json
import math
from pathlib import Path, PurePosixPath
import re


ROOT = Path(__file__).resolve().parent
LANES = ("candidate", "gopherlua", "golua")
ROWS = (
    ("binary-trees", "programs", "binarytrees", 1e6, 2, "ms"),
    ("fannkuch-redux", "programs", "fannkuchredux", 1e6, 2, "ms"),
    ("n-body", "programs", "nbody", 1e6, 2, "ms"),
    ("spectral-norm", "programs", "spectralnorm", 1e6, 2, "ms"),
    ("Go calls Lua with scalar arguments", "embedding", "go_to_lua_scalars", 1, 2, "ns"),
    ("Lua calls Go 1,000 times", "embedding", "lua_to_go_scalar_1000", 1e3, 2, "µs"),
    ("Lua echoes a 128-byte Go string", "embedding", "go_string_echo_128B", 1, 2, "ns"),
    ("Lua checksums a reused Go-built table", "embedding", "prebuilt_go_table_16_4_to_lua", 1, 1, "ns"),
    ("Build a table in Go, then checksum it in Lua", "embedding", "create_fill_go_table_16_4_to_lua", 1e3, 3, "µs"),
)
REPORT_LINK = re.compile(r"\[measurement report\]\(benchmarks/results/[^)]+/\)")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def update(text, summary, report):
    require(summary.get("schema_version") == 1, "unsupported median schema")
    require(summary.get("validation") == {
        "status": "complete", "processes": 60, "samples_per_row": 15,
        "unique_rows": 44, "samples": 660}, "summary does not describe the completed comparison")
    path = PurePosixPath(report)
    require(not path.is_absolute() and path.parts[:2] == ("benchmarks", "results")
            and len(path.parts) == 3 and ".." not in path.parts and report.endswith("/")
            and re.fullmatch(r"benchmarks/results/[a-zA-Z0-9_.-]+/", report) is not None,
            "report must be a relative benchmarks/results/<archive>/ link")
    lines = text.splitlines(keepends=True)
    replacements = {}
    for label, group, case, divisor, precision, unit in ROWS:
        prefix = "| " + label + " | "
        matches = [index for index, line in enumerate(lines) if line.startswith(prefix)]
        require(len(matches) == 1, f"expected exactly one README row: {label}")
        index = matches[0]
        require(len(lines[index].split("|")) == 6, f"unexpected README columns: {label}")
        values = summary["medians"][group][case]
        times = []
        for lane in LANES:
            require(values[lane]["samples"] == 15, f"unexpected sample count: {case}/{lane}")
            time_ns = values[lane]["ns_per_op"]
            require(isinstance(time_ns, (int, float)) and math.isfinite(time_ns) and time_ns > 0,
                    f"invalid timing: {case}/{lane}")
            times.append(time_ns)
        cells = []
        for time_ns in times:
            cell = f"{time_ns / divisor:.{precision}f} {unit}"
            cells.append("**" + cell + "**" if time_ns == min(times) else cell)
        replacements[index] = prefix + " | ".join(cells) + " |\n"
    require(len(replacements) == 9, "expected exactly nine timing rows")
    for index, line in replacements.items():
        lines[index] = line
    updated, count = REPORT_LINK.subn("[measurement report](" + report + ")", "".join(lines))
    require(count == 1, "expected exactly one existing measurement report link")
    # All other lines, including the older retained-memory measurements, must match.
    before = [line for index, line in enumerate(text.splitlines(keepends=True)) if index not in replacements]
    after = [line for index, line in enumerate(updated.splitlines(keepends=True)) if index not in replacements]
    require(REPORT_LINK.sub("REPORT", "".join(before)) == REPORT_LINK.sub("REPORT", "".join(after)),
            "unexpected change outside timing cells and report link")
    return updated


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, required=True, help="separate publication checkout")
    parser.add_argument("--medians", type=Path, default=ROOT / "full-medians.json")
    parser.add_argument("--report", required=True, help="relative benchmarks/results/<archive>/ link")
    parser.add_argument("--dry-run", action="store_true", help="print the diff without writing")
    args = parser.parse_args()
    repo = args.repo.resolve()
    require(repo != (ROOT / "candidate").resolve(), "use a separate publication checkout")
    readme = repo / "README.md"
    original = readme.read_text()
    updated = update(original, json.loads(args.medians.read_text()), args.report)
    if args.dry_run:
        print("".join(difflib.unified_diff(original.splitlines(keepends=True), updated.splitlines(keepends=True),
                                         fromfile="a/README.md", tofile="b/README.md")), end="")
    else:
        readme.write_text(updated)
        print(f"Updated 27 timing cells and the measurement report link in {readme}")


if __name__ == "__main__":
    main()
