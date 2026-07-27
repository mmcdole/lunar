package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkloadArgumentsCarryOneMeasurementPolicy(t *testing.T) {
	opts := options{
		preset: "large", mode: "load", measurement: "timing",
		fixture: "/fixture", data: "/input.dat", guarded: true,
		contextCheckInterval: 256,
	}
	got := strings.Join(workloadArguments(opts, "baseline"), " ")
	for _, fragment := range []string{
		"-preset large", "-mode load", "-measurement timing", "-fixture /fixture",
		"-data /input.dat", "-format jsonl", "-guarded", "-context-check-interval 256",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("arguments %q do not contain %q", got, fragment)
		}
	}
}

func TestContextTaxArgumentsKeepBaselineRawAndGuardCandidate(t *testing.T) {
	opts := options{
		comparisonMode: "context-tax",
		preset:         "large", mode: "load", measurement: "timing",
		fixture: "/fixture", data: "/input.dat", contextCheckInterval: 256,
	}
	baseline := strings.Join(workloadArguments(opts, "baseline"), " ")
	candidate := strings.Join(workloadArguments(opts, "candidate"), " ")
	if strings.Contains(baseline, "-guarded") || strings.Contains(baseline, "-context-check-interval") {
		t.Fatalf("context-tax baseline is not raw: %q", baseline)
	}
	if !strings.Contains(candidate, "-guarded") || !strings.Contains(candidate, "-context-check-interval 256") {
		t.Fatalf("context-tax candidate is not guarded at interval 256: %q", candidate)
	}
}

func TestAugmentRecordAddsPairingMetadata(t *testing.T) {
	raw := []byte(`{"schema_version":1,"mode":"load","elapsed_ns":100}`)
	augmented, err := augmentRecord(raw, "candidate", "abc123", "collection-1", 3, 7)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(augmented, &record); err != nil {
		t.Fatal(err)
	}
	if record["implementation"] != "candidate" || record["sample_run"] != float64(3) || record["sample_sequence"] != float64(7) {
		t.Fatalf("pairing metadata = %#v", record)
	}
	if record["binary_sha256"] != "abc123" {
		t.Fatalf("binary hash = %#v, want abc123", record["binary_sha256"])
	}
	if record["collection_id"] != "collection-1" {
		t.Fatalf("collection ID = %#v, want collection-1", record["collection_id"])
	}
	if record["elapsed_ns"] != float64(100) {
		t.Fatalf("measurement was not preserved: %#v", record)
	}
}

func TestAugmentRecordRejectsNonObjectAndTrailingJSON(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`[1,2,3]`),
		[]byte(`{"mode":"load"}\n{"mode":"save"}`),
	} {
		if _, err := augmentRecord(raw, "candidate", "abc123", "collection-1", 1, 1); err == nil {
			t.Fatalf("accepted invalid profiler output %q", raw)
		}
	}
}

func TestNewCollectionIDIsHexEncoded(t *testing.T) {
	collectionID, err := newCollectionID()
	if err != nil {
		t.Fatal(err)
	}
	if len(collectionID) != 32 {
		t.Fatalf("collection ID length = %d, want 32", len(collectionID))
	}
	for _, character := range collectionID {
		if !strings.ContainsRune("0123456789abcdef", character) {
			t.Fatalf("collection ID %q is not lowercase hexadecimal", collectionID)
		}
	}
}

func TestValidateOptionsRequiresSharedData(t *testing.T) {
	opts := options{
		baseline: "baseline", candidate: "candidate",
		baselineOutput: "baseline.jsonl", candidateOutput: "candidate.jsonl",
		runs: 1, measurement: "timing", comparisonMode: "implementations", timeout: time.Second,
	}
	if err := validateOptions(&opts); err == nil || !strings.Contains(err.Error(), "-data is required") {
		t.Fatalf("missing shared input error = %v", err)
	}
}

func TestValidateOptionsRequiresCompletePreTranchePair(t *testing.T) {
	opts := options{
		baseline: "baseline", candidate: "candidate", preTranche: "pre-tranche",
		baselineOutput: "baseline.jsonl", candidateOutput: "candidate.jsonl",
		runs: 1, measurement: "timing", comparisonMode: "implementations", timeout: time.Second,
	}
	if err := validateOptions(&opts); err == nil || !strings.Contains(err.Error(), "supplied together") {
		t.Fatalf("incomplete pre-tranche error = %v", err)
	}
}

func TestValidateOptionsRejectsPreTrancheContextTax(t *testing.T) {
	opts := options{
		baseline: "baseline", candidate: "candidate", preTranche: "pre-tranche",
		baselineOutput: "baseline.jsonl", candidateOutput: "candidate.jsonl",
		preTrancheOutput: "pre-tranche.jsonl", data: "input.dat",
		runs: 1, measurement: "timing", comparisonMode: "context-tax",
		contextCheckInterval: 256, timeout: time.Second,
	}
	if err := validateOptions(&opts); err == nil || !strings.Contains(err.Error(), "does not accept") {
		t.Fatalf("context-tax pre-tranche error = %v", err)
	}
}

