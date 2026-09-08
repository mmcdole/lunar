The [source rationale](source-rationale.md), [semantic review](integrated/review.md), [compiler review](integrated/compiler-review.md), and [candidate patch](integrated/candidate.patch) describe the single integrated candidate based on main. Go source snapshots use .go.txt filenames and retain their original bytes.

The [paired profile analysis](attribution/profile-review.md) and [operation counts](attribution/counts/README.md) concern the earlier shared-array candidate. They establish attribution and path frequency, not integrated-candidate speed. The original [array-access experiment](../2026-09-07-linux-amd64-array-access/README.md) remains separate; no samples are pooled with it.

Integrated Go and CBOR pilots and full collections remain in distinct files/directories. Every attempted cohort and original manifest is retained, including exploratory or failed results. A complete archive does not itself establish a passed performance gate. The coordinator writes the final assessment separately.

archive-manifest.json records original artifact paths and archived hashes. Logs and manifests retain the exact commands used on the measurement machine. Executable binaries and large compiler dumps are omitted; source/build identities and binary hashes remain. This helper does not change any runtime or root README file.
