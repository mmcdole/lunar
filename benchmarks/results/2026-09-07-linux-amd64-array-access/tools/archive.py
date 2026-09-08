#!/usr/bin/env python3
"""Archive completed array-access experiments without changing runtime or README.

Run only after the measurement coordinator confirms all collection has stopped:
  python3 archive.py --measurements-complete --check-only
  python3 archive.py --measurements-complete

Original manifests retain their recorded machine-local paths. archive-manifest.json
maps source paths to archived paths; report links are rewritten for offline review.
"""

import argparse
import collections
import datetime
import fcntl
import hashlib
import importlib.util
import json
import math
from pathlib import Path
import re
import subprocess
import sys


BASE = Path('/tmp/lunar-array-access')
PROFILE = Path('/tmp/lunar-side-by-side')
PUC51 = Path('/tmp/lunar-puc-assessment/lua-5.1.5')
PUC55 = PROFILE / 'lua-5.5.1'
MAIN = '5fc51e449a6661340056544e995aae4fdf89dcb2'
VARIANTS = {
    'initial': (BASE, 'cd44f540ce289c1b85483290f0f36366d4c1cbf8'),
    'unsigned': (BASE / 'unsigned', 'c013ee6fc609324345cc92358dbd435e3358b5c3'),
    'shared': (BASE / 'shared', 'a98a15738f29dbf4684e7ee51213ab8f811673df'),
}
DEST = BASE / 'publication/benchmarks/results/2026-09-07-linux-amd64-array-access'
SUFFIXES = {'.json', '.jsonl', '.log', '.txt', '.py', '.sh', '.patch', '.go'}
OMIT_COPIED = {'compiler-review.md', 'puc-source-review.md', 'review.md', 'build-manifest.pending.json'}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def read_json(path):
    return json.loads(path.read_bytes())


def encode_json(value):
    return (json.dumps(value, indent=2, sort_keys=True) + '\n').encode()


def verify(path, expected):
    require(path.is_file(), f'missing referenced artifact: {path}')
    payload = path.read_bytes()
    digest = expected if isinstance(expected, str) else expected['sha256']
    require(sha(payload) == digest, f'SHA-256 mismatch: {path}')
    if isinstance(expected, dict) and 'bytes' in expected:
        require(len(payload) == expected['bytes'], f'byte count mismatch: {path}')
    return payload


def ensure_final(value, path):
    if isinstance(value, dict):
        require(value.get('status') not in ('running', 'collecting', 'building'),
                f'refusing unfinished manifest: {path}')
        for child in value.values():
            ensure_final(child, path)
    elif isinstance(value, list):
        for child in value:
            ensure_final(child, path)


def matching_script(directory, expected, pattern):
    matches = [p for root in (directory, BASE) for p in root.glob(pattern)
               if p.is_file() and sha(p.read_bytes()) == expected]
    require(matches, f'no matching {pattern} for {expected} at {directory}')
    return matches[0]


