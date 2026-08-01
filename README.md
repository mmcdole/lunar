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

Lunar implements Lua 5.1, plus Lua 5.2-style `goto` and labels, entirely in
Go: compiler, bytecode VM, coroutines, binary chunks, and standard libraries.
Its compiler, VM, libraries, and pattern engine are checked against the
[Lua 5.1 reference implementation](https://www.lua.org/ftp/).

## Quick start

```go
package main

import (
	"fmt"

	"github.com/mmcdole/lunar"
)

func main() {
	state, err := lua.New(lua.Options{
		Libraries: lua.LibrarySet{
			lua.BaseLibrary,
			lua.StringLibrary,
			lua.TableLibrary,
		},
	})
	if err != nil {
		panic(err)
	}
	defer state.Close()

	greet, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
		name, ok := frame.String(0)
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

	results, err := state.DoString(
		"@hello.lua",
		`return greet("world"):upper()`,
	)
	if err != nil {
		panic(err)
	}

	greeting, _ := results[0].AsString()
	fmt.Println(greeting)
}
```

The library list is an allow-list. Its zero value installs no standard
libraries; the example chooses only base, string, and table. `:upper()` works
because `StringLibrary` installed the string metatable. Use
`lua.CoreLibraries()` for the usual capability-safe profile,
`lua.FullLibraries()` for trusted scripts, or an exact `lua.LibrarySet` like
the example. Individual `Open*` methods remain available for deliberate later
grants.

`DoString` loads source supplied directly by Go. Named script-file loading is a
separate permission: `ScriptLoader` defaults to denied, while `HostLoader`,
`FSLoader`, and `FuncLoader` explicitly select where scripts may come from.

See [Embedding Lunar](docs/embedding.md) for callbacks, tables, contexts,
errors, coroutines, and lifecycle management.

## Performance

These results are medians from 15 runs on an Apple M3 Pro with Go 1.25.1 at
source revision `1d43aec`. Lower is better.

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

| Loading a 9 MB CBOR graph | Lunar | GopherLua |
| --- | ---: | ---: |
| Total memory allocated while loading | **107.5 MB** | 784.6 MB |
| Live heap added after garbage collection | **72.24 MiB** | 542.26 MiB |

The [full results](benchmarks/results/2026-07-28-darwin-arm64-m3-pro/) include
confidence intervals, allocation counts, and raw output. The
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
and finalizers are implemented. Current intentional limits are:

- no C ABI, native C-module loading, or light userdata;
- no `debug.sethook` or `debug.gethook`;
- garbage collection runs synchronously rather than incrementally;
- no deterministic VM-instruction budget; and
- no high-level table iteration convenience beyond the precise `Table.Next`
  primitive.

One `State` can execute one goroutine at a time. Separate States may run
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
- [Performance](docs/performance.md): measurement groups and regression policy
- [Third-party notices](THIRD_PARTY_NOTICES.md): adapted algorithms, artwork,
  and benchmark sources

Lunar is available under the [MIT License](LICENSE).
