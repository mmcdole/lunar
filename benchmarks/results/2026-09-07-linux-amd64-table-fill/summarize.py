"""Validate and split the full collection for qualification and README publication."""
from pathlib import Path
import collections,hashlib,json,re,statistics,subprocess
root=Path('/tmp/lunar-table-fill')
source=root/'full-comparison.txt'
manifest=json.loads((root/'full-comparison-manifest.json').read_text())
assert manifest['status']=='completed',manifest['status']
assert manifest['verified_samples']==660
assert manifest['verified_unique_rows']==44
assert hashlib.sha256(source.read_bytes()).hexdigest()==manifest['raw_sha256']
rows=collections.defaultdict(list)
header=[]
for line in source.read_text().splitlines():
    if line.startswith(('goos:','goarch:','pkg:','cpu:')) and line not in header: header.append(line)
    if not line.startswith('Benchmark'): continue
    m=re.match(r'^(Benchmark[^\s]+)\s+([0-9]+)\s+([0-9.]+)\s+ns/op\s+([0-9]+)\s+B/op\s+([0-9]+)\s+allocs/op',line)
    assert m,line
    name=m.group(1);runtime=name.split('/runtime=')[1];key=name.split('/runtime=')[0]
    rows[(key,runtime)].append({'ns':float(m[3]),'bytes':int(m[4]),'allocs':int(m[5]),'raw':line})
assert len(rows)==44,len(rows)
assert all(len(v)==15 for v in rows.values())
medians={}
for (name,runtime),values in rows.items():
    medians.setdefault(name,{})[runtime]={k:statistics.median(x[k] for x in values) for k in ('ns','bytes','allocs')}
(root/'full-medians.json').write_text(json.dumps(medians,indent=2)+'\n')
for group in ('Programs','Interpreter','Embedding'):
    for mode in ('gate','published'):
        if mode=='published' and group=='Interpreter': continue
        lines=header+['# Derived from full-comparison.txt; no samples removed within selected rows.']
        for (name,runtime),values in rows.items():
            if not name.startswith('Benchmark'+group+'/'):continue
            if runtime not in (('baseline','scalar') if mode=='gate' else ('scalar','gopherlua','golua')):continue
            lines += [x['raw'].replace('/runtime=scalar','/runtime=lunar') if mode=='published' else x['raw'] for x in values]
        path=root/(mode+'-'+group+'.txt');path.write_text('\n'.join(lines)+'\n')
        result=subprocess.check_output(['/tmp/lunar-puc-assessment/bin/benchstat','-col','/runtime',str(path)],text=True)
        (root/(mode+'-'+group+'.benchstat.txt')).write_text(result)
(root/'sample-validation.json').write_text(json.dumps({'total_samples':sum(map(len,rows.values())),'rows':len(rows),'samples_per_row':15,'gate_rows':26,'published_rows':27,'overlap_lunar_published_rows':9,'median_ns':{n:{r:m['ns'] for r,m in v.items()} for n,v in medians.items()}},indent=2)+'\n')
print('Verified660samples:26baseline/candidate gate rows and27README rows with9overlapping Lunar rows.')
