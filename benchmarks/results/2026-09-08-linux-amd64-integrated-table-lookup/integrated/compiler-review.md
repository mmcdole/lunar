# Integrated lookup compiler review

The integrated candidate increases the dynamic setter's stack reservation from **120 to 128 bytes** and shared `rawSlot` from **40 to 48 bytes**. It also removes the separate bounds comparison for the direct array access in both paths. These are Go 1.26.0 linux/amd64 compiler facts, not evidence that either change improves or harms elapsed time.

Reviewed clean main `5fc51e449a6661340056544e995aae4fdf89dcb2` and candidate `22ad3f1e7b751031360705eea9338b5f0428a1e5`. Existing benchmark binaries were inspected without rebuilding or executing them:

| Binary | SHA-256 |
| --- | --- |
| `/tmp/lunar-table-fill/baseline.test` | `9753e1de4e8e3971adf7fd66de55d6254328958859af4569bd3585e71905b74a` |
| `candidate.test` | `948fd90c9326f54056196dd908d531db2871278dac46a31f82e7cf5c2e86505e` |

## Frames and code

| Function | Text bytes, main → candidate | Decoded instructions | Stack reservation |
| --- | ---: | ---: | ---: |
| `rawSlot` | 135 → 539 | 38 → 144 | 40 → 48 B |
| `executeRawTableSet` | 652 → 1132 | 165 → 300 | 120 → 128 B |
| `executeRawTableGet` | 1068 → 1068 | 274 → 274 | 40 → 40 B |
| `executeRawStringTableGet` | 1077 → 1077 | 280 → 280 | 48 → 48 B |
| `executeRawStringTableSet` | 880 → 880 | 227 → 227 | 112 → 112 B |
| `runInstructions` | 6885 → 6885 | 1561 → 1561 | 192 → 192 B |
| `tryEnterFixedNativeCall` | 453 → 453 | 125 → 125 | 72 → 72 B |
| `invokeNativeCall` | 1209 → 1209 | 244 → 244 | 216 → 216 B |
| `finishNativeCall` | 701 → 701 | 166 → 166 | 96 → 96 B |

Reservations exclude the pushed frame pointer. The setter uses `ADDQ $-0x80, SP`; this is a 128-byte reservation, not zero. Its larger frame also changes the guard prologue from a direct stack-pointer comparison to `LEAQ -0x8(SP), R12` followed by comparison. Thus inline classification has a concrete frame/prologue cost even though the public function signature and result tuple did not expand.

Whole-function instruction counts include fallback and panic paths. Main delegates classification to other functions, so these counts are not executed-operation counts or a comparison of total lookup cost. The seven unchanged functions in the table have matching normalized instruction/register/branch shapes. Literal bytes differ with relocated targets and static data references; they are not byte-identical binaries.

## Inlining, bounds and escapes

`rawSlot` remains non-inlined: compiler cost 156 on main, 498 in the candidate, versus budget 80. Both VM helpers remain explicitly `go:noinline`. The candidate no longer calls `normalizeTableKey` from these changed paths.

In the read array branch, the unsigned bound at `0x62adab` and exactness check at `0x62adb8` lead directly to the address computation and slot loads. In the write branch, the exactness check at `0x5c0903` and unsigned bound at `0x5c0913` lead directly to the nil-slot read. There is no second `tableVector.at` bounds comparison on either branch. This is narrower than claiming all bounds checks disappeared: operand checks and other storage paths remain.

The write fallback re-emits an exactness `UCOMISD` at `0x5c0967`, despite the source-level `exact` local. It reuses the original converted number in registers; there is no second float-to-int conversion in that function. Source reuse therefore does not imply every machine comparison is reused.

Escape diagnostics report no new local moved to the heap in the two changed functions. `rawSlot`'s key still does not escape and the table content still flows to its returned slot. The setter's values/function content still escapes through stored values, as on main. Panic-message escape diagnostics are not allocations on valid array accesses. These compiler observations do not replace allocation measurements.

## Reproduction and artifacts

Diagnostic package builds ran on CPU 8, with each source checkout as the working directory:

```sh
taskset -c 8 env GOCACHE=/tmp/lunar-puc-assessment/go-cache \
  go build -gcflags=-m=2 . > compiler.txt 2>&1
```

Outputs are `baseline-compiler.txt` and `compiler.txt` in this artifact directory. The installation does not ship `go tool nm` or `go tool objdump`; the successful disassembly commands used the same tool sources through `go run cmd/nm -size` and `go run cmd/objdump -s`, on CPU 8 with the same cache. Outputs are `baseline-symbols.txt`, `candidate-symbols.txt` and the corresponding `*-disassembly.txt` files. `compare-disassembly.py` records frame reservations, byte hashes, instruction counts and normalized shapes in `disassembly-comparison.json` and `disassembly-summary.txt`. No runtime edits, benchmark/profile execution or commits were performed.
