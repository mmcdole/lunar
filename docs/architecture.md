# Architecture

Badger Lua targets Lua 5.1 semantics and PUC-Lua-class interpreter
performance. Its design starts from a private runtime model instead of an
embedding stack made from Go interface values.

## Invariants

1. Each reference object has exactly one canonical Go object. Immutable
   strings have value semantics; pointer identity is an internal optimization.
   Canonical objects are retained and passed by pointer; copying a pointer is
   ordinary, but copying or overwriting the pointed-to struct is unsupported
   and guarded by `go vet`.
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

The low-level interface currently includes:

- `Frame` for direct typed callback arguments and results;
- `CallInto` for caller-owned result storage.

It will later add:

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
- `native.go`: borrowed native call frames, typed argument and result access,
  captured values, terminal outcomes, and the Go callback seam;
- `load.go`: public source compilation and State-bound Function loading;
- `invoke.go`: protected main-Thread calls and owning or caller-supplied result
  egress;
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
invalidate a frame. Open upvalues use typed pointers into the value stack;
the one operation that replaces its backing array retargets every open cell
before execution resumes. The saved high-water mark makes dead-suffix cleanup
constant-time even when nested frame ends are not monotonic.

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
`a845765`, an identical precompiled and warmed source benchmark on an Apple M3
Pro with Go 1.25.1 measured these median costs per 1,000 direct fixed
Lua-to-Lua calls:

| Call shape | Badger | Frozen Badger `169b37d` | GopherLua `75f4976` | PUC 5.1.5 |
| --- | ---: | ---: | ---: | ---: |
| no results | 34.7 us | 29.5 us | 39.0 us | 19.3 us |
| one result | 35.2 us | 29.1 us | 40.7 us | 19.1 us |
| two results | 41.0 us | 39.9 us | 50.7 us | 24.9 us |
| one result from a closed upvalue | 36.5 us | 29.6 us | 42.0 us | 18.2 us |

Each engine compiled and initialized the same source outside the timed region,
ran 10,000 untimed warmups, and then ran 15 samples of 5,000 outer
invocations. Each outer invocation made 1,000 Lua-to-Lua calls. Current and
frozen Badger executed without heap allocation; GopherLua allocated about
14 KB in 54 allocations per outer invocation. Current Badger beats GopherLua
in every cell, trails frozen Badger in every cell, and has a 1.82-times
geometric-mean gap to PUC. The absolute cross-language ratios remain
directional because the engines were measured sequentially without CPU
affinity or power-state control; the relative diagnosis is large enough to be
unambiguous.

A CPU profile of the one-result case attributes about 34.5% of samples
cumulatively to call entry and 15.5% to return completion. Window checks,
activation commit, value reservation, result adjustment, and publication are
individually visible despite zero heap allocation. This establishes that the
gap is primarily repeated control and state transition work, not construction
of the compact activation and not garbage collection. Profiles and raw local
results remain outside source control; this section retains the actionable
conclusion and enough detail to reproduce the checkpoint.

PUC Lua 5.1 provides the relevant control-flow model. `OP_CALL` publishes the
caller's PC, invokes `luaD_precall`, and, for a Lua callee, jumps to a
`reentry` label inside `luaV_execute`. `OP_RETURN` invokes `luaD_poscall` and
jumps to the same label. Ordinary Lua calls do not return through a separate
outer dispatcher. The reentry reloads the closure, base, constants, and PC
after the stack may have moved.

Badger translates that model rather than reproducing its C details:

1. Before implementation, add identical source and loop-count fixtures to
   Badger, frozen Badger, upstream GopherLua, and PUC 5.1. Compile and warm
   each engine independently outside the timed region.
2. Keep direct fixed-arity Lua calls and their common fixed-result returns
   inside the executor, with a frame-reload label after each transition.
3. Limit the first fast path to a direct Lua Function, fixed arguments and
   results, a non-vararg callee, sufficient value/frame capacity, satisfied
   limits, and no hook, native, yield, `__call`, or other semantic slow path.
