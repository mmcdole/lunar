"""Screen the additional frame-copy change before expanding its measurement."""
import json
import os
from pathlib import Path
import subprocess

root = Path('/tmp/lunar-native-main')
assert (root / 'comparison.txt').read_text().splitlines()[-1].startswith('# completed:')
env = dict(os.environ, GOCACHE='/tmp/lunar-puc-assessment/go-cache', GOGC='100', GOMEMLIMIT='off', GOMAXPROCS='1')
with (root / 'cold-frame-root-race.log').open('w') as log:
    subprocess.run(['taskset', '-c', '8', 'go', 'test', '-race', './...'], cwd=root / 'native_cold_frame', env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
manifest_path = root / 'cold-frame-manifest.json'
manifest = json.loads(manifest_path.read_text())
manifest['race_check'] = 'Passed before pilot timing'
manifest['validation'].append({'name': 'root-race', 'command': ['go', 'test', '-race', './...'], 'cwd': str(root / 'native_cold_frame'), 'exit_code': 0, 'log': 'cold-frame-root-race.log'})
manifest_path.write_text(json.dumps(manifest, indent=2) + '\n')

with (root / 'cold-pilot.txt').open('x') as out:
    out.write('# Preliminary five-sample screen; 1s target, same controls, alternating fresh processes; not a full-suite qualification.\n')
    for round in range(1, 6):
        order = ['no_clear', 'cold_frame'] if round % 2 else ['cold_frame', 'no_clear']
        for lane in order:
            print(f'pilot {round}/5: {lane}', flush=True)
            result = subprocess.check_output([
                'taskset', '-c', '2', str(root / (lane + '.test')), '-test.run', '^$',
                '-test.bench', '^(BenchmarkPrograms|BenchmarkEmbedding)$/^(program=nbody|case=lua_to_go_scalar_1000)$/^runtime=lunar$',
                '-test.benchtime', '1s', '-test.cpu', '1', '-test.count', '1',
            ], cwd=root / 'baseline/benchmarks', env=env, text=True)
            assert len([line for line in result.splitlines() if line.startswith('Benchmark')]) == 2
            out.write(f'# round: {round}; runtime: {lane}\n' + result.replace('/runtime=lunar', '/runtime=' + lane))
            out.flush()
    out.write('# completed: 5 samples for each of 4 rows (20 samples total)\n')
