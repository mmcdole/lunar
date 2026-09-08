#!/usr/bin/env python3
"""Validate and aggregate completed equal-count CBOR load CPU profiles.

This reads existing profile data and runs pprof reports on CPU 8. It never runs
a workload, benchmark, or worker. Run only after the coordinator's signal:
  python3 analyze-profiles.py --profiles-complete
"""

import argparse
import collections
import datetime
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path('/tmp/lunar-array-followup')
CONFIG = Path('/tmp/lunar-array-access/shared/cbor/inputs.json')
ENV = dict(os.environ, GOMAXPROCS='1', GOGC='100', GOMEMLIMIT='off',
           GOCACHE='/tmp/lunar-puc-assessment/go-cache', GOTELEMETRY='off')
SELECT = re.compile(r'(rawSlot|executeRawTableSet|normalizeTableKey|positiveIntegerIndex|'
                    r'existingArrayIndex|rawIntSlot|rawNormalizedSlot|resolveNormalizedSlot|'
                    r'rawSetNormalizedSlot|findRecord|hashNumber|rawStringKeySlot|'
                    r'runAutomaticCollection|measureSemanticHeap|addStringBacking|'
                    r'runNativeCall|enterCall|prepareCall)$')
ANNOTATE = (r'(rawSlot|executeRawTableSet|normalizeTableKey|positiveIntegerIndex|'
            r'existingArrayIndex|rawIntSlot|rawNormalizedSlot|resolveNormalizedSlot)$')


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(payload):
    return hashlib.sha256(payload).hexdigest()


def json_bytes(value):
    return (json.dumps(value, indent=2, sort_keys=True) + '\n').encode()


def verify(path, expected):
    payload = path.read_bytes()
    require(digest(payload) == expected, f'SHA-256 differs: {path}')
    return payload


def varint(data, offset):
    value, shift = 0, 0
    while offset < len(data) and shift < 70:
        byte = data[offset]
        offset += 1
        value |= (byte & 127) << shift
        if byte < 128:
            return value, offset
        shift += 7
    raise ValueError('invalid protobuf varint')


def fields(data):
    """Decode the protobuf wire fields needed by the public pprof profile schema."""
    result = collections.defaultdict(list)
    offset = 0
    while offset < len(data):
        tag, offset = varint(data, offset)
        field, wire = tag >> 3, tag & 7
        require(field > 0, 'invalid protobuf field number')
        if wire == 0:
            value, offset = varint(data, offset)
        elif wire == 2:
            size, offset = varint(data, offset)
            require(offset + size <= len(data), 'truncated protobuf message')
            value, offset = data[offset:offset + size], offset + size
        elif wire in (1, 5):
            size = 8 if wire == 1 else 4
            require(offset + size <= len(data), 'truncated fixed-width protobuf field')
            value, offset = data[offset:offset + size], offset + size
        else:
            raise ValueError(f'unsupported protobuf wire type {wire}')
        result[field].append(value)
    return result


def integers(values):
    result = []
    for value in values:
        if isinstance(value, int):
            result.append(value)
        else:
            offset = 0
            while offset < len(value):
                number, offset = varint(value, offset)
                result.append(number)
    return result


def parse_profile(payload):
    profile = fields(gzip.decompress(payload))
    strings = [s.decode() for s in profile[6]]
    types = []
    for value in profile[1]:
        item = fields(value)
        types.append((strings[item[1][0]], strings[item[2][0]]))
    require(('cpu', 'nanoseconds') in types, f'CPU nanosecond sample type absent: {types}')
    index = types.index(('cpu', 'nanoseconds'))
    functions = {}
    for value in profile[5]:
        item = fields(value)
        functions[item[1][0]] = strings[item[2][0]]
    locations = {}
    for value in profile[4]:
        item = fields(value)
        # pprof line records are ordered from the innermost inline frame outward.
        locations[item[1][0]] = [functions[fields(line)[1][0]] for line in item[4]]
    flat, cumulative = collections.Counter(), collections.Counter()
    total, events = 0, 0
    period = profile[12][0]
    require(period == 10_000_000, f'unexpected sampling period: {period}')
    for value in profile[2]:
        item = fields(value)
        values = integers(item[2])
        weight = values[index]
        require(0 <= weight < 2**63 and weight % period == 0, 'invalid CPU sample weight')
        stack = [function for location in integers(item[1]) for function in locations[location]]
        require(stack or weight == 0, 'nonzero CPU sample lacks symbolized frames')
        if weight:
            flat[stack[0]] += weight
            # Count a function once per stack, including recursive/inline frames.
            for function in set(stack):
                cumulative[function] += weight
        total += weight
        events += weight // period
    require(total == sum(flat.values()) and total > 0, 'flat CPU weights do not sum to total')
    return {'sampled_cpu_ns': total, 'sample_events': events, 'period_ns': period,
            'profile_duration_ns': profile[10][0], 'flat_ns': dict(flat), 'cum_ns': dict(cumulative)}


