# Architecture

Badger Lua targets Lua 5.1 semantics and PUC-Lua-class interpreter
performance. Its design starts from a private runtime model instead of an
embedding stack made from Go interface values.

## Invariants

1. Each reference object has exactly one canonical Go object. Immutable
   strings have value semantics; pointer identity is an internal optimization.
2. `Value` is an owning, opaque, compact value. It never hides a Go pointer in
   an integer.
3. Registers, table slots, closed upvalues, call arguments, and call results
   use the compact representation directly.
4. Prototypes are immutable after verification. Functions have fixed
   executable kind and upvalue shape; Lua-visible environments and upvalue
   contents remain controllably mutable.
5. A `State` directly owns runtime-wide resources and one main `Thread`.
   Coroutines are additional canonical Threads sharing that State.
6. A State has one active executor. Consumers serialize execution and
   mutation.
7. Standalone Table operations are raw. Operations that may invoke Lua live on
   State or Frame.
8. Borrowed views have distinct types and checked lifetimes. An owning Value is
   never secretly borrowed.
9. Reference values cannot cross State runtimes implicitly. Immutable strings
   are scalar values and may be shared by States and Prototypes.
10. Closing a State prevents execution and mutation but retained owning handles
    remain safe to inspect.

## Consumer interfaces

The friendly interface uses typed constructors and observers, direct table
methods, and protected calls returning owned result slices.

The low-level interface will add:

- `Frame` for direct typed callback arguments and results;
- `CallInto` for caller-owned result storage;
- `Cursor` for allocation-free table traversal after setup; and
- `Builder` for bulk construction whose allocations follow storage growth,
  not inserted-value count.

These are two interfaces at one seam. Neither wraps or copies canonical
objects.

## Source layout

The root `lua` package owns both the public interface and runtime
implementation. This keeps compact values private without introducing an
artificial package seam.

Files are organized by substantial runtime concepts:

- `value.go`: compact values, kinds, scalar semantics, and object identity;
- `state.go`: runtime ownership, lifecycle, globals, userdata, and errors;
- `string.go`: State-neutral immutable strings, stable hashing, and bounded
  runtime-local short-string reuse;
- `table.go`: dense and hash storage plus raw table semantics;
- `opcode.go`: canonical Lua 5.1 instruction encoding;
- `lexer.go`: direct source scanning with one-token lookahead;
- `compiler.go`: per-compilation ownership plus mutable function emission
  state;
- `parser.go`: recursive-descent chunk, statement, scope, and name parsing;
- `expression.go`: transient expression descriptors, precedence, and source
  grammar;
- `codegen.go`: expression lowering, register placement, conditional exits,
  and constant folding;
- `compiler_call.go`: contiguous call windows, method calls, tail calls, and
  Lua 5.1 multiple-result adjustment;
- `constructor.go`: table-constructor grammar, record stores, list batching,
  and allocation hints;
- `prototype.go` and `verify.go`: exact-size immutable executable metadata
  and its publication-time verifier;
- `function.go`: canonical functions and compact upvalues;
- later `load.go`: source and bytecode loading;
- later `execute.go`: dispatch and activations;
- later `call.go`: Lua/native calls, frames, outcomes, and continuations; and
- later `library_*.go`: standard libraries using native frames.

A file is split only when the resulting modules have independently meaningful
interfaces or invariants. Tiny helper and test files are avoided.

## Compiler lowering

The compiler delays scalar result placement until an expression reaches its
consumer. Arithmetic, lookup, unary, and concatenation instructions are
emitted with an unresolved destination and bound directly to the final local,
return slot, or enclosing operand. Sealing rejects any unresolved result.

Comparisons and logical operators remain control-flow expressions until a
value is required. Separate true and false exit lists preserve Lua's
operand-valued `and` and `or` semantics without eagerly constructing
booleans. Flat, right-associated concatenation chains are emitted as one
instruction over a contiguous register span. The executor must reduce that
span from right to left so `__concat` calls observe Lua 5.1 ordering.

An indexed expression retains its table register and RK key until it is read
or assigned. Its descriptor records the lowest temporary owning those
operands, allowing a chained read to overwrite that slot only after both
operands have been captured by `GETTABLE`. This preserves left-to-right
evaluation without allocating an intermediate node or extending temporary
lifetimes across the enclosing expression.

A function call owns one contiguous register window: callable, implicit
receiver when present, explicit arguments, then results. Only the final
unparenthesized call or vararg expression in a list may remain open. Open
producers are emitted directly beside their consuming call, return, or
`SETLIST`; prototype verification rejects any broken adjacency. `SELF` may
legally overlap its output base with its receiver register, so the executor
must capture the receiver and key before writing either output.

A table constructor pins its table at the bottom of its temporary register
suffix. Record fields are evaluated and stored immediately; list fields are
staged contiguously above the table and flushed in 50-value `SETLIST` blocks.
One pending list field is retained so only a syntactically final call or
vararg can stream an open result count. `NEWTABLE` receives floating-byte
array and record hints after the complete constructor is known; the executor
must decode both operands before reserving storage.

## Build order

1. Canonical Value and object ownership.
2. State-neutral immutable strings and compact tables.
3. A direct recursive-descent compiler producing verified immutable
   Prototypes without retaining an AST.
4. Executor, calls, upvalues, errors, and coroutines.
5. Native-frame standard libraries and embedding operations.
6. Debug facilities and optional extensions.
7. Profile-driven quickening, inline caches, and executor specialization.

The compiler remains in the root package so it can build private compact
constants without introducing an exported intermediate representation.
Pattern matching is isolated only when the string library needs that seam.

## Qualification

Correctness is measured against the Lua 5.1 language behavior, not another Go
implementation's internal quirks. Official Lua 5.1 test scripts may be
included with their own provenance and license. Focused Go tests cover the
public interface, ownership, lifetime, race, and invalid-use contracts.

Performance comparisons use:

- `github.com/mmcdole/badger-lua` commit
  `169b37d` as the frozen predecessor;
- PUC Lua 5.1.5 as the interpreter target; and
- LuaJIT 2.1 with JIT disabled as an additional interpreter reference.

Canonical benchmark results will record toolchain, platform, binary and
fixture hashes, time, allocations, allocated bytes, retained heap, and GC CPU.
Rolling local results belong under ignored `.bench/`, not in source control.
