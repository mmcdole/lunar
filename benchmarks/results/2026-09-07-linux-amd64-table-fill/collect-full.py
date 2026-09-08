"""Collect the Lunar gate and README program/embedding comparison once.

No builds occur here. Root schedules this collector after other timings finish.
"""
import argparse
import collections
import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import tempfile
import time
from pathlib import Path

ROOT = Path('/tmp/lunar-table-fill')
PROGRAMS = ('binarytrees', 'fannkuchredux', 'nbody', 'spectralnorm')
INTERPRETER = ('numeric_for_10000', 'fixed_lua_calls_1000', 'table_field_get_set_10000', 'string_append_256')
EMBEDDING = ('go_to_lua_scalars', 'lua_to_go_scalar_1000', 'go_string_echo_128B', 'prebuilt_go_table_16_4_to_lua', 'create_fill_go_table_16_4_to_lua')
LANES = (
    {'label': 'baseline', 'binary': 'baseline', 'runtime': 'lunar', 'interpreter': True},
    {'label': 'scalar', 'binary': 'scalar', 'runtime': 'lunar', 'interpreter': True},
    {'label': 'gopherlua', 'binary': 'scalar', 'runtime': 'gopherlua', 'interpreter': False},
    {'label': 'golua', 'binary': 'scalar', 'runtime': 'golua', 'interpreter': False},
)
ROUNDS = 15
BENCHTIME = '500ms'
CPU = '2'


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(repo, *args):
    return subprocess.check_output(['git', '-C', str(repo), *args], text=True).strip()


def expected_rows(lane):
    suffix = '/runtime=' + lane['runtime']
    names = {'BenchmarkPrograms/program=' + name + suffix for name in PROGRAMS}
    names.update('BenchmarkEmbedding/case=' + name + suffix for name in EMBEDDING)
    if lane['interpreter']:
        names.update('BenchmarkInterpreter/case=' + name + suffix for name in INTERPRETER)
    return names


def benchmark_filter(lane):
    groups = 'BenchmarkPrograms|BenchmarkEmbedding'
    if lane['interpreter']:
        groups += '|BenchmarkInterpreter'
    return '^(' + groups + ')$/.*$/^runtime=' + lane['runtime'] + '$'


def checked_rows(output, lane):
    rows = []
    for line in output.splitlines():
        if not line.startswith('Benchmark'):
            continue
        fields = line.split()
        if len(fields) < 4 or not fields[1].isdigit() or int(fields[1]) < 1:
            raise RuntimeError('malformed benchmark row: ' + line)
        if 'ns/op' not in fields or 'B/op' not in fields or 'allocs/op' not in fields:
            raise RuntimeError('missing Go benchmark metrics: ' + line)
        rows.append(re.sub(r'-\d+$', '', fields[0]))
    if len(rows) != len(set(rows)) or set(rows) != expected_rows(lane):
        raise RuntimeError('unexpected benchmark rows for ' + lane['label'] + ': ' + repr(rows))
    if 'PASS' not in output.splitlines():
        raise RuntimeError('benchmark process did not report PASS: ' + lane['label'])
    return rows


