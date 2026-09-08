# Contingent integrated table lookup

One candidate is defensible if the pending operation counts and paired CBOR profiles support repeated lookup preparation as a material concern. Those results were unavailable when this proposal was written. The source difference alone does not explain the shared candidate's 3.75% CBOR load regression.

Compare main `5fc51e449a6661340056544e995aae4fdf89dcb2` with shared candidate `a98a15738f29dbf4684e7ee51213ab8f811673df`. Lunar line references below use main. No implementation, builds or workloads were run for this design.

## The PUC technique

PUC 5.1.5's `luaH_get` dispatches by key type. A numeric key converts to an integer and roundtrips exactly before `luaH_getnum` checks array membership. An integer array miss proceeds directly to numeric hash lookup; a string goes directly to string lookup (`src/ltable.c:435–491`). `luaH_set` retains the resulting slot, including an allocated nil array slot (`:494–503`). [Official source](https://www.lua.org/source/5.1/ltable.c.html).

The transferable technique is to carry the selected key path through lookup. It is not PUC's eager insertion before `__newindex`, nor its C conversion behavior. PUC 5.5.1 also dispatches numeric and string reads/writes directly, but its separate integer subtype and encoded set results are outside this change (`src/ltable.c:1019–1040`, `:1131–1144`). [Modern source](https://www.lua.org/source/5.5/ltable.c.html).

## Proposed implementation

Start from main, not the rejected shortcut. Change only shared `rawSlot` and the classification/lookup portion of `executeRawTableSet`; leave `normalizeTableKey` and its other callers unchanged.

**Reads:** replace `rawSlot`'s normalizer-plus-dispatch sequence (`table.go:649–672`) with one `switch key.kind()`:

```text
nil:       return absent
number:    number = key's float64
           index = int(number)
           exact = float64(index) == number
           if exact and uint(index-1) < uint(array length):
               return array value and its non-nil status
           reject NaN as an absent read
           canonicalize a zero key
           return store.get(key, hashNumber(number))
string:    return store.get(key, existing string hash)
other:     return store.get(key, existing reference hash)
```

After the allocated-array test misses, a read needs no global positive-index classification: the key must be looked up in the record store. Preserve the existing store lookup implementation, including string equality; do not add a separate string-store optimization. This keeps shared VM/native/raw coverage, without an enlarged return value or another helper call.

**Writes:** replace the normalizer call inside `executeRawTableSet` (`execute_table.go:190–200`) with the same type dispatch, retaining its existing local normalized key, index, array-key flag and hash. For numbers, compute the conversion and exactness once:

1. An exact allocated-array index supplies the location and current non-nil status directly. Skip `resolveNormalizedSlot` for this case.
2. On an array miss, preserve main's insertion classification: `arrayKey = exact && index > 0 && number <= 1<<53`. The roundtrip and `1<<53` ceiling together preserve the target's `int` limits; do not treat the roundtrip alone as sufficient for huge 64-bit integers. Compute numeric hashing only for non-array keys, as main does.
3. Reject NaN before metamethod handling and canonicalize zero; strings and other kinds compute their existing hashes. Resolve these remaining paths once with the existing resolver.
4. Join the existing found/absent handling (`execute_table.go:201–216`): a found slot uses `replaceResolvedSlot`; absence checks `__newindex` and uses `rawSetNormalizedSlot` only when allowed.

A single local join for the already-resolved array case can avoid repeating the nil-slot probe without adding a result struct or expanding a return tuple. Declare the existing location/found locals before dispatch; a small explicit jump to the common found/absent handling is sufficient. Do not retain pointers across insertion or metamethod calls.

This reuses an allocated nil slot's lookup result, not an insertion reservation. Existing insertion code may still inspect that slot, compact tombstones or grow storage. Hash misses retain the existing live-lookup/retained-record lookup behavior; eliminating those probes is a different experiment.

## Hazards and scope

- Accepted array indices remain within `1..array.len()`, capped at `1<<26`. Go's implementation-dependent result for an out-of-range float-to-int conversion is harmless only with the exact roundtrip: any accepted result must represent the original number exactly. Preserve the `1<<53` positive-index ceiling on write misses, including on 32-bit targets.
- Fractions, NaN, infinities, negative/zero keys, numeric strings and huge exact integers must retain main's behavior. Normalize key zero, never the stored value's signed zero.
- An allocated nil slot is absent for `__index` and `__newindex`. Do not call `replaceResolvedSlot` on it or bypass `setInteger` insertion policy. Existing non-nil replacement/deletion retains occupancy, pointer writes, weak-reference behavior and cache invalidation.
- Keep opcode operand capture, `SELF` aliasing, slow metamethod dispatch, constant-string helpers and host boundary import logic unchanged. Shared raw reads still include native callers; raw writes outside the VM retain main's normalizer.

Estimated scope is two runtime functions, roughly 60–90 added or rewritten lines, with short duplicated type dispatch and no new storage/API/cache state. That is larger than the prior shortcut but still bounded. Compiler diagnostics must check frames, escapes and emitted code; fewer source steps do not guarantee fewer instructions or spills. Main already skips hashing positive integer array keys.

Proceed only if evidence supports this scope. Permit one candidate, then existing semantic tests and paired complete-program, CBOR and embedding measurements. Stop if attribution remains unclear, implementation expands into storage redesign, or a reproducible material regression remains; continue with numeric operand handling instead.
