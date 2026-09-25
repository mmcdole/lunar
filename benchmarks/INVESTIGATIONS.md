# Performance investigations

Started 2026-09-08 from main `99aacff48447e29a85a043d0b257e3cca12315b8`,
after the dynamic table-access improvement. Prioritize changes that shorten
ordinary Lua execution across complete programs. Instruction counts identify
coverage; only timings establish a benefit.

| Priority | Investigation | PUC comparison and evidence | Main constraint |
| --- | --- | --- | --- |
| 1 — prototype parked | Known numeric operands | PUC 5.5 selects arithmetic instructions for constants and immediates. Lunar repeatedly resolves generic register-or-constant operands even when a numeric constant is known. Earlier complete-program counts found constant operands in 66% of binary-trees arithmetic, 90% of fannkuch, 44% of spectral-norm, and 47%/71% of CBOR load/save. | Preserve Lua 5.1 double arithmetic, coercion, evaluation order, metamethod arguments, and chunk compatibility. More instructions can also enlarge dispatch. |
| 2 — next | Ordinary calls with open results and tail calls | PUC integrates these call shapes into ordinary entry and tail preparation. Lunar's fastest entry covers fixed results; other shapes repeat preparation. Earlier CBOR runs executed millions of eligible calls. | Much setup is required. The four canonical programs have almost no open-result calls and no tail calls, so application coverage matters. |
| 3 | Collection accounting | PUC maintains allocation totals and accounts during collection. Lunar scans surviving objects and deduplicates string backing after reachability collection. An earlier CBOR-save profile attributed 11% of CPU to accounting, including 8% to string deduplication. | Removing a repeated traversal retains the deduplication work. Exact heap limits, weak tables, and finalization must remain correct. |
| 4 | Allocation volume in object construction | Allocation and pointer barriers are substantial in the earlier binary-trees profile. PUC also demonstrates combined closure/upvalue-pointer allocations where Lunar uses separate storage. | Profile constructors before choosing a layout change; closure packaging needs workload evidence of its own. Compact Go layouts must preserve pointer scanning and ownership. |

The frequency and CBOR profile observations above were collected at
`5fc51e449a6661340056544e995aae4fdf89dcb2`, before the latest table change.
They guide investigation, not estimates of the current PUC gap or recoverable
execution time. Profile shares overlap.

PUC sources: [5.5 compiler](https://www.lua.org/source/5.5/lcode.c.html),
[5.5 interpreter](https://www.lua.org/source/5.5/lvm.c.html),
[5.1 call preparation](https://www.lua.org/source/5.1/ldo.c.html), and
[5.1 collector](https://www.lua.org/source/5.1/lgc.c.html).
PUC 5.5 supplies implementation ideas; our timing comparator is unmodified
PUC 5.1.5 with double numbers and no JIT.

## First experiment: numeric constants

Prototype `d8867c86b145dedac2b2332aa1707cf6bd64b9eb` passed correctness and
Lua 5.1 chunk interoperability checks. Two six-sample program/control cohorts
and six-pair CBOR load/save pilots do not support advancing it: no clear
complete-program improvement, slower program times in the repeat, and an
encouraging but variable CBOR-load result. The implementation is parked;
the broader operand-handling question remains open. See the
[pilot summary](results/2026-09-08-linux-amd64-numeric-operands/README.md).

Start with expressions such as `index + 1`, `depth - 1`, and scaling by a
literal. Test whether encoding the known operand location and numeric type
removes enough repeated work to improve whole-program execution. Keep both
operand orders and the existing coercion/metamethod fallback. This differs
from the previously rejected arithmetic-handler split: that experiment did
not remove generic operand selection.

The first prototype covers ADD/SUB/MUL/DIV/MOD with exactly one numeric
constant and one register. Excluding power and two-constant forms narrows the
earlier CBOR coverage to 36% of load arithmetic and 65% of save arithmetic;
the rounded canonical-program shares above are unchanged. It selects internal
instructions when a verified prototype is sealed, so source and loaded chunks
receive the same treatment. Dumping lowers them to ordinary Lua 5.1 instructions.

Use isolated baseline and candidate checkouts under `.bench/numeric-operands/`.
Check compiler, verifier, arithmetic semantics, and Lua 5.1 dump/load behavior
before timing. Compare unchanged complete programs first, including n-body as
a useful control: only 1.75% of its arithmetic had constant operands in the
earlier count. Include the interpreter/embedding controls and CBOR load/save
before considering the change qualified.

Proceed only for a repeatable complete-program benefit with acceptable speed,
allocation, and retained-memory results. A promising pilot is not merge
qualification. Follow the [measurement rules](README.md) for a full comparison;
update the existing root README tables only from a complete reviewed cohort.
Keep PUC timings in investigation evidence.

Store raw samples, binaries, profiles, and experimental scripts in `.bench/`.
Publish concise findings here or in a dated result summary. Do not restore the
supporting-file archives removed during cleanup.
