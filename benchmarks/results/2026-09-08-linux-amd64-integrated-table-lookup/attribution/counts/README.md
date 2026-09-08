# Array lookup operation counts

These are diagnostic counts from one complete CBOR load, one complete CBOR save and one canonical fannkuch-redux run at input 8. They are not timing evidence. The instrumented binaries must never be used to qualify performance.

## Results

The existing-value column counts the condition that benefits from the shortcut. On writes, an allocated nil slot still takes the original insertion/metamethod path.

| Operation | Total | Existing nonnil array value | Allocated nil slot | String key | Zero key | Positive integer beyond length |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| CBOR load reads | 1,767,163 | 1,389,940 | 0 | 0 | 377,223 | 0 |
| CBOR load writes | 938,452 | 0 | 6 | 828,692 | 0 | 109,754 |
| CBOR save reads | 2,387,931 | 0 | 0 | 2,387,931 | 0 | 0 |
| CBOR save writes | 2,998,869 | 183,513 | 1,192,102 | 0 | 0 | 1,623,254 |
| Fannkuch reads | 945,059 | 945,059 | 0 | 0 | 0 | 0 |
| Fannkuch writes | 869,481 | 869,457 | 9 | 0 | 0 | 15 |

All remaining primary categories—other nonnumeric keys, negative integers, fractions, NaN and infinities—are zero in these three operations. They remain explicit in the JSON and are covered by the counter validation test.

Every positive integer beyond logical length in this sample is exactly the next index. Of these writes, 73,468 during load, 732,293 during save and nine during fannkuch fit existing reserved capacity. These are lookup-time classifications, not claims that every call allocates backing storage. No observed key exceeds Lunar's maximum array limit. No counted write stores nil.

All counted reads come from `executeRawTableGet`, the VM's dynamic-key path. Counters for VM slow reads, `Frame.Index`, Lua `rawget`, host `RawGet`, and other shared `rawSlot` callers are zero within each scoped operation. Compiler-proven constant-string helpers and table operations that do not execute the changed predicate are outside this count inventory.

Fannkuch exercises successful array reads exclusively and successful array writes 99.9972% of the time. CBOR load has no successful array-write shortcuts: every write continues into the original path. CBOR save sends all dynamic reads and 93.8806% of writes past the shortcut. This establishes a material workload difference and a concrete reason to examine repeated dispatch on misses. It does not assign time cost to those checks, establish the full cause of CBOR's measured regression, or predict the speed of a replacement.

## Scope and validation

The diagnostic source is a private clone of shared candidate `a98a15738f29dbf4684e7ee51213ab8f811673df`. Diagnostic revision is `e7b914117534c138f8e0728a89252873d1dece9b`. Counters observe the actual `existingArrayIndex` result and the slot value before any write. Primary outcomes are mutually exclusive; next-index, reserved-capacity and maximum-array-limit fields are subsets, not extra operations.

CBOR counter scope begins immediately before `loadBenchmarkGraph` or `saveBenchmarkGraph` and ends immediately afterward. State/library setup, file staging, the graph preparation required by save, roundtrip saving after load, structural verification and teardown are excluded. Fannkuch counters surround only the canonical harness's `prepared.run`; compilation, wrapper initialization and result retrieval/verification are excluded. The diagnostic outputs omit elapsed/allocation fields.

Both CBOR runs match the pinned input, codec and workload hashes and the structural oracle: 341 areas, 36,705 rooms, 109,742 exits, 183,513 tables, 938,452 entries, 9,208,046 encoded bytes, digest `dfced0fa169e1abb659ef54220f2395c7c3b5757c1b8ae129b3487c7a489eead`. Fannkuch produces exactly `1616\nPfannkuchen(8) = 22\n`.

Root tests, conformance tests, vet and a focused counter/category/scope test passed on CPU 6. Both diagnostic binaries were built on CPU 6. The three authorized operations ran serially on CPU 2 with `GOMAXPROCS=1`, `GOGC=100`, `GOMEMLIMIT=off`; CPU 2 was released after completion. Validation on CPU 8 checked count partition sums and subset bounds, all source/harness/patch/binary hashes, input hashes, oracles and exact fannkuch output. No optimization was implemented and no timing conclusion is drawn from these binaries.

## Artifacts

- [Exact counts and original records](counts-summary.json).
- [Build/run commands, source and harness hashes, worker hashes, fixture identity and oracle](manifest.json).
- [Diagnostic patch against the shared candidate](instrumentation.patch).
- [Preparation script](prepare.py) and [completed-record validation](validate.py).
- Original [load](cbor-load.json), [save](cbor-save.json) and [fannkuch](fannkuchredux.json) outputs; root, audit and execution logs are preserved alongside them.

Binary SHA-256: CBOR `fc7391067c55966c58930b79fe4ed5370f703f0024b77631e55a922509d6ce5d`; program harness `d2da26733fa51ebf88959597f6b64c06904701f1de021fcce2860a51f64e3175`. Patch SHA-256: `1471af5c52efde7e928b247b11c025e8606a1a795f5365bbf676c80d072e6144`.
