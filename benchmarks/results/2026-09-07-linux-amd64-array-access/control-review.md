# Array-access controls and shared-read variant

The callback slowdown remains an observed tradeoff. Its timed loop does not execute the changed numeric lookup predicate, and source review does not establish the cause. Moving the read shortcut into the shared lookup broadens its coverage; it does not remove an operation executed by the callback loop. The completed full comparison and CBOR measurements must decide whether the final change is worth that tradeoff.

This review is read-only for runtime source and binaries. No benchmark or workload was executed. Statistics were computed from completed collector output on CPU 8.

## Recorded measurements

All comparisons below use main `5fc51e449a6661340056544e995aae4fdf89dcb2` as baseline. A positive timing change is slower. Five-sample pilots do not provide the default benchstat 95% median confidence interval; their p-values are not evidence of equivalence when greater than 0.05.

| Collection | Candidate | Samples per row | Callback baseline → candidate | Change | p |
| --- | --- | ---: | --- | ---: | ---: |
| Initial full, 44 rows | `cd44f54` | 15 | 50.97 → 51.65 µs | +1.34% | <0.001 |
| Initial focused repeat, 6 rows | `cd44f54` | 15 | 50.54 → 51.18 µs | +1.26% | <0.001 |
| Unsigned pilot, 26 rows | `c013ee6` | 5 | 50.888 → 51.387 µs | +0.98% | 0.008 |
| Shared-read pilot, 26 rows | `a98a157` | 5 | 51.505 → 51.988 µs | +0.94% | 0.056 |

The shared pilot is inconclusive at the default significance threshold, with the same slower direction as the three preceding collections. It cannot be described as a demonstrated fix or a passed no-regression gate. Callback allocation traffic remains zero bytes and zero allocations in every lane of these runs.

N-body is less consistent: initial full +1.52% (p<0.001), the independent initial-candidate repeat −0.84% (p<0.001), unsigned pilot −1.15% (p=0.151), shared pilot +0.80% (p=0.548). These are distinct measurement cohorts and must remain separate; selecting one sign or pooling different candidates would hide the uncertainty.

The shared pilot retains whole-program gains: binary-trees −2.38%, fannkuch-redux −21.56%, and spectral-norm −4.76% (each p=0.008, n=5). Its reused-table embedding row improves 13.89%. Numeric-loop, fixed-Lua-call, constant-field, and string-append controls show no significant difference in that pilot. These observations justify completing the full comparison, not declaring the final qualification finished.

Evidence files are the initial `full-{programs,embedding,interpreter}.benchstat.txt`, `focused-repeat-{programs,embedding}.benchstat.txt`, `unsigned/gate-pilot.txt`, and this directory's `gate-pilot-{programs,embedding,interpreter}.benchstat.txt`. The full initial collection was independently validated as 60 processes, 44 rows and 660 samples. Both completed pilots were independently validated as 10 processes, 26 rows and 130 samples, including raw and restored per-process output hashes, exact case inventory, rotated order, manifest counts, source/build agreement and identical harness hashes.

## Callback source trace

References below are relative to this directory's `candidate` checkout, revision `a98a15738f29dbf4684e7ee51213ab8f811673df`.

- `benchmarks/embedding_test.go:19` defines the Lua loop: `total = total + host_add(index, 2)`, repeated 1,000 times. It contains no numeric table access or table assignment.
- `benchmarks/embedding_test.go:272` registers a native function that reads two numeric arguments and returns their sum. Registration, global installation, loading, warmup and the explicit Go collection all precede `b.Loop` (`:283`, `:286`, `:300`, `:305`, `:307`). The timed invocation uses `State.CallInto` (`:293`) and consumes its numeric result (`:297`).
- `execute.go:476` sends `GETGLOBAL` to `executeRawStringTableGet`. Its `GETGLOBAL` arm loads the function environment and constant string (`execute_table.go:121`), then calls `rawStringKeySlot` (`:139`). That function directly calls `store.getStringSlot` (`table.go:679`). This route never calls `rawSlot` or `existingArrayIndex`.
- `Frame.Number` reads an argument slot and converts its numeric bits (`native.go:177`). `Frame.ReturnNumber` publishes a numeric result (`native.go:440`). Neither performs table indexing. The `rawSlot` use elsewhere in `native_call.go:257` belongs to `Frame.Index`; the benchmark callback does not call that API.
- The call driver enters the existing fixed native-call path (`execute.go:204`); this variant has no source changes to the execution driver, native arguments, or native return handling.

Thus the callback is a useful surrounding-runtime control. Its slower result cannot be explained as one extra numeric-key check per callback. Existing compiler reports likewise identify no increased frame or changed inspected constant-string/native instruction shape in the initial variant, and literal equality of those inspected function bytes between the initial and unsigned candidates. Those findings do not prove every binary property is equal and do not identify an alignment, cache or branch-prediction mechanism. No such mechanism is claimed here.

Unlike the callback, n-body does enter numeric lookup for `bodies[i]` and `bodies[j]` (`benchmarks/programs/nbody.lua:61`, `:65`, `:90`, `:94`). Its many named fields and native `sqrt` calls use other paths. The source contains both affected work and surrounding work; it does not predict the net timing sign.

## Shared-read structure

Relative to the unsigned variant, `a98a157` restores `executeRawTableGet` to `result, found := table.rawSlot(key)` (`execute_table.go:94`) and moves the read shortcut into `tableObject.rawSlot` (`table.go:649`). The existing-nonnil-slot write shortcut remains in `executeRawTableSet` (`execute_table.go:190`). The unsigned index predicate and its validated edge-key behavior are unchanged.

This is broader than an executor-only read optimization. Other `rawSlot` callers include `Table.RawGet` (`table.go:238`), `Frame.Index` (`native_call.go:257`), Lua `rawget` (`library_base.go:492`), and table-chain reads. Exact existing array keys avoid general normalization there too. Conversely, callers with other key types encounter the extra failed predicate before their original normalization. Constant-string opcode reads retain their separate direct path.

That scope is a defensible generality argument. It is not a source-level explanation for curing the callback row. The shared variant should be assessed on its complete recorded results, with the callback delta stated even when p exceeds 0.05. No further variants or padding changes are proposed by this review.

## Reusable summaries

The two-lane pilot validator and summary reads inputs only and writes its results to stdout. It was checked against both completed five-sample pilots:

```sh
taskset -c 8 python3 shared/summarize-pilot.py \
  --directory shared --name gate-pilot --samples 5
```

Add `--validate-only` to omit benchstat. The statistics command it uses is the pinned tool with `-col /runtime` and separate `.name:Programs`, `.name:Embedding`, and `.name:Interpreter` filters.

After the shared 15-sample, 44-row collector completes, the existing strict full validator can be reused without changing or overwriting the initial artifacts:

```sh
taskset -c 8 python3 initial/summarize.py \
  --raw shared/full-comparison.txt \
  --manifest shared/full-comparison-manifest.json \
  --collector shared/collect.py \
  --out-dir shared
```

That invocation produces this directory's `full-medians.json`, three baseline/candidate reports, and two candidate/GopherLua/go-lua reports. It requires `status=complete`, all 660 samples and all recorded hashes. It has not been run against the incomplete shared full collection. README publication remains deferred; no README updater was executed in this review.
