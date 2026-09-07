import collections,hashlib,json,os,pathlib,subprocess
root=pathlib.Path('/tmp/lunar-native-next')
env=dict(os.environ,GOGC='100',GOMEMLIMIT='off',GOMAXPROCS='1')
binaries={'table':pathlib.Path('/tmp/lunar-table-pr/final-lunar.test'),'native':root/'candidate.test'}
manifest=json.loads((root/'manifest.json').read_text())
manifest['timing_run']=True
manifest['timing']={'samples':15,'benchtime':'500ms','GOGC':100,'GOMEMLIMIT':'off','GOMAXPROCS':1,'cpu':1,'affinity':2,'power_policy':'WSL2; host power/frequency controls unavailable; serialized on CPU2; no concurrent benchmark workloads','order':'alternate table/native across fresh processes','baseline_revision':'f890f7450d729d848c0c3355587b71ed251bc156','binary_sha256':{key:hashlib.sha256(path.read_bytes()).hexdigest() for key,path in binaries.items()},'scope':'incremental native-entry change over unmerged table candidate; not a main-branch acceptance comparison'}
(root/'timing-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
with (root/'comparison.txt').open('x') as out:
 out.write('# See timing-manifest.json; same controls and cgo flags as table comparison.\n')
 for round in range(1,16):
  for lane in (['table','native'] if round%2 else ['native','table']):
   print(f'round {round}/15: {lane}',flush=True)
   result=subprocess.check_output(['taskset','-c','2',str(binaries[lane]),'-test.run','^$','-test.bench','^(BenchmarkPrograms|BenchmarkInterpreter|BenchmarkEmbedding)$/.*$/^runtime=lunar$','-test.benchtime','500ms','-test.cpu','1','-test.count','1'],cwd=root/'repo/benchmarks',env=env,text=True)
   assert len([line for line in result.splitlines() if line.startswith('Benchmark')])==13
   out.write(f'# round: {round}; runtime: {lane}\n'+result.replace('/runtime=lunar','/runtime='+lane));out.flush()
 out.write('# completed: 15 samples for each of 26 rows (390 samples total)\n')
