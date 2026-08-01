---
status: accepted
---

# Add demand-backed fixed-result calls

## Context

[ADR 0005](0005-use-intent-named-call-result-shapes.md) kept the lossless
`Call`, added named zero- and one-result forms, and deferred a numeric `CallN`
until a real embedder demonstrated demand. The first substantial integration
did so.

Rune's engine-neutral script seam asks each backend to invoke a function with
an exact result count. Three production hook paths—echo, output, and prompt—
each require exactly two results. Lua and LuaJIT express that directly. Rune's
Lunar backend instead needs a 35-line `callValues` method—25 lines of it
result-shape branching—plus a dedicated 16-line error helper file to compose
the operation:

- zero results use `CallDiscard`;
- one result uses `CallOne`;
- wider result counts allocate a destination and use `CallInto`;
- missing results are padded by the adapter; and
- extra results turn `ResultCapacityError` into a routine success path.

That last branch is more than cosmetic. Lunar first materializes every result
inside `ResultCapacityError`, including values the caller explicitly intends
to discard, and the adapter then copies the retained prefix. The workaround is
therefore both harder to read and less efficient than the fixed-result VM
operation it is reconstructing.

The original assumption that arities greater than one would be too uncommon
for a public operation did not survive contact with this integration.

## Decision

Add symmetric State and Frame operations:

```go
func (state *State) CallN(
	callable Value,
	resultCount int,
	arguments ...Value,
) ([]Value, error)

func (frame Frame) CallN(
	callable Value,
	resultCount int,
	arguments ...Value,
) ([]Value, error)
```

Both invoke the target in protected mode and ask the VM for exactly
`resultCount` results. Lua pads missing results with nil and discards extras.
The returned slice and its Values are owning. A zero count executes the call
and returns a nil slice.

`ErrInvalidResultCount` reports a negative count or one outside Lunar's
supported fixed-result range. Invalid counts are rejected before Lua executes.
A supported count that cannot fit the State's configured value stack instead
returns the existing Lua `ResourceError`, also before the target executes.

Keep the existing intent-named methods. `CallOne` avoids a result-slice
allocation and communicates the common scalar intent. `CallDiscard` requests
no values and materializes none. `Call` remains the unambiguously lossless
default, and `CallInto` remains the caller-storage form for an unknown result
count.

Do not add `ResumeN`. Yielded values commonly form a coroutine protocol, and
the Rune evidence concerns ordinary function calls only.

## Consequences

- Rune's fixed-arity seam maps directly onto Lunar and can delete its
  result-shape switch and capacity-error recovery helper.
- Fixed arities discard unwanted results inside the VM instead of allocating
  and exposing them through an error.
- State and reentrant Frame calls have the same result-shape vocabulary.
- The public surface now contains an integer arity operation, but callers that
  do not require exact arity retain clearer intent-named alternatives.
- [ADR 0005](0005-use-intent-named-call-result-shapes.md) remains authoritative
  except for its decision to omit `CallN`.