4. Publish the caller PC before attempting entry, then use a small trusted
   helper whose false result leaves values, frames, top, and extent unchanged.
   Sealed prototypes and canonical Functions have already established
   ownership, executable shape, and bytecode validity; internal calls must not
   repeat public-boundary validation.
5. On return, close open upvalues before moving or clearing registers, place
   results in the original callable slot, and restore the caller through the
   same reload seam. Preserve `stopDepth`, pending continuation resumption,
   caller PC and extents, top, tail-call bookkeeping, and dead-root clearing.
   A direct return is eligible only when its result count is fixed, the
   surviving caller remains above `stopDepth`, and popping the callee does not
   make a continuation ready. A continuation attached to a deeper frame does
   not by itself disqualify the call. Do not add a second dispatch loop.
6. Make available value/frame capacity the cheap branch. Stack growth,
   resource failures, varargs, open calls, `__call`, native functions,
   yielding, hooks, and continuation resumption retain explicit slow paths.
7. Profile register nil-filling, dead-root clearing, result adjustment, and
   upvalue checks independently. PUC clears the complete unused register
   window, but Go pointer writes and garbage-collector liveness have different
   costs. Skipping initialization would require new definite-assignment and
   observability analysis; the current verifier does not prove it. Internal
   slot zero is a numeric zero rather than Lua nil, so unused registers and
   missing results must use the canonical nil slot.
8. Add callsite caching only if profiles still justify it after the structural
   transition is fixed.

The qualification gate is a same-source matrix covering fixed zero/one/many
results, open results, excess and missing arguments, varargs, recursion, tail
calls, closures and upvalues, nested continuations, protected-call boundaries,
`__call`, limits, errors, and future native/yield cases. Warm fixed calls must
remain allocation-free, beat the frozen Badger and upstream GopherLua
comparators on the identical fixture, and close a material part of the current
roughly 1.82-times geometric-mean gap to PUC 5.1. The first implementation
target is at least a 20% reduction in all four fixed-call cells, with no cell
slower than its checkpoint beyond measurement noise. That target is a tranche
gate, not a claim that the remaining PUC gap is irreducible. Repeated samples
and a declared statistical comparison must show that unrelated numeric and
table kernels do not regress. Assembly inspection must also confirm that the
reload path does not enlarge the persistent activation or materially expand
the executor's hot Go frame.

The implemented direct-reentry path passes that gate:

| Call shape | Before reentry | Direct reentry | Change |
| --- | ---: | ---: | ---: |
| no results | 34.674 us | 25.929 us | -25.2% |
| one result | 35.233 us | 26.072 us | -26.0% |
| two results | 41.011 us | 32.146 us | -21.6% |
| one result from a closed upvalue | 36.495 us | 27.757 us | -23.9% |

The four-cell geometric mean is 24.2% faster than the pre-reentry checkpoint,
12.2% faster than frozen Badger, and 35.0% faster than GopherLua. Every cell
is allocation-free and faster than both Go comparators. The remaining
geometric-mean gap to PUC 5.1.5 at that checkpoint is 37.9%.

A follow-up profile showed that direct reentry still passed fixed calls
through generic layout, vararg, activation-append, and result-adjustment
machinery. The fixed transition now relies on facts already proved by the
bytecode verifier and private runtime:

- fixed argument and result windows fit the caller's live frame, so only the
  callee frame end can extend the value stack;
- value and activation capacities cannot exceed their immutable configured
  limits;
- entering a call cannot reduce the live extent, so it has no dead suffix to
  clear;
- a successfully returning activation is unobservable and need not publish
  its final program counter; and
- zero, one, and two adjusted results can be moved directly, with both sources
  loaded before an overlapping two-result write.

The checked and trusted paths still share the canonical fixed-register setup,
activation publication, and return mutation. The trusted path merely resolves
policy before entering those operations. Upvalues close before result moves,
pointer writes retain Go barriers, missing results receive canonical nil,
dead roots are cleared, and larger result counts retain overlap-safe slice
copying.