def validate_benchmark(path, manifest):
    """Validate every pilot, full run and repeat using its own declared row set."""
    raw = path.with_name(path.name.removesuffix('-manifest.json') + '.txt')
    payload = verify(raw, manifest['raw_sha256'])
    matching_script(path.parent, manifest['collector_sha256'], 'collect*.py')
    if manifest.get('status') != 'complete':
        return  # Failed cohorts are kept intact, never accepted as complete.
    lines = payload.decode().splitlines(keepends=True)
    require(lines[0] == f'# Source and measurement settings: {path.name}\n',
            f'raw manifest reference differs: {raw}')
    blocks = []
    for line in lines[1:]:
        marker = re.fullmatch(r'# round=(\d+) lane=(\w+)\n', line)
        if marker:
            blocks.append((int(marker[1]), marker[2], []))
        else:
            require(blocks, f'content before process header: {raw}')
            blocks[-1][2].append(line)
    lanes = manifest['lanes']
    expected_order = []
    count = manifest['samples_per_row']
    for number in range(1, count + 1):
        shift = (number - 1) % len(lanes)
        expected_order.extend((number, lane[0]) for lane in lanes[shift:] + lanes[:shift])
    require([(n, lane) for n, lane, _ in blocks] == expected_order,
            f'raw round/lane order mismatch: {raw}')
    require([(p['round'], p['lane']) for p in manifest['processes']] == expected_order,
            f'manifest process coverage mismatch: {path}')
    lane_info = {lane: (binary, runtime) for lane, binary, runtime in lanes}
    counts = collections.Counter()
    configs = set()
    for (number, lane, body), process in zip(blocks, manifest['processes']):
        output = ''.join(body)
        _, runtime = lane_info[lane]
        restored = output.replace(f'/runtime={lane}', f'/runtime={runtime}')
        require(sha(restored.encode()) == process['stdout_sha256'],
                f'process output hash differs: {path} round {number} {lane}')
        require(output.splitlines().count('PASS') == 1 and not any(
            line.startswith(('FAIL', '--- FAIL:', 'panic:')) for line in body),
            f'unsuccessful process: {path} round {number} {lane}')
        configs.add(tuple(line for line in body if line.startswith(('goos:', 'goarch:', 'pkg:', 'cpu:'))))
        found = []
        for line in body:
            if not line.startswith('Benchmark'):
                continue
            fields = line.split()
            require(len(fields) == 8 and int(fields[1]) > 0, f'invalid benchmark row: {raw}')
            require(fields[3::2] == ['ns/op', 'B/op', 'allocs/op'], f'invalid units: {raw}')
            values = [float(x) for x in fields[2::2]]
            require(all(math.isfinite(x) and x >= 0 for x in values) and values[0] > 0,
                    f'invalid measurements: {raw}')
            name = re.sub(r'-\d+$', '', fields[0])
            require(name.endswith('/runtime=' + lane), f'wrong runtime label: {raw}')
            original = name.removesuffix('/runtime=' + lane) + '/runtime=' + runtime
            found.append(original)
            counts[(lane, original)] += 1
        require(len(found) == len(set(found)) and sorted(found) == manifest['expected_rows'][lane],
                f'wrong process rows: {raw} round {number} {lane}')
    require(len(configs) == 1 and len(next(iter(configs))) == 4, f'mixed platform headers: {raw}')
    expected_counts = {(lane, row): count for lane, rows in manifest['expected_rows'].items() for row in rows}
    require(dict(counts) == expected_counts, f'raw sample counts differ: {raw}')
    require(sum(counts.values()) == manifest['samples'] and len(counts) == manifest['unique_rows'],
            f'manifest counts differ: {path}')
    require(manifest['counts'] == [{'lane': lane, 'row': row, 'samples': n}
                                   for (lane, row), n in sorted(counts.items())],
            f'manifest row counts differ: {path}')


def validate_json(path, value):
    ensure_final(value, path)
    if not isinstance(value, dict):
        return
    if 'raw_sha256' in value:
        if 'collector_sha256' in value:
            validate_benchmark(path, value)
        else:
            raw = path.with_name(path.name.removesuffix('-manifest.json') + '.txt')
            verify(raw, value['raw_sha256'])
    provenance = value.get('provenance', {})
    if 'raw_file' in provenance:
        verify(path.parent / provenance['raw_file'], provenance['raw_sha256'])
        verify(path.parent / provenance['manifest_file'], provenance['manifest_sha256'])
        matching_script(path.parent, provenance['collector_sha256'], 'collect*.py')
        matching_script(path.parent, provenance['summarizer_sha256'], 'summarize*.py')
    for name, fingerprint in value.get('artifacts', {}).items():
        verify(path.parent / name, fingerprint)
    for check in value.get('checks', []):
        if 'log' in check and 'sha256' in check:
            verify(path.parent / check['log'], check['sha256'])
        elif 'log_sha256' in check:
            prefix = path.name.removesuffix('validation.json')
            verify(path.parent / (prefix + check['name'] + '.log'), check['log_sha256'])
    for name, local in (('candidate_build_manifest', 'candidate-build.json'),
                        ('inputs_manifest', 'inputs.json')):
        if name in value:
            verify(path.parent / local, value[name])
    for name in ('orchestrator', 'program_gate_evidence'):
        if name in value:
            verify(Path(value[name]['path']), value[name])
    if path.name == 'candidate-build.json':
        verify(path.parent / 'inputs.json', value['inputs'])
        verify(path.parent / 'candidate.patch', value['patch'])
        verify(path.parent / 'candidate-build.log', value['candidate-build.log'])
        verify(Path(value['binary']['path']), value['binary'])
    if path.name == 'inputs.json':
        for name, fingerprint in value['files'].items():
            verify(Path(name), fingerprint)
        verify(Path(value['baseline_build_manifest']), value['baseline_build_manifest_sha256'])


