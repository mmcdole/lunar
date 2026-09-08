#!/usr/bin/env python3
"""Build only after a committed-source signal; collect only after program gate review."""
import argparse
import datetime
import fcntl
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys

ROOT = Path(__file__).resolve().parent
CONFIG = json.loads((ROOT / 'inputs.json').read_text())
ENVIRONMENT = {'GOMAXPROCS': '1', 'GOGC': '100', 'GOMEMLIMIT': 'off'}


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def fingerprint(path):
    path = Path(path)
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1 << 20), b''):
            digest.update(block)
    return {'sha256': digest.hexdigest(), 'bytes': path.stat().st_size}


def write_json(path, value):
    temporary = Path(str(path) + '.pending')
    temporary.write_text(json.dumps(value, indent=2) + '\n')
    temporary.replace(path)


def command_text(command, cwd=None):
    return subprocess.check_output(command, cwd=cwd, text=True).strip()


def source_identity(expected):
    source = CONFIG['candidate_source']
    revision = command_text(['git', '-C', source, 'rev-parse', 'HEAD'])
    dirty = command_text(['git', '-C', source, 'status', '--porcelain'])
    if revision != expected or dirty:
        raise RuntimeError(f'candidate source mismatch: revision={revision}, status={dirty!r}')
    return {'path': source, 'revision': revision, 'clean': True,
            'tree': command_text(['git', '-C', source, 'rev-parse', 'HEAD^{tree}'])}


def verify_inputs():
    for path, expected in CONFIG['files'].items():
        if fingerprint(path) != expected:
            raise RuntimeError('pinned input changed: ' + path)
    if fingerprint(CONFIG['baseline_build_manifest'])['sha256'] != CONFIG['baseline_build_manifest_sha256']:
        raise RuntimeError('baseline build manifest changed')


def run_logged(command, log_path, env, cwd=None):
    with Path(log_path).open('xb') as log:
        return subprocess.run(command, cwd=cwd, env=env, stdout=log,
                              stderr=subprocess.STDOUT, check=False).returncode


def build(args):
    verify_inputs()
    identity = source_identity(args.revision)
    manifest_path = ROOT / 'candidate-build.json'
    binary = ROOT / 'candidate-worker'
    if manifest_path.exists() or binary.exists():
        raise RuntimeError('candidate build already exists; preserve it in this experiment directory')
    env = dict(os.environ, **ENVIRONMENT, GOCACHE='/tmp/lunar-puc-assessment/go-cache')
    command = ['taskset', '-c', args.cpu, 'go', 'build', '-trimpath', '-o', str(binary), './cmd/workload']
    manifest = {'status': 'running', 'started_at': now(), 'source': identity,
                'command': command, 'cwd': str(Path(identity['path']) / 'benchmarks/cbor'),
                'environment': {key: env[key] for key in (*ENVIRONMENT, 'GOCACHE')},
                'cpu_affinity': args.cpu, 'inputs': fingerprint(ROOT / 'inputs.json'),
                'host': platform.uname()._asdict()}
    write_json(manifest_path, manifest)
    try:
        result = run_logged(command, ROOT / 'candidate-build.log', env, manifest['cwd'])
        manifest['exit_code'] = result
        if result:
            raise RuntimeError(f'candidate build failed with exit {result}')
        manifest['source_after_build'] = source_identity(args.revision)
        manifest['binary'] = {'path': str(binary), **fingerprint(binary)}
        build_info = json.loads(command_text(['go', 'version', '-m', '-json', str(binary)]))
        manifest['go_build_info'] = build_info
        settings = {row['Key']: row['Value'] for row in build_info['Settings']}
        if settings.get('vcs.revision') != args.revision or settings.get('vcs.modified') != 'false':
            raise RuntimeError('compiled worker did not embed the expected clean source revision')
        baseline_go = CONFIG['baseline_build']['go_environment']
        if build_info['GoVersion'] != baseline_go['GOVERSION']:
            raise RuntimeError('candidate Go version differs from pinned baseline')
        for name in ('GOOS', 'GOARCH', 'GOAMD64', 'CGO_ENABLED'):
            if settings.get(name) != baseline_go[name]:
                raise RuntimeError('candidate build setting differs: ' + name)
        patch = command_text(['git', '-C', identity['path'], 'diff',
                              CONFIG['baseline_revision'], args.revision, '--'])
        (ROOT / 'candidate.patch').write_text(patch + '\n')
        manifest['patch'] = fingerprint(ROOT / 'candidate.patch')
        verify_inputs()
        manifest['status'] = 'complete'
    except BaseException as error:
        manifest['status'] = 'failed'
        manifest['error'] = repr(error)
        raise
    finally:
        manifest['finished_at'] = now()
        for name in ('candidate-worker', 'candidate-build.log'):
            if (ROOT / name).exists():
                manifest[name] = fingerprint(ROOT / name)
        write_json(manifest_path, manifest)
    print(json.dumps({'build': str(manifest_path), 'binary': manifest['binary']}), flush=True)


