<p align="center">
  <img src="assets/lunik.png" alt="Lunik gopher orbiting the Moon" width="240">
</p>

<h1 align="center">Lunik</h1>

<p align="center">
  A fast, memory-efficient Lua 5.1 runtime for Go.
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/mmcdole/lunik"><img src="https://pkg.go.dev/badge/github.com/mmcdole/lunik.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

Lunik is a Lua 5.1 compiler, bytecode VM, and standard library written entirely
in Go. It focuses on Lua compatibility, low interpreter overhead, and an
embedding API that feels natural from Go.

The compiler, VM, libraries, and pattern engine are checked against the
[Lua 5.1 reference implementation](https://www.lua.org/ftp/). The runtime
supports coroutines, weak tables, finalizers, and PUC-compatible binary chunks.

> [!NOTE]
> The module is named Lunik; its Go package is named `lua`. Import
> `github.com/mmcdole/lunik` and use it as `lua`.

## Quick start

Lunik requires Go 1.24 or newer.

```sh
go get github.com/mmcdole/lunik
```

```go
package main

import (
	"fmt"

	"github.com/mmcdole/lunik"
)

func main() {
	state, err := lua.New(lua.Options{})
	if err != nil {
		panic(err)
	}
	defer state.Close()

	chunk, err := state.LoadString("@answer.lua", `return 6 * 7`)
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

Loading compiles a chunk; calling it executes the chunk and returns owned Lua
values.

## Call Go from Lua

Native functions let a script call application code without exposing Go's
objects or reflection surface. With a `State` created as above:

```go
prices := map[string]float64{
	"widget": 12.50,
}

unitPrice, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
	sku, ok := frame.String(0)
	if !ok {
		return frame.ArgTypeError(0, lua.StringKind)
	}
	price, ok := prices[sku]
	if !ok {
		return frame.RaiseString("unknown product: " + sku)
	}
	return frame.ReturnNumber(price)
})
if err != nil {
	panic(err)
}
if err := state.SetGlobal("unit_price", unitPrice.Value()); err != nil {
	panic(err)
}

chunk, err := state.LoadString("@checkout.lua", `
	local subtotal = unit_price("widget") * 4
	return subtotal * 0.90
`)
if err != nil {
	panic(err)
}
results, err := state.Call(chunk.Value())
if err != nil {
	panic(err)
}

total, _ := results[0].AsNumber()
fmt.Printf("%.2f\n", total)
```

See [Embedding Lunik](docs/embedding.md) for callbacks, tables, contexts,
errors, coroutines, and lifecycle management.

## Performance

Lunik measures interpreter workloads, embedding overhead, allocation traffic,
and retained heap separately. The following medians come from 15 samples on
an Apple M3 Pro with Go 1.25.1:

| Established Lua program | Lunik | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | **162.2 ms** | 172.4 ms | 179.8 ms |
| fannkuch-redux | **23.63 ms** | 33.17 ms | 40.14 ms |
| n-body | **59.03 ms** | 193.39 ms | 197.77 ms |
| spectral-norm | **52.82 ms** | 160.14 ms | 153.66 ms |

| Embedding operation | Lunik | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Go to Lua, scalars | **61.21 ns** | 61.51 ns | 146.80 ns |
| Lua to Go callback, 1,000 calls | **62.49 µs** | 99.58 µs | 84.37 µs |
| Convert and echo a 128-byte Go string | 86.20 ns | **81.41 ns** | 144.00 ns |
| Pass and checksum a prebuilt table | **312.4 ns** | 578.3 ns | 972.9 ns |
| Create, fill, pass, and checksum a table | 2.230 µs | **1.405 µs** | 1.645 µs |

| 9 MB CBOR graph | Lunik | GopherLua |
| --- | ---: | ---: |
| Allocation traffic | **107.5 MB** | 784.6 MB |
| Retained heap increase | **72.24 MiB** | 542.26 MiB |

These are individual workloads, not a composite score. The
[full result archive](benchmarks/results/2026-07-28-darwin-arm64-m3-pro/)
contains every benchmark row, confidence intervals, allocation counts, raw
output, and paired CBOR measurements. The
[benchmark protocol](benchmarks/README.md) documents timing boundaries,
environment controls, validation, and pinned runtime versions.

## Embedding behavior

- A new `State` has no libraries. Applications opt into the base, coroutine,
  math, string, table, IO, OS, package, and debug libraries individually.
- Context-aware load, call, and resume operations support cancellation.
  `Options` also provides deterministic execution and load limits.
- Lua `os.exit` returns a `*lua.ExitRequest`; it never terminates the Go
  process.
- The public API uses typed, owning values. Native callbacks use borrowed
  frames for low-overhead argument and result handling.

One `State` permits one active executor. Separate States may run concurrently.

## Compatibility

| | Lunik | [GopherLua](https://github.com/yuin/gopher-lua) | [Shopify go-lua](https://github.com/Shopify/go-lua) |
| --- | --- | --- | --- |
| Language target | Lua 5.1 | Lua 5.1 plus 5.2-style `goto` | Lua 5.2 |
| Host interface | Owned typed values and borrowed callback frames | Public `LValue` objects and callback stacks | Lua C API-style stack indices |
| Coroutines | Supported | Supported | Not implemented |
| Cancellation | Per-operation Go contexts | Context installed on the State | No Go context API |
| `os.exit` | Returned to the host | Terminates the process | Terminates the process |
| Binary chunks | PUC-compatible Lua 5.1 read/write | No PUC-compatible chunk I/O | Lua 5.2 read/write through the Go API |

Comparator versions and the measurement protocol are pinned in the
[benchmark module](benchmarks/README.md).

## Scope

The compiler, VM, coroutines, binary chunks, Lua-visible collection, and
standard runtime are implemented. Current intentional limits are:

- no C ABI, native C-module loading, or light userdata;
- no `debug.sethook` or `debug.gethook`;
- synchronous rather than incremental semantic collection;
- no retained-heap quota; and
- no public high-level table iterator or State-level metamethod-aware
  indexing.

The public embedding API is still stabilizing.

## Documentation

- [Embedding](docs/embedding.md): setup, calls, callbacks, values, contexts,
  errors, and lifecycle
- [Architecture](docs/architecture.md): compiler, VM, runtime representation,
  and API boundaries
- [Collection](docs/collection.md): Lua reachability, weak tables, and
  finalization
- [Performance](docs/performance.md): measurement groups and regression policy
- [Third-party notices](THIRD_PARTY_NOTICES.md): adapted algorithms, artwork,
  and benchmark sources

Lunik is available under the [MIT License](LICENSE).
