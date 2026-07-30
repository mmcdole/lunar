# Lunar v1 public interface plan

Status: working plan for the first public release.

Lunar is not released or in production. Public interface changes in this plan
are therefore clean breaks: the implementation will not retain aliases or
deprecation shims for accidental pre-release names.

## Goals

The v1 interface should preserve Lunar's defining boundaries:

- Go receives owning Lua values rather than an exposed execution stack.
- A native callback receives a borrowed, zero-based `Frame`.
- unqualified operations follow Lua semantics; `Raw` operations do not invoke
  metamethods;
- operations return ordinary Go errors instead of using panic as runtime
  control flow;
- contexts and deterministic budgets belong to an outer operation; and
- libraries, source access, and other host capabilities remain explicit.

## Execution rules

Operations that may execute Lua have host-side `State` forms and, when
applicable, context-aware forms. The corresponding operation on a live native
callback belongs to `Frame` and inherits the enclosing context and execution
budget.

A callback must use `Frame` for reentrant Lua work. Panics remain reserved for
callback-author invariants such as a negative argument index, stale Frame, or
Outcome from the wrong invocation. Closed States, foreign Values, invalid
keys, capacity failures, and Lua execution failures return errors.

## Phase 0: canonical identity and vocabulary

1. Change the module path and all internal modules to
   `github.com/mmcdole/lunar`.
2. Keep `package lua`.
3. Replace remaining legacy branding in documentation, assets, diagnostics,
   benchmark descriptions, and repository configuration.
4. Add package-level `String`; keep `State.String` as the reuse-optimized form.
5. Rename Value reference accessors to `AsTable`, `AsFunction`, `AsUserData`,
   and `AsThread`.
6. Replace `State.NewTable(arrayHint, recordHint)` with `State.NewTable()` and
   `State.NewTableWithCapacity(arrayHint, recordHint)`.
7. Replace `Frame.LuaThread(index)` and `Frame.Thread()` with
   `Frame.Thread(index)` and `Frame.CurrentThread()`.
8. Audit Table read methods that currently cannot distinguish a closed State
   from a missing value. Before v1 they must either return errors or define a
   real post-close snapshot contract; returning errors is preferred.

Exit criteria:

- no public pre-release aliases remain;
- all examples use the final names;
- all module paths and product prose say Lunar; and
- the repository builds and tests under the renamed module.

## Phase 1: primitive traversal and Lua operations

Add `Table.Next(after)` as the exact traversal primitive. It starts with Lua
nil, reports completion separately from errors, supports deleting the current
key, and reports invalid continuation keys. Insertion during traversal remains
undefined.

Add State and State-context forms of:

- `Index` and `SetIndex`;
- `ToString`;
- `Len`;
- `Equal`;
- `LessThan`; and
- `LessEqual`.

Add or retain corresponding Frame forms. `Len` should preserve an arbitrary
metamethod result as a `Value`; `ToString` should return a Go string and reject
a non-string `__tostring` result.

Make `Global` and `SetGlobal` Lua operations. Add `GlobalContext`,
`SetGlobalContext`, `RawGlobal`, and `SetRawGlobal`.

Exit criteria:

- direct paths and every applicable metamethod path are tested;
- context cancellation, illegal yield, ownership, reentry, and error
  traceback behavior are covered; and
- raw operations are proven not to execute Lua.

## Phase 2: Go module workflow

Add `State.PreloadModule` and `State.SetFunctions`.

The State owns one preload table for its lifetime. `OpenPackage` publishes that
same table on every opening, so host registrations work before or after the
package library is opened and survive reopening.

`SetFunctions` validates the table, every function, and every capture before
publishing any field. A failed call does not partially install the requested
function set.

Exit criteria:

- preloading works before and after `OpenPackage`;
- reopening preserves host and Lua additions to the stable preload table;
- replacement of a named preload is deterministic; and
- invalid functions, captures, and foreign tables leave the target unchanged.

## Phase 3: unified source policy

One State-owned policy governs `LoadFile`, `DoFile`, Lua `loadfile`, Lua
`dofile`, and the Lua source searcher used by `require`.

The policy supports:

- operating-system files;
- an `fs.FS` adapter;
- a context-aware host opener; and
- complete denial of source-file loading.

String and reader loading remain available because the host directly supplies
their contents. Preloaded Go modules remain available when source files are
denied. General IO-library file access is a separate capability.

Decision gate before implementation:

- decide whether zero-value `Options` denies files or retains OS behavior;
- decide whether denied mode also rejects the standard-input form of
  `loadfile` and `dofile` (recommended: yes);
- settle the logical path contract and initial `package.path`; and
- decide when OS-mode `LUA_PATH` is snapshotted (recommended: during `New`).

Exit criteria:

- no source-loading entry point bypasses the policy;
- custom filesystems never consult process-global paths;
- denied mode performs no OS source open;
- Windows and slash-based `fs.FS` paths have explicit tests; and
- errors retain useful logical filenames and underlying causes.

## Phase 4: native callback authoring

