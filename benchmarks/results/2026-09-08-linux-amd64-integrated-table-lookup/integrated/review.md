# Integrated lookup review

Reviewed clean candidate `22ad3f1e7b751031360705eea9338b5f0428a1e5` against main `5fc51e449a6661340056544e995aae4fdf89dcb2`. **No correctness defect found in the two runtime changes.** This is source review, not performance qualification. Lunar line references use the candidate.

## Why this experiment is supported

The diagnostic load has 938,452 dynamic writes and no existing-non-nil array shortcut hits: 828,692 string keys, 109,754 next-index misses and six allocated nil slots. Its dynamic reads include 1,389,940 array values and 377,223 zero-key misses. Save has 2,387,931 dynamic string reads, plus 1,192,102 nil-slot and 1,623,254 next-index writes. Fannkuch overwhelmingly exercises existing array values. This confirms distinct hit and fallback workloads; it does not measure each category's cost.

The five-load CPU profiles shift `executeRawTableSet` from 230 to 340 ms cumulative and `rawSlot` from 50 to 80 ms, while other unchanged functions shift too. Neither those samples nor the counters isolate the previous 3.75% elapsed-time regression. The integrated candidate tests the net value of retaining classification through lookup.

## Numeric conversion proof

The early array predicates (`table.go:658`; `execute_table.go:205`) are portable for Lunar's supported array bound of `1<<26`. `uint(index-1) < uint(array.len())` admits only signed indices `1..array.len()` on both 32-bit and 64-bit Go targets. Every admitted index is exactly representable as float64. The roundtrip therefore rejects fractions, infinities, NaN and huge keys even when their initial conversion produces an implementation-dependent integer. Signed overflow in `index-1` is defined in Go; it does not invalidate this range test.

The write fallback predicate (`execute_table.go:219`) is equivalent to main's `positiveIntegerIndex`:

- On 32-bit targets, every possible `int` is exactly representable as float64. An out-of-range number cannot equal the roundtrip of any result; positive representable integers pass.
- On 64-bit targets, any positive input at most `1<<53` lies within the integer range, so conversion truncates normally and the roundtrip distinguishes integers from fractions. Larger positive inputs fail the explicit ceiling. Nonpositive inputs cannot equal the conversion back of a positive integer.

The `1<<53` ceiling matters: a roundtrip by itself is insufficient to classify huge integers because converting an int64 back to float64 may round. The implementation retains that ceiling. NaN exits before write/metamethod processing; zero key bits are canonicalized. Stored value bits are untouched. These conclusions follow the Go specification's numeric-conversion and integer-overflow rules, inspected in the installed `doc/go_spec.html`.

## Lookup and mutation preservation

`rawSlot` (`table.go:649–673`) now sends numeric array misses directly to the record store. That matches main: a numeric key outside the allocated array must be resolved there. Exact positive integers previously reconstructed by `rawIntSlot` have the same numeric key and hash; zero still canonicalizes; NaN still reads absent. String and reference lookup continue through the same `store.get` implementation. Main already avoided hashing on successful integer array access.

The VM write join (`execute_table.go:205–250`) carries a known array location and its actual non-nil status. An allocated nil slot joins the original absence path; it is never passed to `replaceResolvedSlot`. The existing `__newindex` check and `rawSetNormalizedSlot` preserve insertion policy, compaction, growth and occupancy. A non-nil slot uses the same replacement/deletion routine as main, preserving pointer writes, weak-reference behavior and cache invalidation. A non-array key retains the existing resolver and normalized insertion arguments. Its unused numeric `index` may differ when `arrayKey=false`; these callees do not use that index in that case.

No location or backing pointer survives a metamethod call or insertion. Read metamethod handling, invalid-key error routing, opcode operand capture, `SELF` aliasing, constant-string helpers and host boundary import logic remain unchanged. Existing normalizer callers outside these two paths remain unchanged.

## Validation limits

The added tests exercise mixed numeric/string keys, nil metamethods, value-kind changes, deletion/weak references and boundaries around `2^31`, `2^53` and `2^63`. Root reports root tests, conformance and vet passed; this reviewer has not rerun them. Source proof covers both int widths; an amd64 execution alone does not establish 32-bit test coverage. Compiler/frame review is recorded separately when complete. No runtime edits or workload execution were made for this review.
