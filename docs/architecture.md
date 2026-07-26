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
   Coroutines are additional canonical Threads sharing that State. Each
   Thread has one Lua 5.1 global-environment pointer.
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
- `CallInto` for caller-owned result storage; and
- `Frame.Index` and `Frame.SetIndex` for ordinary Lua indexing and assignment,
  including bounded metamethod chains and synchronous Lua handlers, without a
  second value representation.

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
- `table.go`: dense-array storage, raw table semantics, mutation accounting,
  borders, and traversal;
- `table_store.go`: chained-scatter record storage, cached hashes, collision
  relocation, deletion continuations, tombstone recycling, and rehashing;
- `number.go`: deterministic numeric-string syntax and compact numeric
  coercion;
- `metamethod.go`: raw event lookup and shared-handler selection;
- `opcode.go`: canonical Lua 5.1 instruction encoding;
- `lexer.go`: refillable byte-window scanning, core tokenization, names,
  numbers, and one-token lookahead;
- `lexer_string.go`: token-text capture, quoted and long strings, comments,
  delimiters, and newline handling;
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
- `chunk.go`: verified native-ABI Lua 5.1 binary chunk encoding and decoding;
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
- `native_call.go`: protected reentrant calls and ordinary Lua indexing from
  a borrowed Frame;
- `protected.go`: metadata-only protected checkpoints, live-stack error
  handlers, emergency quota headroom, and compact result publication;
- `load.go`: bounded sequential input, immutable refill windows, source or
  binary selection, the fixed-string fast path, and State-bound Function
  construction;
- `load_reader.go`: streaming Reader and file adapters, reader-error
  preservation, interpreter-line handling, and the public `Load`,
  `LoadContext`, `LoadFile`, and `LoadFileContext` boundaries;
- `invoke.go`: protected main-Thread calls and owning or caller-supplied result
  egress;
- `coroutine.go`: canonical Thread construction, compact resume transfer,
  suspension lifecycle, and State-wide execution ownership;
- `context.go`: operation-scoped host control, context ownership, polling
  budgets, cancellation failures, and terminal exit requests;
- `resource.go`: exactly-once native resource cleanup, finalizer tokens, and
  the State-close registry used by resource-owning libraries;
- `process.go` and the build-constrained `process_*.go` files: shell
  discovery, raw process status, manual pipes, asynchronous wait ownership,
  and command-processor-root lifecycle shared by IO and OS;
- `io_process.go`: the canonical file adapter, release policy, context
  interruption, and Lua `io.popen` surface for process pipes;
- `pattern.go`: byte-oriented Lua 5.1 pattern matching with bounded recursion
  and cooperative context polling;
- `library_base.go`, `library_load.go`, `library_coroutine.go`,
  `library_math.go`, `library_table.go`, `library_string.go`,
  `library_string_format.go`, `library_package.go`, `library_io.go`, and
  `library_os.go`: the implemented Lua 5.1 runtime library surface using
  native frames. `library_os_time.go` owns calendar conversion,
  `os_date_format.go` owns deterministic C-locale date formatting, and the
  build-constrained `os_clock_*.go` files read process CPU time.
  `library_base.go` also owns
  the auxiliary layer shared by every library file, corresponding to PUC's
  `lauxlib` plus the runtime operations libraries need: argument coercion,
  positioned argument and general diagnostics, compact result publication,
  reentrant compact calls, ordinary indexing, and less-than. `library_load.go`
  owns the Lua-visible source readers and file-loading boundaries;
  `library_package.go` owns module discovery and the registry-backed load
  cache. Later `library_*.go` files add the remaining standard libraries.

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
`__call`, limits, errors, and native/yield cases. Warm fixed calls must
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
arithmetic except exponentiation, comparisons, and prepared numeric loops.
Ordinary instructions neither publish frame state nor reread the activation.

Exponentiation remains in the cold driver. On arm64 with Go 1.25.1, placing
`math.Pow` in the switch enlarged the executor frame from 160 to 176 bytes, so
numeric power accepts a cold-driver round trip instead of taxing every
dispatch activation.

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
and cached frame state remains valid. Calls, returns, errors, and yield
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

Frames become invalid as soon as they produce a return, error, or yield
outcome.
Construction and result preflight reject foreign Values before changing the
stack. A Lua error retains the native activation long enough to capture it in
the traceback and then follows centralized unwind. A Go panic is not
translated into a Lua value; the borrowed native activation and token are
removed before the panic propagates. A successful Outcome retains only the
lightweight runtime identity and token, not the executing Thread or the
State's object graph.

Native yield is a terminal outcome equivalent to returning `lua_yield`.
It is admitted only in a non-main coroutine at its outermost native activation
with no pending metamethod, iterator, or protected-call continuation. The
native activation remains on the compact activation stack, but its borrowed
Frame and Go callback do not survive suspension. Resume arguments complete
that retained activation through its existing result destination and requested
result count.

Reentrant `Frame.Call` and `Frame.CallInto` use the nested checkpoint described
below. A yield crossing that native/API boundary is a Lua 5.1 error. The
checkpoint restores the original argument top and frame extent before the
callback resumes, so argument access continues to describe the same call.
Context-aware public calls make their active context available through
`Frame.Context` and inherit it through nested calls. Active Thread ownership
and aggregate native-call depth are State-wide, so nested coroutine resumes
cannot evade the native-depth limit and State close cannot race a callback on
any Thread.

