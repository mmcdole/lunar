# Array-access fallback review

The shared candidate `a98a15738f29dbf4684e7ee51213ab8f811673df` improves fannkuch and spectralnorm in the completed comparison, but fails qualification: CBOR load is **3.75% slower**, with a reported interval of **2.97% to 4.67% slower**. The source establishes added fallback work; it does not establish how much of that regression the work causes. No new measurements, builds or runtime changes were made for this review.

Lunar references below use candidate line numbers unless marked main. The comparison base is `5fc51e449a6661340056544e995aae4fdf89dcb2`; paths are relative to the repository root.

## What the candidate adds

Main starts dynamic reads and writes with `normalizeTableKey`, which classifies the key and preserves the result for storage lookup (`table.go:611–665`; `execute_table.go:189–215`, main). The candidate first attempts `existingArrayIndex`, then restarts that unchanged normalization on failure (`table.go:649–665`, `:1676–1685`; `execute_table.go:190–208`). The failed attempt does not pass its classification or conversion result onward.

| Operation reaching the changed helper | Additional source-level work before the original path |
| --- | --- |
| Dynamic string, boolean, reference or nil key | An `isNumber` test and failed early branch; the subsequent normalizer still classifies the key. |
| Numeric key outside the allocated array, including zero, negative keys and growth beyond its current length | Float-to-integer conversion and unsigned array-range test; normalization then performs its original numeric checks and conversion when applicable. |
| Fractional numeric key whose converted integer is within array range | Those operations plus the failed exact roundtrip, followed by normal normalization. |
| Write to an allocated but nil array slot | Successful numeric guard and a nil-slot read, followed by normalization and `resolveNormalizedSlot`, which reads that slot again. The insertion/metamethod path remains necessary. |

Array reads that find nil return through the shortcut with `found=false`; they retain their caller's existing metamethod handling. Existing non-nil writes use `replaceResolvedSlot`, preserving deletion and weak-reference accounting. Constant-string VM helpers and integer-specific `rawIntSlot` callers do not enter this new guard. Shared `rawSlot` does broaden exposure to native/raw readers. None of these changes eliminates hashing on existing array hits: main already avoided that hashing.

## What PUC actually does

PUC 5.1.5 selects the numeric path inside `luaH_get`'s type switch (`src/ltable.c:469–491`). Exact integers enter `luaH_getnum`, whose unsigned array-range miss continues directly to numeric hash lookup (`:435–447`). Strings go directly to `luaH_getstr` (`:472`), without first attempting numeric conversion. Thus its array decision belongs to the selected numeric lookup path, rather than a speculative prepass before a complete restart. `luaH_set` reuses this lookup and retains an existing slot, including a nil array slot (`:494–503`). [Official source](https://www.lua.org/source/5.1/ltable.c.html).

This is not a claim that PUC never repeats classification: generic hashing dispatches again, and insertion after rehash can call `luaH_set` again (`:399–406`). Nor should Lunar copy PUC 5.1's eager insertion before `__newindex` wholesale (`src/lvm.c:134–150`). PUC 5.5.1 likewise dispatches reads and writes by key type, with integer/float-to-integer branches and dedicated string branches (`src/ltable.c:1019–1040`, `:1131–1144`). Its separate integer subtype and encoded set results require different machinery. [Modern table source](https://www.lua.org/source/5.5/ltable.c.html).

## The next question

**Which failed shortcut categories account for CBOR load's extra work, and does their measured cost explain the regression?** The workload supplies concrete suspects: `decoder[typ]` includes the permanently non-array key zero; decoded arrays insert new elements; decoded maps use dynamic keys (`benchmarks/cbor/testdata/cbor.lua:333`, `:367–399`, `:514–521`). Their source presence establishes relevance, not their dynamic frequency or cost.

The justified next evidence is paired main/candidate profiles plus a separate diagnostic count of reads versus writes, nonnumeric misses, numeric range/exactness misses, nil-slot writes and successful hits, distinguishing VM and native callers. Keep diagnostic instrumentation outside qualification timings. If repeated classification is material, PUC suggests integrating array access into type-directed lookup while preserving useful classification on misses. That is a hypothesis to evaluate after attribution, not grounds for another placement variant now. Guard cost, instruction layout and unrelated runtime costs remain unproven explanations for the full 3.75% slowdown.
