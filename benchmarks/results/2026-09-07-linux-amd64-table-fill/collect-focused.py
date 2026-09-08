"""Collect serialized, alternating Go benchmark processes from clean revisions."""
import argparse, collections, hashlib, json, os, platform, subprocess, time
from pathlib import Path
p=argparse.ArgumentParser()
p.add_argument('--name',required=True)
p.add_argument('--samples',type=int,required=True)
p.add_argument('--benchtime',default='500ms')
p.add_argument('--bench',default='^(BenchmarkPrograms|BenchmarkInterpreter|BenchmarkEmbedding)$/.*$/^runtime=lunar$')
p.add_argument('--rows',type=int,default=13)
p.add_argument('--lanes',nargs='+',default=['baseline','candidate'])
a=p.parse_args()
root=Path('/tmp/lunar-table-fill')
env=dict(os.environ,GOGC='100',GOMEMLIMIT='off',GOMAXPROCS='1')
manifest={'name':a.name,'samples':a.samples,'benchtime':a.benchtime,'bench_filter':a.bench,'expected_rows_per_process':a.rows,'go':subprocess.check_output(['go','version'],text=True).strip(),'system':platform.platform(),'environment':{k:env[k] for k in ['GOGC','GOMEMLIMIT','GOMAXPROCS']},'affinity':2,'cpu':1,'order':'rotated runtime processes each round','power_policy':'WSL2; host power and frequency controls unavailable; serialized CPU2; no concurrent benchmark workloads','timed_region':'B.Loop; setup/compilation/warmup/final validation excluded; required embedding API result consumption timed','runtimes':{},'started':time.time()}
for lane in a.lanes:
    repo=root/lane
    assert not subprocess.check_output(['git','status','--porcelain'],cwd=repo,text=True),lane
    binary=root/(lane+'.test')
    manifest['runtimes'][lane]={'revision':subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip(),'binary':str(binary),'sha256':hashlib.sha256(binary.read_bytes()).hexdigest()}
manifest['status']='running'
manifest_path=root/(a.name+'-manifest.json')
with manifest_path.open('x') as out: json.dump(manifest,out,indent=2);out.write('\n')
counts=collections.Counter()
with (root/(a.name+'.txt')).open('x') as out:
    out.write('# See '+manifest_path.name+' for source, executable, environment and collection settings.\n')
    for round in range(a.samples):
        shift=round%len(a.lanes)
        for lane in a.lanes[shift:]+a.lanes[:shift]:
            print(f'round {round+1}/{a.samples}: {lane}',flush=True)
            result=subprocess.check_output(['taskset','-c','2',manifest['runtimes'][lane]['binary'],'-test.run','^$','-test.bench',a.bench,'-test.benchtime',a.benchtime,'-test.cpu','1','-test.count','1'],cwd=root/'baseline/benchmarks',env=env,text=True)
            rows=[line for line in result.splitlines() if line.startswith('Benchmark')]
            assert len(rows)==a.rows,(lane,len(rows),a.rows)
            for line in rows: counts[(lane,line.split()[0])]+=1
            out.write(f'# round: {round+1}; runtime: {lane}\n'+result.replace('/runtime=lunar','/runtime='+lane));out.flush()
    assert len(counts)==a.rows*len(a.lanes)
    assert all(n==a.samples for n in counts.values())
    out.write(f'# completed: {sum(counts.values())} benchmark samples\n')
for lane,record in manifest['runtimes'].items():
    assert not subprocess.check_output(['git','status','--porcelain'],cwd=root/lane,text=True),lane
    assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=root/lane,text=True).strip()==record['revision']
    assert hashlib.sha256(Path(record['binary']).read_bytes()).hexdigest()==record['sha256']
manifest['status']='completed'
manifest['raw_sha256']=hashlib.sha256((root/(a.name+'.txt')).read_bytes()).hexdigest()
manifest['finished']=time.time()
manifest['verified_samples']=sum(counts.values())
manifest_path.write_text(json.dumps(manifest,indent=2)+'\n')
