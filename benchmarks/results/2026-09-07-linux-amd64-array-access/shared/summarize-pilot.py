#!/usr/bin/env python3
"""Validate and summarize a completed two-lane, 13-case array-access pilot."""

import argparse
import collections
import hashlib
import importlib.util
import json
import math
from pathlib import Path
import re
import statistics
import subprocess

ROOT = Path('/tmp/lunar-array-access')
spec = importlib.util.spec_from_file_location('full_summary', ROOT / 'summarize.py')
full = importlib.util.module_from_spec(spec)
spec.loader.exec_module(full)


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, default=ROOT / 'shared')
    parser.add_argument('--name', default='gate-pilot')
    parser.add_argument('--samples', type=int, default=5)
    parser.add_argument('--cpu', type=int, default=8)
    parser.add_argument('--validate-only', action='store_true')
    args = parser.parse_args()
    raw_path = args.directory / (args.name + '.txt')
    manifest_path = args.directory / (args.name + '-manifest.json')
    raw = raw_path.read_bytes()
    manifest_bytes = manifest_path.read_bytes()
    manifest = json.loads(manifest_bytes)
    require(manifest['status'] == 'complete', 'collection is not complete')
    require(manifest['samples_per_row'] == args.samples, 'unexpected sample count')
    require(manifest['lanes'] == [list(lane) for lane in full.LANES[:2]], 'unexpected lanes')
    require(manifest['raw_sha256'] == sha(raw), 'raw digest differs')
    require(manifest['collector_sha256'] == sha((args.directory / 'collect.py').read_bytes()),
            'collector digest differs')
    expected = {lane: sorted(rows) for lane, rows in full.expected_rows().items()
                if lane in ('baseline', 'candidate')}
    require(manifest['expected_rows'] == expected, 'expected complete 13-case coverage in both lanes')
    require(manifest['unique_rows'] == 26 and manifest['samples'] == args.samples * 26,
            'manifest totals differ')
    require(manifest['environment'] == {'GOGC': '100', 'GOMEMLIMIT': 'off', 'GOMAXPROCS': '1'}
            and manifest['cpu_affinity'] == 2 and manifest['test_cpu'] == 1
            and manifest['benchtime'] == '500ms', 'measurement settings differ')
    for lane in expected:
        require({k: v for k, v in manifest['sources'][lane].items() if k != 'repo'} ==
                manifest['build_manifest'][lane], 'source/build identity differs')
    require(manifest['sources']['baseline']['harness_sha256'] ==
            manifest['sources']['candidate']['harness_sha256'], 'harness differs')
    lines = raw.decode().splitlines(keepends=True)
    require(lines[0] == '# Source and measurement settings: ' + manifest_path.name + '\n',
            'wrong manifest link in raw output')
    blocks = []
    body = None
    for line in lines[1:]:
        match = full.MARKER.fullmatch(line)
        if match:
            body = []
            blocks.append((int(match[1]), match[2], body))
        else:
            require(body is not None, 'unexpected raw prefix')
            body.append(line)
    order = [(round_number, lane) for round_number in range(1, args.samples + 1)
             for lane in (('baseline', 'candidate') if round_number % 2 else ('candidate', 'baseline'))]
    require([(n, lane) for n, lane, _ in blocks] == order, 'raw round/lane order differs')
    require([(p['round'], p['lane']) for p in manifest['processes']] == order,
            'manifest round/lane order differs')
    counts = collections.Counter()
    samples = collections.defaultdict(list)
    for (round_number, lane, body), process in zip(blocks, manifest['processes']):
        output = ''.join(body)
        require(sha(output.replace('/runtime=' + lane, '/runtime=lunar').encode()) ==
                process['stdout_sha256'], 'process digest differs')
        require(output.splitlines().count('PASS') == 1, 'process did not pass')
        names = []
        for line in body:
            if not line.startswith('Benchmark'):
                continue
            fields = line.split()
            require(len(fields) == 8 and fields[1].isdigit() and int(fields[1]) > 0,
                    'invalid benchmark line')
            name = re.sub(r'-\d+$', '', fields[0])
            require(name.endswith('/runtime=' + lane), 'unexpected runtime')
            original = name.replace('/runtime=' + lane, '/runtime=lunar')
            names.append(original)
            counts[(lane, original)] += 1
            require(fields[3::2] == ['ns/op', 'B/op', 'allocs/op'], 'unexpected metrics')
            values = [float(value) for value in fields[2::2]]
            require(all(math.isfinite(value) and value >= 0 for value in values) and values[0] > 0,
                    'invalid measurement')
            samples[(original.removesuffix('/runtime=lunar'), lane)].append(values[0])
        require(len(names) == 13 and len(set(names)) == 13 and sorted(names) == expected[lane],
                f'row coverage differs in round {round_number} {lane}')
    expected_counts = {(lane, name): args.samples for lane, names in expected.items() for name in names}
    require(dict(counts) == expected_counts, 'raw sample counts differ')
    require(manifest['counts'] == [
        {'lane': lane, 'row': name, 'samples': count}
        for (lane, name), count in sorted(expected_counts.items())], 'recorded sample counts differ')
    print(f'Validated {len(blocks)} processes, 26 rows, {sum(counts.values())} samples.', flush=True)
    for name in sorted({name for name, _ in samples}):
        before = statistics.median(samples[(name, 'baseline')])
        after = statistics.median(samples[(name, 'candidate')])
        print(f'{name}: {before:g} -> {after:g} ns/op, {100 * (after / before - 1):+.2f}%', flush=True)
    if args.validate_only:
        return
    for group in ('Programs', 'Embedding', 'Interpreter'):
        command = ['taskset', '-c', str(args.cpu), str(ROOT.parent / 'lunar-puc-assessment/bin/benchstat'),
                   '-col', '/runtime', '-filter', '.name:' + group, str(raw_path)]
        subprocess.run(command, check=True)


if __name__ == '__main__':
    main()
