# Shared existing-array lookup

Candidate `a98a15738f29dbf4684e7ee51213ab8f811673df` adds an early array lookup to Lunar's shared `rawSlot` read function and retains the corresponding VM write path. The comparison base is main `5fc51e449a6661340056544e995aae4fdf89dcb2`. Lunar paths below are relative to the repository root; their line numbers refer to the candidate unless marked otherwise.

## Source rationale

PUC Lua 5.1.5's `luaH_get` checks whether a numeric key roundtrips exactly through an integer, then calls `luaH_getnum` (`src/ltable.c:469–478`). That helper first uses an unsigned range check to return an existing array slot, falling back to hash lookup only outside the array (`:435–447`). `luaH_set` reuses the same lookup for writes (`:494–503`). See the official [Lua 5.1.5 table source](https://www.lua.org/source/5.1/ltable.c.html). The API's `lua_rawget` calls `luaH_get` too (`src/lapi.c:557–563`), confirming shared lookup coverage in the [API source](https://www.lua.org/source/5.1/lapi.c.html). Its VM separately handles nil results and `__index`/`__newindex` (`src/lvm.c:108–150`), as shown in the official [VM source](https://www.lua.org/source/5.1/lvm.c.html).

PUC Lua 5.5.1 exposes the same early array decision in `luaH_fastgeti` and `luaH_fastseti` (`src/ltable.h:49–64`): range-check the integer key, access array storage, otherwise fall back. Its VM selects these paths for tagged integer operands (`src/lvm.c:1317–1328`, `:1368–1382`). Lunar adopts the access ordering while retaining Lua 5.1's double-valued numbers and existing opcodes; modern PUC's integer subtype is not required. Official sources: [table helpers](https://www.lua.org/source/5.5/ltable.h.html), [VM](https://www.lua.org/source/5.5/lvm.c.html).

On main, `rawSlot` always enters full key normalization before reaching the integer/array accessor (`table.go:649–672`), and dynamic VM writes normalize before resolving storage (`execute_table.go:189–215`). The candidate checks existing array membership first in `rawSlot` (`table.go:649–665`) and in the VM write helper (`execute_table.go:189–196`). **Main already avoids numeric hashing for positive exact integer keys** (`table.go:638–640`). The opportunity is shorter classification and access, not elimination of hashing that previously happened on array hits.

## General behavior and safeguards

`existingArrayIndex` (`table.go:1676–1685`) accepts a numeric key only when `uint(index-1) < uint(array.len())` and the integer converts back to the original number exactly. The unsigned predicate admits only indices `1..array.len()`; the exactness test rejects fractions, NaN, infinities and large numbers even if their initial integer conversion returns an implementation-dependent value. The supported array bound, `1<<26` (`table.go:14–17`), keeps every accepted index exactly representable on 32-bit and 64-bit targets. Zero, negative zero, negative keys, numeric-looking strings and all other misses retain the existing fallback and original key. This uses Go's conversion/overflow rules; it does not depend on C behavior for exceptional conversions.

The shared read returns the slot plus its actual non-nil status. VM reads retain existing `__index` handling (`execute_table.go:94–103`), and native `Frame` indexing retains its own metamethod handling (`native_call.go:255–263`). Raw readers still receive nil for a missing field. The VM write shortcut requires a currently non-nil slot and immediately calls `replaceResolvedSlot` (`table.go:734–766`), retaining value representation, pointer updates, deletion accounting and cache invalidation. Nil-slot insertion and `__newindex` behavior remain in the established write path. `SELF` still captures its receiver/key in the existing order (`execute_table.go:75–85`).

The read placement covers dynamic VM indexing, Lua `rawget` (`library_base.go:483–493`), host `Table.RawGet` (`table.go:224–240`), and the initial raw probe in native `Frame` indexing. It applies to any supported array size and any value kind, with no caches or workload thresholds. Integer-specific APIs and helpers already using `rawIntSlot`, including `ipairs` and native table concatenation, do not automatically gain from this change.

Fannkuch's copies/swaps, spectralnorm's vectors, binarytrees' reference-valued children and nbody's body indexing provide distinct complete-program uses. CBOR load/save and embedding controls expose raw/native reads, dynamic string-map access, array growth and misses. Most CBOR output-array writes insert new elements; an existing-non-nil write shortcut should not receive credit for those insertions.

## Qualification limits

Sharing the read path broadens raw-reader coverage while keeping VM reads behind the existing `rawSlot` call. It also adds a failed numeric guard to general non-array lookups. The resulting tradeoff must be measured across complete programs and embedding workloads.

This placement does not establish that earlier callback regressions are fixed. The scalar callback benchmark's timed Lua loop calls `host_add` and performs numeric accumulation (`benchmarks/embedding_test.go:19–26`), without an existing-array lookup to accelerate. Prior variants' gains or regressions are not results for this revision. No timing, implementation edits or benchmark execution was performed for this rationale.

## Exact PUC provenance

Line references above use files extracted from these official source archives. The archive hashes pin the reviewed patch releases even if online source viewers later change:

| Release | Official source archive | SHA-256 |
| --- | --- | --- |
| Lua 5.1.5 | [lua-5.1.5.tar.gz](https://www.lua.org/ftp/lua-5.1.5.tar.gz) | `2640fc56a795f29d28ef15e13c34a47e223960b0240e8cb0a82d9b0738695333` |
| Lua 5.5.1 | [lua-5.5.1.tar.gz](https://www.lua.org/ftp/lua-5.5.1.tar.gz) | `1c4b4068d67061f2a2231ad2b5422e77acea1487ea9890f6320af614f4373dce` |

Both hashes were recomputed from the reviewed local archives. Lua 5.1.5 is the semantic reference; Lua 5.5.1 supplies additional implementation evidence, not an equivalent-version performance comparison.