### Context-aware execution

`CallContext`, `CallIntoContext`, `ResumeContext`, and `ResumeIntoContext`
attach a context to one public execution operation. The context is not part of
a State or Thread and is cleared before the operation returns. In particular,
a coroutine does not retain a context while suspended; each later resume may
use a different context or the raw context-free boundary.

Cancellation is a host interruption, not a Lua value raised by `error`.
`pcall`, `xpcall`, `coroutine.resume`, and `coroutine.wrap` therefore cannot
catch it. Go receives a `ContextError` for which `errors.Is` recognizes both
`ctx.Err()` and a distinct `context.Cause(ctx)`. Work performed before a
cancellation safepoint remains visible.

The executor samples cancellation at backward control-flow edges, tail-call
transitions, native-call boundaries, and final return or yield. A bounded
backedge budget keeps the channel check out of ordinary instruction dispatch.
This is cooperative cancellation, not a per-instruction deadline. A
`NativeFunc` must observe `Frame.Context` itself while it blocks or performs
long-running Go work; the runtime checks again after the callback returns but
cannot preempt Go code.

Lua 5.1's `os.exit` is a second host-control operation. Badger never calls
`os.Exit`, closes the State, or decides that ending one script should end an
embedding process. Instead the public call or resume returns an `ExitError`
whose cause is an immutable `*ExitRequest`; `errors.As` exposes its
`ExitCode()`. The first request is terminal across `pcall`, `xpcall`,
coroutines, loaders, and nested native calls. A native callback may inspect a
returned request, but cannot suppress it and continue the same public
execution. The request and its traceback remain valid after `State.Close`,
and a host that declines to terminate anything may reuse the unwound State.

This guarantee uses one operation-scoped pointer in `State` and one cold check
at native-call and nested-call seams. It adds no field to compact values,
activations, Threads, or the instruction loop.

The pattern matcher follows the same rule while it owns control in Go. Raw
calls take an unpolled path; context-aware calls sample recursive
backtracking, repeated search attempts, balanced scans, and greedy expansion.
This makes pathological patterns interruptible without putting a channel
operation in every byte comparison.

The executor has one source and one dispatch loop. A single cold backedge block
preserves its 160-byte frame and the direct ordinary dispatch branch. On the
standing Apple M3 Pro benchmark, active polling adds about 4.1% to a numeric
loop and roughly 1% to representative field and Lua-call loops, with no
allocation. Tiny public boundaries instead use an absolute budget: their fixed
admission and final checks can add roughly 10–14 ns even though no work is
boxed or allocated.

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

Runtime strings use the same 16-byte representation in Values, registers,
tables, constants, and closed upvalues. One GC-visible pointer addresses
immutable bytes, while the other word packs the kind, a 24-bit length, and a
32-bit hash. Strings beyond the packed-length range use one uncommon owning
fallback object. Compiler-only names use a separate pointer-sized interned
descriptor because they are metadata rather than runtime values. Tables and
the executor access strings through one text, hash, and equality kernel; they
do not branch on the ordinary and long encodings. Pointer equality is only a
fast path, and content equality remains authoritative.

The empty string and all 256 one-byte strings use finite process-wide backing.
They are selected before runtime-local admission or caller-storage retention,
so every production path observes the same compact representation without
allocating. This is safe across States because strings are immutable scalar
values; Go pointer identity remains an internal shortcut rather than Lua
semantics. Longer recurring strings use the bounded State-local probation and
protected sets. Badger does not use an unbounded strong interner or let a
retained short string pin an unrelated arena page.

Runtime number coercion accepts numbers and complete numeric strings. The
shared parser recognizes signed decimal fractions and exponents, signed
hexadecimal integers, and the six ASCII whitespace bytes used by Lua. Finite
warm-path parsing does not allocate. It intentionally rejects locale-specific
spellings, named infinities and NaNs, hexadecimal floats, embedded NUL, and
trailing data instead of inheriting platform-dependent libc behavior.
Primitive number spelling likewise remains locale-independent and uses one
Lua 5.1 `%.14g`-style formatter for diagnostic Values and execution-time
coercion. Future `tostring` uses that primitive only after applying its own
metamethod semantics; `print` and `string.format` remain separate semantic
layers.

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
numbers. Non-coercible pairs select `__concat` from the left value before the
right and suspend with one marked continuation. The returned value is
installed at the exact reduced pair before reduction continues, so later
pairs observe event mutation performed by earlier handlers. Public Values are
never constructed by this path.

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

Each Table has one positive-integer array and one chained-scatter record
store. Allocation policy belongs to Table rather than either lane. At a
genuine insertion that needs new backing, the table counts live positive
integers by power-of-two range and chooses the largest array span that would
be more than half occupied, bounded at `2^26` as in PUC Lua 5.1. Remaining
fields receive the smallest sufficient record store. A conservative integer
range summary proves when a record-only growth cannot change that answer,
while a dense array with no integer records can jump directly to the same
exact power-of-two span without a preliminary full scan. The initial
four-slot array class and already-reserved slice capacity are deliberate Go
allocation adaptations; neither changes the global density rule at a fresh
allocation.

Existing fields update and delete in place. A deleted record retains its key
and collision links so `next` can continue from it; a later absent insertion
is the legal seam for compaction or movement between lanes because Lua makes
traversal order undefined after adding a field. Physical redistribution does
not create an additional logical mutation or invalidate the string-keyed
metamethod absence cache.

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

