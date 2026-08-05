<p align="center">
  <img src="assets/lunar.png" alt="Lunar gopher orbiting the Moon" width="240">
</p>

<h1 align="center">Lunar</h1>

<p align="center">
  A fast, memory-efficient Lua 5.1 runtime for Go.
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/mmcdole/lunar"><img src="https://pkg.go.dev/badge/github.com/mmcdole/lunar.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

Lunar is a complete implementation of Lua 5.1 (plus 5.2-style `goto` and
labels) in pure Go. The compiler, VM, and standard libraries are all tested
against the [reference implementation](https://www.lua.org/ftp/).

## Quick start

```go
package main

import (
	"fmt"

	"github.com/mmcdole/lunar"
)

func main() {
	state, err := lua.New(lua.Options{Libraries: lua.CoreLibraries()})
	if err != nil {
		panic(err)
	}
	defer state.Close()

	results, err := state.DoString("@demo.lua", `return ("lunar"):upper()`)
	if err != nil {
		panic(err)
	}

	text, _ := results[0].AsString()
	fmt.Println(text) // LUNAR
}
```

Libraries and file access are opt-in. `lua.CoreLibraries()` installs
everything that can't touch the host, and `lua.FullLibraries()` adds IO, OS,
and debug for scripts you trust. Loading scripts from disk is disabled until
you configure a `ScriptLoader`.

## Calling Go from Lua

`NewNativeFunction` wraps a Go function as a Lua value. Inside the callback,
everything goes through the `Frame`: read arguments with its typed accessors,
then return a result or throw a Lua error:

```go
// Callable from Lua as greet(name).
greet, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
	name, ok := frame.String(0) // first argument, must be a string
	if !ok {
		frame.ThrowArgTypeError(0, lua.StringKind)
	}
	return frame.ReturnString("hello, " + name)
})
if err != nil {
	panic(err)
}
if err := state.SetGlobal("greet", greet.Value()); err != nil {
	panic(err)
}

// results[0] is "hello, moon"
results, err := state.DoString("@hello.lua", `return greet("moon")`)
```

See [Embedding Lunar](docs/embedding.md) for the full guide: calls,
callbacks, tables, errors, cancellation, coroutines, and lifecycle.

## Performance

Medians from 15 runs on an Apple M3 Pro with Go 1.25.1; each linked result
set records its exact source revision. Lower is better.

| Established Lua program | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | **162.2 ms** | 172.4 ms | 179.8 ms |
| fannkuch-redux | **23.63 ms** | 33.17 ms | 40.14 ms |
| n-body | **59.03 ms** | 193.39 ms | 197.77 ms |
| spectral-norm | **52.82 ms** | 160.14 ms | 153.66 ms |

| Embedding operation | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Go calls Lua with scalar arguments | **61.21 ns** | 61.51 ns | 146.80 ns |
| Lua calls Go 1,000 times | **62.49 µs** | 99.58 µs | 84.37 µs |
| Lua echoes a 128-byte Go string | 86.20 ns | **81.41 ns** | 144.00 ns |
| Lua checksums a reused Go-built table | **312.4 ns** | 578.3 ns | 972.9 ns |
| Build a table in Go, then checksum it in Lua | 2.230 µs | **1.405 µs** | 1.645 µs |

| Live heap added after loading and GC | Lunar | GopherLua | Ratio |
| --- | ---: | ---: | ---: |
| 9 MB CBOR graph: 183,513 tables, 938,452 entries | **72.2 MiB** | 542.3 MiB | 7.5× |
| 25,000 four-field tables, repeated 16 B keys | **7.3 MiB** | 72.0 MiB | 9.9× |
| 25,000 four-field tables, unique 16 B keys | **8.8 MiB** | 72.0 MiB | 8.2× |
| 25,000 four-field tables, repeated 80 B keys | **14.9 MiB** | 78.1 MiB | 5.3× |
| One table, 100,000 unique 16 B keys | **6.5 MiB** | 14.7 MiB | 2.26× |
| One table, 100,000 unique 256 B keys | **29.4 MiB** | 37.6 MiB | 1.28× |
| One table, 100,000 unique 1 KiB keys | **102.7 MiB** | 110.9 MiB | 1.08× |

The ratio depends on workload shape: Lunar wins on per-table overhead and
on reusing repeated strings up to 64 bytes, while raw string bytes cost
both runtimes the same, so the gap narrows toward 1× as string payload
dominates. Loading the CBOR graph also allocates 7.3× less transient
memory (**107.5 MB** versus 784.6 MB).

The [full results](benchmarks/results/) include confidence intervals,
allocation counts, and raw output; the
[benchmark protocol](benchmarks/README.md) lists the commands, inputs, and
runtime versions.

## Compatibility

| | Lunar | [GopherLua](https://github.com/yuin/gopher-lua) | [Shopify go-lua](https://github.com/Shopify/go-lua) |
| --- | --- | --- | --- |
| Lua version | Lua 5.1 with Lua 5.2-style `goto` | Lua 5.1 with Lua 5.2-style `goto` | Lua 5.2 |
| Go API | Functions return typed values; callbacks use typed `Frame` accessors | Values are `LValue` objects; callbacks pass arguments and results through an `LState` stack | Mirrors the Lua C API; values are addressed by numeric stack position |
| Libraries in a new state | None by default; select any subset at construction or open one later | All standard libraries | None; call `OpenLibraries` or open them individually |
| Script-file loading | Denied by default; select host files, an `fs.FS`, or a host function | Ambient OS file access | Ambient OS file access when the applicable libraries are open |
| Coroutines | Supported from Lua and Go | Supported from Lua and Go | Not implemented |
| Cancellation | One installed context covers execution, loading, and coroutines | One context on the state, execution only | No context-based cancellation |
| `os.exit` | Returns an `*lua.ExitRequest` to Go | Exits the entire Go process | Exits the entire Go process |
| Binary chunks | Reads and writes Lua 5.1 bytecode when byte order and type sizes match | Cannot read or write standard Lua bytecode files | Reads and writes Lua 5.2 bytecode through the Go API |

## Scope

The compiler, VM, standard libraries, coroutines, binary chunks, weak tables,
and finalizers are implemented. Current intentional limits:

- no C ABI, native C-module loading, or light userdata;
- no `debug.sethook` or `debug.gethook`;
- garbage collection runs synchronously rather than incrementally;
- no deterministic VM-instruction budget; and
- no table-iteration helpers beyond the `Table.Next` primitive.

A `State` serves one goroutine at a time; separate States can run
concurrently.

The public embedding API is still stabilizing.

## Documentation

- [Embedding](docs/embedding.md): setup, calls, callbacks, values, contexts,
  errors, and lifecycle
- [Architecture](docs/architecture.md): compiler, VM, runtime representation,
  and API boundaries
- [Language compatibility](docs/language-compatibility.md): the `goto`
  extension and intentional Lua 5.1/5.2/LuaJIT choices
- [Collection](docs/collection.md): Lua reachability, weak tables, and
  finalization
- [Third-party notices](THIRD_PARTY_NOTICES.md): adapted algorithms, artwork,
  and benchmark sources

Lunar is available under the [MIT License](LICENSE).
