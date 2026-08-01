# Language compatibility

Lunar implements Lua 5.1 with Lua 5.2-style `goto` statements and labels:

```lua
goto retry
::retry::
```

Labels are visible throughout their lexical block, but not in sibling blocks
or nested functions. A goto can leave local-variable scopes but cannot enter
one. Leaving a scope closes only the captured locals whose lifetimes the jump
actually exits.

## Intentional choices

PUC Lua 5.2 and LuaJIT agree on the core control-flow and local-scope rules but
diverge at a few edges. Lunar makes these choices deliberately:

- `goto` is contextual rather than reserved. Existing Lua 5.1 programs can
  continue to use `goto` as an identifier, matching default LuaJIT. It is a
  goto statement only when followed by a label name in statement position.
- Labels are block scoped. The same label name may be reused in a nested or
  sibling block, matching PUC Lua 5.2 and LuaJIT. An unresolved inner goto is
  not bound early to an outer label because a later label in its own block
  takes precedence.
- A trailing label before `end`, `else`, `elseif`, or end of source is outside
  the scopes of that block's locals. Intervening labels and semicolons do not
  change that rule. A label before `until` is not relaxed because a repeat
  condition remains inside the repeat body's local scope.
- Semicolon handling is widened only for those label chains. Lunar continues
  to reject unrelated leading or repeated empty statements, preserving its
  established Lua 5.1 grammar instead of adopting PUC Lua 5.2's general empty
  statement.
- `break` must remain the final non-semicolon statement in its block. This
  preserves Lua 5.1 and default LuaJIT behavior; PUC Lua 5.2's broader `break`
  grammar is outside Lunar's goto extension.
- Undefined-label and jump-into-local errors point at the offending goto.
  Duplicate-label errors point at the second declaration and identify the
  first declaration's line. Lunar retains its existing line-only source
  positions rather than copying a reference parser's lookahead position.

## Compiler lowering

Goto adds no VM opcode and does not change Lunar's Lua 5.1 `JMP` semantics.
The compiler reserves a no-op `JMP +0` immediately before each goto jump. If
label resolution proves the edge exits local scopes, that no-op is rewritten
to the existing `CLOSE` instruction at the first exited local's register. This
is safe even when a later part of the function is what captures that local. If
the source and target have the same local-scope watermark, the instruction
remains a no-op and closes nothing.

This differs intentionally from GopherLua's unconditional `CLOSE 0` before
every goto. An unconditional close is incorrect for a same-scope backward
jump because it can detach a closure from a local that remains live:

```lua
local x = 1
local f = function() return x end
::again::
x = x + 1
if x < 3 then goto again end
assert(f() == 3)
```

Lunar, PUC Lua 5.2, and LuaJIT keep `x` open across this jump. Lunar's existing
bytecode verifier continues to validate the resulting ordinary `JMP` and
`CLOSE` instructions and their targets.

## Primary-source comparison

- [PUC Lua 5.2 parser and goto resolution](https://www.lua.org/source/5.2/lparser.c.html#closegoto)
- [PUC Lua 5.2 jump close patching](https://www.lua.org/source/5.2/lcode.c.html#luaK_patchclose)
- [PUC Lua 5.2 goto rules](https://www.lua.org/manual/5.2/manual.html#3.3.4)
- [LuaJIT goto resolver](https://github.com/LuaJIT/LuaJIT/blob/faaf663340347a78b22ed94c63c24fe090bd9784/src/lj_parse.c#L1204-L1382)
- [LuaJIT label and goto parser](https://github.com/LuaJIT/LuaJIT/blob/faaf663340347a78b22ed94c63c24fe090bd9784/src/lj_parse.c#L2849-L2917)
- [GopherLua goto lowering](https://github.com/yuin/gopher-lua/blob/75f497656b1c6864139dd2a7d88cf96d09550814/compile.go#L1124-L1140)
- [GopherLua label resolution](https://github.com/yuin/gopher-lua/blob/75f497656b1c6864139dd2a7d88cf96d09550814/compile.go#L358-L519)