Runtime failure appends each active traceback segment immediately before the
corresponding frames unwind. The unwind then closes upvalues, drops
activations, and clears dead stack roots. This segment rule lets a protected
nested call return only its own trace while later propagation adds each outer
segment exactly once. A caught error does not allocate a traceback.

Engine-created syntax and resource failures carry an owned string error Value,
matching Lua 5.1's protected load/call boundary; a native callback may still
raise any Lua Value. Only VM-created resource strings are eligible for source
positioning, so classifying an arbitrary raised Value as a ResourceError cannot
replace that Value. A deterministic frame or value limit reached by a Lua
instruction is an ordinary Lua error positioned at that instruction, including
when the youngest activation is native and its Lua caller must be found below
it. A failure at a Go ingress boundary with no active Lua frame remains
source-less. ResourceError is only additional classification for Go callers,
not a separate uncatchable control-flow class. Actual Go allocation failure is
not treated as a recoverable Lua quota failure.

Lua `pcall` and `xpcall` install metadata-only checkpoints inside this same
executor rather than recursively invoking the public `State.Call` boundary.
A checkpoint intercepts failure before traceback allocation or unwind.
`xpcall` runs its exact-Function handler above the still-live failing frames,
then closes scratch upvalues and restores only frames, continuations, and
value extents created above the protected depth. Lua-visible mutations remain.
Arbitrary error Values retain identity; a missing or failing handler becomes
Lua 5.1's fixed `error in error handling` value.

Error handlers share bounded, non-compounding emergency capacity beyond the
configured frame, value, and native-call limits. The normal limits and
capacity-based fast-call admission are restored before the protecting native
call returns. Deterministic ResourceError values are catchable. A future
controlled allocator failure would instead need Lua's distinct source-less,
handler-skipping memory-error path; an unrecoverable Go runtime allocation
failure is not converted.

### Native reentry

`Frame.Call` and `Frame.CallInto` let an executing native callback invoke Lua,
another native function, or a Function-valued `__call` handler synchronously.
They reuse the current Thread, activation stack, compact slots, and executor;
`State.Call` remains the separate idle-State ingress boundary. The outer Frame
is deliberately invalid while a nested callback runs and becomes valid again
after the nested call returns.

The reentry checkpoint retains only stack depths, extents, and native ownership
metadata. Inputs are validated and staged before execution, so argument and
destination storage may overlap. Failure restores private call machinery but
does not roll back Lua-visible side effects. `CallInto` leaves a short
destination unchanged and reports the exact required result count. `RaiseError`
clones a returned failure before rethrowing it; traceback segments are appended
by the unwind rule rather than eagerly inspecting live outer frames.

Reentry does not create another yield route. Yielding across the current native
boundary remains Lua 5.1's ordinary
`attempt to yield across metamethod/C-call boundary` error, while a distinct
child coroutine resumed by the nested call may suspend normally.

On arm64, warmed one-result `Frame.CallInto` calls take about 114 ns for a Lua
target, 116 ns for a native target, and 126 ns for a callable table on the
qualification machine, all with zero allocations. `Frame.Call` allocates only
its nonempty owning result slice. Persistent object sizes and the 160-byte
`runInstructions` frame are unchanged. The shared protected-call path retains
its frozen performance; its private two-representation argument staging adds
16 transient stack bytes at a nested call boundary.

### Coroutines and suspension

A coroutine is another canonical `Thread` owned by the same `State`; it does
not use a Go goroutine, scheduler channel, alternate stack representation, or
second executor. The active Thread pointer moves at a resume boundary while
the parent becomes `normal`, then returns to the parent when the child yields,
returns, or fails. Aggregate native depth remains State-wide across this
switch, preventing a chain of coroutine resumes from bypassing the Go-stack
limit.

Yield leaves Lua activations, registers, and open upvalues intact. The
outermost yielding native activation itself is the suspension record: yielded
slots occupy its existing result destination, and resume arguments later
complete that activation using its requested result adjustment. Once results
cross to the resumer, their temporary slots are cleared and the suspended live
extent returns to the saved caller extent. A dead coroutine clears and releases
all reusable stack, activation, and continuation backing storage because it can
never execute again.

Lua's `coroutine.resume` and `coroutine.wrap` transfer slots directly between
Threads. Only the public Go `Thread.Resume` boundary materializes owning
`Value` results; `ResumeInto` accepts caller storage and remains allocation-free
when warm. Arbitrary error Values retain identity, including nil and reference
objects. The Lua library derives argument names and wrapper source prefixes
from the immediate call site, including tail calls, rather than keeping
diagnostic provenance in the hot representation.

New coroutines inherit the creating Thread's global-environment pointer.
Changing either Thread's pointer later is isolated, while mutations to an
inherited table remain shared. Native Function and userdata construction use
the currently executing Function's environment, matching Lua 5.1's
`getcurrenv`; loaders instead use the executing Thread's global environment.
This distinction is explicit at the boundary and adds no field to activations
or to the executor frame.

Lua 5.1 permits yield through ordinary Lua calls and ordinary `__call`, but
rejects it across protected calls, metamethod continuations, generic
iterators, nested native calls, and the main Thread. The runtime enforces that
rule from the retained activation, per-Thread native depth, and existing
continuation stack; it needs no separate barrier object or counter in the hot
loop.

