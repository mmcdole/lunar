# Embedding Lunar

This guide follows the path most embedders take:

- [Getting started](#getting-started): create a State and run Lua.
- [Crossing the boundary](#crossing-the-boundary): expose Go code and exchange
  values.
- [Handling failure](#handling-failure): inspect Lua errors and exit requests.
- [Host policy](#host-policy): control script files, cancellation, and host
  capabilities.
- [Going further](#going-further): typed userdata, coroutines, and compiled
  chunks.

## Getting started

### Create a State

Standard libraries are an allow-list. Select the exact set the application
permits when constructing the State:

```go
state, err := lua.New(lua.Options{
	Libraries: lua.LibrarySet{
		lua.BaseLibrary,
		lua.StringLibrary,
		lua.TableLibrary,
	},
})
if err != nil {
	return err
}
defer state.Close()
```

The zero value of `Options.Libraries` installs none. `CoreLibraries()` selects
base (including coroutine), package, table, string, and math; package can use
preloaded Go modules but has no script-file authority without a `ScriptLoader`.
`FullLibraries()` adds IO, OS, and debug, so it is intended for trusted scripts.

Selection order does not matter and duplicates are ignored. `BaseLibrary`
includes Lua 5.1's coroutine library; select `CoroutineLibrary` alone to expose
coroutines without base globals. Individual methods such as `OpenString` and
`OpenIO` remain available when a host deliberately grants a library later.
Prefer construction-time selection when permissions are known up front: `New`
reports one error and never returns a partially configured State.

`Options` configures script loading, State-local streams, time, timezone,
execution limits, and load limits. A State owns native resources created by
its libraries. Always close the State. `Close` can report buffered-write or
native-resource cleanup errors; applications using IO or process resources
should handle that error.

### Run Lua code

`DoString` and, when the `ScriptLoader` permits it, `DoFile` load and execute a
chunk in one operation:

```go
results, err := state.DoString("@answer.lua", `return 6 * 7`)
if err != nil {
	return err
}
answer, _ := results[0].AsNumber()
```

Loading and execution both observe the State's installed context.

Use the separate loading and calling APIs when a compiled chunk will be called
more than once. Loading compiles source but does not execute it:

```go
chunk, err := state.LoadString("@price.lua", `
	return function(quantity, unit_price)
		return quantity * unit_price
	end
`)
if err != nil {
	return err
}

loaded, err := state.Call(chunk.Value())
if err != nil {
	return err
}
price, ok := loaded[0].AsFunction()
if !ok {
	return fmt.Errorf("chunk did not return a function")
}

result, err := state.CallOne(
	price.Value(),
	lua.Number(6),
	lua.Number(7.5),
)
if err != nil {
	return err
}
total, _ := result.AsNumber()
```

`CallOne` is the usual choice when one value is expected. It follows Lua's
normal adjustment rule: no result becomes Lua nil and extra results are
discarded. Use `Call` when every result matters; it returns an owned slice
whose values remain valid across later calls and after State closure. Use
`CallN` when a protocol expects an exact result arity greater than one:

```go
coordinates, err := state.CallN(position.Value(), 2, entity.Value())
if err != nil {
	return err
}
x, _ := coordinates[0].AsNumber()
y, _ := coordinates[1].AsNumber()
```

`CallN` pads missing results with Lua nil and discards extras inside the VM. A
count of zero executes the call and returns a nil slice. A negative or
unsupported count returns `lua.ErrInvalidResultCount`. The intent-named forms
remain clearer for zero and one result, and `CallOne` avoids a result-slice
allocation. Use `CallDiscard` for a side-effect-only call:

```go
if err := state.CallDiscard(notify.Value(), result); err != nil {
	return err
}
```

Use `CallInto` when the caller already has result storage:

```go
var destination [1]lua.Value
count, err := state.CallInto(
	price.Value(),
	[]lua.Value{lua.Number(6), lua.Number(7.5)},
	destination[:],
)
if err != nil {
	return err
}
if count != 1 {
	return fmt.Errorf("price returned %d results", count)
}
total, _ := destination[0].AsNumber()
```

If the destination is too short, `CallInto` leaves it unchanged and returns a
`*lua.ResultCapacityError`. The Lua call has already run, so its side effects
are not rolled back, but the values remain recoverable:

```go
var capacity *lua.ResultCapacityError
if errors.As(err, &capacity) {
	results := capacity.Results()
	_ = results // Every completed result, as caller-owned Values.
}
```

## Crossing the boundary

### Expose a Go function

`NativeFunc` receives a borrowed `Frame`. The Frame is both the argument view
and the active execution capability for that callback. Argument indexes are
zero-based and typed accessors do not coerce values.

```go
multiply, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
	left, ok := frame.Number(0)
	if !ok {
		frame.ThrowArgTypeError(0, lua.NumberKind)
	}
	right, ok := frame.Number(1)
	if !ok {
		frame.ThrowArgTypeError(1, lua.NumberKind)
	}
	return frame.ReturnNumber(left * right)
})
if err != nil {
	return err
}
if err := state.SetGlobal("host_multiply", multiply.Value()); err != nil {
	return err
}
```

An outer Go caller enters an idle State through `State.Call*`. Once Lua calls
a `NativeFunc`, that State is already executing. Any synchronous call back
into Lua must therefore use the callback's `Frame.Call*`, even when the call
happens indirectly through application helpers:

```go
func callHook(
	frame lua.Frame,
	hook lua.Value,
	event lua.Value,
) (lua.Value, error) {
	return frame.CallOne(hook, event)
}
```

Pass the Frame through every helper that may reenter Lua. `frame.State()`
returns the owning State for State-bound operations such as constructing or
loading values; it does not turn the active State into an idle one. Calling
`State.Call*` through it during the callback returns `lua.ErrRunning`.

Use the helper whose name matches the contract you want:

- `Number` and `String` require the exact Lua kind.
- `CoerceNumber` additionally accepts a complete numeric string.
- `CoerceString` additionally spells a number as Lua would.
- `Integer` requires an exact, finite, integral number representable by
  `int64`.
- `IntegerInRange` adds an inclusive caller-supplied range.
- `IsMissingOrNil` lets optional arguments share one explicit default path.

An optional argument composes `IsMissingOrNil` with the accessor it needs:

```go
limit := int64(25)
if !frame.IsMissingOrNil(1) {
	value, ok := frame.IntegerInRange(1, 1, 100)
	if !ok {
		frame.ThrowArgTypeError(1, lua.NumberKind)
	}
	limit = value
}
```

For example, this callback accepts a string-or-number label and an optional
bounded integer:

```go
describe, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
	label, ok := frame.CoerceString(0)
	if !ok {
		frame.ThrowArgTypeError(
			0,
			lua.StringKind,
			lua.NumberKind,
		)
	}

	limit := int64(25)
	if !frame.IsMissingOrNil(1) {
		limit, ok = frame.IntegerInRange(1, 1, 100)
		if !ok {
			frame.ThrowArgError(
				1,
				"integer from 1 through 100 expected",
			)
		}
	}
	return frame.ReturnString(fmt.Sprintf("%s:%d", label, limit))
})
```

These methods report invalid Lua input with `ok == false`; they do not panic
or silently truncate it. `ThrowArgTypeError` accepts one or more distinct
expected kinds and translates the zero-based Go index to a one-based Lua
argument ordinal.

A callback completes by returning a `Return*` Outcome, returning a `Yield*`
Outcome from a yieldable coroutine, or calling a `Throw*` method.

The Frame becomes invalid after a terminal outcome or callback return. Owning
`Value`s and typed handles read from it may be retained. `Frame.Call`,
`Frame.CallN`, `Frame.CallOne`, `Frame.CallDiscard`, `Frame.CallInto`,
`Frame.Index`, and `Frame.SetIndex` continue the active execution and are the
supported reentrant operations.

State a callback needs travels in the Go closure. An owning `Value` held that
way keeps its Lua object reachable, so a closure is both the simpler and the
safer place for it.

### Report failures from a callback

A Go panic is not converted into a Lua error. It propagates to Go after Lunar
restores the Frame. Use `Throw*` for failures that Lua should be able to
catch.

A callback ends in exactly one of three ways: it returns a `Return*` Outcome,
it returns a `Yield*` Outcome, or it throws.

| method                       | raises                                    |
| ---------------------------- | ----------------------------------------- |
| `Throw(value)`               | an arbitrary Lua value                    |
| `ThrowString(text)`          | a string                                  |
| `ThrowError(err)`            | a Go error, retained as the cause         |
| `Rethrow(failure)`           | a `*lua.Error` from a nested operation    |
| `ThrowArgError(i, reason)`   | `bad argument #i (reason)`                |
| `ThrowArgTypeError(i, kind)` | `bad argument #i (kind expected, got …)`  |

Throwing rather than returning a failure is what lets a helper called at any
depth inside the callback report the error, which is where argument checks
usually live:

```go
func checkString(frame lua.Frame, index int) string {
	text, ok := frame.String(index)
	if !ok {
		frame.ThrowArgTypeError(index, lua.StringKind)
	}
	return text
}

greet, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
	return frame.ReturnString("hello " + checkString(frame, 0))
})
```

Throws are statements, not expressions, so a guard clause reads as one and the
callback continues below it only when the check passed. A callback whose whole
body is a failure still needs a terminal return that never executes.

`Throw*` unwinds with a private panic that Lunar recovers at the native call
boundary, leaving the callback in the same state a returned Outcome would.
Host code between the `Throw*` and the `NativeFunc` must not recover it, so a
deferred recover in that range should re-panic values it does not recognize.
Panics that are not throws keep propagating to Go unchanged.

Use `ThrowError(err)` for an ordinary host failure. Lunar raises `err.Error()`
as the Lua value and retains `err` as the Go cause, so a later host caller can
use `errors.Is` or `errors.As`. Use `Rethrow(failure)` for a `*lua.Error`
returned by a nested Frame operation; it preserves the original arbitrary Lua
value, category, cause, and traceback. A direct `*lua.Error` passed to
`ThrowError` is defensively rethrown, but explicit `Rethrow` documents the
intended boundary.

```go
results, err := frame.Call(callback, arguments...)
if err != nil {
	var failure *lua.Error
	if errors.As(err, &failure) {
		frame.Rethrow(failure)
	}
	frame.ThrowError(err)
}
return frame.ReturnValues(results...)
```

`ReturnArguments` is the allocation-conscious echo path when a callback wants
to return every argument unchanged.

### Values and tables

Use `lua.Nil`, `lua.Bool`, `lua.Number`, and `lua.String` for State-neutral
scalar values. `state.String` may reuse the State's short-string cache when
constructing the same strings repeatedly. A `Value` can expose its exact kind
or convert to a typed object handle.

```go
table, err := state.NewTableWithCapacity(4, 2)
if err != nil {
	return err
}
if err := table.RawSetInt(1, lua.String("first")); err != nil {
	return err
}
if err := table.RawSetString("enabled", lua.Bool(true)); err != nil {
	return err
}
if err := state.SetGlobal("config", table.Value()); err != nil {
	return err
}
```

`Table.RawGet*` and `Table.RawSet*` never invoke metamethods. A native callback
that needs ordinary Lua `__index` or `__newindex` behavior can use
`Frame.Index` and `Frame.SetIndex`. `Table.Next` walks a table in Lua's raw
traversal order; start with `lua.Nil()` and pass each returned key back.

Data that already exists as a Go tree crosses in one pass:

```go
config, err := state.NewTableFrom(map[string]any{
	"host":    "aardmud.org",
	"port":    4000,
	"filters": []any{"combat", "chat"},
})
```

`NewTableFrom` converts scalars, `[]byte`, `[]any`, `map[string]any`, and an
owning `Value` that already belongs to this State. Slices become one-based
sequences and nested maps become nested tables. Any other Go type reports
`ErrUnsupportedTreeValue` rather than guessing, and no partially built table
becomes reachable. Conversion performs raw assignments only, so it never
invokes `__newindex` and is usable from a callback through `Frame.State`.

### Apply Lua operators

`Index`, `SetIndex`, `Len`, `Equal`, and `ToString` apply Lua's own semantics,
including metamethods. Each exists on both `State` and `Frame`:

```go
name, err := state.Index(config.Value(), lua.String("name"))
count, err := state.Len(items.Value())
same, err := state.Equal(left, right)
```

These share the executor's implementation, so a host operation and the
equivalent Lua expression agree on coercion, metamethod selection, and float
edge cases. `Raw` operations on `Table` bypass metamethods; unqualified
operations do not.

Lunar deliberately stops there. Arithmetic, concatenation, and ordering are
things Go does directly on values you have already extracted, and routing them
back through the VM buys only metamethod dispatch that host code rarely wants.
`Equal` is the exception because `__eq` has no Go equivalent.

Tables, functions, threads, and userdata belong to the State that created
them. Importing one into another State returns `lua.ErrForeignValue`. Scalars
and immutable strings may be used by more than one State.

## Handling failure

### Errors and exit requests

Execution failures are returned as `*lua.Error`. The error owns the original
Lua value and a traceback:

```go
var failure *lua.Error
if errors.As(err, &failure) {
	switch failure.Category() {
	case lua.RuntimeError:
		// Lua raised an ordinary execution error.
	case lua.SyntaxError:
		// Source or bytecode was rejected.
	case lua.ResourceError:
		// A Lua-compatible execution or loading limit was exceeded.
	case lua.ContextError:
		// The host context was cancelled or expired.
	case lua.ExitError:
		// Lua requested os.exit; inspect ExitRequest as below.
	case lua.LimitError:
		// A host-enforced ceiling such as MaxHeapBytes was exceeded.
	}
}
```

Lua `os.exit` never terminates the Go process. It returns an exit error that
unwraps to `*lua.ExitRequest`:

```go
var request *lua.ExitRequest
if errors.As(err, &request) {
	status := request.ExitCode()
	_ = status // Apply application-specific lifecycle policy.
}
```

### Position a host failure

`Frame.Where` reports the source position of an activation, formatted the way
Lua positions runtime errors. Level 0 is the native call itself and level 1 is
the activation that called it, so `Where(1)` attributes a failure to the call
site:

```go
reject, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
	frame.ThrowString(frame.Where(1) + "host rejected the request")
	return lua.Outcome{} // unreachable
})
// chunk.lua:12: host rejected the request
```

`Throw*` never positions the message it is given, so a host that wants
runtime-identical attribution adds it with `Where`, and one that wants a bare
message simply omits it.

For a whole stack, `Error.Traceback` returns the activations a failure unwound
through, and `TraceFrame.String` renders one the way Lua would. Neither
executes Lua, so both stay valid after the State closes.

## Host policy

### Control script loading

The zero value of `Options.ScriptLoader` denies script-file access. String and
reader loading still work because the host supplies those bytes directly:

```go
state, err := lua.New(lua.Options{})
// DoString and Load(reader) work.
// LoadFile and DoFile return lua.ErrScriptLoadingDisabled.
```

Use `HostLoader` for trusted applications that intentionally expose paths from
the host operating system:

```go
state, err := lua.New(lua.Options{
	Libraries:    lua.CoreLibraries(),
	ScriptLoader: lua.HostLoader(),
})
```

`HostLoader` snapshots `LUA_PATH` during `New` and uses OS path syntax. It is
also the only loader with which filename-less Lua `loadfile()` and `dofile()`
may read `Options.Stdin`.

Use `FSLoader` to restrict loading to an `fs.FS`, including an embedded script
tree:

```go
//go:embed scripts
var embedded embed.FS

scripts, err := fs.Sub(embedded, "scripts")
if err != nil {
	return err
}
state, err := lua.New(lua.Options{
	Libraries:    lua.CoreLibraries(),
	ScriptLoader: lua.FSLoader(scripts),
})
```

Filesystem names are slash-separated logical paths. `FSLoader` does not read
`LUA_PATH` or consult the process working directory. Its default module search
path is `?.lua;?/init.lua`, so `require("tools.json")` tries
`tools/json.lua`, then `tools/json/init.lua`.

`FuncLoader` accepts a context-aware host opener for generated, database,
network, overlay, or application-specific scripts:

```go
state, err := lua.New(lua.Options{
	Libraries:    lua.CoreLibraries(),
	ScriptLoader: lua.FuncLoader(func(
		ctx context.Context,
		name string,
	) (io.ReadCloser, error) {
		return openApplicationScript(ctx, name)
	}),
})
```

The opener receives a non-nil operation context, and Lunar closes every reader
it returns. During `require`, an error matching `fs.ErrNotExist` tries the next
candidate; other errors stop the search and remain available to Go through
`errors.Is` or `errors.As` if Lua does not catch them.

All loaders can replace the initial Lua path templates:

```go
loader := lua.FSLoader(scripts).
	WithPackagePath("modules/?.lua;modules/?/init.lua")
```

Lua may later assign `package.path`; that changes candidate names but never the
State's script backend. Preloaded Go modules remain usable when script loading
is denied. `package.cpath` is empty, there are no C-module searchers, and
`package.loadlib` reports that dynamic libraries are unavailable.

`ScriptLoader` governs program loading only. Selecting or opening the IO or OS
library grants its separate host capabilities; selecting or opening base or
package does not grant script-file access.

### Cancellation

One installed context governs everything a State executes:

```go
ctx, cancel := context.WithTimeout(context.Background(), time.Second)
defer cancel()

if err := state.SetContext(ctx); err != nil {
	return err
}
defer func() { _ = state.RemoveContext() }()

results, err := state.Call(handler)
```

The context is ambient. It outlives one call, applies to every thread of the
State including coroutines, and governs loading as well as execution, so a
script cannot escape it by looping inside a coroutine or a `require`.
Cancellation returns a `*lua.Error` in the `lua.ContextError` category, and
Lua `pcall` cannot catch it.

`SetContext` takes effect immediately, including from inside a native callback,
so a watchdog can be re-armed during the call it governs — pause it before
handing control to code that legitimately blocks, then install a fresh deadline
afterwards. `Frame.Context` returns the installed context, or
`context.Background` when none is installed.

Because the context outlives the operation, a cancelled context that is left
installed refuses every later operation on that State. A host that cancels per
operation clears it afterwards, as the `defer` above does.

`SetContext` is a State operation and must be serialized like any other. A host
deciding to cancel from another goroutine does so through its own context,
which is what `context.WithCancel` already provides.

A State with no installed context arms no polling at all: the check exists only
while a context is present. Lunar cannot preempt a callback while it is running
Go code, so blocking or long-running callbacks observe `Frame.Context`
themselves.

### Bound memory

`MaxValues` and `MaxFrames` bound execution slots and activations; they say
nothing about bytes, so one slot may hold an arbitrarily large string.
`MaxHeapBytes` bounds the logical Lua heap that `HeapBytes` measures:

```go
state, err := lua.New(lua.Options{
	MaxHeapBytes: 64 << 20,
})
```

The limit bounds the logical Lua heap `HeapBytes` measures — Lua objects and
their owned storage, not process memory. Opaque userdata payloads and Go
allocator overhead sit outside the count, so actual process usage is higher.

Crossing the limit schedules a collection, and the runtime raises an
uncatchable `LimitError` only if the heap is still over the limit once
unreachable objects are gone. A program that allocates far more than the
limit in total therefore runs normally as long as it retains little; one that
retains more than the limit fails. Because the check happens at execution
safe points rather than inside the allocator, a single allocation can
overshoot the limit before the runtime observes it, so `MaxHeapBytes` bounds
sustained retention rather than peak allocation. Collection runs more often
as retention approaches the limit, so a State held near saturation trades
throughput for enforcement.

While an xpcall error handler runs, the limit widens by
`max(64 KiB, MaxHeapBytes/8)` so the handler can allocate its report,
mirroring the emergency capacity `MaxValues` and `MaxFrames` grant handlers.

Every operation that runs the executor observes the limit, including
host-initiated Lua operations such as `Call`, `SetGlobal`, and `Index`. Raw
operations and explicit `Collect` do not, so a host can still build and
inspect a State that holds more than the limit allows.

### Control the collector

`Collect` performs a full collection and runs pending finalizers.
`HeapBytes` measures the live heap.

```go
if err := state.StopGC(); err != nil {
	return err
}
defer func() { _ = state.RestartGC() }()
```

`StopGC` and `RestartGC` keep a latency-sensitive section free of collection.
Both require an idle State and return `ErrRunning` otherwise. They drive the
same control block as Lua's `collectgarbage`, so a change made through either
is visible to the other, and everything else `collectgarbage` exposes stays
available to scripts.

Nothing is reclaimed while the collector is stopped. Explicit `Collect` still
works, and a State with `MaxHeapBytes` still enforces it, so a long stop can
surface a limit that automatic collection would have avoided.

Lunar's collector is synchronous — a cycle runs to completion at a safe
point — so `Collect` is a complete step and there is no separate `StepGC`.

## Going further

### Typed userdata

Use `UserDataType[T]` when a Lua userdata represents a particular Go class.
The descriptor combines two checks that are easy to omit when using bare
`UserData`:

- exact identity of the class's Lua metatable; and
- assignability of the stored Go payload to `T`.

Registration is State-local:

```go
counterType, err := lua.NewUserDataType[*Counter](
	state,
	"example.Counter",
)
```

Repeating the same name and `T` reuses the canonical metatable. Reusing the
name with another Go type returns `ErrUserDataTypeConflict`. Registrations live
outside the Lua registry, so `debug.getregistry` cannot replace the name-to-
metatable association.

Build normal Lua methods by using a methods table as `__index`:

```go
methods, err := state.NewTable()
if err != nil {
	return err
}
if err := state.SetFunctions(
	methods,
	map[string]lua.NativeFunc{
		"add": func(frame lua.Frame) lua.Outcome {
			counter, ok := counterType.FromArgument(frame, 0)
			if !ok {
				frame.ThrowArgError(
					0,
					counterType.Name()+" expected",
				)
			}

			amount := int64(1)
			if !frame.IsMissingOrNil(1) {
				amount, ok = frame.IntegerInRange(
					1,
					-1_000,
					1_000,
				)
				if !ok {
					frame.ThrowArgError(
						1,
						"bounded integer expected",
					)
				}
			}
			counter.Value += amount
			return frame.ReturnNumber(float64(counter.Value))
		},
	},
); err != nil {
	return err
}
if err := counterType.Metatable().RawSetString(
	"__index",
	methods.Value(),
); err != nil {
	return err
}
```

Construct instances through the descriptor:

```go
counter, err := counterType.New(&Counter{Value: 10})
if err != nil {
	return err
}
if err := state.SetGlobal("counter", counter.Value()); err != nil {
	return err
}
```

Lua can then use ordinary colon calls:

```lua
counter:add(5)
```

The receiver is Frame argument zero. `FromArgument` rejects another userdata
class even if it carries the same Go payload type, and rejects the registered
metatable if its payload is not assignable to `T`. `FromValue` performs the
same checks for an owning Value:

```go
counter, ok := counterType.FromValue(value)
```

The metatable is the Lua class authority, not an unforgeable creation marker.
Host code may intentionally classify compatible bare userdata by installing
the exact metatable. Opening the debug library gives Lua raw metatable powers
that bypass normal `__metatable` protection, so it remains an explicitly
trusted capability.

Descriptor metadata and `FromValue` remain readable after State closure, like
the underlying owning `UserData`. `New` returns `ErrClosed` after closure.
Changing a typed userdata's payload through `SetData` is allowed, but later
typed extraction fails safely if the new payload no longer satisfies `T`.

### Coroutines and concurrency

`State.NewThread` creates a coroutine from a callable Lua value. `Resume`
returns an owned result slice; `ResumeInto` uses caller-provided storage. If
that storage is too short, the coroutine has already advanced but the values
remain available from `ResultCapacityError.Results`. `Resume` and `ResumeInto`
observe the State's installed context under the same rules as State calls.

One State permits one active executor. Serialize State methods, thread resumes,
and mutation through owning object handles. While Lua is executing, reentry
from a native callback must use the Frame APIs. Separate States may execute
concurrently.

Owning reference values participate in State-local Lua reachability. The
ownership and collection rules are described in
[Semantic collection](collection.md).

### Cache compiled chunks

`Compile` produces a `*Prototype` without a State, and `LoadPrototype`
installs one into any State, so a build step can compile once and every State
pays only the binding. Lua's own `string.dump` still produces a binary chunk
that `LoadString` accepts when bytes on disk are what a host wants.

The format is Lua 5.1's binary chunk, which `string.dump` also writes, so
chunks move between Lua and Go in both directions. Lua 5.1 chunks describe the
host ABI in their header rather than defining a portable encoding, so the bytes
load only on a matching byte order, pointer width, and number format. Treat
them as an architecture-keyed cache and keep the source to recompile from.

Binary chunks encode structure a decoder must trust. `State.Load` and
`State.LoadString` apply the State's configured load limit while decoding;
load untrusted chunks through a State whose `MaxLoadBytes` reflects the host's
policy.