def git(repo, *arguments):
    return subprocess.check_output(['git', '-C', str(repo), *arguments])


def eligible(path):
    return (path.is_file() and not path.is_symlink() and path.suffix in SUFFIXES
            and not any(word in path.name for word in ('-compiler.', '-disassembly.', '-symbols.'))
            and path.name not in ('archive.py', 'build-manifest.pending.json'))


def prepare(destination):
    planned = {}
    originals = {}
    checks = []

    def add(relative, payload, origin, original_payload=None, transformation=None):
        relative = str(relative)
        # Evidence snapshots must never become packages in the benchmarks module.
        # Preserve exact source bytes and source-path metadata while changing only
        # the archived filename, including standalone diagnostic/test overlays.
        if Path(relative).suffix == '.go':
            relative += '.txt'
        require(relative not in planned, f'duplicate archive path: {relative}')
        require(Path(relative).name != 'README.md', f'refusing report README mutation: {relative}')
        planned[relative] = (payload, {'source': str(origin), 'sha256': sha(payload), 'bytes': len(payload)})
        if original_payload is not None:
            planned[relative][1]['source_sha256'] = sha(original_payload)
        if transformation:
            planned[relative][1]['transformation'] = transformation

    def copy(source, relative):
        payload = source.read_bytes()
        originals[source] = sha(payload)
        if source.suffix == '.json':
            validate_json(source, json.loads(payload))
            checks.append(str(source))
        add(relative, payload, source)

    # Refuse missing as well as running planned CBOR cohorts. A finalized failure
    # is preserved, not converted to success or silently omitted.
    for measurement in ('timing', 'retained'):
        for mode in ('load', 'save'):
            path = BASE / f'shared/cbor/{mode}-{measurement}-manifest.json'
            require(path.is_file(), f'planned CBOR cohort has not finalized: {path}')
            require(read_json(path).get('status') in ('complete', 'failed'),
                    f'CBOR cohort is unfinished: {path}')

    for label, (directory, revision) in VARIANTS.items():
        repo = directory / 'candidate'
        require(git(repo, 'rev-parse', 'HEAD').decode().strip() == revision, f'wrong {label} source revision')
        require(not git(repo, 'status', '--porcelain').strip(), f'dirty {label} source checkout')
        for source in sorted(directory.iterdir()):
            if eligible(source):
                copy(source, Path(label) / source.name)
        for subdirectory in ('stats-inputs', 'cbor'):
            for source in sorted((directory / subdirectory).glob('*')):
                if eligible(source) or (subdirectory == 'cbor' and source.is_file()
                                        and source.name.endswith('-report.md')):
                    copy(source, Path(label) / subdirectory / source.name)
        add(Path(label) / 'candidate.patch', git(repo, 'diff', MAIN, revision, '--'),
            f'git:{repo}:{MAIN}..{revision}')
        add(Path(label) / 'revisions.json', (json.dumps({'baseline': MAIN, 'candidate': revision}, indent=2) + '\n').encode(),
            f'git:{repo}:{revision}')
        build = read_json(directory / 'build-manifest.json')
        for lane in ('baseline', 'candidate'):
            lane_repo = Path('/tmp/lunar-table-fill/baseline') if lane == 'baseline' else repo
            lane_revision = MAIN if lane == 'baseline' else revision
            for kind in ('runtime_sha256', 'harness_sha256'):
                for name, expected in build[lane][kind].items():
                    require(sha(git(lane_repo, 'show', f'{lane_revision}:{name}')) == expected,
                            f'{label} {lane} source hash mismatch: {name}')
            verify(directory / (lane + '.test'), build[lane]['sha256'])

    # Reuse the collector's identity/oracle/paired-order checks without invoking
    # build(), collect(), a worker, or any benchmark process.
    sys.dont_write_bytecode = True
    collector = BASE / 'shared/cbor/cbor.py'
    spec = importlib.util.spec_from_file_location('archived_cbor_validator', collector)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    for measurement in ('timing', 'retained'):
        for mode in ('load', 'save'):
            prefix = collector.parent / f'{mode}-{measurement}'
            manifest = read_json(Path(str(prefix) + '-manifest.json'))
            if manifest['status'] == 'complete':
                order = module.validate_records(
                    {lane: Path(str(prefix) + f'-{lane}.jsonl') for lane in ('baseline', 'candidate')},
                    mode, measurement, manifest['recorded_pairs'], read_json(collector.parent / 'candidate-build.json'))
                require(order == manifest['sample_order'], f'CBOR recorded order differs: {prefix}')

    for source in sorted((PROFILE / 'profiles').iterdir()):
        if source.is_file():
            copy(source, Path('main-profile/profiles') / source.name)
    for name in ('profile-manifest.json', 'source-provenance.json',
                 'current-main-puc51-manifest.json', 'current-main-puc51.txt'):
        copy(PROFILE / name, Path('main-profile') / name)

    # Small source snapshots make local source links usable without a temp checkout.
    repo = BASE / 'shared/candidate'
    revision = VARIANTS['shared'][1]
    for name in ('table.go', 'execute_table.go', 'native_call.go', 'execute_array_access_test.go'):
        add(Path('sources/shared') / name, git(repo, 'show', f'{revision}:{name}'), f'git:{repo}:{revision}:{name}')
    for root, label in ((PUC51, 'puc51'), (PUC55, 'puc55')):
        for name in ('ltable.c', 'ltable.h', 'lvm.c', 'lapi.c'):
            copy(root / 'src' / name, Path('sources') / label / name)
        copy(root / ('COPYRIGHT' if (root / 'COPYRIGHT').exists() else 'doc/readme.html'),
             Path('sources') / label / 'license-source.txt')

    cbor_base = Path('/tmp/lunar-table-fill/cbor')
    copy(cbor_base / 'baseline-manifest.json', 'cbor-common/baseline-build-manifest.json')
    for source in sorted((cbor_base / 'fixture').iterdir()):
        if source.is_file():
            copy(source, Path('cbor-common/fixture') / source.name)
    # Keep reproducible harness/generator source; executable workers and the 9 MB
    # generated dataset are identified by hashes, not copied into Git.
    for name in ('go.mod', 'stock.mod', 'stock.sum', 'cmd/run/main.go', 'cmd/compare/main.go',
                 'cmd/workload/main.go', 'cmd/generate/main.go', 'internal/fixture/fixture.go'):
        full_name = 'benchmarks/cbor/' + name
        add(Path('cbor-common/source') / name, git(repo, 'show', f'{revision}:{full_name}'),
            f'git:{repo}:{revision}:{full_name}')

    copy(BASE / 'shared/puc-rationale.md', 'source-rationale.md')
    source = BASE / 'shared/review.md'
    original = source.read_bytes()
    review = original.decode()
    review = re.sub(r'candidate/([\w.]+\.go):(\d+)', r'sources/shared/\1.txt#L\2', review)
    review = re.sub(re.escape(str(PUC51 / 'src')) + r'/([\w.]+):(\d+)', r'sources/puc51/\1#L\2', review)
    require('/tmp/' not in review, 'unmapped temporary link in runtime review')
    originals[source] = sha(original)
    add('review.md', review.encode(), source, original, 'Local source links mapped to archived source snapshots.')
    # Preserve this exact file: it is hashed as the exploratory gate evidence.
    copy(BASE / 'shared/control-review.md', 'shared/control-review.md')
    # These reports are finalized by the coordinator before the archive is run.
    # Keeping them under shared/ preserves their relative report links unchanged.
    for name in ('program-gate.md', 'fallback-review.md'):
        copy(BASE / 'shared' / name, Path('shared') / name)
    source = BASE / 'shared/control-review.md'
    original = source.read_bytes()
    control = original.decode()
    for directory, replacement in ((BASE / 'shared', 'shared'), (BASE / 'unsigned', 'unsigned'), (BASE, 'initial')):
        control = control.replace(str(directory), replacement)
    add('control-review.md', control.encode(), source, original,
        'Recorded command paths mapped to archive directories; original retained under shared/.')
    copy(Path(__file__).resolve(), 'tools/archive.py')
    add('EVIDENCE.md', (
        'See the [source rationale](source-rationale.md), [independent runtime review](review.md), '
        'and [callback control review](control-review.md). An [original copy](shared/control-review.md) '
        'is preserved byte for byte because CBOR manifests hash it as the unresolved program-gate '
        'evidence. The linked review maps recorded command paths to archive directories. '
        'The [completed program gate](shared/program-gate.md) and '
        '[fallback analysis](shared/fallback-review.md) retain their original report links.\n\n'
        'Each variant directory retains every recorded pilot, full comparison and repeat, along '
        'with raw output, manifests, collection scripts, statistics, patches, and validation logs. '
        'A failed or exploratory collection is not a passed regression gate. '
        'The shared CBOR directory contains all four serialized timing/retained cohorts.\n\n'
        'The main-profile directory contains main-only profiles and their provenance. Program '
        'profiles include preparation and warmup. Its prior current-main PUC comparison has no '
        'optimization candidate and is separate from the before/after cohorts. Profile costs '
        'are attribution evidence, not candidate speedup measurements.\n\n'
        'Original manifests and logs retain the exact paths and commands used on the measurement '
        'machine. archive-manifest.json maps original artifacts to their archive paths and hashes. '
        'Executable workers, benchmark binaries, large compiler dumps, and generated CBOR data '
        'are omitted; source revisions, build commands, tool/binary/data hashes, fixture source '
        'and generator source are retained. Go source snapshots use .go.txt filenames so they '
        'cannot become build inputs; their original bytes are unchanged. '
        'No README timing cells are changed by this helper.\n'
    ).encode(), 'generated by archive.py')

    # Remove only superseded copied reports with known, unchanged original bytes.
    # Do not prune unknown files or any README prepared by the coordinator.
    obsolete = []
    for label, (directory, _) in VARIANTS.items():
        for name in OMIT_COPIED:
            target = destination / label / name
            source = directory / name
            if target.exists():
                require(source.is_file() and target.read_bytes() == source.read_bytes(),
                        f'refusing to remove changed pre-existing report: {target}')
                obsolete.append(target)
    for source, digest in originals.items():
        require(sha(source.read_bytes()) == digest, f'source changed while preparing archive: {source}')
    for relative, (payload, _) in planned.items():
        require(Path(relative).suffix != '.go', f'Go build input in planned archive: {relative}')
        target = destination / relative
        if target.exists():
            require(target.is_file() and target.read_bytes() == payload,
                    f'refusing to overwrite changed existing evidence: {target}')
    return planned, obsolete, checks


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--measurements-complete', action='store_true', required=True,
                        help='Coordinator has confirmed every attempted collection is finalized.')
    parser.add_argument('--check-only', action='store_true')
    parser.add_argument('--destination', type=Path, default=DEST)
    args = parser.parse_args()
    destination = args.destination.resolve()
    # The collector holds the same lock across all four CBOR cohorts, so a gap
    # between cohorts cannot be mistaken for the end of the experiment.
    with (BASE / 'shared/cbor/operation.lock').open('r') as lock:
        fcntl.flock(lock, fcntl.LOCK_SH | fcntl.LOCK_NB)
        require(not (destination / 'archive-manifest.json').exists(),
                f'archive already finalized: {destination}')
        require(not any(destination.rglob('*.go')), f'existing Go build inputs in archive: {destination}')
        planned, obsolete, checks = prepare(destination)
        if not args.check_only:
            for relative, (payload, _) in planned.items():
                target = destination / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                if not target.exists():
                    with target.open('xb') as output:
                        output.write(payload)
            for target in obsolete:
                target.unlink()
            manifest = {'created_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                        'status': 'complete', 'coordinator_confirmed_measurements_complete': True,
                        'validated_json_artifacts': checks,
                        'removed_superseded_reports': [str(p.relative_to(destination)) for p in obsolete],
                        'files': {name: metadata for name, (_, metadata) in sorted(planned.items())}}
            target = destination / 'archive-manifest.json'
            require(not target.exists(), f'archive manifest already exists: {target}')
            with target.open('xb') as output:
                output.write(encode_json(manifest))
        print(json.dumps({'check_only': args.check_only, 'destination': str(destination),
                          'files': len(planned), 'bytes': sum(len(x[0]) for x in planned.values()),
                          'validated_json_artifacts': len(checks), 'superseded_reports': len(obsolete)}))


if __name__ == '__main__':
    main()
