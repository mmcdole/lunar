---
status: accepted
---

# Use intent-named call result shapes and recover overflow results

The decision below remains in force for the lossless default, the named zero-
and one-result forms, overflow recovery, and coroutine results.
[ADR 0009](0009-add-demand-backed-fixed-result-calls.md) supersedes only the
decision to omit `CallN`, after Rune supplied a concrete fixed-arity embedding
use case.

## Context

Lua functions can return zero, one, or many values. Lunar does not expose an
execution stack, so its host API must express the caller's result intent while
returning owning `Value`s and leaving the State immediately reusable.

The existing `Call` and `Resume` methods safely return all results. Their
caller-storage variants are all-or-nothing, but an undersized destination
currently loses the completed results. This is especially serious for
`ResumeInto`: the coroutine has already yielded or returned and that transition
cannot be replayed.

This comparison covers public host call and coroutine result APIs rather than
language compatibility or VM performance.

## Competitive analysis

| Runtime | Ordinary function calls | Coroutine results | Relevant tradeoff |
| --- | --- | --- | --- |
| [PUC Lua 5.1](https://www.lua.org/manual/5.1/manual.html#lua_call) | `lua_call` and `lua_pcall` take a fixed result count or `LUA_MULTRET`; fixed counts discard extras and fill missing values with nil. Results remain on the exposed stack. | [`lua_resume`](https://www.lua.org/manual/5.1/manual.html#lua_resume) leaves every yielded or returned value on the coroutine stack. | Precise and recoverable, but the host must balance and interpret mutable VM stack state. |
| [LuaJIT 2.1](https://luajit.org/install.html) | It is API-compatible with Lua 5.1, so it has the same integer result count and stack contract. | It retains the Lua 5.1 coroutine stack model. | JIT compilation does not change the embedding result contract. |
| [Luau](https://luau.org/api/#making-calls) | `lua_call` and `lua_pcall` also take a fixed count or `LUA_MULTRET`, with the nil-fill and extra-result rules documented explicitly. | Resumption is stack-based and advances the thread before the host examines its stack. | A modern VM still uses the C-shaped contract; it is powerful but not a good fit for Lunar's no-stack API. |
| [GopherLua 1.1.2](https://github.com/yuin/gopher-lua/blob/v1.1.2/state.go) | `Call`, `PCall`, and `CallByParam` use `NRet`, including `0`, `1`, and `MultRet`; results remain on `LState`'s exposed stack. | `Resume` eagerly returns all yielded or final values as `[]LValue`. | The call path requires stack cleanup; the coroutine path avoids a capacity failure by always creating a result slice. |
| [Shopify go-lua](https://pkg.go.dev/github.com/Shopify/go-lua@v0.0.0-20250718183320-1e37f32ad7d0) | `Call` and `ProtectedCall` use a fixed `resultCount` or `MultipleReturns` and leave results on the stack. | The pinned implementation does not expose a complete public coroutine API. | It closely ports the Lua 5.2 C API rather than designing a Go-native ownership boundary. |
| [mlua 0.12](https://docs.rs/mlua/latest/mlua/struct.Function.html#method.call) | `Function::call<R>` infers discard, one, tuples, or all values through `FromLuaMulti`. Its [implementation](https://docs.rs/mlua/latest/src/mlua/function.rs.html#193-214) requests `LUA_MULTRET` and converts afterward. | [`Thread::resume<R>`](https://docs.rs/mlua/latest/mlua/struct.Thread.html#method.resume) uses the same typed conversion after advancing the coroutine. | Excellent Rust call-site ergonomics, but a conversion error occurs after execution; the completed raw results are then no longer available. |
| [Piccolo](https://docs.rs/piccolo/latest/piccolo/thread/struct.Executor.html) | An `Executor` reaches a result state and `take_result<T>` performs a typed `FromMultiValue` conversion. `Variadic<Vec<Value>>` collects all values. | A yielded result must be taken before the executor can resume. | Separating execution from extraction prevents accidental early resume, but [`take_result`](https://github.com/kyren/piccolo/blob/master/src/thread/thread.rs#L130-L140) drains the results before conversion and introduces a pending-result state machine. |

The comparison establishes three common caller intents:

1. Keep every result.
2. Apply Lua's fixed one-result adjustment.
3. Discard every result.

The stack-oriented APIs encode those intents as integers. The Rust APIs encode
them as return types. Neither representation belongs in Lunar: a public result
count is C-stack vocabulary, while post-execution conversion can turn a host
type mismatch into irreversible result loss.

## Decision

Keep `Call` as the lossless default and add intent-named conveniences:

```go
func (state *State) CallOne(
	callable Value,
	arguments ...Value,
) (Value, error)

func (state *State) CallDiscard(
	callable Value,
	arguments ...Value,
) error
```

Add matching `Frame.CallOne` and `Frame.CallDiscard` methods. Frame forms run
under the State's installed context like every other reentrant operation.

`CallOne` requests exactly one result from the VM. A function returning no
values therefore produces `Nil`, and extra values are discarded. It must not
call `Call` and slice the result afterward.

`CallDiscard` requests exactly zero results. It executes the function and
reports failures without materializing public result values.

Do not add a public numeric result-count parameter or `CallN` for v1. Go has no
tuple return target for arbitrary `N`, and silently adjusting two or more
results is less common than preserving and inspecting all results.

## Recoverable caller-storage overflow

Keep `CallInto`, `Frame.CallInto`, and `ResumeInto` all-or-nothing for their
destinations. If the destination is too short:

- Lua side effects and coroutine transitions remain completed;
- the destination remains entirely unchanged;
- `count` is the exact required size;
- `ResultCapacityError` retains every completed result as an owning `Value`;
  and
- `ResultCapacityError.Results()` returns a caller-owned copy that remains
  valid across later operations and after `State.Close`.

The error text is operation-neutral because the same type covers calls,
coroutine yields, and coroutine returns.

Retaining results in the error is preferred to leaving them pending in the
State or Thread. A hidden pending-result state would recreate stack discipline,
make later operation admission conditional on cleanup, and require a separate
API to distinguish “resume again” from “retrieve the previous transition.”

Overflow is already an exceptional path, so allocating owning results there is
the correct tradeoff. Sufficient-capacity `Into` calls retain their current
boundary-allocation behavior.

## Coroutine conveniences

Do not add `ResumeOne` or `ResumeDiscard` in this phase. Yielded values commonly
form a coroutine protocol, so preserving all of them is the safer default.
Those explicit conveniences can be added compatibly later if real embedding
code demonstrates a need.

## Example

```go
result, err := state.CallOne(price.Value(), lua.Number(6), lua.Number(7.5))
if err != nil {
	return err
}

if err := state.CallDiscard(notify.Value(), result); err != nil {
	return err
}

var destination [1]lua.Value
count, status, err := worker.ResumeInto(nil, destination[:])
var results []lua.Value
if err == nil {
	results = destination[:count]
} else {
	var capacity *lua.ResultCapacityError
	if !errors.As(err, &capacity) {
		return err
	}
	// The coroutine has already advanced, but none of its values were lost.
	results = capacity.Results()
}
handleTransition(status, results)
```

## Consequences

- The common one-result and side-effect-only calls are concise and can use
  fixed-result VM fast paths.
- `Call` remains unambiguously lossless from its `[]Value` return type.
- Completed coroutine transitions cannot lose values because of a host buffer
  sizing mistake.
- Lunar provides stronger overflow recovery than the compared high-level
  APIs without exposing a stack or adding a pending-result lifecycle.
- Type conversion remains an explicit operation on owning `Value`s, separate
  from executing Lua.
