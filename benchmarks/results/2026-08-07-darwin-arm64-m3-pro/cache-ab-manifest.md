# Long-string cache causal A/B manifest

This manifest records how the causal cache measurements in this directory
were built and collected. The two source revisions are an exact parent/child
pair:

- base: `ecda50c5f57499c7c97b5c071b024efc2bcf91bd`
- candidate: `d9c162801840b7f7091b2bb4d337ff1f358bf900`

Collection used Go 1.25.1 on Darwin/arm64, an Apple M3 Pro, `GOGC=100`,
`GOMEMLIMIT=off`, `GOMAXPROCS=1`, and `-test.cpu=1`. Each suite's base and
candidate binaries were built before that suite's timing began; no binary was
rebuilt within a suite.

## Source extraction and builds

The revisions were exported without changing the review worktree:

```sh
BASE_REV=ecda50c5f57499c7c97b5c071b024efc2bcf91bd
CANDIDATE_REV=d9c162801840b7f7091b2bb4d337ff1f358bf900
AB_ROOT=$(mktemp -d)
RESULTS_DIR=$PWD # Run these commands from this results directory.

git archive --format=tar --prefix=base/ \
  --output="$AB_ROOT/base.tar" "$BASE_REV"
git archive --format=tar --prefix=candidate/ \
  --output="$AB_ROOT/candidate.tar" "$CANDIDATE_REV"
tar -xf "$AB_ROOT/base.tar" -C "$AB_ROOT"
tar -xf "$AB_ROOT/candidate.tar" -C "$AB_ROOT"
```

`git get-tar-commit-id` identifies the archives as the revisions above. Their
SHA-256 values are:

| Archive | SHA-256 |
| --- | --- |
| `base.tar` | `716f8151c32737ca294b9dcf3215764254cb25d3d4d6e25d921a9580efb555ad` |
| `candidate.tar` | `3b38bcdc3dcf7df8c08f9ee0c7cd434547a2f75e2b94ab8662a84a4219c029c4` |

The public echo binary was compiled from each unmodified benchmark module.
For the other cases, the review-only sources archived in this directory were
copied under these test filenames before compiling:

| Archived source | Destination |
| --- | --- |
| [`cache-review-ingress-benchmark.go.txt`](cache-review-ingress-benchmark.go.txt) | repository root `cache_review_benchmark_test.go` |
| [`cache-review-cold-embedding-benchmark.go.txt`](cache-review-cold-embedding-benchmark.go.txt) | `benchmarks/cache_review_test.go` |
| [`cache-review-prebuilt-benchmark.go.txt`](cache-review-prebuilt-benchmark.go.txt) | `benchmarks/prebuilt_cache_review_test.go` |

Each variant used its own Go build cache. The binaries were produced with
`go test -c`, first from `benchmarks`, then from the root after adding the
ingress source, and then from `benchmarks` after adding each embedding source:

```sh
GOCACHE="$AB_ROOT/gocache-base" go -C "$AB_ROOT/base/benchmarks" test \
  -c -o "$AB_ROOT/base-bench.test" .
GOCACHE="$AB_ROOT/gocache-candidate" go \
  -C "$AB_ROOT/candidate/benchmarks" test \
  -c -o "$AB_ROOT/candidate-bench.test" .

cp "$RESULTS_DIR/cache-review-ingress-benchmark.go.txt" \
  "$AB_ROOT/base/cache_review_benchmark_test.go"
cp "$RESULTS_DIR/cache-review-ingress-benchmark.go.txt" \
  "$AB_ROOT/candidate/cache_review_benchmark_test.go"
GOCACHE="$AB_ROOT/gocache-base" go -C "$AB_ROOT/base" test \
  -c -o "$AB_ROOT/base-root.test" .
GOCACHE="$AB_ROOT/gocache-candidate" go -C "$AB_ROOT/candidate" test \
  -c -o "$AB_ROOT/candidate-root.test" .

cp "$RESULTS_DIR/cache-review-cold-embedding-benchmark.go.txt" \
  "$AB_ROOT/base/benchmarks/cache_review_test.go"
cp "$RESULTS_DIR/cache-review-cold-embedding-benchmark.go.txt" \
  "$AB_ROOT/candidate/benchmarks/cache_review_test.go"
GOCACHE="$AB_ROOT/gocache-base" go -C "$AB_ROOT/base/benchmarks" test \
  -c -o "$AB_ROOT/base-embedding-review.test" .
GOCACHE="$AB_ROOT/gocache-candidate" go \
  -C "$AB_ROOT/candidate/benchmarks" test \
  -c -o "$AB_ROOT/candidate-embedding-review.test" .

cp "$RESULTS_DIR/cache-review-prebuilt-benchmark.go.txt" \
  "$AB_ROOT/base/benchmarks/prebuilt_cache_review_test.go"
cp "$RESULTS_DIR/cache-review-prebuilt-benchmark.go.txt" \
  "$AB_ROOT/candidate/benchmarks/prebuilt_cache_review_test.go"
GOCACHE="$AB_ROOT/gocache-base" go -C "$AB_ROOT/base/benchmarks" test \
  -c -o "$AB_ROOT/base-prebuilt-review.test" .
GOCACHE="$AB_ROOT/gocache-candidate" go \
  -C "$AB_ROOT/candidate/benchmarks" test \
  -c -o "$AB_ROOT/candidate-prebuilt-review.test" .
```