def record_source(name, builds):
    repo = ROOT / name
    if git(repo, 'status', '--porcelain'):
        raise RuntimeError('source is not clean: ' + name)
    revision = git(repo, 'rev-parse', 'HEAD')
    expected_revision = builds['base_revision' if name == 'baseline' else 'scalar_revision']
    if revision != expected_revision:
        raise RuntimeError('source revision changed: ' + name)
    binary = ROOT / (name + '.test')
    digest = sha256(binary)
    if digest != builds[name + '_sha256']:
        raise RuntimeError('binary differs from build manifest: ' + name)
    relative = ['benchmarks/go.mod', 'benchmarks/go.sum', 'benchmarks/compare_test.go',
                'benchmarks/program_compare_test.go', 'benchmarks/embedding_test.go']
    relative += ['benchmarks/programs/' + name + '.lua' for name in PROGRAMS]
    return {'source': str(repo), 'revision': revision, 'tree': git(repo, 'rev-parse', 'HEAD^{tree}'),
            'clean': True, 'binary': str(binary), 'binary_sha256': digest,
            'source_sha256': {path: sha256(repo / path) for path in relative},
            'binary_build_info': subprocess.check_output(['go', 'version', '-m', str(binary)], text=True)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--name', default='full-comparison', help='new output basename in /tmp/lunar-table-fill')
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]*', args.name):
        parser.error('name must be a simple output basename')
    raw_path = ROOT / (args.name + '.txt')
    manifest_path = ROOT / (args.name + '-manifest.json')
    if raw_path.exists() or manifest_path.exists():
        raise SystemExit('refusing to overwrite existing collection evidence')
    build_path = ROOT / 'build-manifest.json'
    builds = json.loads(build_path.read_text())
    sources = {name: record_source(name, builds) for name in ('baseline', 'scalar')}
    if sources['baseline']['source_sha256'] != sources['scalar']['source_sha256']:
        raise RuntimeError('canonical benchmark sources differ between baseline and scalar')
    env = dict(os.environ, GOGC='100', GOMEMLIMIT='off', GOMAXPROCS='1')
    cpu_model = next((line.split(':', 1)[1].strip() for line in Path('/proc/cpuinfo').read_text().splitlines() if line.startswith('model name')), 'unavailable')
    machine_path = Path('/sys/devices/virtual/dmi/id/product_name')
    manifest = {
        'schema_version': 1, 'name': args.name, 'status': 'running',
        'samples_per_row': ROUNDS, 'benchtime': BENCHTIME, 'expected_unique_rows': 44,
        'expected_total_samples': 660, 'expected_processes': 60,
        'go': subprocess.check_output(['go', 'version'], text=True).strip(),
        'system': platform.platform(), 'cpu_model': cpu_model,
        'machine_model': machine_path.read_text().strip() if machine_path.exists() else 'unavailable',
        'environment': {key: env[key] for key in ('GOGC', 'GOMEMLIMIT', 'GOMAXPROCS')},
        'affinity': int(CPU), 'test_cpu': 1,
        'power_policy': 'WSL2; host power, frequency and external host load uncontrolled; root serializes benchmark processes on guest CPU 2',
        'order': 'baseline, scalar, gopherlua, golua; rotate starting lane by one each round',
        'timed_region': 'Go B.Loop; setup, compilation, one warmup and final validation excluded; required embedding API result consumption included',
        'allocation_metrics': 'Go B/op and allocs/op; allocation traffic, not retained memory',
        'sources': sources, 'build_manifest_sha256': sha256(build_path), 'build_manifest': builds,
        'collector_sha256': sha256(Path(__file__).resolve()),
        'lanes': [{**lane, 'expected_rows': sorted(expected_rows(lane)), 'bench_filter': benchmark_filter(lane)} for lane in LANES],
        'versions': {'gopherlua': 'v1.1.2', 'golua': 'v0.0.0-20250718183320-1e37f32ad7d0'},
        'publication_scope': 'Programs and Embedding: scalar Lunar, GopherLua, go-lua; Interpreter: baseline/scalar gate only; no PUC samples',
        'started': time.time(), 'processes': [],
    }
    manifest_path.write_text(json.dumps(manifest, indent=2) + '\n')
    counts = collections.Counter()
    try:
        with tempfile.TemporaryDirectory(prefix='lunar-full-workers-', dir='/tmp') as staging:
            binaries = {}
            for name, source in sources.items():
                target = Path(staging) / (name + '.test')
                shutil.copyfile(source['binary'], target)
                target.chmod(0o500)
                if sha256(target) != source['binary_sha256']:
                    raise RuntimeError('staged binary hash mismatch: ' + name)
                binaries[name] = target
            with raw_path.open('x') as output:
                output.write('# Manifest: ' + manifest_path.name + '\n')
                output.write('# 15 samples/row; 500ms; CPU 2; GOMAXPROCS=1; GOGC=100; GOMEMLIMIT=off\n')
                output.write('# Lunar baseline/scalar gate plus scalar/GopherLua/go-lua README comparison; no PUC rows\n')
                for round_index in range(ROUNDS):
                    shift = round_index % len(LANES)
                    for lane in LANES[shift:] + LANES[:shift]:
                        print(f"round {round_index+1}/{ROUNDS}: {lane['label']}", flush=True)
                        command = ['taskset', '-c', CPU, str(binaries[lane['binary']]), '-test.run', '^$',
                                   '-test.bench', benchmark_filter(lane), '-test.benchtime', BENCHTIME,
                                   '-test.cpu', '1', '-test.count', '1']
                        started = time.time()
                        process = subprocess.run(command, cwd=ROOT/'baseline'/'benchmarks', env=env,
                                                 text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                                 timeout=15*60)
                        output.write(f"# round: {round_index+1}; lane: {lane['label']}; source-runtime: {lane['runtime']}\n")
                        output.write(process.stdout.replace('/runtime='+lane['runtime'], '/runtime='+lane['label']))
                        output.flush()
                        if process.returncode:
                            raise RuntimeError(f"{lane['label']} benchmark failed with exit {process.returncode}")
                        rows = checked_rows(process.stdout, lane)
                        for row in rows:
                            counts[(lane['label'], row)] += 1
                        manifest['processes'].append({'round': round_index+1, 'lane': lane['label'],
                            'command': command, 'started': started, 'finished': time.time(),
                            'validated_rows': len(rows), 'raw_stdout_sha256': hashlib.sha256(process.stdout.encode()).hexdigest()})
                        manifest_path.write_text(json.dumps(manifest, indent=2) + '\n')
                expected_counts = {(lane['label'], row): ROUNDS for lane in LANES for row in expected_rows(lane)}
                if dict(counts) != expected_counts or sum(counts.values()) != 660:
                    raise RuntimeError('incomplete final benchmark counts')
                for name, source in sources.items():
                    if sha256(binaries[name]) != source['binary_sha256'] or sha256(Path(source['binary'])) != source['binary_sha256']:
                        raise RuntimeError('benchmark binary changed during collection: ' + name)
                    if git(ROOT/name, 'status', '--porcelain') or git(ROOT/name, 'rev-parse', 'HEAD') != source['revision']:
                        raise RuntimeError('source checkout changed during collection: ' + name)
                output.write('# completed: 660 samples, 44 unique rows, 15 samples each, 60 serialized processes\n')
        manifest.update(status='completed', finished=time.time(), verified_samples=sum(counts.values()),
                        verified_unique_rows=len(counts), raw_sha256=sha256(raw_path),
                        counts=[{'lane': lane, 'row': row, 'samples': count} for (lane,row),count in sorted(counts.items())])
    except BaseException as error:
        manifest.update(status='failed', finished=time.time(), error=str(error), verified_samples=sum(counts.values()))
        raise
    finally:
        manifest_path.write_text(json.dumps(manifest, indent=2) + '\n')
    print('completed:', raw_path, flush=True)


if __name__ == '__main__':
    main()