Type errors recover PUC-style local, upvalue, global, field, and method names by
tracing verified bytecode only after failure; no provenance is stored in Values,
activations, or the hot loop. Ordinary Lua control flow does not use Go panic or
interface-valued per-opcode results. The executor returns one small outcome only
when it reaches its requested call depth, yields, or fails.

## Loading and binary chunks

`LoadString`, `Load`, and `LoadFile` recognize Lua's binary signature and
otherwise compile source. `Compile` deliberately remains the State-neutral
source-only operation. Every loading path publishes the same immutable,
verified `Prototype`; loading that Prototype creates a new Function in the
executing Thread's global environment without executing it.

Binary chunks use Lua 5.1's native format. The decoder requires the current
endianness, `size_t`, instruction, and double layout, then validates the entire
prototype tree before publication. Invalid counts, strings, constants, debug
metadata, instructions, and excessive prototype nesting fail as syntax errors.
Like PUC Lua, decoding ends after the root function and does not require or
consume end-of-input.

One compilation-local string table interns source names, constants, local
names, and upvalue names across the complete prototype tree. Exact-size code,
constant, and child vectors allocated by the checked decoder transfer directly
into the sealed Prototype. The compiler still copies its growable vectors at
the publication boundary, preserving the rule that no mutable builder storage
can alias executable metadata.

`Options.MaxLoadBytes` defaults to 64 MiB. Source and binary loading charge
bytes as they are consumed. Binary decoding also applies the same independent
bound to projected decoder and retained Prototype storage before allocating
count-controlled vectors or strings. This separates hostile-input protection
from the executor's value and frame limits. The sequential input caches the
first refill failure, preserves arbitrary reader errors, and can return an
in-piece span without copying; a span crossing pieces receives exact owned
storage that runtime string publication can adopt.

Source lexing is byte-oriented, not line-oriented. A fixed string is scanned
directly; a Reader is exposed through bounded contiguous windows and consulted
again only when the current window is exhausted. Line tracking exists solely
for diagnostics and does not constrain token boundaries. Token text contained
in one input piece borrows that piece, while decoded text or text spanning
pieces receives owned storage. Before an early compiler return, the lexer
commits its consumed prefix so load limits and pending input failures observe
the actual read position.

The Reader adapter reuses one read buffer and copies each nonempty result into
an immutable string piece. A span contained in one piece can borrow that
storage; a span crossing pieces receives exact owned storage. `Load` never
closes its caller-owned Reader. Bytes returned together with an error are
consumed first, exact `io.EOF` ends the input, and every other error—including
a wrapped `io.EOF`—is returned with its original identity. Repeated empty
reads eventually produce `io.ErrNoProgress`.

`LoadFile` opens and closes the named file and records its source as `@path`.
As in Lua 5.1, a first line beginning with `#` is ignored. Text receives a
synthetic newline so diagnostics retain their physical line numbers; a binary
signature immediately after that line is exposed without the newline. The
skipped bytes still count toward `MaxLoadBytes`. Open and read errors identify
the file and retain their underlying cause through `errors.Is` and
`errors.As`.

`LoadStringContext`, `LoadContext`, and `LoadFileContext` apply a context to
compilation or binary decoding only. They reject a nil context, poll before
input and at bounded byte intervals, and never retain the context in the
resulting Function. An ordinary non-binary `LoadString` instead charges the
already-materialized source once and scans it through the direct fixed-string
path.

## Managed native resources

Runtime libraries can attach a native resource to canonical userdata without
exposing that resource through the public payload. Such userdata remains the
single Lua object and compact slot identity; the lifecycle layer is not an
adapter object model. Its public payload is read-only and reports nil, while
the owning library borrows the native value through a scoped lease that keeps
the finalizer token live for the complete host operation.

One lazy registry per State retains lifecycle records for live runtime
resources, but deliberately does not retain their userdata or finalizer tokens.
Explicit close, Go reclamation, and `State.Close` converge on one `sync.Once`
cleanup, but the cleanup receives the release reason and only an explicit
operation receives its transient context. This lets a process-backed resource
wait during an ordinary close, terminate during deterministic State shutdown,
and abandon without blocking a Go finalizer, without retaining a call
context. The finalizer can therefore perform native cleanup when userdata
becomes unreachable, while `State.Close` still deterministically releases
every registered resource, continues after failures, and returns those
failures joined together. Closing clears the record's native value and
registry link, so resource bookkeeping cannot pin unrelated State roots. A
userdata's ordinary Lua 5.1 environment remains an independent, observable
reference. Borrowed standard streams use the same lifecycle record with a
no-close release policy and are never closed.

Finalizers in this layer may release only a private native resource; they
never enter Lua. This is intentionally not an implementation of Lua
`__gc`, weak tables, or `collectgarbage`. Those require a later State-local
semantic collector that can identify host-retained owning handles, order Lua
finalizers, and process resurrection synchronously.

## Standard libraries

Each library has its own explicit opener and no implicit installation. `New`
returns an empty State. Reopening replaces the library table and every
Function in it with fresh canonical objects, so a program cannot half-restore
a tampered library. `OpenBase` also opens `coroutine` because Lua 5.1's
`luaopen_base` registers those functions; `package`, `math`, `table`, and
`string` are separate openers because PUC registers them separately.

Libraries are ordinary native callbacks. They read compact arguments and
publish compact results, so a scalar entry never materializes an owning
`Value`. They do not gain private access to the executor: reentrant Lua calls
use the same nested checkpoint `Frame.Call` uses.