A fresh alternating run of the same exact-source protocol measured:

| Call shape | Direct reentry `dabfc53` | Fixed transition | Change | PUC 5.1.5 |
| --- | ---: | ---: | ---: | ---: |
| no results | 25.384 us | 18.812 us | -25.9% | 22.160 us |
| one result | 26.103 us | 20.559 us | -21.2% | 19.546 us |
| two results | 32.157 us | 27.312 us | -15.1% | 26.236 us |
| one result from a closed upvalue | 27.765 us | 21.553 us | -22.4% | 18.164 us |

Every Badger cell remains allocation-free, and every improvement over
`dabfc53` is significant at p < 0.0001. The four-cell geometric mean is 21.2%
faster than `dabfc53`, directionally 31.1% faster than frozen Badger and 49.2%
faster than GopherLua, and 2.5% slower than PUC 5.1. Zero-result calls beat
PUC in this run; one- and two-result calls are within 5.2%, while closed
upvalue access is the remaining 18.7% outlier. Absolute cross-language ratios
remain directional because the binaries execute sequentially and the PUC
phase visibly drifts, but fixed Lua calls have reached PUC geometric-mean
territory.

The persistent activation remains 32 bytes and the arm64 executor frame
remains exactly 160 bytes. The executor text grows by 16 bytes. Returning the
already-fetched instruction through `code[pc-1]`, rather than retaining a
separate instruction local across the exit, is intentional: it prevents the
Go compiler from adding 16 bytes to the executor frame. The next call-related
question was closed-upvalue addressing, not another callsite cache.

### Permanent upvalue cells

An upvalue has one permanent typed cell pointer. While it is open, the pointer
addresses its captured register in the Thread value stack and its embedded
storage records the absolute stack index used for ordering and relocation.
When the register closes, its value moves into that storage and the pointer is
retargeted to it. Reads and writes therefore use the same single pointer
indirection in both states; the executor does not branch on open versus closed
or reload a Thread, slice, and dynamic index.

The representation is 32 bytes on 64-bit systems:

```text
cell *slot
next *upvalue
storage slot
```

This follows the permanent-cell principle used by PUC Lua while keeping every
Go pointer visible to the collector. A self-pointer into embedded storage and
an interior pointer into the value-stack array are both typed Go pointers.
Stack growth copies the stack first and then walks the descending open-upvalue
list once to retarget cells. Return, tail-call replacement, unwind, explicit
close, and State close copy the value through `writeSlot` before releasing the
stack root. Lua Functions validate their private upvalue slice at construction,
so GETUPVAL and SETUPVAL need no defensive nil checks in the hot executor.

Against the fixed-transition checkpoint, 20 alternating one-CPU pairs measured
the 1,000-call closed-upvalue cell 1.88% faster, with a 95% ratio interval of
0.9750 to 0.9874. Isolated 1,000-operation read and write loops were unchanged
within noise and remained allocation-free. Zero-, one-, and two-result control
calls changed by less than 0.3% in paired medians. The arm64 executor frame
remains 160 bytes and its text shrinks by 208 bytes. Creating and closing one
escaping upvalue improved from 23.26 to 21.18 ns in a separate 20-pair gate;
its allocator charge fell from 48 to 32 bytes while retaining one allocation.

Matched PUC 5.1.5 controls locate the remaining difference outside the
upvalue cell:

| 1,000 operations | Badger | PUC 5.1.5 | Ratio |
| --- | ---: | ---: | ---: |
| closed upvalue read | 4.463 us | 3.306 us | 1.350x |
| closed upvalue write | 4.437 us | 2.883 us | 1.539x |
| local-register move control | 4.55 us | 3.32 us | about 1.37x |
| child call reading an open upvalue | 21.894 us | 19.588 us | 1.118x |

Closed-upvalue access is now at least as fast as Badger's ordinary local-slot
path. Profiles attribute the residual principally to instruction dispatch,
numeric-loop conversion, slot writes, and Go slice/bounds machinery rather
than upvalue state selection. Future call work should target those general
costs or register initialization, not layer another special upvalue cache over
the permanent cell.

