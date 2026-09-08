"""Serialized whole-program qualification with source and executable identities."""
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

ROOT = Path('/tmp/lunar-array-followup/integrated')
PROGRAMS = ('binarytrees', 'fannkuchredux', 'nbody', 'spectralnorm')
INTERPRETER = ('numeric_for_10000', 'fixed_lua_calls_1000', 'table_field_get_set_10000', 'string_append_256')
EMBEDDING = ('go_to_lua_scalars', 'lua_to_go_scalar_1000', 'go_string_echo_128B', 'prebuilt_go_table_16_4_to_lua', 'create_fill_go_table_16_4_to_lua')

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def git(repo, *args):
    return subprocess.check_output(['git', '-C', str(repo), *args], text=True).strip()

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--name', required=True)
    parser.add_argument('--samples', type=int, default=15)
    parser.add_argument('--benchtime', default='500ms')
    parser.add_argument('--programs-only', action='store_true')
    parser.add_argument('--readme', action='store_true')
    args = parser.parse_args()
    assert re.fullmatch(r'[a-zA-Z0-9_.-]+', args.name)
    assert args.samples > 0 and not (args.programs_only and args.readme)
    raw = ROOT / (args.name + '.txt')
    mp = ROOT / (args.name + '-manifest.json')
    assert not raw.exists() and not mp.exists()
    builds = json.loads((ROOT / 'build-manifest.json').read_text())
    sources = {}
    for label in ('baseline', 'candidate'):
        repo = ROOT / label
        expected = builds[label]
        assert not git(repo, 'status', '--porcelain'), label
        assert git(repo, 'rev-parse', 'HEAD') == expected['revision'], label
        assert sha(ROOT / (label + '.test')) == expected['sha256'], label
        sources[label] = {**expected, 'repo': str(repo.resolve())}
    lanes = [('baseline', 'baseline', 'lunar'), ('candidate', 'candidate', 'lunar')]
    if args.readme:
        lanes += [('gopherlua', 'candidate', 'gopherlua'), ('golua', 'candidate', 'golua')]
    expected_rows = {}
    filters = {}
    for label, binary, runtime in lanes:
        groups = ['BenchmarkPrograms']
        rows = ['BenchmarkPrograms/program=' + p for p in PROGRAMS]
        if not args.programs_only:
            groups.append('BenchmarkEmbedding')
            rows += ['BenchmarkEmbedding/case=' + p for p in EMBEDDING]
            if runtime == 'lunar':
                groups.append('BenchmarkInterpreter')
                rows += ['BenchmarkInterpreter/case=' + p for p in INTERPRETER]
        expected_rows[label] = {row + '/runtime=' + runtime for row in rows}
        filters[label] = '^(' + '|'.join(groups) + ')$/.*$/^runtime=' + runtime + '$'
    env = dict(os.environ, GOGC='100', GOMEMLIMIT='off', GOMAXPROCS='1')
    manifest = {'name': args.name, 'status': 'running', 'sources': sources,
        'build_manifest': builds, 'collector_sha256': sha(__file__),
        'samples_per_row': args.samples, 'benchtime': args.benchtime,
        'lanes': lanes, 'expected_rows': {k: sorted(v) for k, v in expected_rows.items()},
        'environment': {k: env[k] for k in ('GOGC', 'GOMEMLIMIT', 'GOMAXPROCS')},
        'cpu_affinity': 2, 'test_cpu': 1, 'order': 'Rotate initial lane each round',
        'platform': platform.platform(), 'go': subprocess.check_output(['go', 'version'], text=True).strip(),
        'power_policy': 'WSL2 host power/frequency/external load uncontrolled; serialized timings CPU2; preparation may run on other guest CPUs',
        'timed_region': 'B.Loop; excludes loading, compilation, setup, warmup and oracle checks; includes required public API result consumption',
        'allocations': 'Go allocation traffic, not retained memory', 'processes': []}
    counts = collections.Counter()
    mp.write_text(json.dumps(manifest, indent=2) + '\n')
    try:
        with tempfile.TemporaryDirectory(prefix='lunar-array-workers-', dir='/tmp') as temp:
            binaries = {}
            for label in sources:
                destination = Path(temp) / (label + '.test')
                shutil.copyfile(ROOT / (label + '.test'), destination)
                destination.chmod(0o500)
                assert sha(destination) == sources[label]['sha256']
                binaries[label] = str(destination)
            with raw.open('x') as stream:
                stream.write('# Source and measurement settings: ' + mp.name + '\n')
                for round_index in range(args.samples):
                    shift = round_index % len(lanes)
                    for label, binary, runtime in lanes[shift:] + lanes[:shift]:
                        command = ['taskset', '-c', '2', binaries[binary], '-test.run=^$',
                            '-test.bench=' + filters[label], '-test.benchtime=' + args.benchtime,
                            '-test.count=1', '-test.cpu=1', '-test.benchmem']
                        started = time.time()
                        result = subprocess.run(command, cwd=ROOT / 'baseline/benchmarks', env=env,
                            capture_output=True, text=True, timeout=900)
                        output = result.stdout + result.stderr
                        stream.write('# round=' + str(round_index + 1) + ' lane=' + label + '\n')
                        stream.write(output.replace('/runtime=' + runtime, '/runtime=' + label))
                        stream.flush()
                        assert result.returncode == 0 and 'PASS' in output.splitlines(), output
                        names = [re.sub(r'-\d+$', '', line.split()[0]) for line in output.splitlines()
                            if line.startswith('Benchmark') and len(line.split()) >= 4]
                        assert len(names) == len(set(names)) and set(names) == expected_rows[label], names
                        counts.update((label, name) for name in names)
                        manifest['processes'].append({'round': round_index + 1, 'lane': label,
                            'command': command, 'wall_seconds': time.time() - started,
                            'stdout_sha256': hashlib.sha256(output.encode()).hexdigest()})
                        mp.write_text(json.dumps(manifest, indent=2) + '\n')
                    print('round', round_index + 1, 'of', args.samples, 'complete', flush=True)
            assert dict(counts) == {(label, row): args.samples for label, rows in expected_rows.items() for row in rows}
            for label, source in sources.items():
                assert git(ROOT / label, 'rev-parse', 'HEAD') == source['revision']
                assert not git(ROOT / label, 'status', '--porcelain')
                assert sha(ROOT / (label + '.test')) == source['sha256']
        manifest.update(status='complete', samples=sum(counts.values()), unique_rows=len(counts),
            raw_sha256=sha(raw), counts=[{'lane': k[0], 'row': k[1], 'samples': v} for k,v in sorted(counts.items())])
    except BaseException as error:
        manifest.update(status='failed', error=str(error), samples=sum(counts.values()))
        raise
    finally:
        mp.write_text(json.dumps(manifest, indent=2) + '\n')

if __name__ == '__main__':
    main()
