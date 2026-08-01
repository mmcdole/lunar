# Lunar v1 public interface plan

Status: superseded in part. The surface decisions this plan reached are
recorded in [ADR 0006](adr/0006-cut-to-a-demand-backed-surface.md),
[ADR 0007](adr/0007-use-one-ambient-context.md), and
[ADR 0008](adr/0008-throw-is-the-callback-failure-path.md), and
[ADR 0009](adr/0009-add-demand-backed-fixed-result-calls.md), which override
the phase text below wherever they disagree. The phases remain as the record
of how the surface was built before it was measured.

Lunar's pre-v0.1 interface was not in production. The public-interface changes
recorded in this historical plan were therefore clean breaks: the
implementation did not retain aliases or deprecation shims for accidental
pre-release names.

## Goals

The v1 interface should preserve Lunar's defining boundaries:

- Go receives owning Lua values rather than an exposed execution stack.
- A native callback receives a borrowed, zero-based `Frame`.
- unqualified operations follow Lua semantics; `Raw` operations do not invoke
  metamethods;
- operations return ordinary Go errors instead of using panic as runtime
  control flow;
- contexts and deterministic budgets belong to an outer operation; and
- libraries, script-file access, and other host capabilities remain explicit.

## Execution rules

Operations that may execute Lua observe the `State`'s installed ambient
context. The corresponding operation on a live native callback belongs to
`Frame` and inherits that context and the enclosing execution budget.

A callback must use `Frame` for reentrant Lua work. Panics remain reserved for
callback-author invariants such as a negative argument index, stale Frame, or
Outcome from the wrong invocation. Closed States, foreign Values, invalid
keys, capacity failures, and Lua execution failures return errors.

## Standard-library selection

`Options.Libraries` accepts an arbitrary `LibrarySet`. Its zero value installs
no libraries. `CoreLibraries()` and `FullLibraries()` return common presets;
they are conveniences, not separate construction modes.

`CoreLibraries` contains base (including coroutine), package, table, string,
and math. The package library has no script-file authority without a
`ScriptLoader`.
`FullLibraries` adds IO, OS, and debug and is therefore intended only for
trusted scripts.

Selection order does not affect installation and duplicates are ignored. `New`
validates every selection before installing anything and returns no State if a
library fails to initialize. A literal supports unusual subsets, including
`CoroutineLibrary` without `BaseLibrary`.

Retain the individual `OpenBase`, `OpenString`, and other library methods for
hosts that deliberately grant capabilities after construction. Do not add a
second bulk `OpenLibraries` or `OpenStandardLibraries` API for v1; constructor
profiles cover the common bulk cases.

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

## Phase 3: unified script loading

One State-owned `ScriptLoader` governs `LoadFile`, `DoFile`, Lua `loadfile`, Lua
`dofile`, and the Lua source searcher used by `require`.

The loader supports:

- operating-system files;
- an `fs.FS` adapter;
- a context-aware host opener; and
- complete denial of script-file loading.

String and reader loading remain available because the host directly supplies
their contents. Preloaded Go modules remain available when source files are
denied. General IO-library file access is a separate capability.

Decisions:

- the zero-value loader denies script-file loading;
- `HostLoader` grants OS files, snapshots `LUA_PATH` during `New`, and is the
  only mode that permits filename-less `loadfile` and `dofile`;
- `FSLoader` and `FuncLoader` use slash-separated logical names and default
  `package.path` to `?.lua;?/init.lua`;
- `WithPackagePath` sets the initial `package.path`, which Lua may later
  mutate without changing the script backend;
- only `fs.ErrNotExist` advances `require` to another candidate;
- selecting or opening base or package grants no script-file authority;
- the script loader does not govern the separate IO-library filesystem surface;
  and
- the pure-Go package library has no C searchers, exposes an empty
  `package.cpath`, and retains only an unavailable `package.loadlib` stub.

Exit criteria:

- no script-loading entry point bypasses the loader;
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

Decisions:

- `CoerceNumber` accepts exact numbers and complete numeric strings;
- `CoerceString` accepts exact strings and primitive numbers, without
  invoking `__tostring`;
- `Integer` accepts only exact, finite, integral numbers representable by
  `int64`;
- `IntegerInRange` applies an inclusive range and rejects an inverted range;
- `IsMissingOrNil` composes with every exact or coercing helper instead of
  multiplying the API into `Optional*` variants; and
