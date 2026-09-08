Preparation is separate from collection. All files here belong to the table-fill assessment; baseline/candidate checkouts remain untouched by these commands.

Baseline preparation completed from clean revision `5fc51e449a6661340056544e995aae4fdf89dcb2`. Exact executed commands are in `prepare-baseline.sh`, with combined output in `prepare-baseline.log`. The nested CBOR module's tests and vet passed. The compiler emitted harmless read-only module stat-cache warnings during builds; every build exited successfully.

`baseline-manifest.json` records the Go/compiler environment, clean revision/tree, relevant source hashes, executable hashes, and fixture hashes. Both source codec/wrapper files are staged under `fixture/`. The generated small (8,011 bytes) and large (9,208,046 bytes) corpora match their documented SHA-256 values. Small load/save retained smoke outputs passed the structural digest oracle and reported the expected clean source revision.

After the candidate author commits a clean checkout:

```sh
bash /tmp/lunar-table-fill/cbor/prepare-candidate.sh > /tmp/lunar-table-fill/cbor/prepare-candidate.log 2>&1
```

Root must schedule each following collection serially with every other benchmark. The paired runner snapshots both workers, randomizes order with seed 1, and launches fresh processes. Default CPU affinity is logical CPU 2; GOMAXPROCS=1, GOGC=100, GOMEMLIMIT=off. Both workers are Lunar; implementation labels distinguish their revisions.

```sh
bash /tmp/lunar-table-fill/cbor/collect-pair.sh save timing
bash /tmp/lunar-table-fill/cbor/collect-pair.sh load timing
bash /tmp/lunar-table-fill/cbor/collect-pair.sh save retained
bash /tmp/lunar-table-fill/cbor/collect-pair.sh load retained
```

Each command collects 15 samples per implementation plus two discarded paired warmups, checks clean source identity and the pinned baseline binary/revision, then writes a descriptive bootstrap report. No numerical acceptance gate is introduced by these helper commands. Filenames encode operation, measurement and implementation. Existing evidence is not overwritten. `collect-pair.sh` optionally accepts a third run-count argument for explicitly labelled exploratory checks; retain 15 samples for the publication archive.

Timing excludes save's preparatory decode and excludes all structural/round-trip validation. Full worker process duration also includes those operations, so collection wall time is substantially longer than reported `elapsed_ns`. Retained measurements stabilize Go heap twice before/after the operation; report both absolute `heap_retained` and signed `heap_delta` separately. Neither may replace allocated-byte traffic.

Optional table-shape controls use the same-Lunar workers; the repository's `scripts/shapes.sh` instead hardwires GopherLua and is not appropriate here. The fill-only candidate does not change storage layout, while these shapes use unhinted string-key maps; they are memory regression controls rather than expected speedup cases.

```sh
# Representative small-record and large-map controls:
python3 /tmp/lunar-table-fill/cbor/collect-shapes.py --output /tmp/lunar-table-fill/cbor/shapes-controls tables-repeated-16 one-unique-16
# Full seven-case suite, if root elects to recollect it:
python3 /tmp/lunar-table-fill/cbor/collect-shapes.py --output /tmp/lunar-table-fill/cbor/shapes-all
```

Both commands default to 15 fresh-process samples per implementation, alternate order, validate source/binary identity, and write an explicit completion manifest. They must also be scheduled serially. No large timing, retained, or shape sample was started during preparation.


A bounded scalar refinement has independent artifacts. The initial candidate workers, manifests, smoke outputs, and pilot evidence stay at their original paths; `collect-pair-initial.sh` preserves the collector used for that pilot. Do not build before the scalar source is committed and clean:

```sh
bash /tmp/lunar-table-fill/cbor/prepare-scalar.sh > /tmp/lunar-table-fill/cbor/prepare-scalar.log 2>&1
```

After root schedules a serial slot, select the scalar candidate through the environment:

```sh
LUNAR_CBOR_CANDIDATE=scalar bash /tmp/lunar-table-fill/cbor/collect-pair.sh save timing
LUNAR_CBOR_CANDIDATE=scalar bash /tmp/lunar-table-fill/cbor/collect-pair.sh load timing
```

This reads `scalar-manifest.json` and `bin/scalar-worker`, while preserving the original baseline. Output names default to `scalar-save-timing-*` / `scalar-load-timing-*`. The JSON implementation role remains `candidate`, as expected by the paired comparator; the scalar revision and binary hash identify the implementation. `LUNAR_CBOR_PREFIX` can explicitly name a separate exploratory tranche, and `LUNAR_CBOR_WARMUPS` can adjust discarded warmups. All evidence and log paths are checked before collection to prevent overwrites. Large timing has not been started for this refinement.
