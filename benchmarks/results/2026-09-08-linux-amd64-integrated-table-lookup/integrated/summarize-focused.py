#!/usr/bin/env python3
"""Validate the completed 90-sample focused repeat, then run pinned benchstat."""
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
BENCHSTAT = Path('/tmp/lunar-puc-assessment/bin/benchstat')
BENCHSTAT_SHA = 'f31a1e15da9b1c831c8082cb3cee774abf63fbfcb01fb7a4405aaf785ba3d054'
CASES = ('go_to_lua_scalars', 'lua_to_go_scalar_1000', 'go_string_echo_128B')


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--collection-complete', required=True, action='store_true')
    parser.parse_args()
    raw = (ROOT / 'focused-repeat.txt').read_bytes()
    manifest_bytes = (ROOT / 'focused-repeat-manifest.json').read_bytes()
    manifest = json.loads(manifest_bytes)
    plan_bytes = (ROOT / 'focused-repeat-plan.json').read_bytes()
    plan = json.loads(plan_bytes)
    require(manifest['status'] == 'complete', 'repeat is unfinished')
    require(sha(raw) == manifest['raw_sha256'], 'raw hash differs')
    require(sha((ROOT / 'collect-focused.py').read_bytes()) == manifest['collector_sha256'] == plan['collector']['sha256'],
            'collector identity differs from plan')
    require(sha((ROOT / 'build-manifest.json').read_bytes()) == plan['build_manifest_sha256'], 'build manifest changed')
    require(tuple(plan['cases']) == CASES and manifest['samples_per_row'] == 15, 'unexpected case/sample plan')
    require(manifest['samples'] == 90 and manifest['unique_rows'] == 6, 'wrong manifest sample count')
    require(manifest['lanes'] == [['baseline', 'baseline', 'lunar'], ['candidate', 'candidate', 'lunar']],
            'unexpected lanes')
    for key in ('environment', 'cpu_affinity', 'test_cpu', 'benchtime'):
        require(manifest[key] == plan[key], f'measurement setting differs: {key}')
    require(manifest['go'] == 'go version go1.26.0 linux/amd64', 'Go version differs')
    for lane in ('baseline', 'candidate'):
        source = manifest['sources'][lane]
        require(source['revision'] == plan['sources'][lane]['revision']
                and source['sha256'] == plan['sources'][lane]['binary_sha256'], 'source identity differs')
        require({key: value for key, value in source.items() if key != 'repo'} == manifest['build_manifest'][lane],
                'embedded source/build provenance differs')
        require(sha((ROOT / (lane + '.test')).read_bytes()) == source['sha256'], 'current binary differs')
        require(subprocess.check_output(['git', '-C', source['repo'], 'rev-parse', 'HEAD']).decode().strip()
                == source['revision'], 'source HEAD differs')
        require(not subprocess.check_output(['git', '-C', source['repo'], 'status', '--porcelain']).strip(),
                'source checkout is dirty')
    require(manifest['sources']['baseline']['harness_sha256'] == manifest['sources']['candidate']['harness_sha256'],
            'benchmark harness differs between lanes')
    expected = {'BenchmarkEmbedding/case=' + case + '/runtime=lunar' for case in CASES}
    require(manifest['expected_rows'] == {lane: sorted(expected) for lane in ('baseline', 'candidate')},
            'manifest row inventory differs')
    lines = raw.decode().splitlines(keepends=True)
    require(lines[0] == '# Source and measurement settings: focused-repeat-manifest.json\n', 'raw header differs')
    blocks = []
    for line in lines[1:]:
        marker = re.fullmatch(r'# round=(\d+) lane=(\w+)\n', line)
        if marker:
            blocks.append((int(marker[1]), marker[2], []))
        else:
            require(blocks, 'raw text before first process marker')
            blocks[-1][2].append(line)
    order = [(round_number, lane) for round_number in range(1, 16)
             for lane in (('baseline', 'candidate') if round_number % 2 else ('candidate', 'baseline'))]
    require([(number, lane) for number, lane, _ in blocks] == order, 'raw process order/count differs')
    require([(p['round'], p['lane']) for p in manifest['processes']] == order, 'manifest process order/count differs')
    counts, values, inputs, configs = collections.Counter(), collections.defaultdict(list), collections.defaultdict(list), set()
    for (number, lane, body), process in zip(blocks, manifest['processes']):
        output = ''.join(body)
        require(sha(output.replace('/runtime=' + lane, '/runtime=lunar').encode()) == process['stdout_sha256'],
                f'process output hash differs: round {number} {lane}')
        require(output.splitlines().count('PASS') == 1 and not any(
            line.startswith(('FAIL', '--- FAIL:', 'panic:')) for line in body), 'unsuccessful benchmark process')
        command = process['command']
        require(command[:3] == ['taskset', '-c', '2'] and Path(command[3]).name == lane + '.test'
                and command[4:] == ['-test.run=^$', '-test.bench=' + plan['filter'], '-test.benchtime=500ms',
                                    '-test.count=1', '-test.cpu=1', '-test.benchmem'], 'process command/filter differs')
        config = tuple(line for line in body if line.startswith(('goos:', 'goarch:', 'pkg:', 'cpu:')))
        require(len(config) == 4, 'missing platform headers')
        configs.add(config)
        inputs[lane].extend(config)
        inputs[lane].append('\n')
        found = []
        for line in body:
            if not line.startswith('Benchmark'):
                continue
            fields = line.split()
            require(len(fields) == 8 and fields[1].isdigit() and int(fields[1]) > 0, 'invalid benchmark sample')
            require(fields[3::2] == ['ns/op', 'B/op', 'allocs/op'], 'unexpected benchmark metrics')
            name = re.sub(r'-\d+$', '', fields[0])
            require(name.endswith('/runtime=' + lane), 'unexpected runtime label')
            stem = name.removesuffix('/runtime=' + lane)
            original = stem + '/runtime=lunar'
            found.append(original)
            counts[(lane, original)] += 1
            metrics = [float(value) for value in fields[2::2]]
            require(all(math.isfinite(value) and value >= 0 for value in metrics) and metrics[0] > 0,
                    'invalid sample values')
            values[(stem.split('=', 1)[1], lane)].append({'round': number, 'ns_per_op': metrics[0],
                                                        'bytes_per_op': metrics[1], 'allocs_per_op': metrics[2]})
            inputs[lane].append(stem + '\t' + '\t'.join(fields[1:]) + '\n')
        require(len(found) == 3 and set(found) == expected, 'missing or duplicate process rows')
    require(len(configs) == 1, 'mixed hardware/platform headers')
    expected_counts = {(lane, row): 15 for lane in ('baseline', 'candidate') for row in expected}
    require(dict(counts) == expected_counts, 'raw sample counts differ')
    require(manifest['counts'] == [{'lane': lane, 'row': row, 'samples': count}
                                   for (lane, row), count in sorted(expected_counts.items())], 'manifest counts differ')
    require(sha(BENCHSTAT.read_bytes()) == BENCHSTAT_SHA, 'benchstat binary differs')
    outputs = {lane: ROOT / ('focused-repeat-' + lane + '.benchstat-input.txt') for lane in ('baseline', 'candidate')}
    report = ROOT / 'focused-repeat.benchstat.txt'
    validation = ROOT / 'focused-repeat-validation.json'
    require(not any(p.exists() for p in [*outputs.values(), report, validation]), 'focused summary already exists')
    for lane, path in outputs.items():
        path.write_text(''.join(inputs[lane]))
    command = ['taskset', '-c', '8', str(BENCHSTAT)] + [lane + '=' + str(path) for lane, path in outputs.items()]
    result = subprocess.run(command, capture_output=True, text=True)
    report.write_text(result.stdout)
    (ROOT / 'focused-repeat-benchstat.stderr.log').write_text(result.stderr)
    require(result.returncode == 0, 'benchstat failed; output retained')
    medians = {case: {lane: {metric: statistics.median(row[metric] for row in values[(case, lane)])
                            for metric in ('ns_per_op', 'bytes_per_op', 'allocs_per_op')}
                      for lane in ('baseline', 'candidate')} for case in CASES}
    record = {'status': 'complete', 'scope': 'Independent validation of 30 processes, 6 lane/case rows, 90 samples.',
              'raw_file': 'focused-repeat.txt', 'manifest_file': 'focused-repeat-manifest.json',
              'plan_file': 'focused-repeat-plan.json',
              'raw_sha256': sha(raw), 'manifest_sha256': sha(manifest_bytes), 'plan_sha256': sha(plan_bytes),
              'collector_sha256': manifest['collector_sha256'], 'summarizer_sha256': sha(Path(__file__).read_bytes()),
              'processes': 30, 'unique_rows': 6, 'samples': 90, 'samples_per_row': 15,
              'sources': plan['sources'], 'environment': plan['environment'], 'cpu_affinity': 2,
              'benchtime': '500ms', 'medians': medians,
              'samples_by_case': {case: {lane: values[(case, lane)] for lane in ('baseline', 'candidate')} for case in CASES},
              'benchstat': {'path': str(BENCHSTAT), 'sha256': BENCHSTAT_SHA, 'command': command, 'exit_code': 0},
              'artifacts': {p.name: {'sha256': sha(p.read_bytes()), 'bytes': p.stat().st_size}
                            for p in [*outputs.values(), report, ROOT / 'focused-repeat-benchstat.stderr.log']}}
    validation.write_text(json.dumps(record, indent=2) + '\n')
    require((ROOT / 'focused-repeat.txt').read_bytes() == raw
            and (ROOT / 'focused-repeat-manifest.json').read_bytes() == manifest_bytes, 'raw evidence changed')
    print(json.dumps({'validated_samples': 90, 'medians': medians, 'report': str(report)}))


if __name__ == '__main__':
    main()
