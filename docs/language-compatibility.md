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

## Arithmetic precision

Lunar uses IEEE double arithmetic like PUC Lua 5.1. Basic operators round
identically, but results can differ from PUC Lua on Linux in these places:

- Exponentiation. PUC Lua evaluates `^` and `math.pow` with the C library's
  `pow`, which glibc keeps within about 0.52 ULP. Lunar uses Go's `math.Pow`.
  It is exact for `x^2`, `x^-1`, `x^0.5`, and powers of two, but other
  results can be several ULP away, up to about 70 ULP for large integer
  exponents. The last digits printed with `%.17g` can therefore differ.
  Integral powers of ten are correctly rounded, so `10^k == 1ek` holds for
  every finite `k`; glibc misses two of those. The operator, constant
  folding, and `math.pow` share one implementation, so a program always
  agrees with itself.
- Other `math` functions. `math.sin`, `math.exp`, `math.log`, and similar use
  Go's `math` package and can differ from glibc in the last bit.
- Negative zero constants. PUC Lua 5.1 keys a function's numeric constants by
  value, where `-0` and `0` compare equal, so whichever appears first sets the
  sign for both. After `local a = 0 local b = -0`, `1/b` is `inf`; after
  `local b = -0 local a = 0`, `1/a` is `-inf` and `-1/0` is `inf`. Lunar keeps
  each constant's own sign.

## Process handles

Close `io.popen` handles explicitly. `file:close()` flushes buffered output,
closes the pipe, and waits for the command, honoring the execution context. A
handle that is instead collected, or still open at `State.Close`, discards
unflushed output and terminates the command. PUC Lua 5.1 flushes and waits in
both cases, but an embedded runtime must not let collection or `State.Close`
block on an external process. A script can drop a handle to a command that
never exits.

## Primary-source comparison

- [PUC Lua 5.2 parser and goto resolution](https://www.lua.org/source/5.2/lparser.c.html#closegoto)
- [PUC Lua 5.2 jump close patching](https://www.lua.org/source/5.2/lcode.c.html#luaK_patchclose)
- [PUC Lua 5.2 goto rules](https://www.lua.org/manual/5.2/manual.html#3.3.4)
- [LuaJIT goto resolver](https://github.com/LuaJIT/LuaJIT/blob/faaf663340347a78b22ed94c63c24fe090bd9784/src/lj_parse.c#L1204-L1382)
- [LuaJIT label and goto parser](https://github.com/LuaJIT/LuaJIT/blob/faaf663340347a78b22ed94c63c24fe090bd9784/src/lj_parse.c#L2849-L2917)
- [GopherLua goto lowering](https://github.com/yuin/gopher-lua/blob/75f497656b1c6864139dd2a7d88cf96d09550814/compile.go#L1124-L1140)
- [GopherLua label resolution](https://github.com/yuin/gopher-lua/blob/75f497656b1c6864139dd2a7d88cf96d09550814/compile.go#L358-L519)