Keep exact, non-coercing typed argument methods. Add deliberately named helpers
for:

- strict integral `int64` values;
- caller-supplied integer ranges;
- numeric-string to number coercion;
- number-to-string coercion;
- missing-or-nil defaults; and
- diagnostics accepting one of several Kinds.

Do not expose the standard libraries' truncating or saturating integer
compatibility behavior until a concrete module needs it.

Rename the current `Frame.RaiseError(*Error)` to `Frame.Reraise(*Error)`. Add
`Frame.RaiseError(error)` to preserve an ordinary Go error as the cause of a
Lua runtime error, and export `Frame.ReturnArguments`.

Exit criteria:

- realistic third-party native functions need no private library helpers;
- exact and coercing names cannot be confused;
- ordinary Go causes survive through `errors.Is` and `errors.As`; and
- zero-based error ordinals remain correct for calls and method receivers.

## Phase 5: typed userdata

Prototype a State-bound `UserDataType[T]` descriptor. Successful extraction
requires exact metatable identity and the expected Go payload type. Reusing a
name with a different Go type is an error.

The descriptor should expose its name and metatable, construct userdata with
that metatable, and extract payloads from an owning Value or Frame argument.
The type registry should be private to the State rather than redefinable
through `debug.getregistry`.

Exit criteria:

- the wrong userdata class is rejected even when its Go payload type matches;
- a forged metatable is rejected when its payload type does not match;
- duplicate registrations have a deterministic contract; and
- an end-to-end example implements `__index` and colon-called Go methods.

## Phase 6: result-safe calls

Add State and context forms of `CallOne` and `CallDiscard`, plus Frame forms
that inherit the current operation.

`CallOne` uses Lua fixed-result adjustment: no result becomes Lua nil and extra
results are discarded. `CallDiscard` requests zero results without
materializing a result slice.

Keep `CallInto`, `Frame.CallInto`, and `ResumeInto` all-or-nothing for their
destinations. When storage is insufficient, `ResultCapacityError` owns the
complete result list and exposes it through `Results()`.

Exit criteria:

- completed calls and coroutine transitions never lose results;
- overflow results remain valid across later calls and State closure;
- destinations remain unchanged on every failure; and
- the sufficient-capacity fast paths retain their allocation behavior.

## Phase 7: deterministic host ceilings

Add logical-heap and VM-instruction limits.

The heap quota uses the same accounting boundary as `HeapBytes`; it is not a
promise about process RSS, Go allocator overhead, opaque userdata payloads, or
memory allocated by native callbacks. Raw operations must never invoke Lua
finalizers merely to satisfy the quota.

The instruction budget counts Lua VM instructions for one outer State
operation or coroutine resume. Nested calls and metamethods share that budget;
native Go execution does not count toward it.

Decision gate before implementation:

- decide whether either limit has a non-zero default;
- decide whether instruction exhaustion is catchable by Lua (recommended:
  sticky and uncatchable for the outer operation);
- decide whether each Resume receives a fresh budget (recommended: yes); and
- specify heap admission, collection, finalizer, and emergency-error behavior.

Exit criteria:

- adversarial loops terminate deterministically;
- protected calls cannot reset or evade a host ceiling;
- rejected heap growth leaves public structures valid and unmodified;
- the State remains usable according to the documented recovery contract; and
- disabled ceilings add no meaningful regression to the hot VM benchmarks.

## Phase 8: bytecode and release conveniences

Expose `Prototype.MarshalBinary`, `Prototype.InstructionCount`, and bounds-safe
child access. Defer `WriteTo` until it can use the standard
`io.WriterTo` signature and a genuinely streaming encoder.

Add `OpenStandardLibraries` with explicit documentation that it includes
package, IO, OS, and debug capabilities and is not a sandbox-safe default.

Add stable traceback-string formatting that never invokes Lua.

Exit criteria:

- `Prototype` implements `encoding.BinaryMarshaler`;
- serialized chunks round-trip through the verifier;
- the standard-library opener matches the documented complete set; and
- traceback formatting remains safe after State closure.

## Deferred or rejected

Rejected for v1:

- one-based Frame indexes;
- panic-based callback checks;
- panic conversion for ordinary State and Table failures;
- an unchecked `lua.Int(int)` constructor;
- package name `lunar`;
- renaming `Kind` to `Type`; and
- legacy global-module registration.

Deferred until use or profiling justifies them:

- `Table.All`;
- sequence mutation helpers;
- general bulk table mutation;
- `CallGlobal`;
- a separate database/network module resolver;
- streaming bytecode encoding; and
- debug hooks.

## Final release gate

Before the first public release:

1. all breaking names and behavioral decision gates are settled;
2. every operation that may execute Lua documents context, yielding, side
   effects, and reentry;
3. all source-loading paths obey one policy;
4. native modules and typed userdata have complete examples;
5. call and coroutine results are never lost;
6. host ceilings have precise adversarial tests;
7. unit, race, vet, differential Lua 5.1, and benchmark suites pass;
8. documentation contains no stale product identity; and
9. the complete exported API receives one final naming review.
