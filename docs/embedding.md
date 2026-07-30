# Embedding Lunik

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

`Options` configures State-local streams, time, timezone, execution limits, and
load limits. A State owns native resources created by its libraries. Always
close the State. `Close` can report buffered-write or native-resource cleanup
errors; applications using IO or process resources should handle that error.

## Load and call Lua

Loading compiles source but does not execute it:

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
price, ok := loaded[0].Function()
if !ok {
	return fmt.Errorf("chunk did not return a function")
}

results, err := state.Call(
	price.Value(),
	lua.Number(6),
	lua.Number(7.5),
)
if err != nil {
	return err
}
total, _ := results[0].AsNumber()
```

`Call` returns an owned result slice. Its values remain valid across later
calls and after State closure.

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
`*lua.ResultCapacityError` containing the required size. The Lua call has
already run, so its side effects are not rolled back.

## Values and tables

Use `lua.Nil`, `lua.Bool`, and `lua.Number` for State-neutral scalar values.
Use `state.String` for strings. A `Value` can expose its exact kind or convert
to a typed object handle.

```go
table, err := state.NewTable(4, 2)
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

A Go panic is not converted into a Lua error. It propagates to Go after Lunik
restores the Frame. Use `Raise*`, `ArgError`, or `ArgTypeError` for failures
that Lua should be able to catch.

## Cancellation

Use the context-aware methods when a host request must be interruptible:

- `LoadContext` and `LoadFileContext`;
- `LoadStringContext`;
- `CallContext` and `CallIntoContext`; and
- `Thread.ResumeContext` and `Thread.ResumeIntoContext`.

The supplied context is available to native callbacks through
`Frame.Context`. Cancellation returns a `*lua.Error` in the
`lua.ContextError` category. Lua `pcall` cannot catch host cancellation.
Ordinary non-context methods do not install cancellation polling. Lunik cannot
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
returns an owned result slice; `ResumeInto` uses caller-provided storage. The
context-aware forms interrupt execution under the same rules as State calls.

One State permits one active executor. Serialize State methods, thread resumes,
and mutation through owning object handles. While Lua is executing, reentry
from a native callback must use the Frame APIs. Separate States may execute
concurrently.

Owning reference values participate in State-local Lua reachability. The
ownership and collection rules are described in
[Semantic collection](collection.md).
