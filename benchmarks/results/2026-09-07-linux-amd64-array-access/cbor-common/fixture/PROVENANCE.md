# lua-cbor provenance

`cbor.lua` is derived from lua-cbor 1.0.0 by Kim Alvefur and is distributed
under the adjacent MIT license.

- Source: `https://code.zash.se/lua-cbor/`
- Release tag: `1.0.0`
- Mercurial repository: `d94745c60fab80098f9a6a148513089139908192`
- Release node: `a1b820511c92e309cd8294e9cd8c84e09e9958dd`
- Released `cbor.lua` SHA-256:
  `985089600879c0a7ba82c092cf065b975c9a565752fb0c10760406bbec14d262`
- Vendored `cbor.lua` SHA-256:
  `ada339a0d2be2b58eae06e727b37de6b1a3c77a8a2e081f7b41b92fba98bc56e`

The vendored file has one change from the 1.0.0 release. Tagged values forward
the caller's options table when recursively encoding their value:

```diff
-function tagged_mt:__tocbor() return integer(self.tag, 192) .. encode(self.value); end
+function tagged_mt:__tocbor(opts) return integer(self.tag, 192) .. encode(self.value, opts); end
```

The graph benchmark does not use tagged values or codec options. The change is
documented so the exercised source is reproducible byte for byte.

## Synthetic graph corpus

`workload.lua` is project-owned benchmark code. The generated CBOR corpora are
fully synthetic and contain no copied user, application, or production data.
`internal/fixture` derives every name, identifier, coordinate, description,
timestamp, table key, and relationship from fixed counters. Time is fixed at
Unix timestamp `1700000000`.

Generated corpora are intentionally not checked in. With the vendored codec and
current deterministic generator, their reproducibility records are:

| Preset | Bytes | SHA-256 |
|---|---:|---|
| small | 8,011 | `5a840e6955b60c49832742a9e279c0d92163abceb96a22d79e7ce22c98d4b633` |
| large | 9,208,046 | `65c43f4abd104fb629f22aee7801d3b458a93e24e6a6ec6dffb4c4b02252ab7c` |

At this revision, `workload.lua` has SHA-256
`a4666b04a45cb89eeb3ca6b7701e3e4ad2e17af0e3b7eab7f9e01410a9baacbd`.
Generation commands and independent structural counts are in the benchmark
README.
