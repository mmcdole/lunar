# Program benchmarks

`BenchmarkPrograms` complements the synthetic `BenchmarkInterpreter`
microbenchmarks with four current Lua programs from the Computer Language
Benchmarks Game:

- binary-trees Lua #2;
- fannkuch-redux Lua #1;
- n-body Lua #2; and
- spectral-norm Lua #1.

These are local runtime comparisons, not official Computer Language
Benchmarks Game scores. In particular, the inputs are deliberately scaled
down from those used on the official site:

| Program | Local input |
| --- | ---: |
| binary-trees Lua #2 | 12 |
| fannkuch-redux Lua #1 | 8 |
| n-body Lua #2 | 20,000 |
| spectral-norm Lua #1 | 150 |

## Method

Each source file under `programs/` is an unchanged upstream command-line
program. The Go harness generates a small Lua wrapper around it. The wrapper
provides the program's `arg[1]`, captures `io.write` into a string, and exposes
one repeatable `benchmark_program` function. The original source is inserted
once, byte-for-byte, inside that function.

For every engine and program, the harness:

1. creates a fresh state and opens the equivalent base and string libraries,
   plus math only for n-body and spectral-norm;
2. compiles and loads the same generated Lua source once;
3. executes the wrapper's top-level initialization once;
4. invokes the program once as an untimed warmup and validates its output;
5. forces a Go garbage collection, resets the benchmark timer, and measures
   protected calls to the already loaded function; and
6. stops the timer and validates the last measured output.

Compilation, source loading, library setup, warmup, and oracle checks are
outside the timed region. The output capture itself is shared Lua code and is
therefore part of every runtime's measured work, while terminal I/O is not.
Integer-result programs use exact output oracles. Floating-point programs
parse the upstream nine-decimal output and use a `5e-9` absolute tolerance.

`TestProgramsExecute` uses separate tiny smoke inputs so ordinary `go test
./...` remains quick; scaled measurement inputs exist only in
`BenchmarkPrograms`.

Run the correctness tests and a one-sample benchmark smoke with:

```sh
cd benchmarks
go test ./...
GOMAXPROCS=1 go test -run '^$' -bench '^BenchmarkPrograms$' \
  -benchmem -benchtime=1x -count=1 -cpu=1
```

Use a longer benchtime and repeated counts before publishing comparative
numbers.

## Provenance

The files were retrieved on 2026-07-27 from the official
[`benchmarksgame-sourcecode.zip`](https://salsa.debian.org/benchmarksgame-team/benchmarksgame/-/raw/40296663ed350d5fe4a6ab5e367bab61cb77c219/public/download/benchmarksgame-sourcecode.zip)
at the repository snapshot
[`40296663ed350d5fe4a6ab5e367bab61cb77c219`](https://salsa.debian.org/benchmarksgame-team/benchmarksgame/-/tree/40296663ed350d5fe4a6ab5e367bab61cb77c219);
its generated pages are under `public/program/`. The downloaded archive's
SHA-256 is
`aabcf6726cdc14f0f45b99e5daba48584f94bbb48883fd3711a1d040474d1cb4`.
The live program page, archive member, and SHA-256 of each vendored file are
pinned below.

| Vendored file | Official program page | Archive member | SHA-256 |
| --- | --- | --- | --- |
| `binarytrees.lua` | [binary-trees Lua #2](https://benchmarksgame-team.pages.debian.net/benchmarksgame/program/binarytrees-lua-2.html) | `binarytrees/binarytrees.lua-2.lua` | `58afb23db343d5c59e0c23b9d8b6188dab41fc378b0e588f965c0d24000173ed` |
| `fannkuchredux.lua` | [fannkuch-redux Lua #1](https://benchmarksgame-team.pages.debian.net/benchmarksgame/program/fannkuchredux-lua-1.html) | `fannkuchredux/fannkuchredux.lua` | `e6db90f101bafdfc2f213ce700d247e9b719c23a3782fc2c843c39ea5f1b157a` |
| `nbody.lua` | [n-body Lua #2](https://benchmarksgame-team.pages.debian.net/benchmarksgame/program/nbody-lua-2.html) | `nbody/nbody.lua-2.lua` | `841c93a66ccbf952ba188b96f35ff1267f68f75c8869bd3b019bcc3f99099c1a` |
| `spectralnorm.lua` | [spectral-norm Lua #1](https://benchmarksgame-team.pages.debian.net/benchmarksgame/program/spectralnorm-lua-1.html) | `spectralnorm/spectralnorm.lua` | `1acdfef437c9cae18f1dfb8394acc9144028d3e38ca4c581f7d699294fe81fed` |

The hash test makes accidental source edits visible. The files retain their
upstream contributor notices. [The corpus license](programs/LICENSE) is the
archive's top-level `LICENSE` copied byte-for-byte (SHA-256
`5bb4ce0a63be9ab37cd2e162375e4075535d341c18b3ca18d5cf3e4e07b7d010`).
