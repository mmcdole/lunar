#!/usr/bin/env python3
"""Build private operation-count instrumentation; never execute workloads."""
import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT = Path('/tmp/lunar-array-followup/counts')
OUT = Path(__file__).resolve().parent
BASE = 'a98a15738f29dbf4684e7ee51213ab8f811673df'
ENV = dict(os.environ, GOCACHE='/tmp/lunar-puc-assessment/go-cache', GOMAXPROCS='1',
           GOGC='100', GOMEMLIMIT='off', CGO_ENABLED='1')
manifest = {'diagnostic_only': True, 'base_revision': BASE, 'source': str(ROOT),
            'cpu_affinity': 6, 'commands': [], 'status': 'preparing',
            'environment': {key: ENV[key] for key in ('GOCACHE','GOMAXPROCS','GOGC','GOMEMLIMIT','CGO_ENABLED')}}

def write():
    (OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')

def run(command, log, cwd=ROOT):
    with (OUT/log).open('xb') as output:
        result = subprocess.run(command, cwd=cwd, env=ENV, stdout=output, stderr=subprocess.STDOUT)
    manifest['commands'].append({'command':command,'cwd':str(cwd),'log':log,'exit_code':result.returncode})
    write()
    if result.returncode:
        raise RuntimeError(log+' failed')

def git(*args):
    return subprocess.check_output(['git','-C',str(ROOT),*args],text=True).strip()

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

try:
    run(['taskset','-c','6','go','test','-count=1','./...'],'root-tests.log')
    run(['taskset','-c','6','go','vet','./...'],'root-vet.log')
    subprocess.run(['git','-C',str(ROOT),'add','array_access_audit.go','array_access_audit_test.go',
                    'table.go','execute_table.go','native_call.go','library_base.go',
                    'benchmarks/cbor/cmd/workload/main.go','benchmarks/array_access_audit_test.go'],check=True)
    subprocess.run(['git','-C',str(ROOT),'diff','--cached','--check'],check=True)
    (OUT/'instrumentation.patch').write_bytes(subprocess.check_output(['git','-C',str(ROOT),'diff','--cached',BASE]))
    subprocess.run(['git','-C',str(ROOT),'commit','-m','Instrument array lookup outcomes for diagnostic counts'],check=True)
    assert not git('status','--porcelain')
    manifest.update(diagnostic_revision=git('rev-parse','HEAD'),tree=git('rev-parse','HEAD^{tree}'),
                    patch_sha256=sha(OUT/'instrumentation.patch'),
                    go_version=subprocess.check_output(['go','version'],text=True).strip())
    paths=['array_access_audit.go','array_access_audit_test.go','table.go','execute_table.go','native_call.go',
           'library_base.go','go.mod','benchmarks/cbor/go.mod','benchmarks/cbor/cmd/workload/main.go',
           'benchmarks/go.mod','benchmarks/go.sum','benchmarks/array_access_audit_test.go',
           'benchmarks/program_compare_test.go','benchmarks/programs/fannkuchredux.lua']
    manifest['source_and_harness_sha256']={name:sha(ROOT/name) for name in paths}
    run(['taskset','-c','6','go','build','-trimpath','-o',str(OUT/'cbor-counts'),'./cmd/workload'],
        'cbor-build.log',ROOT/'benchmarks/cbor')
    run(['taskset','-c','6','go','test','-c','-o',str(OUT/'program-counts.test')],
        'program-build.log',ROOT/'benchmarks')
    assert not git('status','--porcelain')
    manifest['binary_sha256']={name:sha(OUT/name) for name in ('cbor-counts','program-counts.test')}
    manifest['scope']='CBOR Start/Stop surrounds only loadBenchmarkGraph or saveBenchmarkGraph; fannkuch surrounds only prepared.run. Setup and validation excluded. No diagnostic timing output is emitted.'
    manifest['status']='ready'
except BaseException as error:
    manifest.update(status='failed',error=repr(error))
    raise
finally:
    write()
