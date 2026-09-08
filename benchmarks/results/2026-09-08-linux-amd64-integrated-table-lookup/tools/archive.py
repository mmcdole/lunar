#!/usr/bin/env python3
"""Copy finalized integrated-lookup evidence into the private publication clone.

After the coordinator confirms all Go/CBOR collections have stopped:
  python3 archive.py --measurements-complete --check-only
  python3 archive.py --measurements-complete

This only reads measurement sources and writes the selected report directory.
It never invokes a worker, build, benchmark, git mutation, or README updater.
"""

import argparse
from contextlib import ExitStack
import datetime
import fcntl
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path('/tmp/lunar-array-followup')
SOURCE = ROOT / 'integrated/candidate'
REVISION = '22ad3f1e7b751031360705eea9338b5f0428a1e5'
MAIN = '5fc51e449a6661340056544e995aae4fdf89dcb2'
DEST = ROOT / 'publication/benchmarks/results/2026-09-08-linux-amd64-integrated-table-lookup'
KINDS = {'.json', '.jsonl', '.md', '.txt', '.log', '.py', '.sh', '.patch', '.pprof', '.go'}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha(payload):
    return hashlib.sha256(payload).hexdigest()


def read_json(path):
    return json.loads(path.read_bytes())


def verify(path, expected):
    data = path.read_bytes()
    digest = expected if isinstance(expected, str) else expected['sha256']
    require(sha(data) == digest, f'hash mismatch: {path}')
    if isinstance(expected, dict) and 'bytes' in expected:
        require(len(data) == expected['bytes'], f'byte count mismatch: {path}')


def terminal(value, path):
    if isinstance(value, dict):
        require(value.get('status') not in ('running', 'collecting', 'building'), f'unfinished manifest: {path}')
        for item in value.values():
            terminal(item, path)
    elif isinstance(value, list):
        for item in value:
            terminal(item, path)


def verify_json(path, data):
    """Check the concrete hash-bearing fields used by these existing collectors."""
    terminal(data, path)
    if not isinstance(data, dict):
        return
    directory = path.parent
    if 'raw_sha256' in data:
        default_raw = ('full-comparison.txt' if path.name == 'full-validation.json'
                       else path.name.removesuffix('-manifest.json') + '.txt')
        raw = directory / data.get('raw_file', default_raw)
        verify(raw, data['raw_sha256'])
        if path.name == 'full-validation.json':
            verify(directory / 'full-comparison-manifest.json', data['manifest_sha256'])
        if 'collector_sha256' in data:
            require(any(sha(p.read_bytes()) == data['collector_sha256'] for p in directory.glob('collect*.py')),
                    f'collector hash mismatch: {path}')
    provenance = data.get('provenance', {})
    for name in ('raw', 'manifest'):
        if name + '_file' in provenance:
            verify(directory / provenance[name + '_file'], provenance[name + '_sha256'])
    if 'collector_sha256' in provenance:
        require(any(sha(p.read_bytes()) == provenance['collector_sha256'] for p in directory.glob('collect*.py')),
                f'summary collector hash mismatch: {path}')
    if 'summarizer_sha256' in provenance:
        verify(Path('/tmp/lunar-array-access/summarize.py'), provenance['summarizer_sha256'])
    for name in ('artifacts', 'output_sha256', 'audit_scripts_sha256', 'copied_artifacts'):
        for relative, fingerprint in data.get(name, {}).items():
            verify(directory / relative, fingerprint)
    for check in data.get('checks', []):
        if 'log' in check and 'sha256' in check:
            verify(directory / check['log'], check['sha256'])
        elif 'log_sha256' in check:
            prefix = path.name.removesuffix('validation.json')
            verify(directory / (prefix + check['name'] + '.log'), check['log_sha256'])
    for name in ('source_test', 'combined_test', 'overlay', 'log'):
        if name in data and name + '_sha256' in data:
            verify(Path(data[name]), data[name + '_sha256'])
    for name in ('orchestrator', 'program_gate_evidence', 'source_inputs_manifest', 'source_build_manifest'):
        if name in data:
            verify(Path(data[name]['path']), data[name])
    for name, local in (('candidate_build_manifest', 'candidate-build.json'), ('inputs_manifest', 'inputs.json')):
        if name in data:
            verify(directory / local, data[name])
    if path.name in ('candidate-build.json', 'worker-original-build.json'):
        verify(directory / 'inputs.json', data['inputs'])
        verify(directory / 'candidate.patch', data['patch'])
        verify(directory / 'candidate-build.log', data['candidate-build.log'])
        verify(Path(data['binary']['path']), data['binary'])
        if 'reuse' in data:
            verify(Path(data['reuse']['record']), data['reuse'])
            verify(directory / data['reuse']['original_manifest'], data['reuse']['original_manifest_sha256'])
    if path.name == 'inputs.json':
        for original, fingerprint in data['files'].items():
            verify(Path(original), fingerprint)
        verify(Path(data['baseline_build_manifest']), data['baseline_build_manifest_sha256'])
    if path.name == 'build-manifest.json':
        for lane in ('baseline', 'candidate'):
            verify(directory / (lane + '.test'), data[lane]['sha256'])
            source = directory / lane
            for kind in ('runtime_sha256', 'harness_sha256'):
                for name, digest in data[lane][kind].items():
                    verify(source / name, digest)
    if 'pairs' in data and 'profile_sha256' in next(iter(data.get('runs', [])), {}):
        require(data['status'] == 'complete' and len(data['runs']) == 10 and data['pairs'] == 5,
                'profile cohort is incomplete')
        for run in data['runs']:
            require(run['exit_code'] == 0 and run['oracle_passed'], 'profile run failed validation')
            prefix = directory / f"{run['lane']}-{run['pair']}"
            for suffix, key in (('.jsonl', 'output_sha256'), ('.stderr.log', 'stderr_sha256'),
                                ('.cpu.pprof', 'profile_sha256')):
                verify(Path(str(prefix) + suffix), run[key])
        verify(ROOT / 'profile-pairs.py', data['collector_sha256'])
    if 'input_manifest_sha256' in data:
        verify(ROOT / 'profiles/manifest.json', data['input_manifest_sha256'])
    if data.get('diagnostic_only') and 'output_sha256' in data:
        verify(directory / 'instrumentation.patch', data['patch_sha256'])
        verify(directory / 'counts-summary.json', data['summary_sha256'])
        for original, fingerprint in data['fixture_sha256'].items():
            verify(Path(original), fingerprint)
        for binary, fingerprint in data['binary_sha256'].items():
            verify(directory / binary, fingerprint)
        for name, fingerprint in data['source_and_harness_sha256'].items():
            verify(Path(data['source']) / name, fingerprint)


