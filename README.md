# Badger Lua

Badger Lua is a pure-Go implementation of Lua 5.1. It is being built around
one compact runtime representation, opaque canonical objects, and two ways to
embed Lua:

- a friendly typed Go interface for ordinary applications; and
- a low-level frame and table interface for allocation-sensitive code.

Both interfaces operate on the same values, tables, functions, and threads.
Executable state has one authoritative representation.

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
library now includes Lua 5.1 `pcall` and `xpcall` over the same executor;
their warmed success paths allocate nothing. Canonical coroutines now suspend
and resume the same compact activation stack, including the Lua 5.1
`coroutine` library and exact native/metamethod yield barriers; warmed resume
paths allocate nothing. A native callback can synchronously reenter Lua
through protected `Frame.Call` or caller-buffer `Frame.CallInto` without a
second executor or object representation; warmed `CallInto` paths allocate
nothing. Context-aware calls and coroutine resumes provide bounded cooperative
cancellation without changing the raw execution path or retaining a caller's
context across suspension. The explicitly opened `math`, `table`, and
`string` libraries provide the Lua 5.1 surfaces over compact scalar arguments
and raw storage. This includes PUC-compatible table sorting, byte-oriented
patterns and formatting, reentrant replacement callbacks, and native Lua 5.1
binary chunks produced by `string.dump`. Source or binary chunks can be loaded
from fixed strings, `io.Reader` streams, or files through `LoadString`, `Load`,
and `LoadFile`, with context-aware variants. Fixed source strings scan
directly; streamed input is consumed as immutable pieces and preserves reader
error identity. The base library exposes Lua 5.1's `load`, `loadstring`,
`loadfile`, and `dofile`; file loading honors a leading interpreter line, and
`dofile` returns all chunk results through the compact stack. Warmed scalar and
sequence library calls allocate nothing. The remaining standard libraries are
still under construction.

See [the architecture](docs/architecture.md) for the invariants and build
order. Adapted reference algorithms retain their original permissive license
in [the third-party notices](THIRD_PARTY_NOTICES.md).
