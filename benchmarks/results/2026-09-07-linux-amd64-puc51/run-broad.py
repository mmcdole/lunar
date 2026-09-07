import os,pathlib,subprocess,json,hashlib,sys
root=pathlib.Path('/tmp/lunar-puc-assessment')
env=dict(os.environ,GOCACHE=str(root/'go-cache'),GOMAXPROCS='1',GOGC='100',GOMEMLIMIT='off',CGO_ENABLED='1',CGO_CFLAGS=f'-I{root}/lua-5.1.5/src',CGO_LDFLAGS=f'{root}/lua-5.1.5/src/liblua.a -lm -ldl')
variants={'stack_clear':['stack-clearing.patch'],'all_table':['all_table_locals.patch'],'combined_hot_paths':['all_table_locals.patch','native-entry.patch'],'small_fixes':['pow.patch','identity.patch','short_concat.patch'],'arithmetic':['arithmetic-split.patch'],'fields':['table_executor_locals.patch'],'native_entry':['native-entry.patch']}
names=sys.argv[1:] or list(variants)
manifest={}
with (root/'broad-validation.txt').open('a') as log:
 for name in names:
  print('build and check',name,flush=True)
  path=root/('broad-'+name)
  subprocess.run(['git','clone','--quiet','--no-hardlinks',str(root/'baseline'),str(path)],check=True)
  for patch in variants[name]: subprocess.run(['git','apply',str(root/patch)],cwd=path,check=True)
  subprocess.run(['git','add','-A'],cwd=path,check=True)
  subprocess.run(['git','-c','user.name=Lunar measurement','-c','user.email=measurement@localhost','commit','-qm','Assess '+name],cwd=path,check=True)
  log.write('\nVARIANT '+name+'\n');log.flush()
  for cmd,cwd in [(['go','test','./...'],path),(['go','vet','./...'],path),(['go','test','-tags','puc51','./...'],path/'benchmarks'),(['go','vet','-tags','puc51','./...'],path/'benchmarks')]:
   log.write(' '.join(cmd)+'\n');log.flush()
   subprocess.run(cmd,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT,check=True)
  binary=root/(name+'.test')
  subprocess.run(['go','test','-tags','puc51','-c','-o',str(binary)],cwd=path/'benchmarks',env=env,check=True)
  manifest[name]={'revision':subprocess.check_output(['git','rev-parse','HEAD'],cwd=path,text=True).strip(),'patches':{p:hashlib.sha256((root/p).read_bytes()).hexdigest() for p in variants[name]},'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest()}
manifest['baseline']={'revision':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root/'baseline',text=True).strip(),'binary_sha256':hashlib.sha256((root/'compare.test').read_bytes()).hexdigest()}
label='-'.join(names)
(root/(label+'-manifest.json')).write_text(json.dumps(manifest,indent=2)+'\n')
with (root/(label+'-broad.txt')).open('x') as out:
 out.write('# 15 samples; 500ms; GOGC=100; GOMEMLIMIT=off; GOMAXPROCS=1; cpu=1; affinity=2; machine/power same as comparison.txt\n# Clean isolated candidate revisions in associated manifest; variant labels replace runtime=lunar in Go output\n')
 for round in range(1,16):
  order=['baseline']+names
  n=(round-1)%len(order);order=order[n:]+order[:n]
  print('broad round',round,flush=True)
  for name in order:
   binary=root/('compare.test' if name=='baseline' else name+'.test')
   out.write(f'# round: {round}; variant: {name}\n');out.flush()
   result=subprocess.run(['taskset','-c','2',str(binary),'-test.run','^$','-test.bench','^(BenchmarkPrograms|BenchmarkInterpreter)$/.*$/^runtime=lunar$','-test.benchtime','500ms','-test.count','1','-test.cpu','1'],cwd=root/'baseline'/'benchmarks',env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,check=True).stdout
   if sum(l.startswith('Benchmark') for l in result.splitlines())!=8: raise RuntimeError('incomplete benchmark rows')
   out.write(result.replace('/runtime=lunar','/runtime='+name));out.flush()
 out.write('# completed: 15 samples per variant for all 8 canonical rows\n')
