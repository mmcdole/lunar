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
- `number.go`: deterministic numeric-string syntax and compact numeric
  coercion;
- `metamethod.go`: raw event lookup and shared-handler selection;
- `opcode.go`: canonical Lua 5.1 instruction encoding;
- `lexer.go`: direct source scanning with one-token lookahead;
- `compiler.go`: per-compilation ownership plus mutable function emission
  state;
- `parser.go`: recursive-descent chunk, statement, scope, and name parsing;
- `expression.go`: transient expression descriptors, precedence, and source
  grammar;
- `codegen.go`: expression lowering, register placement, conditional exits,
  and constant folding;
- `assignment.go`: iterative target capture, alias preservation, result
  adjustment, and ordered stores;
- `compiler_function.go`: lexical capture resolution, function bodies,
  closure publication, and function-definition sugar;
- `compiler_call.go`: contiguous call windows, method calls, tail calls, and
  Lua 5.1 multiple-result adjustment;
- `compiler_loop.go`: loop scopes, `break` exits, and numeric and generic
  iteration layouts;
- `constructor.go`: table-constructor grammar, record stores, list batching,
  and allocation hints;
- `prototype.go` and `verify.go`: exact-size immutable executable metadata
  and its publication-time verifier;
- `function.go`: canonical functions and compact upvalues;
- `call.go`: compact activations, shared-stack call layout, varargs, tail
  replacement, and result adjustment;
- `execute.go`: the compact instruction switch, cold execution driver, calls,
  runtime faults, and traceback capture;
- `execute_numeric.go`: cold numeric coercion, comparisons, numeric-loop
  preparation, and numeric event selection;
- `execute_string.go`: primitive length, batched concatenation, and string
  event reduction;
- `execute_continuation.go`: allocation-free suspension and resumption around
  Lua calls that require post-call execution work;
- `execute_table.go`: globals, table access, method lookup, constructors,
  list installation, and indexed metamethod resolution;
- later `load.go`: source and bytecode loading;
- later native-frame additions to `call.go`: Go calls, outcomes, and
  continuations; and
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
booleans. Statement conditions consume those exits directly; `if` chains are
lowered iteratively, and each arm closes its own captured locals before
escaping to the merge point. A trailing `NOT` is removed when its only
consumer is control flow, replacing it with one test of the original register
at the opposite polarity. Flat, right-associated concatenation chains are
emitted as one instruction over a contiguous register span. The executor must
reduce that span from right to left so `__concat` calls observe Lua 5.1
ordering.

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
legally overlap its output base with its receiver register. The executor
therefore retains the receiver, publishes it to `R(A+1)`, then reads the key
and performs lookup in the same order as Lua 5.1. This preserves even the
observable behavior of verified bytecode whose key register overlaps
`R(A+1)`.

A table constructor pins its table at the bottom of its temporary register
suffix. Record fields are evaluated and stored immediately; list fields are
staged contiguously above the table and flushed in 50-value `SETLIST` blocks.
One pending list field is retained so only a syntactically final call or
vararg can stream an open result count. `NEWTABLE` receives floating-byte
array and record hints after the complete constructor is known; the executor
must decode both operands before reserving storage.

An assignment captures compact local, upvalue, global, or indexed targets
without retaining general expression nodes. All table and key operands are
evaluated left-to-right before the right-hand side. If a later local target
would overwrite a register used by an earlier indexed target, one temporary
snapshots that local and every conflicting operand is rewritten to it. Values
are then assigned right-to-left and the statement releases its entire
temporary suffix once. Equal target/value counts keep the final expression
deferred so it can write directly to the rightmost destination.

A nested function is sealed before its parent publishes it. Its ordered
upvalue layout records only whether each value comes from a parent register
or a parent upvalue and the corresponding compact index. `CLOSURE` is followed
immediately by canonical `MOVE` or `GETUPVAL` binding words; these are metadata
consumed by closure creation, not ordinary instructions. Transitive capture
creates one pass-through upvalue per function level, while repeated references
reuse one descriptor.

Lexical blocks record whether they own a captured local. Continuing past such
a block emits one `CLOSE` at the lowest captured register before that register
suffix can be reused. Function return, tail-call replacement, and error
unwinding instead close the whole activation. Closure creation must read every
binding before writing its destination, which makes `local function f()`
recursive even when the closure destination and captured local are the same
register.

