import datetime,hashlib,json,os,platform,shutil,subprocess,tempfile,time
from pathlib import Path
ROOT=Path('/tmp/lunar-array-followup/profiles')
CONFIG=json.loads(Path('/tmp/lunar-array-access/shared/cbor/inputs.json').read_text())
CAND=json.loads(Path('/tmp/lunar-array-access/shared/cbor/candidate-build.json').read_text())
ENV=dict(os.environ,GOMAXPROCS='1',GOGC='100',GOMEMLIMIT='off')
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def git(p,*args):return subprocess.check_output(['git','-C',str(p),*args],text=True).strip()
lanes={'baseline':{'binary':CONFIG['baseline_worker'],'sha256':CONFIG['files'][CONFIG['baseline_worker']]['sha256'],'revision':CONFIG['baseline_revision'],'source':'/tmp/lunar-table-fill/baseline'},'candidate':{'binary':CAND['binary']['path'],'sha256':CAND['binary']['sha256'],'revision':CAND['source']['revision'],'source':CAND['source']['path']}}
for source in lanes.values():
 assert sha(source['binary'])==source['sha256']
 assert git(source['source'],'rev-parse','HEAD')==source['revision'] and not git(source['source'],'status','--porcelain')
for p,x in CONFIG['files'].items():assert sha(p)==x['sha256'],p
manifest={'status':'running','purpose':'Five paired operation-only CPU profiles; attribution, not speed qualification. Sampling granularity 10ms. Preparation, output validation and post-load roundtrip excluded.','started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'lanes':lanes,'collector_sha256':sha(__file__),'environment':{k:ENV[k] for k in ('GOMAXPROCS','GOGC','GOMEMLIMIT')},'cpu_affinity':2,'platform':platform.platform(),'pairs':5,'runs':[]}
mp=ROOT/'manifest.json';assert not mp.exists()
def save():mp.write_text(json.dumps(manifest,indent=2)+'\n')
save()
try:
 with tempfile.TemporaryDirectory(prefix='lunar-profile-workers-',dir='/tmp') as temporary:
  staged={}
  for label,source in lanes.items():
   p=Path(temporary)/label;shutil.copyfile(source['binary'],p);p.chmod(0o500);assert sha(p)==source['sha256'];staged[label]=str(p)
  for pair in range(1,6):
   for label in (('baseline','candidate') if pair%2 else ('candidate','baseline')):
    prefix=ROOT/(label+'-'+str(pair)); prof=Path(str(prefix)+'.cpu.pprof');output=Path(str(prefix)+'.jsonl');log=Path(str(prefix)+'.stderr.log')
    command=['taskset','-c','2',staged[label],'-preset','large','-mode','load','-measurement','profile','-cpu-profile',str(prof),'-fixture',CONFIG['fixture'],'-data',CONFIG['data'],'-format','jsonl']
    started=time.time()
    with output.open('x') as out,log.open('x') as err:
     result=subprocess.run(command,env=ENV,stdout=out,stderr=err,timeout=900)
    run={'pair':pair,'lane':label,'command':command,'exit_code':result.returncode,'wall_seconds':time.time()-started,'output_sha256':sha(output),'stderr_sha256':sha(log)}
    manifest['runs'].append(run);save();assert result.returncode==0
    rows=[json.loads(line) for line in output.read_text().splitlines() if line.strip()];assert len(rows)==1
    row=rows[0]
    for key,expected in {'revision':lanes[label]['revision'],'measurement':'profile','mode':'load','preset':'large','execution':'raw','context_check_interval':0,'oracle':CONFIG['expected_oracle'],'go_version':'go1.26.0','input_sha256':CONFIG['files'][CONFIG['data']]['sha256'],'codec_sha256':CONFIG['files'][str(Path(CONFIG['fixture'])/'cbor.lua')]['sha256'],'workload_sha256':CONFIG['files'][str(Path(CONFIG['fixture'])/'workload.lua')]['sha256']}.items():assert row.get(key)==expected,(key,row)
    assert not row.get('revision_modified',False)
    run['profile_sha256']=sha(prof);run['oracle_passed']=True;save()
   print('profile pair',pair,'of 5 complete',flush=True)
 for source in lanes.values():
  assert sha(source['binary'])==source['sha256']
  assert git(source['source'],'rev-parse','HEAD')==source['revision'] and not git(source['source'],'status','--porcelain')
 manifest['status']='complete'
except BaseException as e:
 manifest['status']='failed';manifest['error']=repr(e);raise
finally:
 manifest['finished_at']=datetime.datetime.now(datetime.timezone.utc).isoformat();save()
