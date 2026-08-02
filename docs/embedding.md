# Embedding Lunar

This guide follows the path most embedders take:

- [Getting started](#getting-started): create a State and run Lua.
- [Calling Go from Lua](#calling-go-from-lua): expose Go functions and report
  failures.
- [Values and tables](#values-and-tables): build Lua data and apply Lua
  operators.
- [Handling failure](#handling-failure): inspect Lua errors and exit requests.
- [Host policy](#host-policy): control script files, cancellation, and memory.
- [Going further](#going-further): typed userdata, coroutines, and compiled
  chunks.

## Getting started

A `State` is one self-contained Lua universe: its globals, heap, loaded
libraries, and coroutines. Everything in this guide happens through a State,
and separate States are isolated from each other.

### Create a State

Standard libraries are an allow-list. Pick the set your application permits
when constructing the State:

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

The zero value of `Options.Libraries` installs nothing. `CoreLibraries()`
selects base (including coroutine), package, table, string, and math;
`FullLibraries()` adds IO, OS, and debug, so reserve it for trusted scripts.
The package library can use preloaded Go modules, but it has no script-file
authority without a `ScriptLoader`. Individual methods such as `OpenString`
and `OpenIO` let you grant a library later, but prefer construction-time
selection when you know the permissions up front: `New` reports one error and
never returns a partially configured State.

`Options` also configures script loading, State-local streams, time, timezone,
and execution and load limits. A State owns native resources created by its
libraries, so always close it. `Close` can report buffered-write or cleanup
errors; handle that error if you use the IO or OS libraries.

### Run Lua code

`DoString` (and `DoFile`, when the `ScriptLoader` permits it) loads and runs a
chunk in one step:

```go
results, err := state.DoString("@answer.lua", `return 6 * 7`)
if err != nil {
	return err
}
answer, _ := results[0].AsNumber()
```

If you'll call a chunk more than once, load it once and keep the function.
Loading compiles source without executing it:

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

total, err := state.CallOne(price.Value(), lua.Number(6), lua.Number(7.5))
if err != nil {
	return err
}
amount, _ := total.AsNumber()
```

The call methods differ only in how they treat results:

- `CallOne` returns exactly one value, following Lua's normal adjustment rule:
  no result becomes nil, extras are discarded. It also avoids a result-slice
  allocation, so it's the usual choice.
- `Call` returns every result as an owned slice whose values stay valid across
  later calls and after State closure.
- `CallN` returns an exact count, padding with nil and discarding extras
  inside the VM. A count of zero runs the call and returns a nil slice; a
  negative count returns `lua.ErrInvalidResultCount`.
- `CallDiscard` runs a side-effect-only call.
- `CallInto` writes results into caller-provided storage and returns the
  count. If the destination is too short it's left unchanged and the error is
  a `*lua.ResultCapacityError`; the call has already run, and
  `ResultCapacityError.Results` recovers every completed result as
  caller-owned Values.

Loading and execution both observe the State's installed context (see
[Cancellation](#cancellation)).

## Calling Go from Lua

### Expose a Go function

A `NativeFunc` receives a borrowed `Frame`. The Frame is how the callback
reads its arguments and how it acts on the running State. Argument indexes
are zero-based, and the typed accessors do not coerce:

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

Go enters an idle State through `State.Call*`. Once Lua calls a `NativeFunc`,
the State is already executing, so any synchronous call back into Lua must go
through the callback's `Frame.Call*`, even when it happens indirectly through
application helpers:

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
returns the owning State for State-bound work such as constructing values, but
it does not make the State idle: calling `State.Call*` through it during a
callback returns `lua.ErrRunning`.

Pick the accessor whose name matches the contract you want:

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

These methods report invalid input with `ok == false`; they never panic or
silently truncate. `ThrowArgTypeError` accepts one or more expected kinds and
translates the zero-based Go index to a one-based Lua argument ordinal.

A callback completes in exactly one of three ways: it returns a `Return*`
Outcome, it returns a `Yield*` Outcome from a yieldable coroutine, or it calls
a `Throw*` method. The Frame becomes invalid afterwards, though owning
`Value`s and typed handles read from it may be retained. Keep any other state
a callback needs in its Go closure; an owning `Value` held there stays
reachable.

### Report failures from a callback

A Go panic is not converted into a Lua error; it propagates to Go after Lunar
restores the Frame. Use `Throw*` for failures Lua should be able to catch:

| method                       | raises                                    |
| ---------------------------- | ----------------------------------------- |
| `Throw(value)`               | an arbitrary Lua value                    |
| `ThrowString(text)`          | a string                                  |
| `ThrowError(err)`            | a Go error, retained as the cause         |
| `Rethrow(failure)`           | a `*lua.Error` from a nested operation    |
| `ThrowArgError(i, reason)`   | `bad argument #i (reason)`                |
| `ThrowArgTypeError(i, kind)` | `bad argument #i (kind expected, got …)`  |

Because throws unwind rather than return, a helper at any depth inside the
callback can report the error, which is where argument checks usually live:

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
code below it runs only when the check passed. A callback whose whole body is
a failure still needs a terminal return that never executes.

Under the hood, `Throw*` unwinds with a private panic that Lunar recovers at
the native call boundary. Host code between the `Throw*` and the `NativeFunc`
must not swallow it: a deferred recover in that range should re-panic values
it does not recognize. Ordinary panics keep propagating to Go unchanged.

Use `ThrowError(err)` for an ordinary host failure: Lunar raises
`err.Error()` as the Lua value and retains `err` as the Go cause, so a later
host caller can use `errors.Is` or `errors.As`. Use `Rethrow(failure)` for a
`*lua.Error` returned by a nested Frame operation, since it preserves the
original Lua value, category, cause, and traceback:

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

`ReturnArguments` returns every argument unchanged without allocating a
result slice.

## Values and tables

Build scalars with `lua.Nil`, `lua.Bool`, `lua.Number`, and `lua.String`;
they belong to no particular State. `state.String` may reuse the State's
short-string cache when you construct the same strings repeatedly. A `Value` can expose its exact kind or
convert to a typed object handle.

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

`Table.RawGet*` and `Table.RawSet*` never invoke metamethods; a callback that
wants ordinary `__index` or `__newindex` behavior uses `Frame.Index` and
`Frame.SetIndex`. `Table.Next` walks a table in Lua's raw traversal order:
start with `lua.Nil()` and pass each returned key back.

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
sequences; nested maps become nested tables. Any other Go type reports
`ErrUnsupportedTreeValue` rather than guessing, and no partially built table
becomes reachable. Conversion performs raw assignments only, so it never
invokes `__newindex` and is usable from a callback through `Frame.State`.

### Apply Lua operators

`Index`, `SetIndex`, `Len`, `Equal`, and `ToString` apply Lua's own semantics,
metamethods included. Each exists on both `State` and `Frame`:

```go
name, err := state.Index(config.Value(), lua.String("name"))
count, err := state.Len(items.Value())
same, err := state.Equal(left, right)
```

These share the executor's implementation, so a host operation and the
equivalent Lua expression agree on coercion, metamethod selection, and float
edge cases. Lunar deliberately stops there: arithmetic, concatenation, and
ordering are things Go does directly on extracted values, and routing them
through the VM would buy only metamethod dispatch that host code rarely wants.
`Equal` is the exception because `__eq` has no Go equivalent.

Tables, functions, threads, and userdata belong to the State that created
them; importing one into another State returns `lua.ErrForeignValue`. Scalars
and immutable strings may be shared.

## Handling failure

### Errors and exit requests

Execution failures come back as `*lua.Error`, which owns the original Lua
value and a traceback:

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

For a whole stack, `Error.Traceback` returns the activations a failure unwound
through, and `TraceFrame.String` renders one the way Lua would. Neither
executes Lua, so both stay valid after the State closes.

### Position a host failure

`Frame.Where` reports the source position of an activation, formatted the way
Lua positions runtime errors. Level 0 is the native call itself and level 1 is
its caller, so `Where(1)` attributes a failure to the call site:

```go
reject, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
	frame.ThrowString(frame.Where(1) + "host rejected the request")
	return lua.Outcome{} // unreachable
})
// chunk.lua:12: host rejected the request
```

`Throw*` never adds position information to the message it is given: prepend
`Where` when you want the failure attributed like a runtime error, or omit it
for a bare message.

## Host policy

### Control script loading

The zero value of `Options.ScriptLoader` denies script-file access. String and
reader loading still work because the host supplies those bytes directly:

```go
state, err := lua.New(lua.Options{})
// DoString and Load(reader) work.
// LoadFile and DoFile return lua.ErrScriptLoadingDisabled.
```

`HostLoader` intentionally exposes paths from the host operating system:

```go
state, err := lua.New(lua.Options{
	Libraries:    lua.CoreLibraries(),
	ScriptLoader: lua.HostLoader(),
})
```

It snapshots `LUA_PATH` during `New` and uses OS path syntax. It is also the
only loader with which filename-less Lua `loadfile()` and `dofile()` may read
`Options.Stdin`.

`FSLoader` restricts loading to an `fs.FS`, such as an embedded script tree:

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

Filesystem names are slash-separated logical paths; `FSLoader` reads neither
`LUA_PATH` nor the process working directory. Its default module search path
is `?.lua;?/init.lua`, so `require("tools.json")` tries `tools/json.lua`, then
`tools/json/init.lua`.

`FuncLoader` accepts a context-aware host opener for generated, database,
network, overlay, or otherwise application-specific scripts:

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
it returns. During `require`, an error matching `fs.ErrNotExist` tries the
next candidate; other errors stop the search and remain visible to Go through
`errors.Is` or `errors.As` if Lua does not catch them.

Every loader can replace the initial Lua path templates:

```go
loader := lua.FSLoader(scripts).
	WithPackagePath("modules/?.lua;modules/?/init.lua")
```

Lua may later assign `package.path`; that changes candidate names but never
the State's script backend. Preloaded Go modules remain usable when script
loading is denied. `package.cpath` is empty, there are no C-module searchers,
and `package.loadlib` reports that dynamic libraries are unavailable.

`ScriptLoader` governs program loading only. The IO and OS libraries carry
their own host capabilities; selecting base or package grants no script-file
access.

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

The context is ambient: it outlives one call, applies to every thread of the
State including coroutines, and governs loading as well as execution, so a
script cannot escape it by looping inside a coroutine or a `require`.
Cancellation returns a `*lua.Error` in the `lua.ContextError` category, and
Lua `pcall` cannot catch it. Because the context outlives the operation, a
cancelled context left installed refuses every later operation on that State.
If you cancel per operation, clear the context afterwards, as the `defer`
above does.

`SetContext` takes effect immediately, including from inside a native
callback, so a watchdog can be re-armed during the call it governs: pause it
before handing control to code that legitimately blocks, then install a fresh
deadline afterwards. `Frame.Context` returns the installed context, or
`context.Background` when none is installed.

`SetContext` is a State operation and must be serialized like any other; to
cancel from another goroutine, cancel the context itself, which is what
`context.WithCancel` already provides. A State with no installed context arms
no polling at all. Lunar cannot preempt a callback while it is running Go
code, so blocking or long-running callbacks should observe `Frame.Context`
themselves.

### Bound memory

`MaxValues` and `MaxFrames` bound execution slots and activations. They say
nothing about bytes (one slot may hold an arbitrarily large string), so
`MaxHeapBytes` separately bounds the logical Lua heap:

```go
state, err := lua.New(lua.Options{
	MaxHeapBytes: 64 << 20,
})
```

The limit covers what `HeapBytes` measures: Lua objects and their owned
storage, not process memory. Opaque userdata payloads and Go allocator
overhead sit outside the count, so actual process usage is higher.

Crossing the limit schedules a collection, and the runtime raises an
uncatchable `LimitError` only if the heap is still over the limit once
unreachable objects are gone. A program that allocates freely but retains
little runs normally; one that retains more than the limit fails. The check
happens at execution safe points rather than inside the allocator, so a
single allocation can overshoot before the runtime observes it;
`MaxHeapBytes` bounds sustained retention, not peak allocation. Collection
runs more often as retention approaches the limit, so a State held near
saturation trades throughput for enforcement.

While an xpcall error handler runs, the limit widens by
`max(64 KiB, MaxHeapBytes/8)` so the handler can allocate its report,
mirroring the emergency capacity `MaxValues` and `MaxFrames` grant handlers.

Every operation that runs the executor observes the limit, including
host-initiated ones such as `Call`, `SetGlobal`, and `Index`. Raw operations
and explicit `Collect` do not, so a host can still build and inspect a State
that holds more than the limit allows.

### Control the collector

`Collect` performs a full collection and runs pending finalizers; `HeapBytes`
measures the live heap.

```go
if err := state.StopGC(); err != nil {
	return err
}
defer func() { _ = state.RestartGC() }()
```

`StopGC` and `RestartGC` keep a latency-sensitive section free of collection.
Both require an idle State and return `ErrRunning` otherwise. They drive the
same control block as Lua's `collectgarbage`, so a change made through either
is visible to the other.

Nothing is reclaimed while the collector is stopped. Explicit `Collect` still
works, and `MaxHeapBytes` is still enforced, so a long stop can surface a
limit that automatic collection would have avoided.

Lunar's collector is synchronous: a cycle runs to completion at a safe
point, so `Collect` is a complete step and there is no separate `StepGC`.

## Going further

### Typed userdata

Use `UserDataType[T]` when a Lua userdata represents a particular Go class.
The descriptor combines two checks that are easy to omit with bare
`UserData`: exact identity of the class's Lua metatable, and assignability of
the stored Go payload to `T`. Registration is State-local:

```go
counterType, err := lua.NewUserDataType[*Counter](
	state,
	"example.Counter",
)
```

Repeating the same name and `T` reuses the canonical metatable; reusing the
name with another Go type returns `ErrUserDataTypeConflict`. Registrations
live outside the Lua registry, so `debug.getregistry` cannot replace the
name-to-metatable association.

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

Lua then uses ordinary colon calls:

```lua
counter:add(5)
```

The receiver is Frame argument zero. `FromArgument` rejects another userdata
class even if it carries the same Go payload type, and rejects the registered
metatable if its payload is not assignable to `T`. `FromValue` performs the
same checks for an owning Value.

The metatable is the Lua class authority, not an unforgeable creation marker:
host code may intentionally classify compatible bare userdata by installing
the exact metatable. Opening the debug library gives Lua raw metatable powers
that bypass `__metatable` protection, which is why debug remains an
explicitly trusted capability.

Descriptor metadata and `FromValue` remain readable after State closure, like
the underlying owning `UserData`; `New` returns `ErrClosed` once the State is
closed. Changing a typed
userdata's payload through `SetData` is allowed, but later typed extraction
fails safely if the new payload no longer satisfies `T`.

### Coroutines and concurrency

`State.NewThread` creates a coroutine from a callable Lua value. `Resume`
returns an owned result slice; `ResumeInto` uses caller-provided storage, and
if that storage is too short the coroutine has already advanced but the values
remain available from `ResultCapacityError.Results`. Both observe the State's
installed context under the same rules as State calls.

One State permits one active executor. Serialize State methods, thread
resumes, and mutation through owning object handles; while Lua is executing,
reentry from a native callback must use the Frame APIs. Separate States may
execute concurrently.

Owning reference values participate in State-local Lua reachability; the
ownership and collection rules are described in
[Semantic collection](collection.md).

### Cache compiled chunks

`Compile` produces a `*Prototype` without a State, and `LoadPrototype`
installs one into any State, so a build step can compile once and each State
pays only the cost of binding it. Lua's own `string.dump` still produces a binary chunk
that `LoadString` accepts when bytes on disk are what you want.

The format is Lua 5.1's binary chunk, so chunks move between Lua and Go in
both directions. Lua 5.1 chunks describe the host ABI in their header rather
than defining a portable encoding, and the bytes load only on a matching byte
order, pointer width, and number format. Treat them as an architecture-keyed
cache and keep the source to recompile from.

Binary chunks encode structure a decoder must trust. `State.Load` and
`State.LoadString` apply the State's configured load limit while decoding;
load untrusted chunks through a State whose `MaxLoadBytes` reflects your
policy.
