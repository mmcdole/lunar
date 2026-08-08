# Lua 5.1 test suite provenance

`testdata/lua5.1-tests/` contains the official PUC-Rio Lua 5.1 test suite,
vendored byte-for-byte and unmodified.

- Source: <https://www.lua.org/tests/lua5.1-tests.tar.gz>
- Archive SHA-256:
  `49e4ca6561f82ea605908c5041ab5fad66ed9930fa0686675bd51b02767f18ad`
- Vendored files: every `*.lua` file and the suite `README`
- Omitted files: `etc/` and `libs/` (C sources and a makefile for the
  suite's optional `testC` library, which exercises the C API and cannot
  apply to a pure-Go runtime), and the empty `libs/P1/` directory, which
  the harness recreates in its staging area
- License: the suite is distributed under the Lua license (MIT), Copyright
  (C) 1994-2012 Lua.org, PUC-Rio; see `THIRD_PARTY_NOTICES.md` at the
  repository root

Local policy: the vendored files carry exactly one patch, listed below.
Every other accommodation — skips and environment adjustments alike — lives
in `conformance_test.go` with a stated reason, so a diff against the
upstream archive shows only the entry in this list.

## Local patches

### calls.lua, one line

```diff
-assert(not a and type(b) == "string" and i == 2)
+assert(not a and type(b) == "string" and i <= 2)
```

The upstream assertion counts how many times `load` calls its reader
function before rejecting the invalid chunk `*a = 123`. The reference
lexer always pre-reads one character of lookahead, so its reader is called
exactly twice; Lunar's lexer rejects the statement after reading one
character. The reader-call count is a property of the lexer's buffering,
not of the language. The patch accepts either count without weakening the
requirements that the load fails and returns a string message. The edit
preserves the file's line numbering.