def validate_records(paths, mode, measurement, count, build_manifest):
    records = []
    identities = {'baseline': (CONFIG['baseline_revision'], CONFIG['files'][CONFIG['baseline_worker']]['sha256']),
                  'candidate': (build_manifest['source']['revision'], build_manifest['binary']['sha256'])}
    expected_signature = None
    for label, path in paths.items():
        lane = [json.loads(line) for line in path.read_text().splitlines() if line.strip()]
        if len(lane) != count or sorted(row['sample_run'] for row in lane) != list(range(1, count + 1)):
            raise RuntimeError(f'{label}: incomplete or duplicate recorded pairs')
        for row in lane:
            revision, binary_hash = identities[label]
            if (row.get('revision'), row.get('binary_sha256')) != (revision, binary_hash) or row.get('revision_modified', False):
                raise RuntimeError(f'{label}: wrong or modified binary identity')
            checks = {'schema_version': 2, 'implementation': label, 'mode': mode,
                      'measurement': measurement, 'preset': 'large', 'execution': 'raw',
                      'context_check_interval': 0, 'go_version': CONFIG['baseline_build']['go_environment']['GOVERSION'],
                      'goos': 'linux', 'goarch': 'amd64',
                      'input_sha256': CONFIG['files'][CONFIG['data']]['sha256'],
                      'codec_sha256': CONFIG['files'][str(Path(CONFIG['fixture']) / 'cbor.lua')]['sha256'],
                      'workload_sha256': CONFIG['files'][str(Path(CONFIG['fixture']) / 'workload.lua')]['sha256'],
                      'oracle': CONFIG['expected_oracle']}
            for name, expected in checks.items():
                if row.get(name) != expected:
                    raise RuntimeError(f'{label}: record mismatch for {name}')
            if row['elapsed_ns'] <= 0 or not row.get('collection_id'):
                raise RuntimeError('invalid elapsed time or missing collection identity')
            if measurement == 'retained' and not all(name in row for name in ('heap_before', 'heap_retained', 'heap_delta')):
                raise RuntimeError('retained sample omitted measured heap fields')
            signature = (row['collection_id'], row['runtime_version'])
            if expected_signature is None:
                expected_signature = signature
            if signature != expected_signature:
                raise RuntimeError('mixed collection or runtime signature')
        records.extend(lane)
    records.sort(key=lambda row: row['sample_sequence'])
    if [row['sample_sequence'] for row in records] != list(range(1, 2 * count + 1)):
        raise RuntimeError('recorded sample sequence is incomplete or repeated')
    for start in range(0, len(records), 2):
        pair = records[start:start + 2]
        if {row['implementation'] for row in pair} != {'baseline', 'candidate'} or any(row['sample_run'] != start // 2 + 1 for row in pair):
            raise RuntimeError('sample order does not form complete adjacent pairs')
    return [{'sequence': row['sample_sequence'], 'run': row['sample_run'],
             'implementation': row['implementation']} for row in records]


def collect_one(args, mode, measurement, build_manifest):
    verify_inputs()
    source_identity(args.revision)
    if fingerprint(build_manifest['binary']['path'])['sha256'] != build_manifest['binary']['sha256']:
        raise RuntimeError('candidate binary changed since build')
    count = 15 if measurement == 'timing' else 3
    prefix = ROOT / (mode + '-' + measurement)
    if any(ROOT.glob(prefix.name + '-*')):
        raise RuntimeError(f'refusing to overwrite prior attempt: {prefix}')
    paths = {label: Path(str(prefix) + '-' + label + '.jsonl') for label in ('baseline', 'candidate')}
    command = ['taskset', '-c', args.cpu, CONFIG['runner'],
               '-baseline', CONFIG['baseline_worker'], '-candidate', build_manifest['binary']['path'],
               '-expect-baseline-sha256', CONFIG['files'][CONFIG['baseline_worker']]['sha256'],
               '-expect-baseline-revision', CONFIG['baseline_revision'], '-require-clean',
               '-comparison-mode', 'implementations', '-preset', 'large', '-mode', mode,
               '-measurement', measurement, '-fixture', CONFIG['fixture'], '-data', CONFIG['data'],
               '-runs', str(count), '-warmups', str(args.warmups), '-seed', str(args.seed),
               '-baseline-output', str(paths['baseline']), '-candidate-output', str(paths['candidate'])]
    manifest_path = Path(str(prefix) + '-manifest.json')
    manifest = {'status': 'running', 'started_at': now(), 'command': command,
                'environment': ENVIRONMENT, 'cpu_affinity': args.cpu, 'host': platform.uname()._asdict(),
                'mode': mode, 'measurement': measurement, 'recorded_pairs': count,
                'discarded_warmup_pairs': args.warmups, 'randomization_seed': args.seed,
                'program_gate_reviewed_passed': True,
                'program_gate_evidence': {'path': str(Path(args.program_gate_evidence).resolve()),
                                          **fingerprint(args.program_gate_evidence)},
                'candidate_build_manifest': fingerprint(ROOT / 'candidate-build.json'),
                'inputs_manifest': fingerprint(ROOT / 'inputs.json'),
                'policy': 'Preserve all attempted runs and partial evidence; no automatic retries or exclusions.'}
    write_json(manifest_path, manifest)
    try:
        result = run_logged(command, str(prefix) + '-collection.log', dict(os.environ, **ENVIRONMENT))
        manifest['collection_exit_code'] = result
        if result:
            raise RuntimeError(f'collection failed with exit {result}; partial evidence preserved')
        verify_inputs()
        source_identity(args.revision)
        if fingerprint(build_manifest['binary']['path'])['sha256'] != build_manifest['binary']['sha256']:
            raise RuntimeError('candidate worker changed during collection')
        manifest['sample_order'] = validate_records(paths, mode, measurement, count, build_manifest)
        compare = [CONFIG['compare'], '-baseline', str(paths['baseline']), '-candidate', str(paths['candidate']),
                   '-expect-baseline-sha256', CONFIG['files'][CONFIG['baseline_worker']]['sha256'],
                   '-expect-baseline-revision', CONFIG['baseline_revision'], '-require-clean',
                   '-comparison-mode', 'implementations', '-min-samples', str(count),
                   '-bootstrap', '10000', '-seed', str(args.seed)]
        if measurement == 'timing':
            compare += ['-max-elapsed-ratio', '1']
        manifest['comparison_policy'] = ('Strict paired evidence; candidate median elapsed time <= baseline median. '
                                          'Statistical interpretation still requires review.' if measurement == 'timing' else
                                          'Descriptive retained-memory comparison; three pairs do not qualify timing.')
        manifest['report_exit_codes'] = {}
        for format_name, suffix in [('json', 'json'), ('markdown', 'md')]:
            report_command = compare + ['-format', format_name, '-output', str(prefix) + '-report.' + suffix]
            result = run_logged(report_command, str(prefix) + '-compare-' + suffix + '.log', dict(os.environ, **ENVIRONMENT))
            manifest['report_exit_codes'][format_name] = result
            if result not in (0, 2):
                raise RuntimeError(f'comparison failed with exit {result}')
        manifest['status'] = 'complete'
        manifest['median_timing_gate_passed'] = (all(code == 0 for code in manifest['report_exit_codes'].values())
                                                  if measurement == 'timing' else None)
    except BaseException as error:
        manifest['status'] = 'failed'
        manifest['error'] = repr(error)
        raise
    finally:
        manifest['finished_at'] = now()
        manifest['artifacts'] = {path.name: fingerprint(path) for path in ROOT.glob(prefix.name + '-*')
                                 if path.is_file() and path != manifest_path and not path.name.endswith('.pending')}
        write_json(manifest_path, manifest)
    print(json.dumps({'collection': str(manifest_path), 'status': manifest['status'],
                      'median_timing_gate_passed': manifest['median_timing_gate_passed']}), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest='action', required=True)
    build_parser = subparsers.add_parser('build', help='Root invokes only after committed-source authorization.')
    collect_parser = subparsers.add_parser('collect', help='Root invokes only after the program gate passes.')
    for child in (build_parser, collect_parser):
        child.add_argument('--revision', required=True)
        child.add_argument('--cpu', default='2')
    collect_parser.add_argument('--program-gate-passed', action='store_true', required=True)
    collect_parser.add_argument('--program-gate-evidence', required=True)
    collect_parser.add_argument('--mode', choices=('load', 'save', 'both'), default='both')
    collect_parser.add_argument('--measurement', choices=('timing', 'retained', 'both'), default='both')
    collect_parser.add_argument('--warmups', type=int, default=2)
    collect_parser.add_argument('--seed', type=int, default=1)
    args = parser.parse_args()
    with (ROOT / 'operation.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if args.action == 'build':
            build(args)
        else:
            if args.warmups < 0:
                raise RuntimeError('warmup count cannot be negative')
            manifest = json.loads((ROOT / 'candidate-build.json').read_text())
            if manifest['status'] != 'complete' or manifest['source']['revision'] != args.revision:
                raise RuntimeError('collection requires the matching completed candidate build')
            modes = ('load', 'save') if args.mode == 'both' else (args.mode,)
            measurements = ('timing', 'retained') if args.measurement == 'both' else (args.measurement,)
            for measurement in measurements:
                for mode in modes:
                    collect_one(args, mode, measurement, manifest)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('cbor qualification:', error, file=sys.stderr)
        sys.exit(1)
