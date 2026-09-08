# Table-construction investigation

Main: `5fc51e449a6661340056544e995aae4fdf89dcb2` (PR #15 merged).

The fresh binarytrees profile reports 1,009,044 Go allocations and about
68.9 MB allocated per complete benchmark operation. Table construction is
22.99% of sampled CPU cumulatively. Sampled allocation objects are 67.69%
table headers and 32.31% array backing storage; those constructor paths
account for almost all allocated objects. These overlapping CPU costs are
not an estimate of achievable speedup. The profiling run is not a timing
comparison and does not supersede the 15-sample README measurements.

PUC 5.1.5 source inspection found that Lunar already applies constructor
capacity hints, batched SETLIST, and empty hash avoidance. PUC allocates
its table header and array separately (`ltable.c:263-268,359-369`). A combined
Go allocation would be a Lunar-specific implementation experiment.

A possible smaller experiment is to avoid writing nil into appended SETLIST
slots immediately before writing all of their actual values. The current
fast path proves contiguity and absence of integer hash keys. This applies
to arbitrary bulk-append lengths, but its whole-program benefit is unproven.

The larger allocation experiment must cover general small-table shapes and
sizes, rather than selecting two-element tables because binarytrees uses
them. No candidate has been selected or implemented. A design must preserve
collection, weak tables, table growth, and truthful retained-byte accounting;
retained embedded backing remains a cost after promotion.

Qualification must include complete-program performance beyond binarytrees,
CBOR decode/encode with mixed records and arrays, embedding table creation,
and retained-memory/growth coverage. Existing four interpreter, five embedding,
and four program cases remain the regression gate. Table-shape diagnostics
help explain behavior but cannot establish normal-program speedup alone.

`manifest.json` records executable provenance, byte equivalence to main,
profiling command and environment. CPU and allocation profiles are alongside
this report. There is no measured candidate speedup to publish.

## Generality review

The first experiment to qualify is direct initialization in the existing
contiguous SETLIST append path, across constructor sizes. Coallocation is
deferred until broader evidence justifies its permanent storage cost after
array growth. Neither is implemented or measured yet.

CBOR save is the strongest existing second complete workload: the encoder
creates two one-element arrays per ordinary table (`cbor.lua:241`) and grows
them (`:248-251`). The large graph has 183,513 tables. CBOR load constructs
empty tables and inserts dynamically, so it checks an unhinted path. Existing
table-shape cases use unhinted string-key maps; the embedding create/fill case
uses hints 16/4. These are useful regression controls but cannot demonstrate
tiny-array gains. There is no archived constructor-hint histogram.

Correctness coverage for direct initialization must include interior/trailing
nils, reused capacity and new storage, mixed constructors, multiple SETLIST
batches, open results, occupancy counts, compaction, capacity accounting, and
weak references under collection. A speedup only in diagnostics or a single
table size will not qualify the change.
