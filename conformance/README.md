# Lua 5.1 conformance suite

This package runs the official PUC-Rio Lua 5.1 test suite against Lunar.
The suite files are vendored under `testdata/lua5.1-tests/`; their origin,
archive hash, and the one local patch are recorded in
[`PROVENANCE.md`](PROVENANCE.md).

Run it from the repository root:

```sh
go test ./conformance/
```

## How files run

Each file executes in a fresh `State` with the full standard libraries and
host file loading, inside a staged writable copy of the suite directory,
because several files create and remove files beside themselves. Files are
invoked the way the suite's own `all.lua` driver invokes them: loaded from
source, dumped to a binary chunk with `string.dump`, reloaded with
`loadstring`, and then run. Every passing file therefore also exercises the
binary chunk writer and reader. The driver's return-value assertions
(`attrib.lua` returns 27, `locals.lua` 5, `events.lua` 12, `verybig.lua`
10, `calls.lua` its own `deep` global) are checked the same way.

## Results

17 of the suite's 22 executable files pass. `api.lua` and `code.lua` pass
by design in reduced form: both self-skip their C-side sections when the
suite's optional C test library is absent, exactly as they do under a stock
`lua` binary built without `testC`.

Five files are skipped, each for a stated reason in `conformance_test.go`:

| File | Reason |
| --- | --- |
| `main.lua` | Tests the standalone `lua.c` binary's command line by spawning it with `os.execute`; there is no such binary here. |
| `db.lua` | Requires `debug.sethook`/`debug.gethook`, an intentional Lunar limit (see Scope in the root README). |
| `gc.lua` | Counts incremental collector steps; Lunar collects synchronously. Weak tables and finalizers are covered natively by `collection_weak_test.go` and `collection_finalizer_test.go`. |
| `big.lua` | Asserts 32-bit `size_t` overflow errors; on 64-bit Go the suite's 4 GiB concatenation simply succeeds. |
| `errors.lua` | Asserts the reference's exact error wording. Lunar reports the same source locations and near-tokens with different phrasing (for example `expected expression near <eof>` where the reference prints `unexpected symbol near '<eof>'`). This is the one known language-level formatting deviation; aligning the wording is tracked as follow-up work. |

`checktable.lua` is a debugging utility for the suite's C test library and
is not part of `all.lua`'s run list; it is vendored but not executed.