Loops keep their break targets separate from lexical blocks. A `break` closes
only captured scopes inside the nearest loop, then joins an allocation-free
exit list. `while` closes body locals before its back edge. `repeat...until`
keeps body locals visible to the condition; when one is captured, distinct
true and false cleanup paths close that iteration before exit or repetition.
Numeric `for` uses the canonical four-register index/limit/step/visible-value
window. Generic `for` uses three control registers plus visible results and
reserves the three call slots required by `TFORLOOP`, even when fewer results
are requested. The verifier requires canonical numeric-loop pairs and rejects
iterator frames too small for that call window.

Source vararg functions retain Lua 5.1's default compatibility layout: an
implicit `arg` local follows fixed parameters, and it is materialized by call
setup only when the function never uses `...`. Encountering a vararg
expression clears that requirement. This preserves the standard Lua 5.1
distribution's behavior without putting an argument table on functions that
use the current vararg expression.

## Calls and activations

Every Thread owns one contiguous compact-slot stack shared by all active Lua
calls. An activation stores only a canonical Function, register base, result
destination, published program counter, compressed eliminated-tail-call
count, the caller's saved frame high-water mark, and requested result count.
On 64-bit systems it is 32 bytes. Register windows are ranges in the shared
stack; they are not slices retained by the activation, so stack growth cannot
invalidate a frame or open upvalue. The saved high-water mark makes
dead-suffix cleanup constant-time even when nested frame ends are not
monotonic.

Fixed-argument functions reuse the call's argument area as register zero.
Vararg functions leave their original arguments below the activation and copy
only fixed parameters to a fresh register window above them, matching Lua
5.1's call layout. The activation's result destination and parameter count
therefore locate and count the hidden varargs without another field, pointer,
or slice. Thread top is the fixed frame end during ordinary execution and
becomes the exact boundary only while arguments or results are open.

Tail calls move their callable and arguments to the current activation's
original result destination, close its open upvalues, and replace the
activation in place. A saturating count records eliminated activations without
growing the hot frame or expanding a long tail-recursive traceback. Normal Lua
calls and returns therefore remain iterative inside one executor rather than
recursing through Go. Inactive stack slots and popped activations are cleared
promptly so a warm reusable stack does not retain dead Lua graphs.

### Direct Lua-call checkpoint

The compact frame is not by itself a fast call path. At core checkpoint
`83dcf30`, diagnostic runs on an Apple M3 Pro with Go 1.25.1 put a
fixed-arity loop making 1,000 Lua-to-Lua calls at about 48.1 microseconds,
versus about 31.5 microseconds in frozen Badger `169b37d`. A 128-call upstream
GopherLua `75f4976` fixture projected to about 35.5 microseconds per 1,000
calls. The harnesses were not identical, so these figures locate a problem;
they are not a qualification result. Their allocation-free execution makes
heap allocation unlikely to be the primary cause, but does not isolate call
transition cost from loop, dispatch, and callee-body work.

PUC Lua 5.1 provides the relevant control-flow model. `OP_CALL` publishes the
caller's PC, invokes `luaD_precall`, and, for a Lua callee, jumps to a
`reentry` label inside `luaV_execute`. `OP_RETURN` invokes `luaD_poscall` and
jumps to the same label. Ordinary Lua calls do not return through a separate
outer dispatcher. The reentry reloads the closure, base, constants, and PC
after the stack may have moved.

Badger should translate that model rather than reproduce its C details:

1. Before implementation, add identical source and loop-count fixtures to
   Badger, frozen Badger, upstream GopherLua, and PUC 5.1. Compile and warm
   each engine independently outside the timed region.
2. Keep direct fixed-arity Lua calls and their common fixed-result returns
   inside the executor, with a frame-reload label after each transition.
3. Limit the first fast path to a direct Lua Function, fixed arguments and
   results, a non-vararg callee, sufficient value/frame capacity, satisfied
   limits, and no hook, native, yield, `__call`, or other semantic slow path.
4. Use a small trusted helper for that transition. Sealed prototypes and
   canonical Functions have already established ownership, executable shape,
   and bytecode validity; internal calls must not repeat public-boundary
   validation.
5. On return, close open upvalues before moving or clearing registers, place
   results in the original callable slot, and restore the caller through the
   same reload seam. Preserve `stopDepth`, pending continuation resumption,
   caller PC and extents, top, tail-call bookkeeping, and dead-root clearing.
   Do not add a second dispatch loop.
