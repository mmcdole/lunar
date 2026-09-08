#!/usr/bin/env python3
"""Validate completed diagnostic counts and persist their provenance."""
import hashlib
import json
from pathlib import Path
import subprocess

OUT=Path(__file__).resolve().parent
ROOT=Path('/tmp/lunar-array-followup/counts')
SOURCE_INPUTS=Path('/tmp/lunar-array-access/shared/cbor/inputs.json')
manifest=json.loads((OUT/'manifest.json').read_text())
inputs=json.loads(SOURCE_INPUTS.read_text())

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def git(*args):
    return subprocess.check_output(['git','-C',str(ROOT),*args],text=True).strip()

assert git('rev-parse','HEAD')==manifest['diagnostic_revision']
assert not git('status','--porcelain')
assert sha(OUT/'instrumentation.patch')==manifest['patch_sha256']
assert subprocess.check_output(['git','-C',str(ROOT),'diff',manifest['base_revision'],'HEAD'])==(OUT/'instrumentation.patch').read_bytes()
for name,expected in manifest['source_and_harness_sha256'].items():
    assert sha(ROOT/name)==expected,name
for name,expected in manifest['binary_sha256'].items():
    assert sha(OUT/name)==expected,name

primary=('existing_nonnil_array_value','allocated_nil_slot','nonnumeric_string','nonnumeric_other',
         'numeric_zero','negative_integer','positive_integer_above_length','fractional','nan_or_infinity')
records={}
totals={}
for name in ('cbor-load','cbor-save','fannkuchredux'):
    record=json.loads((OUT/(name+'.json')).read_text())
    assert record['diagnostic_only'] is True and record['runs']==1
    assert not any(key in record for key in ('elapsed_ns','ns_per_op','total_alloc_delta','heap_delta'))
    counters=[*record['audit']['reads_by_caller'].values(),record['audit']['vm_writes']]
    for counter in counters:
        assert all(isinstance(value,int) and value>=0 for value in counter.values())
        assert sum(counter[key] for key in primary)==counter['total']
        for subset in ('above_length_at_next_index_subset','above_length_within_capacity_subset','above_length_exceeds_array_limit_subset'):
            assert counter[subset]<=counter['positive_integer_above_length']
        assert counter['existing_value_write_nil_subset']<=min(counter['existing_nonnil_array_value'],counter['write_nil_subset'])
    if name.startswith('cbor-'):
        assert record['oracle']==inputs['expected_oracle']
        assert record['revision']==manifest['diagnostic_revision']
        assert record['preset']=='large' and record['workload']==name.replace('-','_')
        fixture_inputs={
            'input_sha256':inputs['data'],
            'codec_sha256':str(Path(inputs['fixture'])/'cbor.lua'),
            'workload_sha256':str(Path(inputs['fixture'])/'workload.lua'),
        }
        for field,path in fixture_inputs.items():
            assert record[field]==inputs['files'][path]['sha256']==sha(path)
    else:
        assert record['workload']=='fannkuchredux' and record['input']==8
        assert record['output']=='1616\nPfannkuchen(8) = 22\n'
        assert '--- PASS: TestArrayAccessAuditFannkuch' in (OUT/'fannkuchredux.log').read_text()
    reads={key:sum(counter[key] for counter in record['audit']['reads_by_caller'].values())
           for key in record['audit']['vm_writes']}
    totals[name]={'reads':reads,'writes':record['audit']['vm_writes']}
    records[name]=record

executions=[]
for mode in ('load','save'):
    name='cbor-'+mode
    executions.append({
        'command':['taskset','-c','2',str(OUT/'cbor-counts'),'-mode',mode,'-measurement','timing',
                   '-preset','large','-fixture',inputs['fixture'],'-data',inputs['data'],'-format','jsonl'],
        'cwd':str(ROOT/'benchmarks/cbor'),'exit_code':0,'stdout':name+'.json','stderr':name+'.log',
    })
executions.append({
    'command':['taskset','-c','2',str(OUT/'program-counts.test'),'-test.run','^TestArrayAccessAuditFannkuch$',
               '-test.count=1','-test.v'],
    'cwd':str(ROOT/'benchmarks'),'exit_code':0,'combined_log':'fannkuchredux.log',
    'environment_addition':{'LUNAR_ARRAY_AUDIT_OUT':str(OUT/'fannkuchredux.json')},
})
manifest.update(status='complete',execution_cpu_affinity=2,executions=executions,
                execution_environment={'GOMAXPROCS':'1','GOGC':'100','GOMEMLIMIT':'off'},
                source_inputs_manifest={'path':str(SOURCE_INPUTS),'sha256':sha(SOURCE_INPUTS)},
                fixture_sha256={path:inputs['files'][path]['sha256'] for path in fixture_inputs.values()},
                expected_cbor_oracle=inputs['expected_oracle'],
                validation='All primary outcome counts sum to total; all subset counts are bounded; source, patch, harness, binaries and fixture hashes match; CBOR structural oracles and exact fannkuch output pass.',
                output_sha256={name+suffix:sha(OUT/(name+suffix)) for name in records for suffix in ('.json','.log')})
(OUT/'counts-summary.json').write_text(json.dumps({'diagnostic_only':True,'totals':totals,'records':records},indent=2)+'\n')
manifest['summary_sha256']=sha(OUT/'counts-summary.json')
(OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
for name,groups in totals.items():
    print(name)
    for group,counts in groups.items():
        hits=counts['existing_nonnil_array_value']
        print(f'  {group}: {counts["total"]:,} total; {hits:,} existing values ({100*hits/counts["total"]:.4f}%); {counts["total"]-hits:,} remaining')
print('All diagnostic records validated; no timing conclusions are derived.')
