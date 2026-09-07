"""Independent repeat of callback and spectral-norm differences."""
import collections
import os
from pathlib import Path
import subprocess

root = Path('/tmp/lunar-table-pr')
assert (root / 'final-comparison.txt').read_text().splitlines()[-1].startswith('# completed:')
env = dict(os.environ, GOGC='100', GOMEMLIMIT='off', GOMAXPROCS='1')
pattern = '^(BenchmarkPrograms|BenchmarkEmbedding|BenchmarkDiagnostics)$/^(program=spectralnorm|case=lua_to_go_scalar_1000|case=native_sqrt_10000)$/^runtime=lunar$'
with (root / 'focused-repeat.txt').open('x') as out:
    out.write('# Independent 15-sample repeat, 1s per row, identical final binaries and controls; alternating process order.\n')
    for round in range(1, 16):
        order = ['baseline', 'lunar'] if round % 2 else ['lunar', 'baseline']
        for lane in order:
            print(f'round {round}/15: {lane}', flush=True)
            output = subprocess.check_output(['taskset', '-c', '2', str(root / ('final-' + lane + '.test')), '-test.run', '^$', '-test.bench', pattern, '-test.benchtime', '1s', '-test.cpu', '1', '-test.count', '1'], cwd=root / 'repo/benchmarks', env=env, text=True)
            assert len([line for line in output.splitlines() if line.startswith('Benchmark')]) == 3
            if lane == 'baseline':
                output = output.replace('/runtime=lunar', '/runtime=baseline')
            out.write(f'# round: {round}; runtime: {lane}\n' + output)
            out.flush()
    out.write('# completed: 15 samples for each of 6 rows (90 samples total)\n')
