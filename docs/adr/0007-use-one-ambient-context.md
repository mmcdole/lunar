---
status: accepted
---

# Use one ambient context

## Context

Every operation that could execute Lua had a `Context` twin, twenty on `State`
and two on `Thread`. Because each captured its context at entry, none could be
re-armed while the call it governed was still running — which is why an ambient
`SetInterrupt` taking a `func() error` was added beside them. The result was two
cancellation mechanisms with different lifetimes producing the same
`ContextError`.

## Decision

`SetContext` and `RemoveContext` install one ambient `context.Context` for the
State. It covers execution, loading, and every thread including coroutines, and
it can be replaced from inside a native callback, taking effect during that
call. The twenty-two twins and both interrupt methods are removed.

A host that wants an arbitrary interrupt predicate expresses it as a context,
which `context.WithCancel` already provides.

## Consequences

Twenty-two methods removed and two mechanisms become one. The re-arming case
that motivated `SetInterrupt` is served by the same call a deadline uses.

The cost is that an installed context outlives the operation that motivated it,
so a cancelled context left in place refuses every later operation on that
State. A host that cancels per operation clears it afterwards. This is the
contract gopher-lua already ships and that rune's own engine seam adopted
independently, so the discipline is familiar rather than novel.
