"""Compare merged main, fixed native entry, and removal of redundant clearing."""
import collections
import hashlib
import json
import os
from pathlib import Path
import subprocess

root = Path('/tmp/lunar-native-main')
env = dict(os.environ, GOGC='100', GOMEMLIMIT='off', GOMAXPROCS='1')
lanes = ['baseline', 'native', 'native_no_clear']
manifest = json.loads((root / 'build-manifest.json').read_text())
for lane in lanes:
    repo = root / lane
    binary = root / ('no_clear.test' if lane == 'native_no_clear' else lane + '.test')
    assert not subprocess.check_output(['git', 'status', '--porcelain'], cwd=repo, text=True), lane
    manifest['runtimes'].setdefault(lane, {}).update({
        'revision': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip(),
        'binary': binary.name,
        'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
    })
manifest['timing'] = {
    'samples': 15, 'benchtime': '500ms', 'cpu': 1, 'affinity': 2,
    'order': 'rotated three runtime processes each round',
    'power_policy': 'WSL2; host power/frequency controls unavailable; serialized on CPU2; no concurrent benchmark workloads',
    'scope': 'all four programs, four interpreter controls, and five embedding rows',
    'timed_region': 'B.Loop; preparation, compilation, warmup, final validation excluded; required embedding API result consumption timed',
}
(root / 'timing-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')

with (root / 'comparison.txt').open('x') as out:
    out.write('# See timing-manifest.json for clean revisions, binary hashes, and controls.\n')
    for round in range(1, 16):
        shift = (round - 1) % len(lanes)
        for lane in lanes[shift:] + lanes[:shift]:
            print(f'round {round}/15: {lane}', flush=True)
            binary = root / manifest['runtimes'][lane]['binary']
            result = subprocess.check_output([
                'taskset', '-c', '2', str(binary), '-test.run', '^$',
                '-test.bench', '^(BenchmarkPrograms|BenchmarkInterpreter|BenchmarkEmbedding)$/.*$/^runtime=lunar$',
                '-test.benchtime', '500ms', '-test.cpu', '1', '-test.count', '1',
            ], cwd=root / 'baseline/benchmarks', env=env, text=True)
            assert len([line for line in result.splitlines() if line.startswith('Benchmark')]) == 13
            out.write(f'# round: {round}; runtime: {lane}\n' + result.replace('/runtime=lunar', '/runtime=' + lane))
            out.flush()
    out.write('# completed: 15 samples for each of 39 rows (585 samples total)\n')
