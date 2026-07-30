---
status: accepted
---

# Use lua as the package name

Lunar uses the module path `github.com/mmcdole/lunar` while declaring
`package lua`. The package name makes host code read naturally as `lua.State`
and `lua.Value`; comparison code already needs aliases for competing Lua
packages, and tying the package identifier to the repository basename would
make the primary API less clear.
