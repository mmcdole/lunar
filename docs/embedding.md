# Embedding Lunar

## Create a State

`New` creates an empty State. Open only the libraries the application permits:

```go
state, err := lua.New(lua.Options{})
if err != nil {
	return err
}
defer state.Close()

for _, open := range []func() error{
	state.OpenBase,
	state.OpenMath,
	state.OpenString,
	state.OpenTable,
} {
	if err := open(); err != nil {
		return err
	}
}
```

`OpenBase` also opens the coroutine library. `OpenCoroutine` is available when
coroutines are needed without the base globals. IO, OS, package, and debug are
separate openers.

`Options` configures source access, State-local streams, time, timezone,
execution limits, and load limits. A State owns native resources created by
its libraries. Always close the State. `Close` can report buffered-write or
native-resource cleanup errors; applications using IO or process resources
should handle that error.

## Control source loading

The zero value of `Options.Source` denies source-file access. String and reader
loading still work because the host supplies those bytes directly:

```go
state, err := lua.New(lua.Options{})
// DoString and Load(reader) work.
// LoadFile and DoFile return lua.ErrSourceLoadingDisabled.
```

Use `OSSource` for trusted applications that intentionally expose paths from
the host operating system:

```go
state, err := lua.New(lua.Options{
	Source: lua.OSSource(),
})
```

OS mode snapshots `LUA_PATH` during `New` and uses OS path syntax. It is also
the only mode in which filename-less Lua `loadfile()` and `dofile()` may read
`Options.Stdin`.

Use `FSSource` to restrict loading to an `fs.FS`, including an embedded script
tree:

```go
//go:embed scripts
var embedded embed.FS

scripts, err := fs.Sub(embedded, "scripts")
if err != nil {
	return err
}
state, err := lua.New(lua.Options{
	Source: lua.FSSource(scripts),
})
```

Filesystem names are slash-separated logical paths. `FSSource` does not read
`LUA_PATH` or consult the process working directory. Its default module search
path is `?.lua;?/init.lua`, so `require("tools.json")` tries
`tools/json.lua`, then `tools/json/init.lua`.

`CustomSource` accepts a context-aware host opener for generated, database,
network, overlay, or application-specific sources:

```go
state, err := lua.New(lua.Options{
	Source: lua.CustomSource(func(
		ctx context.Context,
		name string,
	) (io.ReadCloser, error) {
		return openApplicationSource(ctx, name)
	}),
})
```

The opener receives a non-nil operation context, and Lunar closes every reader
it returns. During `require`, an error matching `fs.ErrNotExist` tries the next
candidate; other errors stop the search and remain available to Go through
`errors.Is` or `errors.As` if Lua does not catch them.

All policies can replace the initial Lua path templates:

```go
source := lua.FSSource(scripts).
	WithPackagePath("modules/?.lua;modules/?/init.lua")
```

Lua may later assign `package.path`; that changes candidate names but never the
State's source backend. Preloaded Go modules remain usable when source loading
is denied. `package.cpath` is empty, there are no C-module searchers, and
`package.loadlib` reports that dynamic libraries are unavailable.

SourcePolicy governs program loading only. Opening the IO or OS library grants
their separately documented capabilities; conversely, opening the base or
package library does not grant source access.

## Load and call Lua

`DoString` and, when the SourcePolicy permits it, `DoFile` load and execute a
chunk in one operation:

```go
results, err := state.DoString("@answer.lua", `return 6 * 7`)
if err != nil {
	return err
}
answer, _ := results[0].AsNumber()
```

Their context-aware forms observe the same context during both loading and
execution.

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
`CallDiscard` for a side-effect-only call:

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

## Values and tables

Use `lua.Nil`, `lua.Bool`, and `lua.Number` for State-neutral scalar values.
Use `state.String` for strings. A `Value` can expose its exact kind or convert
to a typed object handle.