func TestPrepareImplementationsAuthenticatesAndSeparatesBinaries(t *testing.T) {
	directory := t.TempDir()
	baseline := filepath.Join(directory, "baseline")
	candidate := filepath.Join(directory, "candidate")
	preTranche := filepath.Join(directory, "pre-tranche")
	for path, contents := range map[string]string{
		baseline: "baseline", candidate: "candidate", preTranche: "pre-tranche",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	baselineHash, err := hashFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	opts := options{
		baseline: baseline, candidate: candidate, preTranche: preTranche,
		baselineOutput: "baseline.jsonl", candidateOutput: "candidate.jsonl",
		preTrancheOutput: "pre-tranche.jsonl", expectBaselineSHA256: baselineHash,
		comparisonMode: "implementations",
	}
	preTrancheHash, err := hashFile(preTranche)
	if err != nil {
		t.Fatal(err)
	}
	opts.expectPreTrancheSHA256 = preTrancheHash
	implementations, err := prepareImplementations(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(implementations) != 3 || implementations[2].label != "pre-tranche" {
		t.Fatalf("implementations = %+v", implementations)
	}
	wrongPreTranche := opts
	wrongPreTranche.expectPreTrancheSHA256 = strings.Repeat("f", 64)
	if _, err := prepareImplementations(wrongPreTranche); err == nil || !strings.Contains(err.Error(), "pre-tranche SHA-256") {
		t.Fatalf("wrong pre-tranche hash error = %v", err)
	}

	if err := os.WriteFile(candidate, []byte("baseline"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareImplementations(opts); err == nil || !strings.Contains(err.Error(), "identical SHA-256") {
		t.Fatalf("identical binary error = %v", err)
	}
}

func TestPrepareImplementationsRequiresSameBinaryForContextTax(t *testing.T) {
	directory := t.TempDir()
	baseline := filepath.Join(directory, "baseline")
	candidate := filepath.Join(directory, "candidate")
	if err := os.WriteFile(baseline, []byte("same"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("same"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := options{
		baseline: baseline, candidate: candidate,
		baselineOutput: "baseline.jsonl", candidateOutput: "candidate.jsonl",
		comparisonMode: "context-tax",
	}
	if _, err := prepareImplementations(opts); err != nil {
		t.Fatalf("same context-tax binary rejected: %v", err)
	}
	if err := os.WriteFile(candidate, []byte("different"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareImplementations(opts); err == nil || !strings.Contains(err.Error(), "context-tax") {
		t.Fatalf("different context-tax binary error = %v", err)
	}
}

func TestStageImplementationsFreezesAndRevalidatesWorkerBytes(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "worker")
	if err := os.WriteFile(source, []byte("original-worker"), 0o755); err != nil {
		t.Fatal(err)
	}
	hash, err := hashFile(source)
	if err != nil {
		t.Fatal(err)
	}
	staged, stagingDirectory, err := stageImplementations([]implementation{{
		label: "candidate", binary: source, sha256: hash,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stagingDirectory)
	if staged[0].binary == source || !strings.HasPrefix(staged[0].binary, stagingDirectory+string(os.PathSeparator)) {
		t.Fatalf("staged worker path = %q, source = %q, staging directory = %q", staged[0].binary, source, stagingDirectory)
	}
	if err := os.WriteFile(source, []byte("source-changed-after-staging"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyStagedImplementations(staged); err != nil {
		t.Fatalf("source mutation changed staged worker: %v", err)
	}
	if err := os.Chmod(staged[0].binary, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged[0].binary, []byte("staged-worker-mutated"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifyStagedImplementations(staged); err == nil || !strings.Contains(err.Error(), "changed during collection") {
		t.Fatalf("staged mutation error = %v", err)
	}
}

func TestValidateWorkerIdentityEnforcesRevisionAndCleanBuild(t *testing.T) {
	revision := strings.Repeat("a", 40)
	opts := options{expectBaselineRevision: revision, requireClean: true}
	clean := []byte(`{"revision":"` + revision + `","revision_modified":false}`)
	if err := validateWorkerIdentity(opts, "baseline", clean); err != nil {
		t.Fatalf("clean baseline rejected: %v", err)
	}
	modified := []byte(`{"revision":"` + revision + `","revision_modified":true}`)
	if err := validateWorkerIdentity(opts, "candidate", modified); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("modified worker error = %v", err)
	}
	wrong := []byte(`{"revision":"` + strings.Repeat("b", 40) + `","revision_modified":false}`)
	if err := validateWorkerIdentity(opts, "baseline", wrong); err == nil || !strings.Contains(err.Error(), "does not match required") {
		t.Fatalf("wrong revision error = %v", err)
	}
	preTrancheOpts := options{expectPreTrancheRevision: revision}
	if err := validateWorkerIdentity(preTrancheOpts, "pre-tranche", wrong); err == nil || !strings.Contains(err.Error(), "pre-tranche revision") {
		t.Fatalf("wrong pre-tranche revision error = %v", err)
	}
}

func TestValidateOptionsRequiresPreTrancheForIdentityExpectations(t *testing.T) {
	opts := options{
		baseline: "baseline", candidate: "candidate",
		baselineOutput: "baseline.jsonl", candidateOutput: "candidate.jsonl",
		expectPreTrancheSHA256: strings.Repeat("a", 64),
		runs:                   1, measurement: "timing", comparisonMode: "implementations", timeout: time.Second,
	}
	if err := validateOptions(&opts); err == nil || !strings.Contains(err.Error(), "require -pre-tranche") {
		t.Fatalf("orphaned pre-tranche identity error = %v", err)
	}
}

func TestCreateEvidenceFileRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.jsonl")
	if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if file, err := createEvidenceFile(path, false); err == nil {
		file.Close()
		t.Fatal("evidence file was overwritten without approval")
	}
	file, err := createEvidenceFile(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(bytes.Repeat([]byte{'x'}, 3)); err != nil {
		t.Fatal(err)
	}
}