- variadic `ThrowArgTypeError` requires one or more distinct valid Kinds and
  preserves caller order in its diagnostic.

Do not expose the standard libraries' truncating or saturating integer
compatibility behavior until a concrete module needs it.

Use `Frame.Rethrow(*Error)` for a nested Lua failure and
`Frame.ThrowError(error)` to preserve an ordinary Go error as the cause of a
Lua runtime error. Export `Frame.ReturnArguments`. A direct `*Error` passed to
`ThrowError` is rethrown defensively; code handling a nested Lua failure should
still say `Rethrow` explicitly.

Exit criteria:

- realistic third-party native functions need no private library helpers;
- exact and coercing names cannot be confused;
- ordinary Go causes survive through `errors.Is` and `errors.As`; and
- zero-based error ordinals remain correct for calls and method receivers.

## Phase 5: typed userdata

Add a State-bound `UserDataType[T]` descriptor. Successful extraction requires
exact metatable identity and a Go type assertion to `T`.

Decisions:

- `NewUserDataType[T](state, name)` creates or reopens a registration;
- the same name and exact `T` reuse one canonical metatable;
- the same name with another `T` returns `ErrUserDataTypeConflict`;
- `Name` and `Metatable` expose immutable descriptor metadata;
- `New` constructs an instance, while `FromValue` and `FromArgument` perform
  both validations without panic control flow;
- registrations live in a private State map, not the Lua registry;
- the collector treats every registered metatable as a State root;
- the metatable is the Lua class authority, so host code may intentionally
  classify compatible bare userdata by installing it; and
- descriptor metadata and owning-value extraction remain readable after
  State closure, while construction returns `ErrClosed`.

Exit criteria:

- the wrong userdata class is rejected even when its Go payload type matches;
- a forged metatable is rejected when its payload type does not match;
- duplicate registrations have a deterministic contract; and
- an end-to-end example implements `__index` and colon-called Go methods.

## Phase 6: result-safe calls

The contract and competitive rationale are recorded in
[ADR 0005](adr/0005-use-intent-named-call-result-shapes.md).

Add State and context forms of `CallOne` and `CallDiscard`, plus Frame forms
that inherit the current operation.

`CallOne` uses Lua fixed-result adjustment: no result becomes Lua nil and extra
results are discarded. `CallDiscard` requests zero results without
materializing a result slice.

Keep `CallInto`, `Frame.CallInto`, and `ResumeInto` all-or-nothing for their
destinations. When storage is insufficient, `ResultCapacityError` owns the
complete result list and exposes a caller-owned copy through `Results()`. Its
error text is operation-neutral because calls and coroutine transitions share
the type.

The original phase deferred a numeric result-count API, `CallN`, until real
embedding code demonstrated a need. Rune supplied that evidence, so ADR 0009
adds `State.CallN` and `Frame.CallN`. `ResumeOne` and `ResumeDiscard` remain
deferred because coroutine results commonly form a protocol.

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

Add stable traceback-string formatting that never invokes Lua.

Exit criteria:

- `Prototype` implements `encoding.BinaryMarshaler`;
- serialized chunks round-trip through the verifier;
- traceback formatting remains safe after State closure.

## Deferred or rejected

Rejected for v1:

- one-based Frame indexes;
- panic-based callback checks;
- panic conversion for ordinary State and Table failures;
- an unchecked `lua.Int(int)` constructor;
- package name `lunar`;
- renaming `Kind` to `Type`;
- a generic `OpenLibraries` or `OpenStandardLibraries` method; and
- legacy global-module registration.

Deferred until use or profiling justifies them:

- `Table.All`;
- sequence mutation helpers;
- general bulk table mutation;
- `CallGlobal`;
- a separate database/network module resolver;
- streaming bytecode encoding; and
- debug hooks.

## Historical first-release gate

The first public-release review used this gate; the items remain useful toward
v1 where they still apply:

1. all breaking names and behavioral decision gates are settled;
2. every operation that may execute Lua documents context, yielding, side
   effects, and reentry;
3. all script-file loading paths obey one `ScriptLoader`;
4. native modules and typed userdata have complete examples;
5. call and coroutine results are never lost;
6. host ceilings have precise adversarial tests;
7. unit, race, vet, differential Lua 5.1, and benchmark suites pass;
8. documentation contains no stale product identity; and
9. the complete exported API receives one final naming review.
