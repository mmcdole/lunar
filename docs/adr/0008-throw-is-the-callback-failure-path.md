---
status: accepted
---

# Throw is the only callback failure path

## Context

`Raise*` returned a sealed terminal `Outcome`; `Throw*` unwound with a private
panic. Both existed, twelve methods for one concept, and choosing the wrong one
was a real mistake: a `Raise*` returned from a helper does not propagate.

## Decision

A native callback ends in exactly one of three ways: it returns a `Return*`
Outcome, it returns a `Yield*` Outcome, or it throws. The `Raise*` methods
become private and keep serving the library paths, which return sealed outcomes
directly and pay no unwind. `Throw*` is the public form.

`Throw*` returns nothing. Go's convention for a function that does not return
is to return nothing and document it, and typing one to hand back an `Outcome`
it never produces would both lie in the signature and reintroduce two spellings
for a single method — the ambiguity this decision exists to remove.

## Consequences

Six methods removed, and the remaining spelling is the general one: only an
unwinding failure can be reported by a helper called at any depth inside the
callback, which is where argument checks live.

Measured against the standard library, twelve guard clauses become plain
statements and read better, and exactly one callback of 136 — one whose entire
body is a failure — needs a terminal return that never executes. That residual
cost belongs to `NativeFunc` returning an `Outcome`, which is what makes
"produced exactly one terminal result" structurally checkable, and is worth
keeping.
