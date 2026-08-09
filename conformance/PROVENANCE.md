# Lua 5.1 test suite provenance

`testdata/lua5.1-tests/` contains the official PUC-Rio Lua 5.1 test suite.
Every vendored file matches the upstream archive byte-for-byte.

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
- Checkout policy: `.gitattributes` disables text normalization for the suite
  so its upstream line endings and intentional whitespace survive on every OS

Local policy: the vendored files stay unmodified. Every accommodation — skips,
environment adjustments, alternate drivers, postconditions, and temporary
source changes — is declared in the Go harness with a stated reason. Suite
execution policy lives in `conformance_test.go`; staged source changes live in
`accommodations_test.go`.

## Staged accommodations

The harness applies the following changes only to its temporary writable copy;
the vendored files remain unchanged. Every replacement must match its expected
upstream text exactly once, so upstream drift fails the Go test instead of
silently weakening coverage.

- `calls.lua`: accept one or two reader calls before rejecting `*a = 123`.
  The reference lexer pre-reads one character and calls the reader twice;
  Lunar rejects the invalid statement after one call. The count is a buffering
  detail, while the staged assertion still requires a reader call, a failed
  load, and a string error.
- `big.lua`: omit its opening 32-bit `size_t` overflow probe, whose 129-way
  concatenation would allocate 4 GiB on a 64-bit runtime. The remaining wide
  constants, jumps, constructors, tables, and coroutine checks run through the
  suite's original special driver.
- `errors.lua`: retain syntax rejection, source-line, token-category, error,
  stack, and compiler-limit checks while accepting Lunar's non-reference
  diagnostic phrases, including `syntax nesting` in place of `syntax levels`.
