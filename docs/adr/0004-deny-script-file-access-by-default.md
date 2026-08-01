---
status: accepted
---

# Deny script-file access by default

A State's zero-value `ScriptLoader` grants no script-file authority. Hosts opt
into operating-system files, an `fs.FS`, or a context-aware custom opener.
The same immutable backend governs State file methods, Lua `loadfile` and
`dofile`, and the Lua source searcher used by `require`.

This keeps the answer to “what source can this State read?” in construction
configuration rather than ambient process state. `HostLoader` remains the
explicit compatibility path for trusted applications. `FSLoader` and
`FuncLoader` make embedded and application-defined module trees first-class
without loader-table surgery.

`package.path` remains mutable Lua configuration: changing it changes logical
candidate names, not the backend that opens them. Source-file authority is
separate from IO- and OS-library capabilities.
