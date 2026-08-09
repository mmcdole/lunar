# Lua 5.1 conformance suite

This package runs the official PUC-Rio Lua 5.1 test suite against Lunar.
The suite files are vendored byte-for-byte under `testdata/lua5.1-tests/`;
their origin, archive hash, and staged accommodations are recorded in
[`PROVENANCE.md`](PROVENANCE.md).

Run it from the repository root:

```sh
go test ./conformance/
```

## How files run

Each file executes in a fresh `State` with the full standard libraries and
host file loading, inside a staged writable copy of the suite directory,
because several files create and remove files beside themselves. The default
driver installs the suite's global `dofile` replacement: it loads source,
dumps it to a binary chunk with `string.dump`, reloads it with `loadstring`,
and runs it. Nested `dofile` calls take that path too. `big.lua` uses the
suite's special source-loaded coroutine driver. The return-value assertions
(`attrib.lua` returns 27, `locals.lua` 5, `events.lua` 12, `verybig.lua`
10, `calls.lua` its own `deep` global) are checked the same way.

## Results

19 of the suite's 22 executable files run to completion in the harness.
`api.lua` and `code.lua` self-skip entirely when the suite's optional `testC`
library is absent; `closure.lua` and other files still run their pure-Lua
coverage while omitting sections guarded by `testC`, as they do under a stock
`lua` binary built without that library.

Three files are skipped, each for a stated reason in `conformance_test.go`:

| File | Reason |
| --- | --- |
| `main.lua` | Tests the standalone `lua.c` binary's command line by spawning it with `os.execute`; there is no such binary here. |
| `db.lua` | Requires `debug.sethook`/`debug.gethook`, an intentional Lunar limit (see Scope in the root README). |
| `gc.lua` | Counts incremental collector steps; Lunar collects synchronously. Weak tables and finalizers are covered natively by `collection_weak_test.go` and `collection_finalizer_test.go`. |

Three passing files use narrow staged accommodations recorded in
[`PROVENANCE.md`](PROVENANCE.md): `calls.lua` accepts either valid lexer refill
count while requiring the reader to run, `big.lua` omits only its 32-bit 4 GiB
string overflow probe, and `errors.lua` accepts Lunar-specific diagnostic
phrases while retaining syntax rejection, source-line, token-category, and
compiler-limit checks.

`checktable.lua` is a debugging utility for the suite's C test library and
is not part of `all.lua`'s run list; it is vendored but not executed.
