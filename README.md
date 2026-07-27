# Badger Lua

Badger Lua is a pure-Go implementation of Lua 5.1. It is being built around
one compact runtime representation, opaque canonical objects, and two ways to
embed Lua:

- a friendly typed Go interface for ordinary applications; and
- a low-level `Frame`/`CallInto` interface for allocation-sensitive callbacks
  and calls, with borrowed table traversal and bulk construction planned.

Both interfaces operate on the same values, tables, functions, and threads.
Executable state has one authoritative representation.
Badger Lua requires Go 1.24 or newer.

The implementation is under active construction. The compact object model,
Lua 5.1 bytecode, immutable verified prototypes, direct lexer, and the
expression, assignment, call, constructor, and closure portions of the
source compiler are in place, including conditionals, `while`,
`repeat...until`, numeric and generic `for`, and `break`. The private
executor runs compact calls, closures, upvalues, varargs, control flow,
numeric arithmetic and comparison, numeric and generic iteration, and their
Lua 5.1 metamethods. It also runs globals, table reads and writes, method
lookup, table constructors, and Lua 5.1 `__index` and `__newindex`
resolution. It also implements Lua 5.1 length and right-to-left concatenation
semantics. Lua can now call allocation-free Go functions through borrowed
typed Frames over the same compact stack, including calls from metamethod and
iterator continuations. Go can compile State-neutral Prototypes, load them
into distinct States, and invoke Lua or callable objects through protected
`Call` and caller-buffer `CallInto` boundaries. Warmed nonallocating calls
through `CallInto` add no boundary allocation. The explicitly opened base
library includes Lua 5.1 `pcall` and `xpcall` over the same executor; their
warmed success paths allocate nothing. State-local collection controls and
weakly registered `newproxy` userdata complete that surface. Canonical
coroutines now suspend and resume the same compact activation stack, including
the Lua 5.1
`coroutine` library and exact native/metamethod yield barriers; warmed resume
paths allocate nothing. A native callback can synchronously reenter Lua
through protected `Frame.Call` or caller-buffer `Frame.CallInto` without a
second executor or object representation; warmed `CallInto` paths allocate
nothing. Context-aware calls and coroutine resumes provide bounded cooperative
cancellation without changing the raw execution path or retaining a caller's
context across suspension. Embedders enforcing deadlines or stopping runaway
scripts should use `CallContext`, `CallIntoContext`, or the context-aware
resume APIs rather than a Lua debug hook. The explicitly opened `math`,
`table`, and `string` libraries provide the Lua 5.1 surfaces over compact
scalar arguments and raw storage. This includes PUC-compatible table sorting,
byte-oriented patterns and formatting, reentrant replacement callbacks, and
native Lua 5.1 binary chunks produced by `string.dump`. Source or binary
chunks can be loaded from fixed strings, `io.Reader` streams, or files through
`LoadString`, `Load`, and `LoadFile`, with context-aware variants. Fixed source
strings scan directly; streamed input is consumed as immutable pieces and
preserves reader error identity. The base library exposes Lua 5.1's `load`,
`loadstring`, `loadfile`, and `dofile`; file loading honors a leading
interpreter line, and
`dofile` returns all chunk results through the compact stack. Each State is
configured with borrowed standard input, output, and error streams;
argumentless loaders and Lua 5.1 `print` use those streams without
process-global redirection. The explicit `package` library provides
registry-backed `require`, preload and Lua-file searchers, `module`, and
`package.seeall`. Native C modules expose Lua 5.1's
standard unavailable-platform result because the compact Go runtime does not
pretend to implement the C `lua_State` ABI. Warmed scalar and sequence library
calls and cached `require` calls allocate nothing. The explicit `io` library
adds opaque managed files, State-local defaults, compact binary-safe reads,
buffered writes, seeking, line iteration, process pipes, and deterministic
owned-resource cleanup. The explicit `os` library provides process CPU time,
per-State calendar conversion and C-locale formatting, environment and
filesystem operations, secure temporary names, deterministic locale selection,
and an embedding-safe `os.exit` request returned to Go without terminating the
host.
Its `os.execute` invokes the platform shell and returns Lua 5.1's single
numeric status while inheriting the embedding process's environment, working
directory, and actual standard descriptors. The explicitly opened `debug`
library implements Lua 5.1 stack inspection, registry, environment,
metatable, and upvalue access, tracebacks, and the interactive console.
`debug.sethook` and `debug.gethook` are deliberately deferred: exact
instruction hooks measurably slowed ordinary execution in the explored
designs. A State-local semantic collector handles cycles, weak tables,
userdata finalization, and explicit Lua 5.1 collection controls independently
of Go's process-wide collector. Embedders use the same machinery through
`State.Collect` and `State.HeapBytes`; native callbacks use the matching
`Frame` methods. Retained allocation growth automatically schedules semantic
cycles at rooted executor safe points; consumers do not need to drive normal
collection manually.

See [the architecture](docs/architecture.md) for the invariants and build
order, [semantic collection](docs/collection.md) for the ownership and Lua GC
design, and [the performance work](docs/performance.md) for current evidence
and qualification gates.

Adapted reference algorithms retain their original permissive license in
[the third-party notices](THIRD_PARTY_NOTICES.md).