6. Make available value/frame capacity the cheap branch. Stack growth,
   resource failures, varargs, open calls, `__call`, native functions,
   yielding, hooks, and initially pending continuations retain explicit slow
   paths.
7. Profile register nil-filling, dead-root clearing, result adjustment, and
   upvalue checks independently. PUC clears the complete unused register
   window, but Go pointer writes and garbage-collector liveness have different
   costs. Skipping initialization would require new definite-assignment and
   observability analysis; the current verifier does not prove it.
8. Add callsite caching only if profiles still justify it after the structural
   transition is fixed.

The qualification gate is a same-source matrix covering fixed zero/one/many
results, open results, excess and missing arguments, varargs, recursion, tail
calls, closures and upvalues, nested continuations, protected-call boundaries,
`__call`, limits, errors, and future native/yield cases. Warm fixed calls must
remain allocation-free, beat frozen Badger and upstream GopherLua, and move
toward the measured PUC 5.1 result. Repeated samples and a declared
statistical comparison must show that unrelated numeric and table kernels do
not regress. Assembly inspection must also confirm that the reload path does
not enlarge the persistent activation or materially expand the executor's hot
Go frame.

## Execution

Execution is split into one iterative dense instruction switch and one cold
driver. The switch retains the current Function, Prototype, register base,
program counter, code, constants, upvalues, and value stack in locals. It
directly executes control flow, moves, loads, upvalue access, number-only
arithmetic and comparisons, and prepared numeric loops. Ordinary instructions
neither publish frame state nor reread the activation.

An instruction that can call or re-enter Lua, grow the execution stack,
coerce a string, invoke a metamethod, or construct an error publishes its
program counter and returns that instruction to the driver. The driver
performs the cold operation and re-enters the same switch. This boundary is
deliberately an instruction value, not an interface or handler object. It
keeps call setup and semantic temporaries out of the switch's live set, which
matters because Go allocates registers for a whole function rather than for
each switch arm. The design keeps one dispatch implementation while allowing
uncommon semantics to have ordinary, testable functions.

While the switch is active, the activation stack and compact value stack
cannot grow, Lua cannot be re-entered, and its cached frame pointer and slices
remain valid. Calls, returns, stack growth, errors, and future yield points are
therefore explicit reload seams.

Direct Lua functions take the inline call path. Other values use a cold raw
`__call` lookup, insert the original value as argument one, and then enter the
same activation machinery. Resolution is not recursive: a non-function
`__call` value is a call error, as in Lua 5.1. Limit checks complete before the
call window is changed, so a failed metamethod call is atomic.

Lua-to-Lua calls append or replace compact activations and continue in the
same Go invocation. They never recurse through Go. Returns copy compact slots
directly into the caller's result window, and open calls and returns use
Thread top as their exact dynamic boundary. Closure creation captures
absolute stack indexes, reads every binding word before publishing the new
closure, and adopts its engine-owned upvalue slice without another copy.
Closure instantiation and open-vararg stack growth stay behind cold helpers so
their allocation and error machinery does not enlarge the always-hot switch
frame.

Runtime number coercion accepts numbers and complete numeric strings. The
shared parser recognizes signed decimal fractions and exponents, signed
hexadecimal integers, and the six ASCII whitespace bytes used by Lua. Finite
warm-path parsing does not allocate. It intentionally rejects locale-specific
spellings, named infinities and NaNs, hexadecimal floats, embedded NUL, and
trailing data instead of inheriting platform-dependent libc behavior.

Number-only arithmetic remains entirely in compact slots. String coercion and
metamethod selection are cold. Binary arithmetic checks the left operand's
event before the right operand's and calls the selected value with the
original operands; unary minus passes its operand twice, matching Lua 5.1.
Equality never coerces, and distinct tables or userdata invoke `__eq` only
when both sides name the same raw handler. Ordering compares numbers or byte
strings directly. Other like-typed values require matching handlers;
`<=` falls back to reversed `__lt` and negates its result.

Length of strings counts bytes, and table length computes a raw Lua 5.1
border; neither primitive consults `__len`. Other values use the exact Lua 5.1
left-then-nil event lookup and pass both values to the selected handler.
Primitive string and table length remain in the instruction loop, with the
table border search isolated in a non-reentrant helper so its loop state does
not enlarge the dispatch frame.