```go
table, err := state.NewTableWithCapacity(4, 2)
if err != nil {
	return err
}
if err := table.RawSetInt(1, state.String("first")); err != nil {
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
`Frame.Index` and `Frame.SetIndex`.

Tables, functions, threads, and userdata belong to the State that created
them. Importing one into another State returns `lua.ErrForeignValue`. Scalars
and immutable strings may be used by more than one State.

## Expose a Go function

`NativeFunc` receives a borrowed `Frame`. Argument indexes are zero-based and
typed accessors do not coerce values.

```go
multiply, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
	left, ok := frame.Number(0)
	if !ok {
		return frame.ArgTypeError(0, lua.NumberKind)
	}
	right, ok := frame.Number(1)
	if !ok {
		return frame.ArgTypeError(1, lua.NumberKind)
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

Use the helper whose name matches the contract you want:

- `Number` and `String` require the exact Lua kind.
- `CoerceNumber` additionally accepts a complete numeric string.
- `CoerceString` additionally spells a number as Lua would.
- `Integer` requires an exact, finite, integral number representable by
  `int64`.
- `IntegerInRange` adds an inclusive caller-supplied range.
- `IsMissingOrNil` lets optional arguments share one explicit default path.

For example, this callback accepts a string-or-number label and an optional
bounded integer:

```go
describe, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
	label, ok := frame.CoerceString(0)
	if !ok {
		return frame.ArgTypeError(
			0,
			lua.StringKind,
			lua.NumberKind,
		)
	}

	limit := int64(25)
	if !frame.IsMissingOrNil(1) {
		limit, ok = frame.IntegerInRange(1, 1, 100)
		if !ok {
			return frame.ArgError(
				1,
				"integer from 1 through 100 expected",
			)
		}
	}
	return frame.ReturnString(fmt.Sprintf("%s:%d", label, limit))
})
```

These methods report invalid Lua input with `ok == false`; they do not panic
or silently truncate it. `ArgTypeError` accepts one or more distinct expected
kinds and translates the zero-based Go index to a one-based Lua argument
ordinal.

A callback must return an `Outcome` created by its current Frame:

- `Return*` publishes results;
- `Raise*` raises a Lua error; and
- `Yield*` suspends a yieldable coroutine.

The Frame becomes invalid after a terminal outcome or callback return. Owning
`Value`s and typed handles read from it may be retained. `Frame.Call`,
`Frame.CallInto`, `Frame.Index`, and `Frame.SetIndex` are the supported
reentrant operations.

Captured values can be supplied after the callback argument to
`NewNativeFunction` and read with `Frame.Capture`. Captures are copied into the
function's private runtime storage.

A Go panic is not converted into a Lua error. It propagates to Go after Lunar
restores the Frame. Use `Raise*`, `ArgError`, or `ArgTypeError` for failures
that Lua should be able to catch.

Use `RaiseError(err)` for an ordinary host failure. Lunar raises `err.Error()`
as the Lua value and retains `err` as the Go cause, so a later host caller can
use `errors.Is` or `errors.As`. Use `Reraise(failure)` for a `*lua.Error`
returned by a nested Frame operation; it preserves the original arbitrary Lua
value, category, cause, and traceback. A direct `*lua.Error` passed to
`RaiseError` is defensively reraised, but explicit `Reraise` documents the
intended boundary.

```go
results, err := frame.Call(callback, arguments...)
if err != nil {
	var failure *lua.Error
	if errors.As(err, &failure) {
		return frame.Reraise(failure)
	}
	return frame.RaiseError(err)
}
return frame.ReturnValues(results...)
```

`ReturnArguments` is the allocation-conscious echo path when a callback wants
to return every argument unchanged.

## Typed userdata

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
				return frame.ArgError(
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
					return frame.ArgError(
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

## Cancellation

Use the context-aware methods when a host request must be interruptible:

- `DoStringContext` and `DoFileContext`;
- `LoadContext` and `LoadFileContext`;
- `LoadStringContext`;
- `CallContext`, `CallOneContext`, `CallDiscardContext`, and
  `CallIntoContext`; and
- `Thread.ResumeContext` and `Thread.ResumeIntoContext`.

The supplied context is available to native callbacks through
`Frame.Context`. Cancellation returns a `*lua.Error` in the
`lua.ContextError` category. Lua `pcall` cannot catch host cancellation.
Ordinary non-context methods do not install cancellation polling. Lunar cannot
preempt a callback while it is running Go code; blocking or long-running
callbacks must observe `Frame.Context()` themselves.

## Errors and exit requests

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
		// A configured deterministic limit was exceeded.
	case lua.ContextError:
		// The host context was cancelled or expired.
	case lua.ExitError:
		// Lua requested os.exit; inspect ExitRequest as below.
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

## Coroutines and concurrency

`State.NewThread` creates a coroutine from a callable Lua value. `Resume`
returns an owned result slice; `ResumeInto` uses caller-provided storage. If
that storage is too short, the coroutine has already advanced but the values
remain available from `ResultCapacityError.Results`. The context-aware forms
interrupt execution under the same rules as State calls.

One State permits one active executor. Serialize State methods, thread resumes,
and mutation through owning object handles. While Lua is executing, reentry
from a native callback must use the Frame APIs. Separate States may execute
concurrently.

Owning reference values participate in State-local Lua reachability. The
ownership and collection rules are described in
[Semantic collection](collection.md).
