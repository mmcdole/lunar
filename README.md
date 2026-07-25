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
through `CallInto` add no boundary allocation. Contexts, coroutines, and the
standard libraries are still under construction.

See [the architecture](docs/architecture.md) for the invariants and build
order.
