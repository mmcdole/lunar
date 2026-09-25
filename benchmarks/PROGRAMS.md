

## Are We Fast Yet

`BenchmarkAWFY` runs 13 programs from the Lua port of
[Are We Fast Yet](https://github.com/smarr/are-we-fast-yet): Richards,
DeltaBlue, Json, CD, Bounce, List, Mandelbrot, NBody, Permute, Queens, Sieve,
Storage and Towers. They represent object-oriented code built from classes,
closures, small objects, strings and arrays. Havlak is omitted because its
smallest verified size takes about 6 s on PUC Lua.

Each program's modules are bundled into one chunk behind a local `require`,
so every runtime loads identical code using only the base, string and math
libraries. A timed operation calls the program's `inner_benchmark_loop` once
and fails unless the program's own verification passes. Inner iteration
counts are scaled for interpreters and listed in `awfy_compare_test.go`.

Lua 5.1 has no bitwise operators. `awfy/bit.lua` is a pure-Lua stand-in for
LuaJIT's `bit` module, written for this repository and used by every runtime.

The files were retrieved on 2026-09-25 from upstream commit
[`74306fec151070fd07157cefeacf19e7e0bcdc89`](https://github.com/smarr/are-we-fast-yet/tree/74306fec151070fd07157cefeacf19e7e0bcdc89/benchmarks/Lua)
and are unchanged. `TestAWFYSourcesMatchUpstream` checks these hashes.
[`awfy/LICENSE.md`](awfy/LICENSE.md) is upstream's license overview, copied
unchanged.

| Vendored file | SHA-256 |
| --- | --- |
| `benchmark.lua` | `f854c782efb9513bd60805e6e833e4df268b997ea9ff95744de0ad6d00733e63` |
| `bounce.lua` | `e54bf6160d07938100b9c4bf00af6603ba500388761ae51e0fd2d920fe7c67df` |
| `cd.lua` | `e1d7114fdba480bdc97207f9f8b0f2cd62b9393377a095cd43f0d130273a365e` |
| `deltablue.lua` | `8f0064b61bfdcafafa260d0c5fb35dc7e55c255ca59b23f8f7beb3024684fb63` |
| `hashindextable.lua` | `c1f415af1b69f85908afc6b8b8fc5f1ae7c3879a1f0feeb2eaae00f97ed4473a` |
| `json.lua` | `79196a37531206523459ea5400aa00ca6425942f4f5c23595f14f96affcabbf1` |
| `list.lua` | `863cff8f08d7b48bbf5f01b12d487dace4aee25777fbcc456c43819a51e59e07` |
| `mandelbrot-fn.lua` | `8bba1d7624431d4c0323c18bab50e270fcdb0d3a0522bc5bc9231bd5a167ab20` |
| `mandelbrot.lua` | `d6b063615e2f6057a3db7257c66325af35dcbdf8b0294eb2580c294b64425e98` |
| `nbody.lua` | `6bd49dde32cf69dd4b9e6a971d182d357ac4b7e29910dfbd79c3626b73d8cd7f` |
| `permute.lua` | `7e7c7cd4dd1d10b85a4474d868a70da04be5b5ec9b287bee8421a111fcee2068` |
| `queens.lua` | `03f9349ae7ba3aad09a5bba4f7102e77ea340e757906f9c8e53c133d8e469381` |
| `richards.lua` | `b9620354ecafb1a1d3fe6b587630ff87df3fde359092eaeb210ed07f572a9990` |
| `sieve.lua` | `3b6a63b07b5ed506e97337ba339983aa71a54021bd73c9f83b4e069aa5d89fed` |
| `som.lua` | `5c07cf378452f0c391c76d3788dc15274ff34b82692bb5cfe6f7e648b82212d0` |
| `storage.lua` | `142e767645d31351104ed3f326d8b24dd68eab06b1ff687fc7233330afefa3d2` |
| `towers.lua` | `d0902a6d929a57e6412586687585732bccfdf39a81c805bbdcd8485b4c1b4c75` |
