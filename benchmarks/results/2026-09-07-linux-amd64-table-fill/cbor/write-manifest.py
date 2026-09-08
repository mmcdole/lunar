from pathlib import Path
import hashlib, json, os, subprocess, sys
root = Path('/tmp/lunar-table-fill/cbor')
label = sys.argv[1]
if label not in ('baseline', 'candidate', 'scalar'):
    raise SystemExit('expected baseline, candidate, or scalar')
source = Path('/tmp/lunar-table-fill') / label

def git(*args):
    return subprocess.check_output(['git', '-C', str(source), *args], text=True).strip()

def fingerprint(path):
    return {'sha256': hashlib.sha256(path.read_bytes()).hexdigest(), 'bytes': path.stat().st_size}

if git('status', '--porcelain'):
    raise SystemExit('source checkout must be clean')
manifest = {
    'implementation': label,
    'source': str(source),
    'revision': git('rev-parse', 'HEAD'),
    'tree': git('rev-parse', 'HEAD^{tree}'),
    'source_clean': True,
    'go_version': subprocess.check_output(['go', 'version'], text=True).strip(),
    'build_flags': ['-trimpath'],
    'build_affinity': '6,7',
    'build_environment': {'GOCACHE': '/tmp/lunar-puc-assessment/go-cache', 'GOMAXPROCS': '2', 'GOGC': '100', 'GOMEMLIMIT': 'off'},
    'go_environment': json.loads(subprocess.check_output(['go', 'env', '-json', 'GOOS', 'GOARCH', 'GOVERSION', 'GOAMD64', 'CGO_ENABLED', 'GOMODCACHE', 'GOFLAGS'], text=True)),
    'planned_collection_environment': {'GOMAXPROCS': '1', 'GOGC': '100', 'GOMEMLIMIT': 'off', 'affinity': '2'},
    'host_policy': 'WSL2 virtual machine; host power, frequency, and external host load uncontrolled; root serializes timed workloads',
    'binaries': {name: fingerprint(root / 'bin' / name) for name in [label+'-worker', label+'-shapes']},
    'fixtures': {name: fingerprint(root / name) for name in ['small.cbor', 'large.cbor', 'fixture/cbor.lua', 'fixture/workload.lua', 'fixture/LICENSE.lua-cbor', 'fixture/PROVENANCE.md']},
    'source_files': {name: fingerprint(source / name) for name in ['go.mod', 'table.go', 'execute_table.go', 'benchmarks/cbor/go.mod', 'benchmarks/cbor/cmd/workload/main.go', 'benchmarks/cbor/cmd/run/main.go', 'benchmarks/cbor/cmd/compare/main.go', 'benchmarks/cbor/cmd/shapes/main.go', 'benchmarks/cbor/internal/fixture/fixture.go']},
}
if label == 'baseline':
    manifest['tools'] = {name: fingerprint(root / 'bin' / name) for name in ['run', 'compare', 'generate']}
expected = {
    'small.cbor': '5a840e6955b60c49832742a9e279c0d92163abceb96a22d79e7ce22c98d4b633',
    'large.cbor': '65c43f4abd104fb629f22aee7801d3b458a93e24e6a6ec6dffb4c4b02252ab7c',
}
for name, want in expected.items():
    if manifest['fixtures'][name]['sha256'] != want:
        raise SystemExit('wrong fixture hash: '+name)
for mode in ('load','save'):
    sample = json.loads((root / (label+'-small-'+mode+'-smoke.jsonl')).read_text())
    if sample['revision'] != manifest['revision'] or sample.get('revision_modified', False):
        raise SystemExit('smoke binary source identity mismatch')
    if sample['oracle']['digest'] != '7bfe6d54697c9c0954bacc2b141d373e2d87ff419ab5f868359db0e673bcadfd':
        raise SystemExit('small oracle mismatch')
(root / (label+'-manifest.json')).write_text(json.dumps(manifest, indent=2)+'\n')
print(label, manifest['revision'], manifest['binaries'][label+'-worker']['sha256'])
