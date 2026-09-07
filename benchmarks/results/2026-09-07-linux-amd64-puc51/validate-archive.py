"""Check completeness and recorded patch hashes; run beside this archive."""
from collections import Counter
import hashlib
import json
from pathlib import Path

root = Path(__file__).resolve().parent
expected = {
    'comparison.txt': 38,
    'small_fixes-broad.txt': 16,
    'arithmetic-fields-native_entry-broad.txt': 32,
    'combined_hot_paths-stack_clear-all_table-broad.txt': 32,
}
summary = {}
for name, keys in expected.items():
    text = (root / name).read_text()
    assert text.splitlines()[-1].startswith('# completed:'), name
    counts = Counter(line.split()[0] for line in text.splitlines()
                     if line.startswith('Benchmark'))
    assert len(counts) == keys, (name, len(counts), keys)
    assert set(counts.values()) == {15}, (name, counts)
    summary[name] = {'rows': keys, 'samples_per_row': 15,
                     'total_samples': sum(counts.values())}
for path in root.glob('*-manifest.json'):
    for variant in json.loads(path.read_text()).values():
        for name, expected_hash in variant.get('patches', {}).items():
            actual = hashlib.sha256((root / 'patches' / name).read_bytes()).hexdigest()
            assert actual == expected_hash, (path.name, name)
print(json.dumps(summary, indent=2))