The retained original binary identities are:

| Binary | SHA-256 |
| --- | --- |
| `base-bench.test` | `d7eb6af82a47154eb75969b1bed3d4b01ab212ea2d90aef88a0e9486024d0082` |
| `candidate-bench.test` | `d29481e23826b2e5cbce8b2ee349216ffb01ccf8f4090c543b06a1e10cb81e8b` |
| `base-root.test` | `978e387ee8fa7233951a4bb9033be9ec305877fc2c5e3ab30f2627da9a572f3d` |
| `candidate-root.test` | `016a2d51fa15f4dff263f07acce9e7054b76cd6a9d61ce82fbc1d596274dc6d6` |
| `base-embedding-review.test` | `ead58e19d53a49509d742e83ce7e82012e3e35e8f5044ecfd8ed8caade86cdd2` |
| `candidate-embedding-review.test` | `d97f959579a321353ac7c994129244ecee9e3ee35fee2643f352e44ea62c8162` |
| `base-prebuilt-review.test` | `8762fde9988970af0f92ff218846f5ff3dfd2d5c5211dbf367cdbd714c9e3390` |
| `candidate-prebuilt-review.test` | `02612ca261b5b6dbc661a73ea10289c847a67edf4fc70cc64d9370d9489f6c39` |

`go version -m` reports Go 1.25.1, Darwin/arm64, `GOARM64=v8.0`, and
`CGO_ENABLED=1` for every binary. These binaries embed their original absolute
temporary source path because they were not built with `-trimpath`; a rebuild
under a different `mktemp` path is therefore not expected to have the same
binary hash.

## Collection schedule

Each process produced one observation for every benchmark selected by its
suite. Odd rounds ran base then candidate; even rounds ran candidate then base,
producing an ABBA schedule across each pair of rounds. Output was appended to a
separate raw file for each variant.

| Suite | Binary suffix | Benchmark expression | Rounds | Target |
| --- | --- | --- | ---: | ---: |
| Public reused-string echo | `bench.test` | `^BenchmarkEmbedding/case=go_string_echo_128B/runtime=lunar$` | 20 | 1 s |
| Ingress, cold, and lifecycle components | `root.test` | `^BenchmarkReview` | 15 | 500 ms |
| Public cache-cold echo | `embedding-review.test` | `^BenchmarkReviewColdGoStringEcho$` | 15 | 500 ms |
| Prebuilt long `Value` echo | `prebuilt-review.test` | `^BenchmarkReviewPrebuiltLongStringEcho$` | 20 | 1 s |

Every invocation used this shape, substituting the row's binary, expression,
and target:

```sh
GOGC=100 GOMEMLIMIT=off GOMAXPROCS=1 "$BINARY" \
  -test.run='^$' \
  -test.bench="$BENCHMARK_EXPRESSION" \
  -test.benchmem \
  -test.benchtime="$BENCHTIME" \
  -test.count=1 \
  -test.cpu=1
```

The original collector retained separate per-variant output rather than a
combined event log; the ABBA order above is the collection procedure. Each of
the eight retained outputs is byte-for-byte identical to its corresponding
`cache-*-base.txt` or `cache-*-candidate.txt` file in this directory.

Medians, confidence intervals, and parent-to-candidate changes were computed
with
`golang.org/x/perf/cmd/benchstat@v0.0.0-20260709024250-82a0b07e230d`.
