# Performance policy

Lunik tracks four separate dimensions:

- Lua execution time;
- Go allocation traffic;
- live and retained heap; and
- Go/Lua embedding cost.

Results from unlike dimensions are not combined. `B/op` is allocation traffic,
not retained memory. The large CBOR graph is a memory workload, not an overall
interpreter score.

## Public measurements

The version-pinned harness, commands, collection policy, and statistical
summary commands are in [`benchmarks/README.md`](../benchmarks/README.md).
Program sources and oracles are in
[`benchmarks/PROGRAMS.md`](../benchmarks/PROGRAMS.md). The fresh-process CBOR
protocol and metric definitions are in
[`benchmarks/cbor/README.md`](../benchmarks/cbor/README.md).

The groups stay separate:

| Group | Measures |
| --- | --- |
| Lua programs | Longer interpreter workloads using established algorithms |
| Interpreter operations | Numeric loops, Lua calls, table access, and string construction |
| Embedding boundary | Go-to-Lua calls, Lua-to-Go callbacks, strings, and host-built tables |
| CBOR graph | Allocation traffic and retained heap for a large decoded table graph |

Program and interpreter rows enter Lua once per timed operation. Embedding rows
measure boundary costs directly; `lua_to_go_scalar_1000` performs 1,000
callbacks per operation. CBOR load enters Lua once and performs the decode
inside Lua.

## Change acceptance

Performance changes follow these rules:

1. Reproduce and profile the affected workload before changing runtime
   structure.
2. Keep compilation, setup, warmup, and validation outside the timed interval.
3. Compare clean recorded revisions with the same toolchain, machine, runtime
   versions, GC settings, inputs, and process policy.
4. Collect repeated samples in balanced runtime order and retain the raw
   output.
5. Re-run correctness, race, vet, and supported-platform checks.
6. Re-run representative table, call, numeric, string, coroutine, and
   embedding rows after a low-level optimization.
7. Report every program row; do not replace unlike workloads with one score.

An optional API or fast path must not add bookkeeping or checks to the ordinary
operation when the feature is unused. Lower allocation traffic does not by
itself justify slower common execution. A statistically supported regression
above 5% requires a documented tradeoff based on representative workloads.

Native PUC Lua measurements are reported separately from Go-hosted runtime
measurements because allocator and process accounting are not interchangeable.
