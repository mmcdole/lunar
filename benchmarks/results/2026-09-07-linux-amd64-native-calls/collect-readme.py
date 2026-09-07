"""Refresh the nine existing README timing rows using the validated candidate."""
import hashlib
import json
import os
from pathlib import Path
import subprocess

root = Path('/tmp/lunar-native-pr')
repo = root / 'repo'
binary = Path('/tmp/lunar-native-main/no_clear.test')
revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip()
assert revision == 'a2f32740110096c4eee57e7ec90ede31b78236c6'
assert not subprocess.check_output(['git', 'status', '--porcelain'], cwd=repo, text=True)
digest = hashlib.sha256(binary.read_bytes()).hexdigest()
assert digest == '00c4a0e60e4407c3552b283d7c5251f25c22050303a178c89a8ae461c4fc4d16'
env = dict(os.environ, GOCACHE='/tmp/lunar-puc-assessment/go-cache', GOGC='100', GOMEMLIMIT='off', GOMAXPROCS='1')
manifest = {
    'revision': revision, 'base_revision': 'd79e0a39f8e4bd14dbf581cf204431cf24b0d073',
    'binary_sha256': digest,
    'build_manifest': '../2026-09-07-linux-amd64-native-entry-main/no-clear-manifest.json',
    'go': subprocess.check_output(['go', 'version'], text=True).strip(),
    'system': subprocess.check_output(['uname', '-srmv'], text=True).strip(),
    'samples': 15, 'benchtime': '500ms', 'GOGC': 100, 'GOMEMLIMIT': 'off', 'GOMAXPROCS': 1,
    'cpu': 1, 'affinity': 2, 'scope': 'all four program and five embedding rows in the root README',
    'power_policy': 'WSL2; host power and frequency controls unavailable; serialized on CPU2; no concurrent benchmark workloads',
    'timing': 'B.Loop; setup, compilation, warmup, and final validation excluded; required embedding API result consumption timed',
    'runtimes': {'lunar': revision},
}
for name, module in [('gopherlua', 'github.com/yuin/gopher-lua'), ('golua', 'github.com/Shopify/go-lua')]:
    manifest['runtimes'][name] = subprocess.check_output(['go', 'list', '-m', '-f', '{{.Version}}', module], cwd=repo / 'benchmarks', env=env, text=True).strip()
(root / 'readme-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
with (root / 'readme-comparison.txt').open('x') as out:
    out.write('# See readme-manifest.json; exact binary already passed the paired 13-case comparison against merged main.\n')
    lanes = ['lunar', 'gopherlua', 'golua']
    for round in range(1, 16):
        shift = (round - 1) % len(lanes)
        for lane in lanes[shift:] + lanes[:shift]:
            print(f'round {round}/15: {lane}', flush=True)
            result = subprocess.check_output([
                'taskset', '-c', '2', str(binary), '-test.run', '^$',
                '-test.bench', '^(BenchmarkPrograms|BenchmarkEmbedding)$/.*$/^runtime=' + lane + '$',
                '-test.benchtime', '500ms', '-test.cpu', '1', '-test.count', '1',
            ], cwd=repo / 'benchmarks', env=env, text=True)
            assert len([line for line in result.splitlines() if line.startswith('Benchmark')]) == 9
            out.write(f'# round: {round}; runtime: {lane}\n' + result)
            out.flush()
    out.write('# completed: 15 samples for each of 27 rows (405 samples total)\n')
