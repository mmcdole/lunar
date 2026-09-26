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
labels) in pure Go. It passes the
[official Lua 5.1 test suite](conformance/).

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

Timings are medians from 15 runs on an AMD Ryzen 7 4800U under
Linux/amd64, using Go 1.27.1. The
[measurement report](benchmarks/results/2026-09-26-linux-amd64-interpreter/)
records revisions, build settings, and confidence intervals. Lower is better.

### Are We Fast Yet

[Are We Fast Yet](https://github.com/smarr/are-we-fast-yet) programs model
typical object-oriented code: classes built with metatables, closures, many
small objects, strings, and arrays.

| Program | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Richards | **595.0 ms** | 1,677.2 ms | 1,990.8 ms |
| DeltaBlue | **124.5 ms** | 325.6 ms | 9,569.5 ms |
| Json | **651.0 ms** | 1,670.2 ms | 1,982.6 ms |
| CD | **2,541 ms** | 6,314 ms | 6,645 ms |
| Bounce | **542.7 ms** | 2,143.7 ms | 2,874.0 ms |
| List | **528.1 ms** | 1,247.2 ms | 1,401.1 ms |
| Mandelbrot | **367.7 ms** | 1,681.1 ms | 2,563.6 ms |
| NBody | **1,363 ms** | 5,631 ms | 8,151 ms |
| Permute | **586.5 ms** | 1,387.6 ms | 1,740.5 ms |
| Queens | **467.9 ms** | 1,187.8 ms | 1,527.9 ms |
| Sieve | **561.5 ms** | 1,899.3 ms | 2,064.2 ms |
| Storage | **519.3 ms** | 2,440.1 ms | 2,801.2 ms |
| Towers | **899.0 ms** | 1,925.1 ms | 3,050.1 ms |

### Benchmarks Game

Four programs from the Computer Language Benchmarks Game, with inputs scaled
for interpreters.

| Program | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | **250.5 ms** | 322.2 ms | 344.0 ms |
| fannkuch-redux | **26.57 ms** | 62.56 ms | 73.84 ms |
| n-body | **63.94 ms** | 335.20 ms | 444.98 ms |
| spectral-norm | **66.49 ms** | 312.13 ms | 344.02 ms |

### Retained memory

Live heap added after loading data and collecting garbage
([report](benchmarks/results/2026-09-25-linux-amd64-memory/)).

| Workload | Lunar | GopherLua | Ratio |
| --- | ---: | ---: | ---: |
| 9 MB CBOR graph: 183,513 tables, 938,452 entries | **72.3 MiB** | 542.3 MiB | 7.5× |
| 25,000 four-field tables, repeated 16 B keys | **7.4 MiB** | 72.0 MiB | 9.7× |
| 25,000 four-field tables, repeated 80 B keys | **15.0 MiB** | 78.1 MiB | 5.2× |
| One table, 100,000 unique 16 B keys | **6.5 MiB** | 14.8 MiB | 2.26× |
| One table, 100,000 unique 256 B keys | **29.4 MiB** | 37.7 MiB | 1.28× |
| One table, 100,000 unique 1 KiB keys | **102.7 MiB** | 110.9 MiB | 1.08× |

Lunar saves most on per-table overhead and repeated short strings; as raw
string bytes dominate, the gap narrows toward 1×. The
[benchmark protocol](benchmarks/README.md) lists commands and inputs.

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