`library_base.go` owns the auxiliary layer the other library files share.
Argument coercion follows PUC's `luaL_checknumber`: exact numbers pass through
and complete numeric strings convert, using the runtime's own deterministic
number grammar rather than a second one. That grammar rejects the
locale-dependent spellings, named infinities, and hexadecimal floats C's
`strtod` happens to accept; the library layer inherits that decision instead
of reintroducing platform dependence at the boundary.

Integer arguments follow `luaL_checkint`, which casts a double straight to C
`int` and is therefore undefined for NaN and for magnitudes outside that type.
Badger truncates toward zero and saturates at the signed 32-bit bounds, so
every input has one defined result while every value C could convert without
undefined behavior converts identically.

Diagnostics reproduce `luaL_argerror` and `luaL_error`, including
`luaL_where(L, 1)`: the failing function's name and the reported source
position both come from the immediate Lua caller's call instruction, and a
library entered directly from Go has neither. A method call does not count its
receiver, so a failure in the receiver of `t:f(...)` becomes Lua 5.1's
distinct bad-self message. The name and category come from the same verified
bytecode tracing the executor uses for runtime type errors; no provenance is
stored in the hot representation.

The base surface currently includes `assert`, `dofile`, `error`, `getfenv`,
`setfenv`, `getmetatable`, `setmetatable`, `load`, `loadfile`, `loadstring`,
`rawequal`, `rawget`, `rawset`, `type`, `next`, `pairs`, `ipairs`, `select`,
`unpack`, `tonumber`, `tostring`, `print`, `pcall`, and `xpcall`. Raw
operations and iterators stay in compact slots and do not consult metamethods.
`pairs` and `ipairs` capture private canonical iterators, so replacing global
`next` cannot change an existing iterator and the private `pairs` generator
remains distinct from that global Function. `error`, `getfenv`, and `setfenv`
resolve physical activations and elided tail-call levels through one cold
stack walker; no debugging data is added to activation records. `error`
preserves arbitrary Lua values, including nil.

Base-10 `tonumber` uses the runtime's deterministic numeric grammar. Explicit
bases use a separate allocation-free, 2-through-36 parser matching the LP64
`strtoul` behavior Lua 5.1 exposes, including its C-string boundary, with a
fixed unsigned 64-bit range rather than a host-dependent `unsigned long`.
Primitive numeric `tostring` formats into a stack buffer and probes the
runtime string pool by bytes; a recurring warm result does not allocate a
temporary Go string.

The four Lua-visible loaders share the same bounded source/binary pipeline as
the Go API. `load` calls its reader through the compact nested-call seam and
preserves arbitrary Lua error values; `loadstring` scans its fixed source
directly when no active context requires bounded polling. `loadfile` returns a
Function or nil plus a diagnostic, while `dofile` raises load failures,
executes the Function, and transfers all of its results directly from the
compact scratch call into the caller's requested result window. None of these
paths constructs an intermediate slice of owning `Value` objects. A
context-aware outer call is inherited from `Frame.Context`; cancellation is a
host `ContextError`, not a `(nil, message)` load result.

`Options.Stdin`, `Stdout`, and `Stderr` give every State its own standard
streams and default to `os.Stdin`, `os.Stdout`, and `os.Stderr`. The State
borrows these interfaces: libraries serialize access under the ordinary
single-executor contract, and `Close` never closes a caller-owned stream.
They govern Lua's stream operations, not child processes: `os.execute`
inherits the embedding process's actual standard descriptors, while
`io.popen` replaces exactly one child descriptor with its pipe and inherits
the actual process descriptors for the rest. Neither operation redirects
through nor flushes the State endpoints.
`loadfile` and `dofile` use the process filesystem and use the State's input
stream when called without a filename. They apply the same leading-`#` file
rule as `State.LoadFile`. Every loaded closure binds to the executing Thread's
global environment, not to a caller Function's private environment.

`print` resolves the current Thread global `tostring` once, then calls that
same callable once for every argument through the compact nested-call seam.
As in Lua 5.1, a returned number or string is accepted, embedded NUL ends the
written text, separators are emitted only after a successful conversion, and
an error preserves output already written by earlier arguments. Writes are
sequential and their errors are deliberately ignored, matching PUC's
unchecked `fputs`; the `io` library reports failures through its own
Lua-visible result convention.

The IO library represents each file as one opaque managed userdata over one
`fileHandle`; there is no public-value mirror or library-private side map.
Regular files own their operating-system handle and close when Lua closes
them, the Go object becomes unreachable, or the State closes. Standard files
borrow the State endpoints and are never closed by Lua or by `State.Close`.
Every opening of the library receives private default-input and default-output
slots, while all openings still share the State's physical standard-stream
cursors.

Input and output endpoints jointly define one logical file cursor. Before a
write or relative seek, the file compensates for bytes fetched by buffered
read-ahead; before a read or seek, it flushes pending output. This keeps
`read`, `write`, and `seek("cur")` coherent without publishing a second cursor
or materializing values at the library boundary. Regular output is fully
buffered by default, as a C `FILE` normally is. Caller-supplied standard Go
writers remain unbuffered unless Lua explicitly selects buffering, making
embedding behavior independent of whether the writer happens to be a
terminal. `print`, `io.write`, and `io.flush` use the same standard-output
endpoint and therefore preserve ordering.

