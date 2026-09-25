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

Medians from 15 runs on an AMD Ryzen 7 4800U under Linux/amd64, using
Go 1.27.1. The [measurement report](benchmarks/results/2026-09-25-linux-amd64-are-we-fast-yet/)
records revisions, build settings, confidence intervals, and a PUC Lua 5.1
comparison. Lower is better.

[Are We Fast Yet](https://github.com/smarr/are-we-fast-yet) programs model
typical object-oriented code: classes built with metatables, closures, many
small objects, strings, and arrays.

| Are We Fast Yet | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Richards | **877.9 ms** | 1,677.6 ms | 1,990.4 ms |
| DeltaBlue | **141.7 ms** | 326.1 ms | 9,503.5 ms |
| Json | **937.6 ms** | 1,681.7 ms | 1,981.0 ms |
| CD | **2,755 ms** | 6,321 ms | 6,623 ms |
| Bounce | **927.3 ms** | 2,211.0 ms | 2,854.5 ms |
| List | **604.2 ms** | 1,258.7 ms | 1,397.7 ms |
| Mandelbrot | **489.8 ms** | 1,697.5 ms | 2,566.3 ms |
| NBody | **1,539 ms** | 5,730 ms | 8,165 ms |
| Permute | **536.1 ms** | 1,398.0 ms | 1,725.3 ms |
| Queens | **504.8 ms** | 1,183.9 ms | 1,537.2 ms |
| Sieve | **721.2 ms** | 1,893.1 ms | 2,058.3 ms |
| Storage | **1,011 ms** | 2,456 ms | 2,812 ms |
| Towers | **886.2 ms** | 1,899.5 ms | 3,000.7 ms |

| Benchmarks Game program | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| binary-trees | **271.1 ms** | 323.7 ms | 345.1 ms |
| fannkuch-redux | **27.73 ms** | 60.38 ms | 73.63 ms |
| n-body | **86.26 ms** | 337.07 ms | 440.58 ms |
| spectral-norm | **81.22 ms** | 311.41 ms | 348.86 ms |

The retained-memory figures below are earlier Apple M3 Pro / Go 1.25.1
measurements: [CBOR graph](benchmarks/results/2026-07-28-darwin-arm64-m3-pro/)
and [table shapes](benchmarks/results/2026-08-05-darwin-arm64-m3-pro/).

| Live heap added after loading and GC | Lunar | GopherLua | Ratio |
| --- | ---: | ---: | ---: |
| 9 MB CBOR graph: 183,513 tables, 938,452 entries | **72.2 MiB** | 542.3 MiB | 7.5× |
| 25,000 four-field tables, repeated 16 B keys | **7.3 MiB** | 72.0 MiB | 9.9× |
| 25,000 four-field tables, repeated 80 B keys | **14.9 MiB** | 78.1 MiB | 5.3× |
| One table, 100,000 unique 16 B keys | **6.5 MiB** | 14.7 MiB | 2.26× |
| One table, 100,000 unique 256 B keys | **29.4 MiB** | 37.6 MiB | 1.28× |
| One table, 100,000 unique 1 KiB keys | **102.7 MiB** | 110.9 MiB | 1.08× |

The ratio depends on workload shape: Lunar wins on per-table overhead and
on reusing repeated strings up to 64 bytes, while raw string bytes cost
both runtimes the same, so the gap narrows toward 1× as string payload
dominates. Loading the CBOR graph also allocates 7.3× less transient
memory (**107.5 MB** versus 784.6 MB).

The [measurement summaries](benchmarks/results/) include confidence intervals,
allocation counts, and measurement conditions; the
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