## Execution

Execution is split into one iterative dense instruction switch and one cold
driver. The switch retains the current Function, Prototype, register base,
program counter, code, constants, upvalues, and value stack in locals. It
directly executes control flow, moves, loads, upvalue access, number-only
arithmetic and comparisons, and prepared numeric loops. Ordinary instructions
neither publish frame state nor reread the activation.

An instruction that must grow an execution-stack backing array, coerce a
string, invoke a metamethod, construct an error, or use an open or vararg call
shape publishes its program counter and returns that instruction to the
driver. The driver performs the cold operation and re-enters the same switch.
This boundary is deliberately an instruction value, not an interface or
handler object. It keeps semantic temporaries out of the switch's live set,
which matters because Go allocates registers for a whole function rather than
for each switch arm.

Direct fixed Lua calls and their fixed-result nested returns are the deliberate
exception. When existing value and activation capacity is sufficient, trusted
helpers commit the transition and jump to one executor reload label. The
reload refreshes the Function, Prototype, bases, PC, code, and value slice
after frame depth or slice length changes. A failed fast admission changes
nothing and returns to the checked driver, which grows capacity or reports the
resource failure. The design therefore keeps one dispatch implementation
without routing ordinary Lua calls through the outer driver.

While the switch is active, execution-stack backing arrays cannot be replaced
and cached frame state remains valid. Calls, returns, errors, and future yield
points are explicit reload or exit seams.

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
closure, and adopts its engine-owned upvalue storage without another copy.
Closure instantiation and open-vararg stack growth stay behind cold helpers so
their allocation and error machinery does not enlarge the always-hot switch
frame.

Native Go functions use the same activation, argument window, result
destination, `__call` insertion, continuation, and result-adjustment
machinery. Lua-to-Lua tail calls replace the current activation immediately.
A tail-called native function is instead pushed transiently, preserving the
Lua call site while the callback can still fail; after a successful native
return, execution resumes at the compiler-emitted open `RETURN`, which
completes the caller. This matches Lua 5.1's C-call lifetime without burdening
every activation with extra source metadata.

A callback receives one borrowed `Frame` over the compact stack and returns a
token-bound terminal `Outcome`; it does not receive a public interface-value
stack. Exact typed reads and scalar returns therefore avoid materializing
`Value`. General owning Values remain available when a callback needs to
retain a reference or return a heterogeneous result. Captured Values live in
fixed private compact storage, corresponding to Lua 5.1 C-closure upvalues.

Compact function slots retain Lua's single public `function` kind while one
private high tag bit distinguishes a Go callback from a Lua closure. Direct
Lua-call admission can therefore reject Go functions from the slot tag,
without first chasing the Function to inspect its body. This mirrors PUC's
private closure subtypes without exposing another public value kind.

Frames become invalid as soon as they produce a return or error outcome.
Construction and result preflight reject foreign Values before changing the
stack. A Lua error retains the native activation long enough to capture it in
the traceback and then follows centralized unwind. A Go panic is not
translated into a Lua value; the borrowed native activation and token are
removed before the panic propagates. A successful Outcome retains only the
lightweight runtime identity and token, not the executing Thread or the
State's object graph.

Yield and reentrant Frame calls are separate later outcomes rather than
implicit behavior in the first callback ABI. A future `Frame.Call` may
re-enter Lua synchronously, but a yield crossing that native/API boundary is
a Lua 5.1 error. It must restore the original argument top and frame extent
before the callback resumes so argument access continues to describe the
same call. Native yield itself will be a terminal outcome, equivalent to
returning `lua_yield`, and will be admitted only at a resumable native-call
position. Context-aware public calls will make their active context available
through `Frame.Context` and inherit it through nested calls. When coroutines
are introduced, active native execution tracking moves to runtime scope so
State close cannot race a callback on any Thread.

### Native-call checkpoint

