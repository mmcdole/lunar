---
status: accepted
---

# Use zero-based Frame indexes

`Frame` models a Go argument sequence rather than an exposed Lua or C stack, so
argument and capture indexes are zero-based. Lua-facing diagnostics translate
the Go index to a one-based ordinal, preserving Lua's “bad argument #1”
vocabulary without introducing a second indexing system into the Go API.