def validate_inputs():
    manifest_path = ROOT / 'profiles/manifest.json'
    manifest_bytes = manifest_path.read_bytes()
    manifest = json.loads(manifest_bytes)
    require(manifest['status'] == 'complete', 'profiles have not completed')
    require(manifest['pairs'] == 5 and len(manifest['runs']) == 10, 'expected five complete pairs')
    require(manifest['environment'] == {'GOMAXPROCS': '1', 'GOGC': '100', 'GOMEMLIMIT': 'off'}
            and manifest['cpu_affinity'] == 2, 'profile environment differs')
    verify(ROOT / 'profile-pairs.py', manifest['collector_sha256'])
    config = json.loads(CONFIG.read_bytes())
    order = [(pair, lane) for pair in range(1, 6)
             for lane in (('baseline', 'candidate') if pair % 2 else ('candidate', 'baseline'))]
    require([(r['pair'], r['lane']) for r in manifest['runs']] == order, 'profile pairing/order differs')
    for lane in manifest['lanes'].values():
        verify(Path(lane['binary']), lane['sha256'])
        revision = subprocess.check_output(['git', '-C', lane['source'], 'rev-parse', 'HEAD'], text=True).strip()
        dirty = subprocess.check_output(['git', '-C', lane['source'], 'status', '--porcelain'], text=True).strip()
        require(revision == lane['revision'] and not dirty, 'source checkout changed after profiling')
    result = []
    for run in manifest['runs']:
        lane, pair = run['lane'], run['pair']
        prefix = ROOT / f'profiles/{lane}-{pair}'
        output = verify(Path(str(prefix) + '.jsonl'), run['output_sha256'])
        verify(Path(str(prefix) + '.stderr.log'), run['stderr_sha256'])
        profile = verify(Path(str(prefix) + '.cpu.pprof'), run['profile_sha256'])
        require(run['exit_code'] == 0 and run['oracle_passed'], 'failed profile run')
        rows = [json.loads(line) for line in output.splitlines() if line.strip()]
        require(len(rows) == 1, 'profile output contains wrong record count')
        row = rows[0]
        checks = {'schema_version': 2, 'mode': 'load', 'measurement': 'profile', 'preset': 'large',
                  'execution': 'raw', 'context_check_interval': 0, 'go_version': 'go1.26.0',
                  'goos': 'linux', 'goarch': 'amd64', 'revision': manifest['lanes'][lane]['revision'],
                  'input_sha256': config['files'][config['data']]['sha256'],
                  'codec_sha256': config['files'][str(Path(config['fixture']) / 'cbor.lua')]['sha256'],
                  'workload_sha256': config['files'][str(Path(config['fixture']) / 'workload.lua')]['sha256'],
                  'oracle': config['expected_oracle']}
        for key, expected in checks.items():
            require(row.get(key) == expected, f'{lane} pair {pair}: record differs for {key}')
        require(not row.get('revision_modified', False), 'profiled source was modified')
        data = parse_profile(profile)
        data.update({'lane': lane, 'pair': pair, 'profile_sha256': run['profile_sha256'],
                     'worker_elapsed_ns': row['elapsed_ns'], 'profile_file': str(prefix) + '.cpu.pprof'})
        result.append(data)
    return manifest, manifest_bytes, result


def format_name(name):
    return name.removeprefix('github.com/mmcdole/lunar.')