def eligible(path):
    return (path.is_file() and not path.is_symlink() and path.suffix in KINDS
            and path.name != 'compiler.txt' and not path.name.endswith('.pending.json')
            and not any(part in path.name for part in ('-compiler.txt', '-disassembly.txt', '-symbols.txt')))


def plan(destination):
    require(subprocess.check_output(['git', '-C', str(SOURCE), 'rev-parse', 'HEAD']).decode().strip() == REVISION,
            'integrated source revision changed')
    require(not subprocess.check_output(['git', '-C', str(SOURCE), 'status', '--porcelain']).strip(),
            'integrated source is dirty')
    required = [ROOT / 'integrated/full-comparison-manifest.json']
    required += [ROOT / f'integrated/cbor-full/{mode}-{kind}-manifest.json'
                 for mode in ('load', 'save') for kind in ('timing', 'retained')]
    for path in required:
        require(path.exists() and read_json(path).get('status') in ('complete', 'failed'),
                f'planned collection has not finalized: {path}')
    payloads, originals = {}, {}

    def add(relative, data, source):
        relative = str(relative)
        if relative.endswith('.go'):
            relative += '.txt'
        require(relative != 'README.md' and relative not in payloads, f'conflicting archive destination: {relative}')
        payloads[relative] = (data, {'source': str(source), 'sha256': sha(data), 'bytes': len(data)})

    def copy(source, relative):
        data = source.read_bytes()
        require(len(data) < 2_000_000, f'unexpected large artifact: {source}')
        originals[source] = sha(data)
        if source.suffix == '.json':
            verify_json(source, json.loads(data))
        add(relative, data, source)

    directories = [('profiles', 'attribution/profiles'), ('profile-analysis', 'attribution/profile-analysis'),
                   ('profile-analysis-source-path-attempt', 'attribution/profile-analysis-source-path-attempt'),
                   ('counts-artifacts', 'attribution/counts'), ('integrated', 'integrated'),
                   ('integrated/stats-inputs', 'integrated/stats-inputs'),
                   ('integrated/cbor-pilot', 'integrated/cbor-pilot'), ('integrated/cbor-full', 'integrated/cbor-full')]
    for original, archived in directories:
        for source in sorted((ROOT / original).glob('*')):
            if eligible(source):
                copy(source, Path(archived) / source.name)
    for original, archived in [('profile-pairs.py', 'attribution/profile-pairs.py'),
                               ('analyze-profiles.py', 'attribution/analyze-profiles.py'),
                               ('profile-review.md', 'attribution/profile-review.md'),
                               ('integrated-design.md', 'source-rationale.md'), ('archive.py', 'tools/archive.py')]:
        copy(ROOT / original, archived)
    copy(Path('/tmp/lunar-array-access/summarize.py'), 'tools/summarize.py')
    patch = subprocess.check_output(['git', '-C', str(SOURCE), 'diff', MAIN, REVISION, '--'])
    if 'integrated/candidate.patch' not in payloads:
        add('integrated/candidate.patch', patch, f'git:{MAIN}..{REVISION}')
    else:
        require(payloads['integrated/candidate.patch'][0] == patch, 'integrated patch differs')
    for name in ('table.go', 'execute_table.go', 'execute_array_access_test.go'):
        data = subprocess.check_output(['git', '-C', str(SOURCE), 'show', f'{REVISION}:{name}'])
        add(Path('sources') / name, data, f'git:{REVISION}:{name}')
    add('EVIDENCE.md', (
        'The [source rationale](source-rationale.md), [semantic review](integrated/review.md), '
        '[compiler review](integrated/compiler-review.md), and [candidate patch](integrated/candidate.patch) '
        'describe the single integrated candidate based on main. Go source snapshots use .go.txt '
        'filenames and retain their original bytes.\n\n'
        'The [paired profile analysis](attribution/profile-review.md) and '
        '[operation counts](attribution/counts/README.md) concern the earlier shared-array candidate. '
        'They establish attribution and path frequency, not integrated-candidate speed. '
        'The original [array-access experiment](../2026-09-07-linux-amd64-array-access/README.md) '
        'remains separate; no samples are pooled with it.\n\n'
        'Integrated Go and CBOR pilots and full collections remain in distinct files/directories. '
        'Every attempted cohort and original manifest is retained, including exploratory or failed '
        'results. A complete archive does not itself establish a passed performance gate. '
        'The coordinator writes the final assessment separately.\n\n'
        'archive-manifest.json records original artifact paths and archived hashes. Logs and '
        'manifests retain the exact commands used on the measurement machine. Executable binaries '
        'and large compiler dumps are omitted; source/build identities and binary hashes remain. '
        'This helper does not change any runtime or root README file.\n'
    ).encode(), 'generated by archive.py')
    for source, digest in originals.items():
        require(sha(source.read_bytes()) == digest, f'artifact changed during archive preparation: {source}')
    require(not any(destination.rglob('*.go')), 'existing .go build inputs in archive')
    for relative, (data, _) in payloads.items():
        target = destination / relative
        require(not relative.endswith('.go'), 'source snapshot would become a Go build input')
        require(not target.exists() or target.read_bytes() == data, f'changed existing archive artifact: {target}')
    return payloads


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--measurements-complete', required=True, action='store_true')
    parser.add_argument('--check-only', action='store_true')
    parser.add_argument('--destination', type=Path, default=DEST)
    args = parser.parse_args()
    require(not (args.destination / 'archive-manifest.json').exists(), 'archive already finalized')
    with ExitStack() as stack:
        for directory in ('cbor-pilot', 'cbor-full'):
            lock = stack.enter_context((ROOT / 'integrated' / directory / 'operation.lock').open('r'))
            fcntl.flock(lock, fcntl.LOCK_SH | fcntl.LOCK_NB)
        payloads = plan(args.destination)
        if not args.check_only:
            for relative, (data, _) in payloads.items():
                target = args.destination / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                if not target.exists():
                    with target.open('xb') as output:
                        output.write(data)
            record = {'status': 'complete', 'created_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                      'source_revision': REVISION, 'baseline_revision': MAIN,
                      'files': {name: data for name, (_, data) in sorted(payloads.items())}}
            with (args.destination / 'archive-manifest.json').open('x') as output:
                json.dump(record, output, indent=2, sort_keys=True)
                output.write('\n')
    print(json.dumps({'check_only': args.check_only, 'destination': str(args.destination),
                      'files': len(payloads), 'bytes': sum(len(data) for data, _ in payloads.values())}))


if __name__ == '__main__':
    main()
