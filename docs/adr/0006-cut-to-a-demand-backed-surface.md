---
status: accepted
---

# Cut the public surface to demand-backed operations

## Context

The pre-release API reached 237 exported functions across 22 types. Much of the
late growth was added on the reasoning that an embedder might want it, which is
not a criterion that ever stops.

## Decision

An operation ships when its absence would make a competent embedder ask "how do
I do X?" Everything else waits until someone asks by name, or until its absence
forces a workaround that reaches past the public API.

The evidence bar scales with the cost of being wrong. Signatures, the error
channel, and the cancellation model are one-way doors and were decided now.
Everything additive is a two-way door: cutting it costs a patch release if the
judgement was wrong, while shipping it costs a permanent obligation.

Measurement, not taste, settled the close calls:

- the `Opt*` family had zero callers outside its own test, and the standard
  library's 136 native callbacks never adopted it;
- gopher-lua exposes `Concat`, `LessThan`, `Equal`, and `RawEqual`, and the one
  real embedder called none of them once in its history, so C-API parity turned
  out to be a poor proxy for demand;
- `lua_arith` is a 5.2 addition, so cutting `Arith` from a 5.1 runtime is
  fidelity to the reference API rather than a gap in it;
- native captures are subsumed by a Go closure over an owning `Value`, which is
  already a collection root; and
- `string.dump` output already loads through `LoadString`, so removing
  `MarshalBinary` removed no capability.

`StopGC` and `RestartGC` survived the same test in the other direction: without
them a sandbox that has not opened the base library has no way to suspend
collection at all, which is a workaround reaching past the API rather than a
missing convenience.

## Consequences

162 exported functions. Each row of the surface answers a question an embedder
actually asks. Restoring any cut operation is an additive minor release, and
the two-way-door analysis is what makes that acceptable.