The read engine produces compact slots directly for line, fixed-count,
whole-file, and numeric reads. It is binary-safe: NUL is ordinary input and a
line ends only at LF, with CR preserved. Numeric input uses the runtime's
deterministic number grammar rather than the host C locale. Fixed-count reads
allocate according to bytes actually received, so an enormous requested count
on a short stream does not reserve an enormous buffer. Constructed IO strings
share the runtime's 1 GiB construction ceiling with `string.rep`; exceeding it
is a catchable resource error rather than a host allocation attempt. Numeric
tokens are independently limited to 64 KiB because they produce one scalar;
this prevents a digit stream from building and then duplicating a
string-sized allocation merely to return one float.

File modes are the portable Lua set—`r`, `w`, or `a`, with at most one `+` and
one ignored binary marker—instead of accepting platform-specific `fopen`
extensions. Counts, offsets, and buffer sizes use the shared defined 64-bit
conversion rather than C's undefined out-of-range floating-point casts. Seek
positions are therefore 64-bit on every supported Go target. `setvbuf`
reconfigures both readable and writable sides without losing prefetched input,
and rejects buffers above 64 MiB before changing the prior policy.

Context-aware input caps each underlying read at 64 KiB and polls both before
and after it, independently of the buffer size selected by `setvbuf`.
Consumption from bytes already buffered also polls every 64 KiB. Cancellation
therefore reaches line, fixed, whole-file, numeric, and zero-length reads
without becoming an IO failure tuple; it remains a host `ContextError`, and
the prefix consumed before the safepoint remains consumed.

The implemented Lua 5.1 surface includes default-file control, open, close,
temporary files, type inspection, reads, writes, lines, flushing, seeking, and
buffer selection. Temporary files are removed when their owned resource
closes. `io.lines(filename)` owns and closes its file at EOF, while
`file:lines()` and default-input iteration leave their file open.

`io.popen` returns the same canonical `FILE*` userdata and uses the same
endpoint, buffering, read, write, and diagnostic paths as every other file.
Its portable mode surface is exactly `r` or `w`; Badger intentionally does not
inherit host-specific `popen` extensions such as Darwin's bidirectional `r+`.
The command uses Lua string coercion and the C-NUL boundary, then runs through
the same raw platform shell as `os.execute`. A manual `os.Pipe` joins the
requested child descriptor directly, so the one permanent process waiter
never races `os/exec` copying goroutines.

An explicit process-file close flushes buffered output, closes the parent pipe
end, waits, and reaps. Lua 5.1's `pclose(file) != -1` contract deliberately
does not expose the child status: normal exit, nonzero exit, and signal death
all return true. Unlike PUC's boolean `pclose` result, Badger still reports a
buffered flush, descriptor-close, or wait-infrastructure failure as an IO
tuple; successful waiting does not hide an earlier failed write or close.
`State.Close` discards buffered pipe output, closes the descriptor, terminates
the still-owned command-processor root, and synchronously observes its
already-running waiter. Collection closes and requests termination of any
still-owned root without blocking the Go finalizer; the permanent waiter
reaps later without retaining a State, Lua object, or call context. It
necessarily retains the `exec.Cmd`, including its command metadata, until the
root exits because `Cmd.Wait` owns `os/exec`'s bookkeeping. This bounded
retention is an embedding-safe divergence from PUC, whose `__gc` may block in
`pclose`.

Context cancellation closes the pipe to wake a blocked read, write, or flush,
marks the Lua file closed, and remains an uncatchable host `ContextError`.
Cancellation, `State.Close`, and collection own only the command-processor
root on every platform. Shell-created descendants and background jobs are
outside Badger's ownership and may survive root termination or exit. Closing
the parent pipe still unblocks Lua promptly; embedders that need descendant
lifecycle control should launch and manage those processes through a host
facility instead of `io.popen`. No operation context is stored in a returned
file.

Unlike an argument-stack bug in PUC Lua 5.1.5, explicit
`io.lines(nil)` follows the documented optional-filename behavior and selects
the default input just like an omitted argument.

The operating-system library currently provides process CPU time, calendar
formatting and conversion, time differences, environment lookup, filesystem
removal and rename, deterministic locale selection, and secure temporary
names. Environment names and paths preserve Lua 5.1's C-string boundary.
Filesystem failures use the same `(nil, message, errno)` convention as IO;
rename diagnostics identify the source path. `os.tmpname` securely creates
and closes a unique file and leaves it present, matching the POSIX Lua 5.1
implementation without using its race-prone name-only fallback.

`Options.Location` gives each State its local timezone. Nil snapshots
`time.Local` during `New`, so a later process-global change cannot silently
change a live State. `Options.Now` optionally supplies wall time for
deterministic or virtual-time embeddings; nil selects `time.Now`. Both
`os.date` and `os.time` stay in compact values. A date table is populated
directly in compact table storage, and `os.time` reads its seven inputs through
ordinary indexing in Lua 5.1 order so `__index` remains observable.

Calendar normalization uses a transition-aware local-time resolver. An
explicit `isdst` selects the nearest matching zone offset, including
non-hour transitions. With no hint, a repeated wall time selects its daylight
occurrence and a missing wall time is interpreted with the nearest standard
offset, matching the reference `mktime` behavior used for qualification.
The State's local-zone choice is deterministic even though libc leaves these
transition decisions platform-dependent.