On the same Apple M3 Pro and Go 1.25.1 setup used for the executor checkpoints,
the warm checked activation and Frame boundary currently measures:

| Outcome shape | Time | Allocations |
| --- | ---: | ---: |
| no results | 30.7 ns | 0 |
| one scalar result | 33.8 ns | 0 |
| two owning Value results | 35.5 ns | 0 |
| read and return one capture | 38.2 ns | 0 |

An executor benchmark making 1,000 calls from Lua measures the Go callback
and an equivalent tiny Lua closure at roughly 49–50 microseconds each; recent
samples put the Go callback about 3% ahead, but the defensible conclusion is
parity within low-single-digit measurement variation. Both paths allocate
zero bytes. A Lua Function is 32 bytes, and a native Function plus its entry
and capture-slice header is 64 bytes before capture backing storage.

### Public-call checkpoint

On the same Apple M3 Pro and Go 1.25.1 setup, a warmed State boundary over a
precompiled one-result identity chunk measures:

| Boundary | Time | Bytes | Allocations |
| --- | ---: | ---: | ---: |
| `CallInto`, Lua Function | 53.05 ns | 0 | 0 |
| `CallInto`, native Function | 52.41 ns | 0 | 0 |
| friendly `Call`, Lua Function | 64.86 ns | 16 | 1 |

`Call` intentionally allocates the exact owned result slice. `CallInto`
copies directly between public owning Values and the same compact stack used
by the executor; it does not create a compatibility stack, adapter objects, or
a second call path. Arguments are staged before result publication, so caller
input and output slices may overlap. A short result destination is detected
after execution but before any destination write, leaving Lua side effects
intact while keeping the caller's buffer unchanged.

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
adjacency. Every verified Lua 5.1 opcode has a private-core execution route.
The executor's default failure remains a fail-closed invariant guard for an
invalid internal instruction, rather than a fallback to another interpreter.

Runtime failure snapshots the active Lua trace before one centralized unwind
closes upvalues, drops activations, and clears dead stack roots. Engine-created
syntax and resource failures carry an owned string error Value, matching Lua
5.1's protected load/call boundary; a native callback may still raise any Lua
Value. A deterministic frame or value limit reached by a Lua instruction is an
ordinary Lua error positioned at that instruction, including when the youngest
activation is native and its Lua caller must be found below it. A failure at a
Go ingress boundary with no active Lua frame remains source-less. ResourceError
is only additional classification for Go callers, not a separate uncatchable
control-flow class. Actual Go allocation failure is not treated as a recoverable
Lua quota failure.

Lua `pcall` and `xpcall` will install protected checkpoints inside this same
executor rather than recursively invoking the public `State.Call` boundary.
The checkpoint must intercept an error before root unwind, allow an `xpcall`
handler to inspect the still-live failing frames, then close upvalues and
restore frames, continuations, and value extents only to the protected depth.
It preserves arbitrary Lua error values; a handler failure becomes Lua 5.1's
fixed `error in error handling` value. Deterministic ResourceError values are
catchable there. A future controlled allocator failure would instead need
Lua's distinct source-less, handler-skipping memory-error path; an unrecoverable
Go runtime allocation failure is not converted.

Type errors recover PUC-style local, upvalue, global, field, and method names by
tracing verified bytecode only after failure; no provenance is stored in Values,
activations, or the hot loop. Ordinary Lua control flow does not use Go panic or
interface-valued per-opcode results. The executor returns one small outcome only
when it reaches its requested call depth or fails; coroutine support will add a
yield outcome when it exists.

## Build order

1. Canonical Value and object ownership.
2. State-neutral immutable strings and compact tables.
3. A direct recursive-descent compiler producing verified immutable
   Prototypes without retaining an AST.
4. Executor, PUC-style direct-call reentry, upvalues, and errors.
5. Native frames plus public source loading and protected calls.
6. Context polling, coroutines, yield, and reentrant native calls.
7. Standard libraries and embedding operations.
8. Debug facilities and optional extensions.
9. Profile-driven quickening, inline caches, and executor specialization.

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
