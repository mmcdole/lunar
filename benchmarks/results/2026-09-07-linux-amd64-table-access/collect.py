"""Collect current-main/candidate checks and fresh README runtime columns."""
import hashlib,json,os,pathlib,subprocess,sys
root=pathlib.Path('/tmp/lunar-table-pr')
stage=sys.argv[1] if len(sys.argv)>1 else 'final'
reference=pathlib.Path('/tmp/lunar-puc-assessment/lua-5.1.5/src')
env=dict(os.environ,GOCACHE='/tmp/lunar-puc-assessment/go-cache',GOGC='100',GOMEMLIMIT='off',GOMAXPROCS='1',CGO_ENABLED='1',CGO_CFLAGS=f'-I{reference}',CGO_LDFLAGS=f'{reference}/liblua.a -lm -ldl')
manifest={'source_base':'86d27109cb8fffef8f439353716e139516a89ace','go':subprocess.check_output(['go','version'],text=True).strip(),'system':subprocess.check_output(['uname','-srmv'],text=True).strip(),'power_policy':'WSL2; host power and frequency controls unavailable; timing workloads serialized on logical CPU 2; no concurrent benchmarks','samples':15,'benchtime':'500ms','GOGC':100,'GOMEMLIMIT':'off','GOMAXPROCS':1,'cpu':1,'affinity':2,'CGO_CFLAGS':env['CGO_CFLAGS'],'CGO_LDFLAGS':env['CGO_LDFLAGS'],'puc_version':'5.1.5','puc_source_sha256':'2640fc56a795f29d28ef15e13c34a47e223960b0240e8cb0a82d9b0738695333','puc_build':'gcc 15.2.0 -O2 -Wall -fPIC -DLUA_USE_POSIX -DLUA_USE_DLOPEN; default double; non-JIT','puc_library_sha256':hashlib.sha256((reference/'liblua.a').read_bytes()).hexdigest(),'timing':'B.Loop wall time; cached function protected calls; setup/compilation/warmup/final validation excluded; embedding API result consumption is timed; PUC has one cgo transition per operation','runtimes':{}}
for lane,repo in [('baseline',root/'baseline'),('lunar',root/'repo')]:
 assert not subprocess.check_output(['git','status','--porcelain'],cwd=repo,text=True)
 for cmd in [['go','test','-tags','puc51','./...'],['go','vet','-tags','puc51','./...']]: subprocess.run(cmd,cwd=repo/'benchmarks',env=env,check=True)
 binary=root/(stage+'-'+lane+'.test')
 subprocess.run(['go','test','-tags','puc51','-c','-o',str(binary)],cwd=repo/'benchmarks',env=env,check=True)
 manifest['runtimes'][lane]={'revision':subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip(),'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest()}
for name,module in [('gopherlua','github.com/yuin/gopher-lua'),('golua','github.com/Shopify/go-lua')]:
 manifest['runtimes'][name]={'version':subprocess.check_output(['go','list','-m','-f','{{.Version}}',module],cwd=root/'repo'/'benchmarks',env=env,text=True).strip(),'binary':stage+'-lunar.test'}
(root/(stage+'-manifest.json')).write_text(json.dumps(manifest,indent=2)+'\n')
with (root/(stage+'-comparison.txt')).open('x') as out:
 out.write('# See the matching stage manifest for clean source revisions, binary hashes and collection controls.\n')
 for round in range(1,16):
  order=['baseline','lunar','gopherlua','golua','puc51']
  shift=(round-1)%len(order);order=order[shift:]+order[:shift]
  for lane in order:
   print(f'round {round}/15: {lane}',flush=True)
   binary=root/(stage+'-baseline.test' if lane=='baseline' else stage+'-lunar.test')
   runtime='lunar' if lane=='baseline' else lane
   groups='BenchmarkPrograms' if lane=='puc51' else '(BenchmarkPrograms|BenchmarkInterpreter|BenchmarkEmbedding)'
   out.write(f'# round: {round}; runtime: {lane}\n');out.flush()
   result=subprocess.run(['taskset','-c','2',str(binary),'-test.run','^$','-test.bench',f'^{groups}$/.*$/^runtime={runtime}$','-test.benchtime','500ms','-test.cpu','1','-test.count','1'],cwd=root/'repo'/'benchmarks',env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,check=True).stdout
   rows=[line for line in result.splitlines() if line.startswith('Benchmark')]
   assert len(rows)==(4 if lane=='puc51' else 13),(lane,len(rows),result)
   if lane=='baseline': result=result.replace('/runtime=lunar','/runtime=baseline')
   out.write(result);out.flush()
 out.write('# completed: 15 samples for each of 56 rows (840 samples total)\n')
