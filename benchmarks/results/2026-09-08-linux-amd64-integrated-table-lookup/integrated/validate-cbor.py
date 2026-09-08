#!/usr/bin/env python3
"""Read-only audit of explicitly named, completed integrated CBOR cohorts."""
import importlib.util
import json
from pathlib import Path
import statistics
import subprocess
import sys

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parent
DATA = ROOT / 'cbor-full'
spec = importlib.util.spec_from_file_location('cbor_collection', DATA / 'cbor.py')
collector = importlib.util.module_from_spec(spec)
spec.loader.exec_module(collector)
config = collector.CONFIG
fingerprint = collector.fingerprint


def read_json(path):
    return json.loads(path.read_text())


def check_fingerprint(path, expected):
    assert fingerprint(path) == {key: expected[key] for key in ('sha256', 'bytes')}, path


collector.verify_inputs()
build = read_json(DATA / 'candidate-build.json')
assert build['status'] == 'complete' and build['exit_code'] == 0
assert collector.source_identity(build['source']['revision']) == build['source'] == build['source_after_build']
check_fingerprint(build['binary']['path'], build['binary'])
check_fingerprint(DATA / 'candidate.patch', build['patch'])
reuse = read_json(DATA / 'worker-reuse.json')
assert fingerprint(DATA / 'worker-reuse.json')['sha256'] == build['reuse']['sha256']
assert fingerprint(DATA / 'worker-original-build.json')['sha256'] == build['reuse']['original_manifest_sha256']
assert fingerprint(reuse['source_build_manifest']['path'])['sha256'] == reuse['source_build_manifest']['sha256']
check_fingerprint(reuse['source_worker']['path'], reuse['source_worker'])
check_fingerprint(reuse['copied_worker']['path'], reuse['copied_worker'])
for name, expected in reuse['copied_artifacts'].items():
    check_fingerprint(DATA / name, expected)
baseline_source = Path(config['baseline_build']['source'])
assert collector.command_text(['git', '-C', str(baseline_source), 'rev-parse', 'HEAD']) == config['baseline_revision']
assert collector.command_text(['git', '-C', str(baseline_source), 'status', '--porcelain']) == ''
for name, expected in config['baseline_build']['source_files'].items():
    check_fingerprint(baseline_source / name, expected)
    if name.startswith('benchmarks/cbor/'):
        check_fingerprint(Path(config['candidate_source']) / name, expected)

results = {}
for prefix in sys.argv[1:]:
    mode, measurement = prefix.split('-')
    assert mode in ('load', 'save') and measurement in ('timing', 'retained')
    count = 15 if measurement == 'timing' else 3
    manifest = read_json(DATA / (prefix + '-manifest.json'))
    report = read_json(DATA / (prefix + '-report.json'))
    assert manifest['status'] == 'complete' and manifest['collection_exit_code'] == 0
    assert manifest['recorded_pairs'] == count and manifest['discarded_warmup_pairs'] == 2
    assert manifest['mode'] == mode and manifest['measurement'] == measurement
    assert manifest['cpu_affinity'] == '2' and manifest['environment'] == collector.ENVIRONMENT
    assert manifest['collection_purpose'] == 'exploratory_tradeoff'
    assert manifest['program_gate_state'] == 'failed' and manifest['program_gate_reviewed_passed'] is False
    assert manifest['median_timing_gate_passed'] is None
    assert manifest['report_exit_codes'] == {'json': 0, 'markdown': 0}
    assert report['qualification'] is False and report['policy']['strict_evidence'] is False
    assert report['policy']['min_samples'] == count and report['policy']['bootstrap'] == 10000
    assert report['policy']['seed'] == manifest['randomization_seed'] == 1
    assert all(gate['enabled'] is False for gate in report['gates'])
    for name, expected in manifest['artifacts'].items():
        check_fingerprint(DATA / name, expected)
    for name, field in [('candidate-build.json', 'candidate_build_manifest'), ('inputs.json', 'inputs_manifest')]:
        check_fingerprint(DATA / name, manifest[field])
    for field in ('orchestrator', 'program_gate_evidence'):
        check_fingerprint(manifest[field]['path'], manifest[field])
    paths = {lane: DATA / (prefix + '-' + lane + '.jsonl') for lane in ('baseline', 'candidate')}
    assert collector.validate_records(paths, mode, measurement, count, build) == manifest['sample_order']
    medians = {}
    for lane, path in paths.items():
        records = [json.loads(line) for line in path.read_text().splitlines()]
        assert report[lane]['samples'] == len(records) == count
        assert report['collection_id'] == records[0]['collection_id']
        medians[lane] = {}
        for field, report_field in [('elapsed_ns', 'elapsed_ns'), ('total_alloc_delta', 'total_alloc_bytes'),
                                    ('mallocs_delta', 'mallocs'), ('heap_delta', 'heap_delta_bytes')]:
            median = statistics.median(row.get(field, 0) for row in records)
            assert median == report[lane][report_field]['median']
            medians[lane][report_field] = median
        if measurement == 'retained':
            assert all(row['heap_retained'] - row['heap_before'] == row['heap_delta'] for row in records)
    command = ['taskset', '-c', '8', config['compare'], '-baseline', str(paths['baseline']),
               '-candidate', str(paths['candidate']), '-expect-baseline-sha256',
               config['files'][config['baseline_worker']]['sha256'], '-expect-baseline-revision',
               config['baseline_revision'], '-require-clean', '-comparison-mode', 'implementations',
               '-min-samples', str(count), '-bootstrap', '10000', '-seed', '1', '-format', 'json']
    process = subprocess.run(command, text=True, capture_output=True, check=True)
    assert not process.stderr
    reproduced = json.loads(process.stdout)
    assert reproduced['policy']['output_path'] == ''
    reproduced['policy']['output_path'] = report['policy']['output_path']
    assert reproduced == report, prefix + ': comparison result differs'
    results[prefix] = {
        'status': 'validated', 'qualification': False, 'records': 2 * count,
        'collection_id': report['collection_id'], 'medians': medians,
        'time_change_percent': -100 * report['elapsed_reduction'],
        'time_change_ci_percent': [-100 * report['elapsed_reduction_ci_high'], -100 * report['elapsed_reduction_ci_low']],
        'manifest': fingerprint(DATA / (prefix + '-manifest.json')),
        'report': fingerprint(DATA / (prefix + '-report.json')),
        'paired_bootstrap_reproduced_exactly': True,
    }

assert results, 'name at least one completed cohort'
print(json.dumps({'status': 'validated', 'qualification': False, 'cpu_affinity': 8,
                  'candidate_revision': build['source']['revision'],
                  'baseline_revision': config['baseline_revision'],
                  'candidate_worker_sha256': build['binary']['sha256'],
                  'baseline_worker_sha256': config['files'][config['baseline_worker']]['sha256'],
                  'oracle': config['expected_oracle'], 'cohorts': results,
                  'checks': 'Pinned inputs, worker reuse and build/source identities, unchanged harness, all artifact hashes, '
                            'complete paired order, every structural oracle, independent medians, exact paired-bootstrap '
                            'report reproduction; no workloads or builds.'}, indent=2))
