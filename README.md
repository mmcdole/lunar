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
`repeat...until`, numeric and generic `for`, and `break`. Full grammar
coverage, the executor, and the standard libraries follow in that order.

See [the architecture](docs/architecture.md) for the invariants and build
order.
