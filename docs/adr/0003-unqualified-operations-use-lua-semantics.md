---
status: accepted
---

# Unqualified operations use Lua semantics

Unqualified public operations follow ordinary Lua semantics and may invoke
metamethods, while operations that bypass metamethods include `Raw` in their
name. This makes the possibility of executing Lua visible at the call site and
keeps storage access distinct from language behavior.