Date names and composite forms use a deterministic English C locale rather
than process-global locale state. The formatter implements the ISO C and
common POSIX/BSD conversion set, emits an unknown conversion byte literally,
and checks the shared 1 GiB construction ceiling before extending its output.
`os.setlocale` validates Lua 5.1's six category names but never mutates the
host locale: queries and the `C`, `POSIX`, and empty requests resolve to `C`;
unavailable locales return nil. This keeps number parsing and byte-string
ordering consistent across States.

`os.clock` reads user plus system process CPU through native Go system calls
on Unix and Windows. It therefore measures the whole embedding process, as C
`clock` does, rather than wall time or one State. Unsupported Go targets fall
back to the runtime's estimated user, GC, and scavenging CPU metrics.
`os.exit` keeps Lua 5.1's optional integer coercion but reports a terminal
host request instead of terminating the embedding process. Lua protection and
coroutines cannot catch it. Go handles it conventionally:

```go
var request *lua.ExitRequest
if errors.As(err, &request) {
	code := request.ExitCode()
	// End one plugin, stop a service, or explicitly call os.Exit(code).
}
```

Status values remain signed and unmasked; the host decides how a Lua status
maps to its own process or service.

`os.execute` preserves Lua 5.1's one-result contract: every call returns
exactly one number. An omitted or nil command queries for a command processor
and normalizes availability to 1 or 0. A supplied command is passed unchanged
after Lua's string coercion and C-NUL truncation to `/bin/sh -c` on POSIX or
the `COMSPEC`/`cmd.exe /c` command line on Windows. POSIX returns the raw wait
status, including its exit-code shift and signal bits; Windows returns the
signed command-interpreter exit status. An unavailable processor or failure
to start or reap it returns -1.

Commands inherit the embedding process's current environment, working
directory, and actual `os.Stdin`, `os.Stdout`, and `os.Stderr`. They do not
inherit State-local `Options` streams or Lua's mutable default IO files. A
context-aware call terminates and reaps the root process before returning a
`ContextError`. Shell-created descendants and background jobs are outside
Badger's ownership on every platform and may survive root termination; hosts
that require descendant lifecycle control should launch and manage those
processes directly.

Two host differences are deliberate. Badger does not reproduce C
`system`'s temporary changes to the embedding process's signal masks and
dispositions. Windows commands use Go's Unicode process interface rather
than the C runtime's locale-dependent narrow-character conversion.

The remaining base entries are intentionally absent rather than partial
stubs. `collectgarbage`, `gcinfo`, and `newproxy` require deliberate
State-local GC, weak-reference, and finalizer semantics rather than
process-wide Go GC shims.

The package library keeps Lua 5.1's `_LOADED` table in the State registry.
Reopening package replaces the public package table, its searcher tables, and
all of its Functions, while preserving that registry table and its cached
module identities. `require` consults the registry directly and follows
ordinary Lua indexing for cache reads and writes; replacing the public
`package.loaded` field therefore does not redirect it. A warmed cache hit
stays entirely in compact slots and allocates nothing. Load cycles use one
State-owned immutable object identity. It has no environment and remains
recognizable if a host mutates the ordinary userdata payload exposed while a
loader runs.

The Go library openers deliberately publish their global and `_LOADED`
entries through raw host assignment. Reopening repairs a library with a fresh
table and fresh Functions instead of executing user metamethods or merging
with a tampered table. This is an embedding-boundary policy; Lua-side
`require`, `module`, and cache access retain their ordinary Lua 5.1
metamethod behavior.

The default searchers preserve Lua 5.1's order: `package.preload`, Lua source
or binary files, C modules, then C root modules. Lua files use the same
bounded, context-aware, single-open loader as the public file API. Search
misses accumulate the standard candidate diagnostics, while syntax, resource,
context, and reader failures remain fatal with their original classification.
The pure-Go runtime deliberately uses PUC's supported
"dynamic libraries not enabled" platform behavior for both C searchers and
`package.loadlib`; calling a symbol would require the complete C
`lua_State` ABI, not merely a dynamic-library handle.

Lua 5.1's `module` and `package.seeall` remain available for source
compatibility. Module tables are created through ordinary Lua assignment,
the calling Lua closure receives the module environment before options run,
and `seeall` installs `_G` as the module metatable's `__index`. Go modules use
the canonical `package.preload` table rather than a second host-only module
registry.

The math library is the exact Lua 5.1 surface, including the `mod` alias the
standard distribution publishes through `LUA_COMPAT_MOD` as the same canonical
Function as `fmod`. Where Go's standard library differs from C, C wins:
`max` and `min` keep PUC's seed-and-replace scan, so a NaN propagates only
when it appears first, and `modf` splits an infinity into that infinity and a
zero of the same sign. Degree conversion divides and radian conversion
multiplies by the same constant, preserving PUC's rounding.

Transcendental results come from Go's `math` package, not from a C library.
Lua 5.1 specifies no accuracy for them and PUC forwards to whatever `libm` the
platform supplies, so bit equality with any particular C library is neither
achievable nor meaningful. Numerical deltas therefore vary with the operating
system, architecture, and C library chosen as the comparator; the runtime does
not promise a cross-libm ULP bound. `math.pow` uses the same primitive as the
`^` operator, so the two always agree with each other, which is the invariant
that matters inside one runtime.

