import bisect
import hashlib
import json
import re
import struct
from pathlib import Path

ROOT = Path('/tmp/lunar-array-followup/integrated')

class ELF:
    def __init__(self, path):
        self.data = path.read_bytes()
        header = struct.unpack_from('<16sHHIQQQIHHHHHH', self.data)
        assert header[0][:6] == b'\x7fELF\x02\x01'
        self.segments = []
        for i in range(header[10]):
            fields = struct.unpack_from('<IIQQQQQQ', self.data, header[5] + i * header[9])
            if fields[0] == 1:
                self.segments.append((fields[3], fields[2], fields[5]))

    def read(self, address, size):
        for start, offset, length in self.segments:
            if start <= address and address + size <= start + length:
                return self.data[offset + address - start:offset + address - start + size]
        raise ValueError(f'No file backing for {address:#x}+{size}')

    def static_string(self, symbol, symbols):
        entry = next((s for s in symbols if s[2] == symbol), None)
        if entry is None or entry[1] != 16:
            raise ValueError(f'Not a 16-byte static string: {symbol}')
        pointer, length = struct.unpack('<QQ', self.read(entry[0], 16))
        assert length < 10000
        return self.read(pointer, length).decode('utf-8')

def read_symbols(path):
    out = []
    for line in path.read_text().splitlines():
        fields = line.split(None, 3)
        if len(fields) == 4:
            try:
                out.append((int(fields[0], 16), int(fields[1]), fields[3]))
            except ValueError:
                pass
    return sorted(out)

def read_dump(path):
    out = {}
    current = None
    for line in path.read_text().splitlines():
        if line.startswith('TEXT '):
            current = line.split()[1].removesuffix('(SB)')
            out[current] = []
        elif current:
            match = re.match(r'\s+\S+\s+(0x[0-9a-f]+)\s+([0-9a-f]+)\s+(.*)', line)
            if match:
                out[current].append((int(match[1], 16), bytes.fromhex(match[2]), match[3].strip()))
    return out

def normalize(rows, symbols, elf):
    start = rows[0][0]
    addresses = [s[0] for s in symbols]
    normalized = []
    references = []
    for pc, machine, text in rows:
        # Instruction-address branches are compared as offsets within the function.
        text = re.sub(r'^(J\w+|LOOP\w*) (0x[0-9a-f]+)$',
                      lambda m: f'{m[1]} function+{int(m[2], 16) - start:#x}', text)
        def reference(match):
            displacement = int(match[1], 16)
            target = pc + len(machine) + displacement
            index = bisect.bisect_right(addresses, target) - 1
            name = None
            offset = None
            if index >= 0:
                address, size, symbol = symbols[index]
                if target < address + size:
                    name, offset = symbol, target - address
            references.append({'instruction_offset': pc - start, 'target': hex(target),
                               'symbol': name, 'symbol_offset': offset})
            return 'RELOC(IP)'
        text = re.sub(r'(-?0x[0-9a-f]+)\(IP\)', reference, text)
        text = re.sub(r'(github\.com/mmcdole/lunar\.\.stmp_\d+)\(SB\)',
                      lambda m: 'static_string:' + repr(elf.static_string(m[1], symbols)), text)
        normalized.append(text)
    return normalized, references

def frame_size(rows):
    for _, _, text in rows[:12]:
        match = re.fullmatch(r'SUBQ \$(0x[0-9a-f]+), SP', text)
        if match:
            return int(match[1], 16)
        match = re.fullmatch(r'ADDQ \$-(0x[0-9a-f]+), SP', text)
        if match:
            return int(match[1], 16)
    return 0

baseline = read_dump(ROOT / 'baseline-disassembly.txt')
candidate = read_dump(ROOT / 'candidate-disassembly.txt')
symbols = {label: read_symbols(ROOT / f'{label}-symbols.txt') for label in ('baseline', 'candidate')}
elf = {'baseline': ELF(Path('/tmp/lunar-table-fill/baseline.test')),
       'candidate': ELF(ROOT / 'candidate.test')}
result = {}
for function in baseline.keys() & candidate.keys():
    left, right = baseline[function], candidate[function]
    ln, lr = normalize(left, symbols['baseline'], elf['baseline'])
    rn, rr = normalize(right, symbols['candidate'], elf['candidate'])
    lb, rb = b''.join(row[1] for row in left), b''.join(row[1] for row in right)
    result[function] = {
        'baseline_address': hex(left[0][0]), 'candidate_address': hex(right[0][0]),
        'baseline_decoded_bytes': len(lb), 'candidate_decoded_bytes': len(rb),
        'baseline_instructions': len(left), 'candidate_instructions': len(right),
        'baseline_stack_subtraction': frame_size(left), 'candidate_stack_subtraction': frame_size(right),
        'literal_code_bytes_identical': lb == rb,
        'normalized_instruction_shape_identical': ln == rn,
        'baseline_code_sha256': hashlib.sha256(lb).hexdigest(),
        'candidate_code_sha256': hashlib.sha256(rb).hexdigest(),
        'baseline_unnamed_rip_references': lr, 'candidate_unnamed_rip_references': rr,
        'normalized_instruction_differences': [
            {'instruction': i, 'baseline': l, 'candidate': r}
            for i, (l, r) in enumerate(zip(ln, rn)) if l != r
        ][:12],
    }
(ROOT / 'disassembly-comparison.json').write_text(json.dumps(result, indent=2, sort_keys=True) + '\n')
for function, record in sorted(result.items()):
    print(function, json.dumps({k: v for k, v in record.items()
          if not k.endswith('_sha256') and 'references' not in k and 'differences' not in k}))
