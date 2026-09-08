"""Collect three independent retained-memory pairs per CBOR operation."""
import os,subprocess
from pathlib import Path
root=Path('/tmp/lunar-table-fill/cbor')
env=dict(os.environ,LUNAR_CBOR_CANDIDATE='scalar',LUNAR_CBOR_WARMUPS='1')
for mode in ('load','save'):
    print('retained memory:',mode,flush=True)
    subprocess.run(['bash',str(root/'collect-pair.sh'),mode,'retained','3'],env=env,check=True)
    print('completed:',mode,flush=True)
