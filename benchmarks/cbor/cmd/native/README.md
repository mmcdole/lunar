# Native Lua reference runner

This command measures the same CBOR graph workload in provisioned PUC Lua
5.1.5 and LuaJIT 2.1 processes. It never downloads, builds, or installs a Lua
runtime. Each sample receives a fresh writable staging directory, so save
measurements cannot overwrite the shared input.

For a qualification archive, fingerprint both native runtimes and write separate
load and save JSONL files. From the nested module (`benchmarks/cbor`):

```sh
go run ./cmd/generate \
  -preset large -output /tmp/lunar-cbor-large.cbor

shasum -a 256 /opt/lua-5.1.5/bin/lua
shasum -a 256 /opt/luajit-2.1/bin/luajit

go run ./cmd/native \
  -qualification \
  -data /tmp/lunar-cbor-large.cbor \
  -lua51 /opt/lua-5.1.5/bin/lua \
  -luajit /opt/luajit-2.1/bin/luajit \
  -expect-lua51-sha256 PUC_LUA_5_1_BINARY_SHA256 \
  -expect-luajit-sha256 LUAJIT_BINARY_SHA256 \
  -mode load -warmups 2 -runs 15 -format jsonl \
  -output /tmp/cbor-native-load.jsonl

go run ./cmd/native \
  -qualification \
  -data /tmp/lunar-cbor-large.cbor \
  -lua51 /opt/lua-5.1.5/bin/lua \
  -luajit /opt/luajit-2.1/bin/luajit \
  -expect-lua51-sha256 PUC_LUA_5_1_BINARY_SHA256 \
  -expect-luajit-sha256 LUAJIT_BINARY_SHA256 \
  -mode save -warmups 2 -runs 15 -format jsonl \
  -output /tmp/cbor-native-save.jsonl
```

`-qualification` is the mandatory archive policy. It requires provisioned and
pinned PUC Lua and LuaJIT binaries, at least 15 recorded runs, JSONL, an explicit
output file, and both separately labelled LuaJIT JIT-off and JIT-on lanes. The
first JSONL record is a `policy` manifest containing those settings, runtime
identities, arguments, and all workload hashes. A valid archive ends with a
`completion` record whose total and per-runtime counts prove that every planned
sample was written. An absent footer is an incomplete run.

The `luajit-2.1-jit-off` samples are the native interpreter reference. JIT-on
is recorded separately as a JIT-compiled reference. `-output` uses exclusive
creation; `-overwrite` must be explicit. Each source runtime is copied to a
private read-only file, rehashed, version-checked, and executed from that stable
copy for the entire run. The input corpus, codec, workload, and measurement
script are likewise copied and rehash-verified once before sampling.

Without `-qualification`, the command remains an explicitly descriptive smoke
tool. It may run one provisioned runtime, fewer samples, text or stdout output,
and `-luajit-jit=false`. JSONL smoke output still records
`"qualification":false` in its policy and completion records; it cannot be
mistaken for the mandatory archive.

`operation_cpu_ns` comes from `os.clock` in both native runtimes. It is useful as
a native-to-native reference, while parent-observed wall and process CPU fields
are also archived. None is silently treated as a hard clock-for-clock gate
against the Go-hosted workers' differently defined operation interval. Use the
paired implementation runner for strict baseline/candidate gates.