`math.random` is the one entry that cannot be reproduced. Lua 5.1 delegates it
to C `rand()`, whose sequence, resolution, and process-global seed are
implementation-defined. Badger instead gives each opened math library one
private xoshiro256** generator seeded through SplitMix64: identical on every
platform, reproducible from a seed, significant across the whole 53-bit
mantissa, and unable to disturb another State's stream. The interface is
PUC's, including the three arities, the empty-interval failures, and
advancing the generator before arguments are inspected. An unseeded library
starts from one fixed seed, mirroring C's implicit `srand(1)`.

The table library operates on raw storage, as Lua 5.1 does. Element access is
raw compact reads and writes, sequence length is the same border the length
operator reports, and only an explicit callback, a comparator, or an `__lt`
handler runs Lua. `table.setn` reports Lua 5.1's obsolescence failure because
the standard distribution leaves `LUA_COMPAT_GETN` undefined.

`table.sort` reproduces PUC's quicksort rather than delegating to Go's sort.
Its behavior is observable: it is unstable, its comparison count and resulting
permutation are visible to a counting comparator, a comparator may read and
mutate the table mid-sort, and an inconsistent order function must produce
Lua's `invalid order function for sorting` failure rather than a Go panic or a
silently wrong result. Recursion always takes the smaller partition, so depth
stays logarithmic even under a hostile comparator.

The string library is byte-oriented, including character classes, positions,
captures, case conversion, and length; it does not interpret UTF-8. Pattern
matching follows PUC Lua 5.1's backtracking and capture order. Genuine
recursive pattern constructs are limited to 8,192 levels so a machine-made
pattern produces the catchable `pattern too complex` error instead of a fatal
Go stack overflow. Character classes use the deterministic C locale rather
than process-global locale state.

Published substrings and captures own their backing storage on a cache miss.
They therefore cannot keep a much larger subject buffer alive merely because
one small result escaped into a table or Go. `gsub` defers copying untouched
subject runs until the first substitution; a no-match result reuses the
original compact string, while function and table replacements reenter Lua
through the shared compact call and indexing seams.

`string.format` implements Lua 5.1's restricted C `printf` grammar rather
than delegating to Go formatting. Platform-defined finite cases follow C's
width, precision, alternate-form, and NUL behavior. Conversions that require
an out-of-range double-to-integer C cast have one documented deterministic
Badger result instead of inheriting architecture-specific undefined behavior.

`string.dump` serializes immutable Prototypes as Lua 5.1 binary chunks,
including nested functions and debug metadata. Like PUC's own format, a chunk
records native endianness, `size_t` width, instruction width, and number
layout; it is intended for a compatible Lua 5.1 ABI, not as a portable
cross-architecture format. Native Go functions are not serializable.

Lua 5.1 relies on its allocator to reject an impossible `string.rep`. Until
the runtime has one output-size policy covering concatenation and every
library builder, `string.rep` alone refuses results above 1 GiB rather than
submitting an obviously hostile allocation to the host. This is explicitly a
local guard, not a general State string quota.

Library callbacks are unprotected, matching `lua_call`. A failure is returned
to the library, which restores its own call machinery and then propagates the
original error; the nested traceback segment is captured before restoration,
so the executor's one-segment-per-frame rule still holds across the boundary.
Lua-visible mutations performed before the failure are not rolled back.

Library correctness is measured against PUC Lua 5.1.5 rather than against
another Go implementation. Each library test file carries a table of recorded
cases: a complete Lua chunk and the outcome PUC produces for it. A separate
test re-derives every recorded outcome from a real interpreter when
`BADGER_LUA51` names one, so the expectations stay verifiable without carrying
the reference binary in this repository. Cases deliberately avoid behavior Lua
5.1 leaves undefined, notably the choice of border in a table with more than
one.

## Build order

1. Canonical Value and object ownership.
2. State-neutral immutable strings and compact tables.
3. A direct recursive-descent compiler producing verified immutable
   Prototypes without retaining an AST.
4. Executor, PUC-style direct-call reentry, upvalues, and errors.
5. Native frames plus public source loading and protected calls.
6. Coroutines and native yield.
7. Reentrant native calls.
8. Context polling and context-aware calls.
9. Standard libraries and embedding operations.
10. Close the measured table, string, and allocation gap described in
    [performance.md](performance.md).
11. State-local Lua collection, weak tables, finalization, and the Lua 5.1
    collection controls.
12. Debug facilities and optional extensions.
13. Profile-driven quickening, inline caches, and executor specialization.

The compiler remains in the root package so it can build private compact
constants without introducing an exported intermediate representation.
Pattern matching is isolated only when the string library needs that seam.

## Qualification

Correctness is measured against the Lua 5.1 language behavior, not another Go
implementation's internal quirks. Official Lua 5.1 test scripts may be
included with their own provenance and license. Focused Go tests cover the
public interface, ownership, lifetime, race, and invalid-use contracts.
Algorithms adapted from the Lua reference sources are identified in
`THIRD_PARTY_NOTICES.md`.

Performance comparisons use:

- `github.com/mmcdole/badger-lua` commit
  `169b37d` as the frozen predecessor;
- PUC Lua 5.1.5 as the interpreter target; and
- LuaJIT 2.1 with JIT disabled as an additional interpreter reference.

Canonical benchmark results will record toolchain, platform, binary and
fixture hashes, time, allocations, allocated bytes, retained heap, and GC CPU.
Rolling local results belong under ignored `.bench/`, not in source control.