def create_report(summary):
    lanes = summary['lanes']
    lines = ['Five paired CBOR load CPU profiles compare the same number of completed operations. '
             'The tables below use absolute sampled CPU across all five loads; per-load values divide '
             'by five. Samples occur every 10 ms. These profiled runs provide attribution, not speed '
             'qualification; the separate unprofiled comparison remains the performance result.\n',
             '| Lane | Loads | Sampled CPU total | CPU per load | Sample events |\n'
             '| --- | ---: | ---: | ---: | ---: |']
    for lane in ('baseline', 'candidate'):
        data = lanes[lane]
        lines.append(f"| {lane} | 5 | {data['sampled_cpu_ns']/1e9:.3f} s | "
                     f"{data['sampled_cpu_ns']/5e6:.1f} ms | {data['sample_events']} |")
    lines += ['\nNormalized cumulative shares divide a function’s cumulative sampled CPU by that '
              'lane’s own total. Cumulative values overlap along call stacks and must not be added. '
              'A share change can result from another part of the program changing. None of these '
              'shares is a removable cost or a predicted speedup. Inline attribution and instruction '
              'layout can move samples between adjacent source lines; a line difference does not '
              'establish that the new guard caused the workload regression.\n',
              '| Pair (actual order) | Baseline sampled CPU | Candidate sampled CPU | Difference |\n'
              '| --- | ---: | ---: | ---: |']
    for pair in range(1, 6):
        runs = {r['lane']: r for r in summary['runs'] if r['pair'] == pair}
        b, c = (runs[x]['sampled_cpu_ns'] for x in ('baseline', 'candidate'))
        order = 'B → C' if pair % 2 else 'C → B'
        lines.append(f'| {pair} ({order}) | {b/1e6:.0f} ms | {c/1e6:.0f} ms | {(c-b)/1e6:+.0f} ms |')
    names = set().union(*(set(lanes[lane]['cum_ns']) for lane in lanes))
    selected = sorted(name for name in names if SELECT.search(name))
    largest = sorted(names, key=lambda name: max(lanes[x]['flat_ns'].get(name, 0)
                                                for x in lanes), reverse=True)[:25]
    for title, functions in [('Table paths and surrounding operations', selected),
                             ('Largest exclusive CPU costs across either lane', largest)]:
        lines += [f'\n{title}:\n',
                  '| Function | B flat | C flat | B cumulative | C cumulative | B share | C share |\n'
                  '| --- | ---: | ---: | ---: | ---: | ---: | ---: |']
        for name in functions:
            b, c = lanes['baseline'], lanes['candidate']
            bf, cf = b['flat_ns'].get(name, 0), c['flat_ns'].get(name, 0)
            bc, cc = b['cum_ns'].get(name, 0), c['cum_ns'].get(name, 0)
            lines.append(f'| `{format_name(name)}` | {bf/1e6:.0f} ms | {cf/1e6:.0f} ms | '
                         f'{bc/1e6:.0f} ms | {cc/1e6:.0f} ms | '
                         f"{100*bc/b['sampled_cpu_ns']:.2f}% | {100*cc/c['sampled_cpu_ns']:.2f}% |")
    lines += ['\nDetailed reports: [baseline annotated table paths](baseline-annotated.txt), '
              '[candidate annotated table paths](candidate-annotated.txt), '
              '[baseline full CPU table](baseline-top.txt), [candidate full CPU table](candidate-top.txt), '
              'and [exact weights for every function and run](summary.json).\n']
    return ('\n'.join(lines) + '\n').encode()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--profiles-complete', required=True, action='store_true')
    parser.add_argument('--cpu', type=int, default=8)
    parser.add_argument('--output', type=Path, default=ROOT / 'profile-analysis')
    args = parser.parse_args()
    require(args.cpu == 8, 'analysis is assigned to CPU 8')
    manifest, manifest_bytes, runs = validate_inputs()
    require(not args.output.exists(), f'refusing to overwrite analysis: {args.output}')
    args.output.mkdir()
    summary = {'purpose': 'Attribution only; equal five-load totals, not timing qualification.',
               'input_manifest_sha256': digest(manifest_bytes), 'runs': runs, 'lanes': {}}
    for lane in ('baseline', 'candidate'):
        data = [r for r in runs if r['lane'] == lane]
        aggregate = {'loads': 5, 'sampled_cpu_ns': sum(r['sampled_cpu_ns'] for r in data),
                     'sample_events': sum(r['sample_events'] for r in data)}
        for metric in ('flat_ns', 'cum_ns'):
            counter = collections.Counter()
            for item in data:
                counter.update(item[metric])
            aggregate[metric] = dict(counter)
        summary['lanes'][lane] = aggregate
    analysis = {'status': 'running', 'cpu_affinity': args.cpu,
                'analyzer_sha256': digest(Path(__file__).read_bytes()),
                'input_manifest_sha256': digest(manifest_bytes), 'commands': [],
                'pprof_main_source_sha256': digest(Path('/usr/lib/go/src/cmd/pprof/pprof.go').read_bytes())}
    path = args.output / 'manifest.json'
    try:
        for lane, identity in manifest['lanes'].items():
            profiles = [r['profile_file'] for r in runs if r['lane'] == lane]
            common = ['taskset', '-c', str(args.cpu), 'go', 'run', 'cmd/pprof', '-symbolize=none']
            for report, options in (
                ('top', ['-top', '-unit=ns', '-nodecount=0', '-nodefraction=0', '-edgefraction=0']),
                ('annotated', ['-list=' + ANNOTATE, '-unit=ms',
                               '-source_path=' + identity['source'],
                               '-trim_path=github.com/mmcdole/lunar@v0.0.0:github.com/mmcdole/lunar']),
            ):
                command = common + options + [identity['binary']] + profiles
                record = {'lane': lane, 'report': report, 'command': command}
                analysis['commands'].append(record)
                path.write_bytes(json_bytes(analysis))
                output = args.output / f'{lane}-{report}.txt'
                error = args.output / f'{lane}-{report}.stderr.log'
                with output.open('xb') as stdout, error.open('xb') as stderr:
                    result = subprocess.run(command, env=ENV, cwd=identity['source'],
                                            stdout=stdout, stderr=stderr, timeout=180)
                record['exit_code'] = result.returncode
                require(result.returncode == 0, f'pprof {lane} {report} failed; output preserved')
                if report == 'top':
                    parsed = {}
                    pattern = re.compile(r'^\s*(\d+)(?:ns)?\s+[-\d.]+%\s+[-\d.]+%\s+'
                                         r'(\d+)(?:ns)?\s+[-\d.]+%\s+(.+)$')
                    for line in output.read_text().splitlines():
                        if match := pattern.match(line):
                            name = match[3].removesuffix(' (inline)')
                            require(name not in parsed, f'duplicate pprof function: {name}')
                            parsed[name] = (int(match[1]), int(match[2]))
                    flat_total = sum(flat for flat, cumulative in parsed.values())
                    require(flat_total == summary['lanes'][lane]['sampled_cpu_ns'],
                            f'pprof flat total and exact protobuf total differ for {lane}')
                    for name, (flat, cumulative) in parsed.items():
                        require(flat == summary['lanes'][lane]['flat_ns'].get(name, 0)
                                and cumulative == summary['lanes'][lane]['cum_ns'].get(name, 0),
                                f'pprof and exact protobuf function weights differ: {lane} {name}')
                    record['verified_function_weights'] = len(parsed)
                else:
                    require('Error:' not in output.read_text(), f'pprof source annotation failed for {lane}')
        require((ROOT / 'profiles/manifest.json').read_bytes() == manifest_bytes,
                'input manifest changed during analysis')
        (args.output / 'summary.json').write_bytes(json_bytes(summary))
        (args.output / 'REPORT.md').write_bytes(create_report(summary))
        analysis['status'] = 'complete'
    except BaseException as error:
        analysis['status'] = 'failed'
        analysis['error'] = repr(error)
        raise
    finally:
        analysis['finished_at'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        analysis['artifacts'] = {p.name: {'sha256': digest(p.read_bytes()), 'bytes': p.stat().st_size}
                                 for p in args.output.iterdir() if p.is_file() and p != path}
        path.write_bytes(json_bytes(analysis))
    print(json.dumps({'output': str(args.output), 'loads_per_lane': 5,
                      'sampled_cpu_ns': {lane: d['sampled_cpu_ns'] for lane, d in summary['lanes'].items()}}))


if __name__ == '__main__':
    main()
