from pathlib import Path
import argparse, hashlib, json, os, subprocess

parser=argparse.ArgumentParser(description='Serial same-Lunar table-shape memory controls; run only when root schedules them')
parser.add_argument('--runs',type=int,default=15)
parser.add_argument('--cpu',default='2')
parser.add_argument('--output',type=Path,default=Path('/tmp/lunar-table-fill/cbor/shapes'))
parser.add_argument('cases',nargs='*',default=['tables-repeated-16','tables-unique-16','tables-repeated-80','one-unique-16','one-unique-64','one-unique-256','one-unique-1024'])
args=parser.parse_args()
if args.runs<1: raise SystemExit('runs must be positive')
root=Path('/tmp/lunar-table-fill/cbor')
identities={label:json.loads((root/(label+'-manifest.json')).read_text()) for label in ('baseline','candidate')}
for label,manifest in identities.items():
    name=label+'-shapes'
    if hashlib.sha256((root/'bin'/name).read_bytes()).hexdigest()!=manifest['binaries'][name]['sha256']:
        raise SystemExit('shape binary changed: '+label)
args.output.mkdir(exist_ok=False)
env=dict(os.environ,GOMAXPROCS='1',GOGC='100',GOMEMLIMIT='off')
collection={'runs':args.runs,'cases':args.cases,'affinity':args.cpu,'environment':{k:env[k] for k in ('GOMAXPROCS','GOGC','GOMEMLIMIT')},'identities':identities,'completed':False}
manifestpath=args.output/'manifest.json'
manifestpath.write_text(json.dumps(collection,indent=2)+'\n')
for round in range(args.runs):
    order=['baseline','candidate'] if round%2==0 else ['candidate','baseline']
    for case in args.cases:
        for label in order:
            binary=root/'bin'/(label+'-shapes')
            result=json.loads(subprocess.check_output(['taskset','-c',args.cpu,str(binary),'-case',case,'-format','jsonl'],env=env,text=True))
            if result['revision']!=identities[label]['revision'] or result.get('revision_modified',False):
                raise SystemExit('shape worker source identity mismatch')
            result.update(implementation=label,sample_run=round+1,binary_sha256=identities[label]['binaries'][label+'-shapes']['sha256'])
            with (args.output/(case+'-'+label+'.jsonl')).open('a') as output: output.write(json.dumps(result)+'\n')
    print('completed shape round',round+1,flush=True)
collection['completed']=True
manifestpath.write_text(json.dumps(collection,indent=2)+'\n')
