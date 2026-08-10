# Lua 5.1 conformance suite

This package runs the official PUC-Rio Lua 5.1 test suite against Lunar.
The original suite files are kept unchanged under `testdata/lua5.1-tests/`.

Run it from the repository root:

```sh
go test ./conformance
```

## Coverage

The harness runs 19 of the suite's 22 executable files. Three do not apply to
Lunar:

| File | Reason |
| --- | --- |
| `main.lua` | Tests the command-line `lua` program. Lunar is an embedded Go library, not a standalone program. |
| `db.lua` | Requires `debug.sethook` and `debug.gethook`, which Lunar intentionally does not implement. |
| `gc.lua` | Measures Lua's incremental garbage collector. Lunar collects synchronously; its weak tables and finalizers have native Go tests instead. |

The optional `testC` library is also absent because it tests Lua's C internals.
`api.lua` and `code.lua` therefore print their built-in skip message and exit;
several other files run their normal Lua tests while skipping only `testC`
sections. `checktable.lua` is a `testC` helper and is not part of the run list.

## Small test adjustments

Some upstream checks assume details of the reference C implementation or its
host system rather than Lua 5.1 behavior. Lunar adjusts them in a temporary
copy while the tests run; the vendored files stay untouched.

| File | Why the original check does not fit | What Lunar checks instead |
| --- | --- | --- |
| `calls.lua` | The reference parser asks for the next piece of source exactly twice. Lunar spots the invalid statement after the first piece. | Allow one or two reads, but still require that the reader ran, parsing failed, and a text error was returned. |
| `big.lua` | The test tries to prove a 32-bit memory limit by building a 4 GiB string. A 64-bit Go program does not have that limit, and the allocation is unsafe for a test run. | Skip only that allocation. All the remaining large-program and coroutine tests still run. |
| `files.lua` | The test renames or deletes files while they are still selected as input or held by a line reader. Unix allows that; Windows does not. | Stop using those files and collect them before renaming or deleting them. |
| `errors.lua` | The test expects the reference interpreter's exact English error messages. Lunar describes the same errors with different words. | Accept Lunar's known wording while still checking rejection, the source line, the kind of bad token, stack errors, and compiler limits. |

Each adjustment must find its exact upstream text once and must preserve line
numbers. If the upstream file changes unexpectedly, the Go test fails instead
of silently changing coverage. The adjustments and their reasons live in
[`accommodations_test.go`](accommodations_test.go).

## How files run

Each file gets a fresh Lunar `State` and a temporary writable copy of the suite.
Most files follow `all.lua`: load the source, dump it to Lua bytecode, reload
the bytecode, and execute it. `big.lua` keeps the suite's special coroutine
invocation. The harness also keeps `all.lua`'s return-value checks.

## Source and license

- Source: <https://www.lua.org/tests/lua5.1-tests.tar.gz>
- SHA-256: `49e4ca6561f82ea605908c5041ab5fad66ed9930fa0686675bd51b02767f18ad`
- Vendored: every upstream `*.lua` file and `README`
- Omitted: the C sources and makefile under `etc/` and `libs/`; the harness
  recreates the empty `libs/P1/` directory when tests run
- License: Lua license (MIT), Copyright (C) 1994-2012 Lua.org, PUC-Rio; see
  [`THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md)

`.gitattributes` disables text normalization for the vendored directory so its
upstream line endings and intentional whitespace remain unchanged on every OS.
