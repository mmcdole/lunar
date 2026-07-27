<p align="center">
  <img src="assets/lugo.png" alt="Lugo" width="220">
</p>

<h1 align="center">Lugo</h1>

<p align="center">
  A Lua 5.1 runtime for Go.
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/mmcdole/lugo"><img src="https://pkg.go.dev/badge/github.com/mmcdole/lugo.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

Lugo is a Lua 5.1 implementation written entirely in Go: compiler, bytecode VM,
coroutines, binary chunks, and standard libraries.

It is aimed at Go programs that run scripts as part of normal operation, where
the host decides what those scripts can reach and what they are allowed to cost.

The compiler, VM, libraries, and patterns are checked against the
[reference implementation](https://www.lua.org/ftp/), including weak tables,
finalizers, and PUC-compatible bytecode. A new State has no libraries until you
open them, any call can be cancelled while it runs, and a script cannot
terminate the host process. Loading a 9 MB object graph costs 72 MiB of live
heap against GopherLua's 544 MiB, and interpreter-bound programs run up to 3x
faster.

The public embedding API is still stabilizing. See
[Architecture](docs/architecture.md) for the runtime model.

## Comparison

| | Lugo | [GopherLua](https://github.com/yuin/gopher-lua) | [Shopify go-lua](https://github.com/Shopify/go-lua) |
| --- | --- | --- | --- |
| Language target | Lua 5.1 | Lua 5.1 plus 5.2-style `goto` | Lua 5.2 |
| Host interface | Owned typed values plus borrowed low-level frames | Public `LValue` object model with callback stacks | Lua C API-style stack indices |
| Runtime model | One compact private representation for both APIs | Go interfaces and public object types | C-style stack over interface-backed values |
| Collection controls | State-local Lua reachability, weak tables, finalizers, and logical heap accounting | `collectgarbage` drives the process-wide Go collector | Process-wide Go collector; no weak tables |
| Coroutines | Supported | Supported | Not implemented |
| Cancellation | Per-operation context APIs; ordinary calls avoid cancellation polling | Context installed on the State | No Go `context.Context` execution API |
| `os.exit` | Returns a host-handled `*ExitRequest` | Terminates the Go process | Terminates the Go process |
| Binary chunks | Reads and writes native-ABI PUC-compatible Lua 5.1 chunks | No PUC-compatible chunk I/O; Lua `string.dump` unsupported | Reads and writes Lua 5.2 chunks through its Go API; Lua `string.dump` unavailable |

Versions used in this comparison are pinned in the
[benchmark module](benchmarks/README.md#runtime-versions).

## Quick start

Lugo requires Go 1.24 or newer.

```sh
go get github.com/mmcdole/lugo
```

```go
package main

import (
	"fmt"

	lua "github.com/mmcdole/lugo"
)

func main() {
	state, err := lua.New(lua.Options{})
	if err != nil {
		panic(err)
	}
	defer state.Close()

	chunk, err := state.LoadString("@hello.lua", `return 6 * 7`)
	if err != nil {
		panic(err)
	}
	results, err := state.Call(chunk.Value())
	if err != nil {
		panic(err)
	}
	answer, _ := results[0].AsNumber()
	fmt.Println(answer)
}
```

A new State starts with no libraries installed. Open only what the script
needs with `OpenBase`, `OpenMath`, `OpenString`, `OpenTable`, `OpenIO`,
`OpenOS`, `OpenPackage`, and `OpenDebug`. `OpenBase` also opens the coroutine
library; `OpenCoroutine` can open it independently.

## Performance

Performance is measured in separate groups:

- four established Lua programs from the Computer Language Benchmarks Game;
- interpreter microbenchmarks for numeric, call, table, and string execution;
- embedding benchmarks for Go-to-Lua calls, Lua-to-Go callbacks, strings, and
  host-built tables; and
- a deterministic 9 MB CBOR graph for fresh-process allocation and
  retained-memory measurements.

### Results

The following medians come from 15 samples on an Apple M3 Pro with Go 1.25.1
at source revision `1a50139`. Lower is better.

| Established Lua program | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | 172.9 ms | 176.5 ms | 186.0 ms |
| fannkuch-redux | 24.25 ms | 33.67 ms | 41.33 ms |
| n-body | 61.30 ms | 195.59 ms | 204.94 ms |
| spectral-norm | 55.63 ms | 166.21 ms | 157.80 ms |

| Embedding operation | Lugo | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Go to Lua, scalars | 64.25 ns | 63.00 ns | 153.90 ns |
| Lua to Go callback, 1,000 calls | 63.65 µs | 103.13 µs | 86.96 µs |
| Go string echo, 128 bytes | 89.04 ns | 85.51 ns | 148.50 ns |
| Go-built table, 16 array and 4 record fields | 2.327 µs | 1.434 µs | 1.679 µs |

| CBOR load memory | Lugo | GopherLua |
| --- | ---: | ---: |
| Allocation traffic | 107.5 MB | 784.6 MB |
| Baseline-subtracted retained heap increase | 72.24 MiB | 542.26 MiB |
| Absolute live Go heap after forced GC | 72.53 MiB | 543.83 MiB |

The [result archive](benchmarks/results/2026-07-27-darwin-arm64-m3-pro/)
contains confidence intervals, allocation counts, interpreter microbenchmarks,
raw Go benchmark output, and paired CBOR JSONL records. Shopify go-lua is not
in the CBOR table because its pinned standard IO library does not implement
`file:read("*a")`.

The program inputs are scaled local inputs, not official Benchmarks Game
scores. Compilation, library setup, warmup, and result validation are outside
the timed region. Every comparison uses protected calls and identical Lua
source. The CBOR load runs in fresh processes and validates roughly 184,000
tables, 938,000 entries, and a canonical structural digest.

The [benchmark protocol](benchmarks/README.md) records runtime versions,
commands, timing boundaries, environment controls, raw-output requirements,
and `benchstat` analysis. [Program provenance](benchmarks/PROGRAMS.md) pins the
upstream revision, source hashes, local inputs, oracles, and license. The
[CBOR protocol](benchmarks/cbor/README.md) records fixture generation,
checksums, metric definitions, process isolation, and paired sampling.

## Status and scope

The Lua 5.1 compiler, VM, coroutines, binary chunks, Lua-visible collection,
and standard runtime are implemented apart from the limits below. Optional
oracle tests can re-run recorded cases against a Lua 5.1.5 executable named by
`LUGO_LUA51`.

Current intentional limits and remaining work:

- no C ABI, native C-module loading, or light userdata; `package.loadlib` and
  the C searchers report that dynamic libraries are unavailable;
- no `debug.sethook` or `debug.gethook`;
- semantic collection is synchronous rather than incremental;
- no retained-heap quota yet; and
- no public high-level table iterator or State-level metamethod-aware indexing.

One State has one active executor. Separate States may run concurrently.

## Documentation

- [Architecture](docs/architecture.md): runtime representation, compiler, VM,
  and API boundaries
- [Embedding](docs/embedding.md): State setup, calls, callbacks, values,
  contexts, errors, and lifecycle
- [Collection](docs/collection.md): Lua reachability, weak tables, and
  finalization
- [Performance](docs/performance.md): measurement groups and regression policy
- [Third-party notices](THIRD_PARTY_NOTICES.md): provenance for adapted
  reference algorithms

Lugo is available under the [MIT License](LICENSE).