Concatenation stays on the compact stack and reduces its register span from
right to left. Each maximal adjacent string/number suffix becomes one output
string, avoiding quadratic pairwise copying and temporary heap strings for
numbers. Numeric text follows deterministic Lua 5.1 `%.14g`-style formatting.
Non-coercible pairs select `__concat` from the left value before the right and
suspend with one marked continuation. The returned value is installed at the
exact reduced pair before reduction continues, so later pairs observe event
mutation performed by earlier handlers. Public Values are never constructed
by this path.

Generic iteration retains the canonical generator, state, and control in
`R(A)` through `R(A+2)`. Each `TFORLOOP` stages those values in its verified
three-slot call window, invokes the generator with exactly `(state, control)`,
and requests exactly `C` results. A callable object receives itself before
those two arguments through the ordinary one-level `__call` rule. Short
returns are nil-filled, excess returns are discarded, and only a nil first
result terminates the loop; false is a valid next control value. A nonnil
first result replaces hidden control before the paired jump is taken.
Configured frame and value failures are checked before the call window
changes, so failed iteration setup is atomic.

A Lua call with pending executor work appends a compact continuation beside,
rather than inside, the activation. The continuation records only result
placement, comparison branching, the remaining concatenation range, or
generic-iterator completion and survives ordinary nested and tail calls. It
is removed on completion or centralized unwind. Frame and value limits are
checked before scratch arguments, an activation, or a continuation are
published, so resource failure is atomic.

Table instructions enter small non-reentrant compact-slot read and write
helpers directly from the switch. They complete raw hits, ordinary
no-metatable misses, and raw inserts without returning through the driver.
Table storage may grow there because it cannot replace the activation or
value-stack slices cached by the switch. A miss that needs semantic resolution
returns a private outcome opcode; the cold helper therefore continues at
metamethod lookup without repeating the raw table probe. These outcome opcodes
are never legal in a Prototype.

Raw non-nil hits bypass metamethods. Missing reads and writes follow at most
100 `__index` or `__newindex` targets; only a Function-valued event is called,
while every other event value is the next target. Getter continuations retain
one result, and setter continuations request and retain none. Nil and NaN are
read misses but remain invalid keys when resolution reaches a table write. An
existing write resolves its array or hash location once and updates that
exact location; it does not repeat the key lookup after deciding that
`__newindex` must be bypassed.

Globals use the executing Function's environment, never an implicit State
global. `NEWTABLE` decodes both floating-byte operands and clamps their
advisory capacities before allocation. `SETLIST` is raw, consumes open
results through Thread top, and installs an ordinary contiguous constructor
batch with one array growth pass. These paths operate only on compact slots;
they do not materialize public Values.

The raw table seam enlarges the arm64 `runInstructions` frame from 80 to 160
bytes. This is one constant Go frame for the interpreter invocation, not one
frame per Lua activation. The larger frame is retained only because
representative dense, sparse, string-field, global, method, missing-field,
polymorphic, and constructor workloads improve while allocation counts remain
unchanged. Full metamethod cases retain the cold path. Further in-switch
specialization remains subject to both assembly inspection and
whole-workload gates.

Numeric `for` converts its hidden initial value, limit, and step exactly once
at `FORPREP`, stores numbers back into the canonical four-register window,
and keeps the visible loop variable separate. Zero and NaN steps follow Lua
5.1's comparison rules rather than being rejected as extensions.

The executor assumes only immutable, sealed Prototypes. Prototype
verification is therefore responsible for instruction bounds, register and
constant operands, closure binding words, test/jump pairs, and open-result
adjacency. Unsupported instructions currently fail at the private execution
boundary; source execution remains private until every verified opcode family
has a complete implementation.

Runtime failure snapshots the active Lua trace before one centralized unwind
closes upvalues, drops activations, and clears dead stack roots. Ordinary Lua
control flow does not use Go panic or interface-valued per-opcode results. The
executor returns one small outcome only when it reaches its requested call
depth or fails; coroutine support will add a yield outcome when it exists.

## Build order

1. Canonical Value and object ownership.
2. State-neutral immutable strings and compact tables.
3. A direct recursive-descent compiler producing verified immutable
   Prototypes without retaining an AST.
4. Executor, PUC-style direct-call reentry, upvalues, errors, and coroutines.
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
