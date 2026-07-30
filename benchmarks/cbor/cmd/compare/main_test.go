package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareTimingSamples(t *testing.T) {
	baseline := writeRecords(t, "baseline.jsonl", []int64{100, 102, 98, 101, 99})
	candidate := writeRecords(t, "candidate.jsonl", []int64{70, 72, 68, 71, 69})

	report, err := compare(options{
		baselinePath: baseline, candidatePath: candidate,
		minSamples: 5, bootstrap: 2_000, seed: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Baseline.Elapsed.Median != 100 || report.Candidate.Elapsed.Median != 70 {
		t.Fatalf("medians = %v/%v, want 100/70", report.Baseline.Elapsed.Median, report.Candidate.Elapsed.Median)
	}
	if report.ElapsedReduction < 0.29 || report.ElapsedReduction > 0.31 {
		t.Fatalf("elapsed reduction = %.4f, want about 0.30", report.ElapsedReduction)
	}
	if report.ElapsedReductionLow >= report.ElapsedReductionHigh {
		t.Fatalf("bootstrap interval is not ordered: %.4f..%.4f", report.ElapsedReductionLow, report.ElapsedReductionHigh)
	}
	if report.Signature.Preset != "small" || report.Signature.Execution != "raw" {
		t.Fatalf("unexpected signature: %+v", report.Signature)
	}
}

func TestCompareRejectsMismatchedWorkloads(t *testing.T) {
	baseline := writeRecords(t, "baseline.jsonl", []int64{100, 101, 99})
	candidate := writeRecords(t, "candidate.jsonl", []int64{70, 71, 69})
	contents, err := os.ReadFile(candidate)
	if err != nil {
		t.Fatal(err)
	}
	contents = bytes.ReplaceAll(contents, []byte(`"preset":"small"`), []byte(`"preset":"large"`))
	if err := os.WriteFile(candidate, contents, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = compare(options{baselinePath: baseline, candidatePath: candidate, minSamples: 3, bootstrap: 100})
	if err == nil || !strings.Contains(err.Error(), "workload signature") {
		t.Fatalf("mismatched workload error = %v", err)
	}
}

func TestCompareAllowsDifferentRuntimeVersions(t *testing.T) {
	baseline := writeRecords(t, "baseline.jsonl", []int64{100, 101, 99})
	candidate := writeRecords(t, "candidate.jsonl", []int64{70, 71, 69})
	replaceInFile(
		t,
		candidate,
		`"runtime_version":"Lua 5.1"`,
		`"runtime_version":"Lunik (Lua 5.1)"`,
	)

	report, err := compare(options{
		baselinePath: baseline, candidatePath: candidate,
		minSamples: 3, bootstrap: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Signature.RuntimeVersion != "Lua 5.1" ||
		report.CandidateRuntimeVersion != "Lunik (Lua 5.1)" {
		t.Fatalf(
			"runtime versions = %q/%q",
			report.Signature.RuntimeVersion,
			report.CandidateRuntimeVersion,
		)
	}
}

func TestCompareRejectsMismatchedSharedInputsAndFixtureSources(t *testing.T) {
	for _, field := range []string{"input_sha256", "codec_sha256", "workload_sha256"} {
		t.Run(field, func(t *testing.T) {
			baseline := writeRecords(t, "baseline.jsonl", []int64{100, 101, 99})
			candidate := writeRecords(t, "candidate.jsonl", []int64{70, 71, 69})
			contents, err := os.ReadFile(candidate)
			if err != nil {
				t.Fatal(err)
			}
			oldHash := strings.Repeat(map[string]string{
				"input_sha256": "d", "codec_sha256": "e", "workload_sha256": "f",
			}[field], 64)
			contents = bytes.ReplaceAll(contents, []byte(`"`+field+`":"`+oldHash+`"`), []byte(`"`+field+`":"`+strings.Repeat("9", 64)+`"`))
			if err := os.WriteFile(candidate, contents, 0o644); err != nil {
				t.Fatal(err)
			}
			_, err = compare(options{baselinePath: baseline, candidatePath: candidate, minSamples: 3, bootstrap: 100})
			if err == nil || !strings.Contains(err.Error(), "workload signature") {
				t.Fatalf("%s mismatch error = %v", field, err)
			}
		})
	}
}

func TestReadSampleSetRejectsMixedBinaryIdentity(t *testing.T) {
	path := writeRecords(t, "samples.jsonl", []int64{100, 101, 99})
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents = bytes.Replace(contents, []byte(`"binary_sha256":"`+strings.Repeat("4", 64)+`"`), []byte(`"binary_sha256":"`+strings.Repeat("9", 64)+`"`), 1)
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSampleSet(path, 3); err == nil || !strings.Contains(err.Error(), "implementation identity") {
		t.Fatalf("mixed identity error = %v", err)
	}
}

func TestCompareAllowsOnlyTheExpectedContextTaxSignatureDifference(t *testing.T) {
	collectionID := strings.Repeat("a", 32)
	binaryHash := strings.Repeat("1", 64)
	revision := strings.Repeat("a", 40)
	baseline := writeMetricRecordsWithCollection(t, "baseline.jsonl", []int64{100, 101, 99}, 1_000, 100, binaryHash, revision, false, collectionID, []int{1, 4, 5})
	candidate := writeMetricRecordsWithCollection(t, "candidate.jsonl", []int64{103, 104, 102}, 1_000, 100, binaryHash, revision, false, collectionID, []int{2, 3, 6})
	contents, err := os.ReadFile(candidate)
	if err != nil {
		t.Fatal(err)
	}
	contents = bytes.ReplaceAll(contents, []byte(`"execution":"raw"`), []byte(`"execution":"guarded"`))
	contents = bytes.ReplaceAll(contents, []byte(`"context_check_interval":0`), []byte(`"context_check_interval":256`))
	if err := os.WriteFile(candidate, contents, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := compare(options{baselinePath: baseline, candidatePath: candidate, minSamples: 3, bootstrap: 100}); err == nil {
		t.Fatal("ordinary implementation comparison accepted raw/guarded signature mismatch")
	}
	report, err := compare(options{
		baselinePath: baseline, candidatePath: candidate,
		comparisonMode: "context-tax", minSamples: 3, bootstrap: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.ComparisonMode != "context-tax" || report.Signature.Execution != "raw" || report.CandidateExecution != "guarded" || report.CandidateContextCheckInterval != 256 {
		t.Fatalf("context-tax report metadata = %+v", report)
	}
	if !report.Policy.StrictEvidence || !report.Policy.CollectionRequired || report.Policy.RequiredMeasurement != "any" {
		t.Fatalf("descriptive context-tax policy = %+v", report.Policy)
	}
	otherRevision := strings.Repeat("b", 40)
	replaceInFile(t, candidate, `"revision":"`+revision+`"`, `"revision":"`+otherRevision+`"`)
	if _, err := compare(options{
		baselinePath: baseline, candidatePath: candidate,
		comparisonMode: "context-tax", minSamples: 3, bootstrap: 100,
	}); err == nil || !strings.Contains(err.Error(), "archived build identities differ") {
		t.Fatalf("different context-tax revision error = %v", err)
	}
	replaceInFile(t, candidate, `"revision":"`+otherRevision+`"`, `"revision":"`+revision+`"`)
	replaceInFile(t, candidate, `"revision_modified":false`, `"revision_modified":true`)
	if _, err := compare(options{
		baselinePath: baseline, candidatePath: candidate,
		comparisonMode: "context-tax", minSamples: 3, bootstrap: 100,
	}); err == nil || !strings.Contains(err.Error(), "archived build identities differ") {
		t.Fatalf("different context-tax dirty identity error = %v", err)
	}
	replaceInFile(t, candidate, `"revision_modified":true`, `"revision_modified":false`)
	replaceInFile(t, candidate, `"binary_sha256":"`+binaryHash+`"`, `"binary_sha256":"`+strings.Repeat("9", 64)+`"`)
	if _, err := compare(options{
		baselinePath: baseline, candidatePath: candidate,
		comparisonMode: "context-tax", minSamples: 3, bootstrap: 100,
	}); err == nil || !strings.Contains(err.Error(), "context-tax baseline and candidate binaries differ") {
		t.Fatalf("different context-tax binary error = %v", err)
	}
}

func TestCompareRejectsMismatchedPairingMetadata(t *testing.T) {
	baseline := writeRecords(t, "baseline.jsonl", []int64{100, 101, 99})
	candidate := writeRecords(t, "candidate.jsonl", []int64{70, 71, 69})
	contents, err := os.ReadFile(candidate)
	if err != nil {
		t.Fatal(err)
	}
	contents = bytes.Replace(contents, []byte(`"sample_run":3`), []byte(`"sample_run":4`), 1)
	if err := os.WriteFile(candidate, contents, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = compare(options{baselinePath: baseline, candidatePath: candidate, minSamples: 3, bootstrap: 100})
	if err == nil || !strings.Contains(err.Error(), "paired sample runs") {
		t.Fatalf("mismatched pair metadata error = %v", err)
	}
}

func TestEvaluateEnforcesNoiseAndSpeedGates(t *testing.T) {
	baseline := writeRecords(t, "baseline.jsonl", []int64{100, 101, 99, 100, 100})
	candidate := writeRecords(t, "candidate.jsonl", []int64{90, 91, 89, 90, 90})
	report, err := compare(options{
		baselinePath: baseline, candidatePath: candidate,
		minSamples: 5, bootstrap: 200, seed: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := evaluate(&report, options{maxCV: 0.05, minSpeedup: 0.20}); err == nil || !strings.Contains(err.Error(), "speedup") {
		t.Fatalf("speed gate error = %v", err)
	}
	report.Candidate.Elapsed.CV = 0.20
	if err := evaluate(&report, options{maxCV: 0.05}); err == nil || !strings.Contains(err.Error(), "CV") {
		t.Fatalf("noise gate error = %v", err)
	}
}

func TestMaxElapsedRatioGateIsExplicitRecordedAndEmittableBeforeFailure(t *testing.T) {
	report := comparisonReport{
		Baseline:        sampleSummary{Elapsed: distribution{Median: 100}},
		Candidate:       sampleSummary{Elapsed: distribution{Median: 80}},
		TotalAllocRatio: floatPointer(0.9), MallocRatio: floatPointer(0.9),
	}
	report.ElapsedReduction = 0.20
	err := evaluate(&report, options{maxElapsedRatio: 0.75})
	if err == nil || !strings.Contains(err.Error(), "elapsed ratio") {
		t.Fatalf("elapsed-ratio gate error = %v", err)
	}
	if !report.Qualification {
		t.Fatal("enabled elapsed gate did not mark report as qualification")
	}
	gate := findGate(t, report.Gates, "elapsed ratio")
	if !gate.Enabled || gate.Relation != "<=" || gate.Threshold != 0.75 || gate.Actual == nil || *gate.Actual != 0.8 || gate.Passed == nil || *gate.Passed {
		t.Fatalf("elapsed gate = %+v", gate)
	}
	var output strings.Builder
	if emitErr := emit(&output, "json", report); emitErr != nil {
		t.Fatal(emitErr)
	}
	for _, fragment := range []string{`"qualification": true`, `"name": "elapsed ratio"`, `"threshold": 0.75`, `"passed": false`} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("failed gate report was not emitted with %s:\n%s", fragment, output.String())
		}
	}

	if err := evaluate(&report, options{}); err != nil {
		t.Fatalf("zero max-elapsed-ratio did not disable gate: %v", err)
	}
	gate = findGate(t, report.Gates, "elapsed ratio")
	if gate.Enabled || gate.Passed != nil {
		t.Fatalf("disabled elapsed gate = %+v", gate)
	}
}

func TestRunComparisonArchivesPolicyAndGateResultsBeforeExitTwo(t *testing.T) {
	collectionID := strings.Repeat("a", 32)
	baselineSequences, candidateSequences := pairedSequences(minimumQualificationSamples)
	baselineHash := strings.Repeat("1", 64)
	baselineRevision := strings.Repeat("a", 40)
	baseline := writeMetricRecordsWithCollection(
		t, "baseline.jsonl", repeatedElapsed(100, minimumQualificationSamples), 1_000, 100,
		baselineHash, baselineRevision, false, collectionID, baselineSequences,
	)
	candidate := writeMetricRecordsWithCollection(
		t, "candidate.jsonl", repeatedElapsed(80, minimumQualificationSamples), 900, 90,
		strings.Repeat("2", 64), strings.Repeat("b", 40), false, collectionID, candidateSequences,
	)
	outputPath := filepath.Join(t.TempDir(), "failed-qualification.json")
	opts := options{
		baselinePath: baseline, candidatePath: candidate,
		minSamples: minimumQualificationSamples, bootstrap: 321, seed: 7,
		maxCV: 0.05, maxElapsedRatio: 0.75,
		expectBaselineSHA256: baselineHash, expectBaselineRevision: baselineRevision,
		requireClean: true, outputPath: outputPath, format: "json",
	}
	var stdout, stderr strings.Builder
	if exitCode := runComparison(opts, &stdout, &stderr); exitCode != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%s", exitCode, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("qualified archive also leaked to stdout: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "elapsed ratio") {
		t.Fatalf("gate failure stderr = %q", stderr.String())
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var archived comparisonReport
	if err := json.Unmarshal(contents, &archived); err != nil {
		t.Fatalf("decode archived report: %v\n%s", err, contents)
	}
	for _, field := range []string{
		`"expected_baseline_sha256"`, `"expected_baseline_revision"`,
		`"expected_pre_tranche_sha256"`, `"expected_pre_tranche_revision"`,
		`"require_clean"`, `"min_samples"`, `"bootstrap"`, `"seed"`,
		`"strict_evidence"`, `"collection_required"`, `"required_measurement"`,
	} {
		if !bytes.Contains(contents, []byte(field)) {
			t.Fatalf("archived report omits policy field %s:\n%s", field, contents)
		}
	}
	absoluteOutput, err := filepath.Abs(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	wantPolicy := comparisonPolicy{
		MinSamples: minimumQualificationSamples, Bootstrap: 321, Seed: 7,
		StrictEvidence: true, CollectionRequired: true, RequiredMeasurement: "timing",
		ExpectedBaselineSHA256: baselineHash, ExpectedBaselineRevision: baselineRevision,
		RequireClean: true, OutputPath: absoluteOutput, Format: "json",
	}
	if archived.Policy != wantPolicy {
		t.Fatalf("archived policy = %+v, want %+v", archived.Policy, wantPolicy)
	}
	if !archived.Qualification || archived.ComparisonMode != "implementations" {
		t.Fatalf("archived qualification metadata = %+v", archived)
	}
	passingGate := findGate(t, archived.Gates, "baseline timing CV")
	failingGate := findGate(t, archived.Gates, "elapsed ratio")
	if passingGate.Passed == nil || !*passingGate.Passed {
		t.Fatalf("passing gate was not archived: %+v", passingGate)
	}
	if failingGate.Passed == nil || *failingGate.Passed || failingGate.Threshold != 0.75 {
		t.Fatalf("failing gate was not archived: %+v", failingGate)
	}
	originalArchive := append([]byte(nil), contents...)
	stdout.Reset()
	stderr.Reset()
	if exitCode := runComparison(opts, &stdout, &stderr); exitCode != 1 || !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("exclusive archive rerun: exit=%d stderr=%q", exitCode, stderr.String())
	}
	contents, err = os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(contents, originalArchive) {
		t.Fatal("refused archive rerun changed the existing report")
	}
	var markdown strings.Builder
	if err := emit(&markdown, "markdown", archived); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"| Comparison mode | implementations |",
		"| Strict evidence validation | true |",
		"| Collection metadata required | true |",
		"| Required measurement | timing |",
		"| Expected baseline SHA-256 | " + baselineHash + " |",
		"| Expected baseline revision | " + baselineRevision + " |",
		"| Clean builds required | true |",
	} {
		if !strings.Contains(markdown.String(), fragment) {
			t.Fatalf("markdown policy lacks %q:\n%s", fragment, markdown.String())
		}
	}
}

func TestDescriptiveRunMayReportToStdout(t *testing.T) {
	baseline := writeRecords(t, "baseline.jsonl", []int64{100, 101, 99})
	candidate := writeRecords(t, "candidate.jsonl", []int64{120, 121, 119})
	var stdout, stderr strings.Builder
	exitCode := runComparison(options{
		baselinePath: baseline, candidatePath: candidate,
		minSamples: 3, bootstrap: 100, format: "json",
	}, &stdout, &stderr)
	if exitCode != 0 || stdout.Len() == 0 || stderr.Len() != 0 {
		t.Fatalf("descriptive stdout result: exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	var report comparisonReport
	if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
		t.Fatal(err)
	}
	if report.Qualification || report.Policy.StrictEvidence || report.Policy.CollectionRequired || report.Policy.RequiredMeasurement != "any" {
		t.Fatalf("descriptive policy = %+v", report.Policy)
	}
}

func TestContextTaxElapsedRatioGateUsesExplicitFivePercentCeiling(t *testing.T) {
	report := comparisonReport{
		Baseline:  sampleSummary{Elapsed: distribution{Median: 100}},
		Candidate: sampleSummary{Elapsed: distribution{Median: 105}},
	}
	if err := evaluate(&report, options{maxElapsedRatio: 1.05}); err != nil {
		t.Fatalf("five-percent context-tax ceiling rejected boundary value: %v", err)
	}
	gate := findGate(t, report.Gates, "elapsed ratio")
	if gate.Passed == nil || !*gate.Passed || gate.Threshold != 1.05 {
		t.Fatalf("context-tax elapsed gate = %+v", gate)
	}

	report.Candidate.Elapsed.Median = 106
	if err := evaluate(&report, options{maxElapsedRatio: 1.05}); err == nil || !strings.Contains(err.Error(), "elapsed ratio") {
		t.Fatalf("six-percent context-tax overhead gate error = %v", err)
	}
}

func TestDescriptiveComparisonDoesNotRejectSlowerCandidate(t *testing.T) {
	report := comparisonReport{
		Baseline:  sampleSummary{Elapsed: distribution{Median: 100}},
		Candidate: sampleSummary{Elapsed: distribution{Median: 150}},
	}
	report.ElapsedReduction = -0.5
	if err := evaluate(&report, options{}); err != nil {
		t.Fatalf("descriptive comparison rejected slower candidate: %v", err)
	}
	if report.Qualification {
		t.Fatal("descriptive comparison was labelled qualification")
	}
}

func TestQualificationGateValidationRequiresPinsCleanSamplesAndFiniteThresholds(t *testing.T) {
	valid := options{
		minSamples: minimumQualificationSamples, maxElapsedRatio: 0.75,
		expectBaselineSHA256:   strings.Repeat("1", 64),
		expectBaselineRevision: strings.Repeat("a", 40),
		requireClean:           true,
		outputPath:             filepath.Join(t.TempDir(), "report.json"),
	}
	if err := validateGateOptions(&valid); err != nil {
		t.Fatalf("valid qualification rejected: %v", err)
	}
	for name, mutate := range map[string]func(*options){
		"missing hash":     func(opts *options) { opts.expectBaselineSHA256 = "" },
		"missing revision": func(opts *options) { opts.expectBaselineRevision = "" },
		"not clean":        func(opts *options) { opts.requireClean = false },
		"too few samples":  func(opts *options) { opts.minSamples = minimumQualificationSamples - 1 },
		"missing output":   func(opts *options) { opts.outputPath = "" },
	} {
		t.Run(name, func(t *testing.T) {
			opts := valid
			mutate(&opts)
			if err := validateGateOptions(&opts); err == nil {
				t.Fatalf("invalid qualification was accepted: %+v", opts)
			}
		})
	}
	for name, mutate := range map[string]func(*options){
		"cv NaN":             func(opts *options) { opts.maxCV = math.NaN() },
		"speedup infinity":   func(opts *options) { opts.minSpeedup = math.Inf(1) },
		"elapsed NaN":        func(opts *options) { opts.maxElapsedRatio = math.NaN() },
		"allocated infinity": func(opts *options) { opts.maxTotalAllocRatio = math.Inf(1) },
		"malloc negative":    func(opts *options) { opts.maxMallocRatio = -1 },
		"excess NaN":         func(opts *options) { opts.minExcessMallocRemoval = math.NaN() },
	} {
		t.Run(name, func(t *testing.T) {
			opts := valid
			mutate(&opts)
			if err := validateGateOptions(&opts); err == nil || !strings.Contains(err.Error(), "finite nonnegative") {
				t.Fatalf("non-finite threshold error = %v", err)
			}
		})
	}
}

func TestReportArchiveRefusesImplicitOverwriteAndEvidenceCollision(t *testing.T) {
	directory := t.TempDir()
	archive := filepath.Join(directory, "report.json")
	if err := os.WriteFile(archive, []byte("existing report"), 0o644); err != nil {
		t.Fatal(err)
	}
	if file, err := createReportFile(archive, false); err == nil {
		file.Close()
		t.Fatal("existing report archive was replaced without -overwrite")
	}
	file, err := createReportFile(archive, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("overwritten report contents = %q, want empty", contents)
	}

	if err := validateGateOptions(&options{overwrite: true}); err == nil || !strings.Contains(err.Error(), "requires -output") {
		t.Fatalf("orphaned -overwrite error = %v", err)
	}
	evidence := filepath.Join(directory, "baseline.jsonl")
	if err := os.WriteFile(evidence, []byte("evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{baselinePath: evidence, outputPath: evidence}
	if err := validateGateOptions(&opts); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("report/evidence collision error = %v", err)
	}
}

func TestQualificationRejectsRetainedMeasurementButDescriptiveComparisonAllowsIt(t *testing.T) {
	collectionID := strings.Repeat("a", 32)
	baselineSequences, candidateSequences := pairedSequences(minimumQualificationSamples)
	baseline := writeMetricRecordsWithCollection(
		t, "baseline.jsonl", repeatedElapsed(100, minimumQualificationSamples), 1_000, 100,
		strings.Repeat("1", 64), strings.Repeat("a", 40), false, collectionID, baselineSequences,
	)
	candidate := writeMetricRecordsWithCollection(
		t, "candidate.jsonl", repeatedElapsed(70, minimumQualificationSamples), 900, 90,
		strings.Repeat("2", 64), strings.Repeat("b", 40), false, collectionID, candidateSequences,
	)
	for _, path := range []string{baseline, candidate} {
		replaceInFile(t, path, `"measurement":"timing"`, `"measurement":"retained"`)
	}
	if _, err := compare(options{
		baselinePath: baseline, candidatePath: candidate,
		minSamples: minimumQualificationSamples, bootstrap: 100,
		maxElapsedRatio:        0.75,
		expectBaselineSHA256:   strings.Repeat("1", 64),
		expectBaselineRevision: strings.Repeat("a", 40),
		requireClean:           true,
		outputPath:             filepath.Join(t.TempDir(), "report.json"),
	}); err == nil || !strings.Contains(err.Error(), "require timing evidence") {
		t.Fatalf("retained qualification error = %v", err)
	}
	report, err := compare(options{
		baselinePath: baseline, candidatePath: candidate,
		minSamples: minimumQualificationSamples, bootstrap: 100,
	})
	if err != nil {
		t.Fatalf("descriptive retained comparison failed: %v", err)
	}
	if report.Policy.RequiredMeasurement != "any" || report.Qualification {
		t.Fatalf("descriptive retained policy = %+v", report.Policy)
	}
}

func TestExcessQualificationRequiresPinnedPreTrancheIdentity(t *testing.T) {
	opts := options{
		preTranchePath: "pre.jsonl", minSamples: minimumQualificationSamples,
		minExcessMallocRemoval: 0.8,
		expectBaselineSHA256:   strings.Repeat("1", 64),
		expectBaselineRevision: strings.Repeat("a", 40),
		requireClean:           true,
		outputPath:             filepath.Join(t.TempDir(), "report.json"),
	}
	if err := validateGateOptions(&opts); err == nil || !strings.Contains(err.Error(), "expect-pre-tranche") {
		t.Fatalf("missing pre-tranche pins error = %v", err)
	}
	opts.expectPreTrancheSHA256 = strings.Repeat("3", 64)
	opts.expectPreTrancheRevision = strings.Repeat("c", 40)
	if err := validateGateOptions(&opts); err != nil {
		t.Fatalf("fully pinned excess gate rejected: %v", err)
	}
}

func TestGatedComparisonRequiresModernCollectionMetadata(t *testing.T) {
	baseline := writeRecords(t, "baseline.jsonl", repeatedElapsed(100, minimumQualificationSamples))
	candidate := writeRecords(t, "candidate.jsonl", repeatedElapsed(70, minimumQualificationSamples))
	_, err := compare(options{
		baselinePath: baseline, candidatePath: candidate,
		minSamples: minimumQualificationSamples, maxElapsedRatio: 0.75,
		expectBaselineSHA256:   strings.Repeat("1", 64),
		expectBaselineRevision: strings.Repeat("a", 40),
		requireClean:           true,
		outputPath:             filepath.Join(t.TempDir(), "report.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "collection_id") {
		t.Fatalf("missing gated collection metadata error = %v", err)
	}
}

func TestGatedComparisonRejectsMismatchedOracleMetadata(t *testing.T) {
	collectionID := strings.Repeat("a", 32)
	baselineSequences := make([]int, minimumQualificationSamples)
	candidateSequences := make([]int, minimumQualificationSamples)
	for index := range baselineSequences {
		baselineSequences[index] = index*2 + 1
		candidateSequences[index] = index*2 + 2
	}
	baseline := writeMetricRecordsWithCollection(
		t, "baseline.jsonl", repeatedElapsed(100, minimumQualificationSamples), 1_000, 100,
		strings.Repeat("1", 64), strings.Repeat("a", 40), false, collectionID, baselineSequences,
	)
	candidate := writeMetricRecordsWithCollection(
		t, "candidate.jsonl", repeatedElapsed(70, minimumQualificationSamples), 900, 90,
		strings.Repeat("2", 64), strings.Repeat("b", 40), false, collectionID, candidateSequences,
	)
	replaceInFile(t, candidate, `"rooms":2`, `"rooms":3`)
	_, err := compare(options{
		baselinePath: baseline, candidatePath: candidate,
		minSamples: minimumQualificationSamples, maxElapsedRatio: 0.75,
		expectBaselineSHA256:   strings.Repeat("1", 64),
		expectBaselineRevision: strings.Repeat("a", 40),
		requireClean:           true,
		outputPath:             filepath.Join(t.TempDir(), "report.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "workload signature") {
		t.Fatalf("oracle metadata mismatch error = %v", err)
	}
}

func TestContextTaxRequiresModernCollectionMetadataEvenWithoutPerformanceGate(t *testing.T) {
	binaryHash := strings.Repeat("1", 64)
	revision := strings.Repeat("a", 40)
	baseline := writeMetricRecords(t, "baseline.jsonl", []int64{100, 101, 99}, 1_000, 100, binaryHash, revision, false)
	candidate := writeMetricRecords(t, "candidate.jsonl", []int64{101, 102, 100}, 1_000, 100, binaryHash, revision, false)
	replaceInFile(t, candidate, `"execution":"raw"`, `"execution":"guarded"`)
	replaceInFile(t, candidate, `"context_check_interval":0`, `"context_check_interval":256`)
	_, err := compare(options{
		baselinePath: baseline, candidatePath: candidate,
		comparisonMode: "context-tax", minSamples: 3, bootstrap: 100,
	})
	if err == nil || !strings.Contains(err.Error(), "collection_id") {
		t.Fatalf("missing context-tax collection metadata error = %v", err)
	}
}

func TestStrictArchivedRecordValidationRejectsMalformedQualificationEvidence(t *testing.T) {
	for name, testCase := range map[string]struct {
		mutate func(t *testing.T, path string)
		want   string
	}{
		"unknown field": {
			mutate: func(t *testing.T, path string) {
				replaceInFile(t, path, `,"oracle":`, `,"unexpected_field":true,"oracle":`)
			},
			want: "unknown field",
		},
		"wrong implementation": {
			mutate: func(t *testing.T, path string) {
				replaceInFile(t, path, `"implementation":"baseline"`, `"implementation":"candidate"`)
			},
			want: "implementation",
		},
		"invalid binary hash": {
			mutate: func(t *testing.T, path string) {
				replaceInFile(t, path, `"binary_sha256":"`+strings.Repeat("1", 64)+`"`, `"binary_sha256":"not-a-hash"`)
			},
			want: "binary_sha256",
		},
		"nonpositive elapsed": {
			mutate: func(t *testing.T, path string) {
				replaceInFile(t, path, `"elapsed_ns":100`, `"elapsed_ns":0`)
			},
			want: "elapsed_ns must be positive",
		},
		"missing oracle count": {
			mutate: func(t *testing.T, path string) {
				replaceInFile(t, path, `"rooms":2`, `"rooms":0`)
			},
			want: "invalid oracle counts",
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeMetricRecordsWithCollection(
				t, "baseline.jsonl", []int64{100}, 1_000, 100,
				strings.Repeat("1", 64), strings.Repeat("a", 40), false,
				strings.Repeat("a", 32), []int{1},
			)
			testCase.mutate(t, path)
			if _, err := readSampleSetPolicy(path, 1, true, "baseline"); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("strict validation error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestCompareRejectsIdenticalImplementationBinaries(t *testing.T) {
	baseline := writeRecords(t, "baseline.jsonl", []int64{100, 101, 99})
	candidate := writeRecords(t, "candidate.jsonl", []int64{90, 91, 89})
	replaceInFile(t, candidate, `"binary_sha256":"`+strings.Repeat("2", 64)+`"`, `"binary_sha256":"`+strings.Repeat("1", 64)+`"`)

	_, err := compare(options{
		baselinePath: baseline, candidatePath: candidate, minSamples: 3, bootstrap: 100,
	})
	if err == nil || !strings.Contains(err.Error(), "identical binary SHA-256") {
		t.Fatalf("identical implementation error = %v", err)
	}
}

func TestEvaluateEnforcesAllocationRatios(t *testing.T) {
	baseline := writeMetricRecords(t, "baseline.jsonl", []int64{100, 101, 99}, 1_000, 100, "baseline-binary", strings.Repeat("a", 40), false)
	candidate := writeMetricRecords(t, "candidate.jsonl", []int64{90, 91, 89}, 900, 90, "candidate-binary", strings.Repeat("b", 40), false)
	report, err := compare(options{
		baselinePath: baseline, candidatePath: candidate, minSamples: 3, bootstrap: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalAllocRatio == nil || report.MallocRatio == nil || *report.TotalAllocRatio != 0.9 || *report.MallocRatio != 0.9 {
		t.Fatalf("allocation ratios = %v/%v, want 0.9/0.9", report.TotalAllocRatio, report.MallocRatio)
	}
	if err := evaluate(&report, options{maxTotalAllocRatio: 0.85}); err == nil || !strings.Contains(err.Error(), "allocated-byte ratio") {
		t.Fatalf("allocated-byte gate error = %v", err)
	}
	if err := evaluate(&report, options{maxMallocRatio: 0.85}); err == nil || !strings.Contains(err.Error(), "malloc ratio") {
		t.Fatalf("malloc gate error = %v", err)
	}
	if err := evaluate(&report, options{maxTotalAllocRatio: 0.90, maxMallocRatio: 0.90}); err != nil {
		t.Fatalf("passing allocation gates failed: %v", err)
	}
}

func TestCompareCalculatesAndGatesExcessMallocRemoval(t *testing.T) {
	collectionID := strings.Repeat("a", 32)
	baselineRevision := strings.Repeat("a", 40)
	preTrancheRevision := strings.Repeat("c", 40)
	preTrancheHash := strings.Repeat("3", 64)
	baseline := writeMetricRecordsWithCollection(t, "baseline.jsonl", []int64{100, 101, 99}, 1_000, 100, strings.Repeat("1", 64), baselineRevision, false, collectionID, []int{1, 5, 7})
	preTranche := writeMetricRecordsWithCollection(t, "pre-tranche.jsonl", []int64{120, 121, 119}, 1_800, 300, preTrancheHash, preTrancheRevision, false, collectionID, []int{3, 6, 8})
	candidate := writeMetricRecordsWithCollection(t, "candidate.jsonl", []int64{90, 91, 89}, 1_100, 120, strings.Repeat("2", 64), strings.Repeat("b", 40), false, collectionID, []int{2, 4, 9})
	opts := options{
		baselinePath: baseline, candidatePath: candidate, preTranchePath: preTranche,
		minSamples: 3, bootstrap: 100,
		expectPreTrancheSHA256: preTrancheHash, expectPreTrancheRevision: preTrancheRevision,
	}
	report, err := compare(opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.PreTranche == nil || report.ExcessMallocRemoval == nil || *report.ExcessMallocRemoval != 0.9 {
		t.Fatalf("pre-tranche report = %+v, removal = %v, want 0.9", report.PreTranche, report.ExcessMallocRemoval)
	}
	if report.CollectionID != collectionID {
		t.Fatalf("collection ID = %q, want %q", report.CollectionID, collectionID)
	}
	if report.Policy.ExpectedPreTrancheSHA256 != preTrancheHash ||
		report.Policy.ExpectedPreTrancheRevision != preTrancheRevision ||
		!report.Policy.StrictEvidence || !report.Policy.CollectionRequired {
		t.Fatalf("pre-tranche policy = %+v", report.Policy)
	}
	if err := evaluate(&report, options{minExcessMallocRemoval: 0.80}); err != nil {
		t.Fatalf("passing excess-removal gate failed: %v", err)
	}
	if err := evaluate(&report, options{minExcessMallocRemoval: 0.95}); err == nil || !strings.Contains(err.Error(), "excess malloc removal") {
		t.Fatalf("excess-removal gate error = %v", err)
	}
	wrongPreTranche := opts
	wrongPreTranche.expectPreTrancheSHA256 = strings.Repeat("4", 64)
	if _, err := compare(wrongPreTranche); err == nil || !strings.Contains(err.Error(), "pre-tranche SHA-256") {
		t.Fatalf("wrong pre-tranche hash error = %v", err)
	}
}

func TestThreeWayComparisonRequiresOneCompleteCollection(t *testing.T) {
	collectionID := strings.Repeat("a", 32)
	baseline := writeMetricRecordsWithCollection(t, "baseline.jsonl", []int64{100, 101}, 1_000, 100, strings.Repeat("1", 64), strings.Repeat("a", 40), false, collectionID, []int{1, 5})
	candidate := writeMetricRecordsWithCollection(t, "candidate.jsonl", []int64{90, 91}, 900, 90, strings.Repeat("2", 64), strings.Repeat("b", 40), false, collectionID, []int{2, 4})
	preTranche := writeMetricRecordsWithCollection(t, "pre-tranche.jsonl", []int64{120, 121}, 1_500, 200, strings.Repeat("3", 64), strings.Repeat("c", 40), false, collectionID, []int{3, 6})
	opts := options{
		baselinePath: baseline, candidatePath: candidate, preTranchePath: preTranche,
		minSamples: 2, bootstrap: 100,
	}
	if _, err := compare(opts); err != nil {
		t.Fatalf("valid three-way collection failed: %v", err)
	}
	replaceInFile(t, preTranche, `"collection_id":"`+collectionID+`"`, `"collection_id":"`+strings.Repeat("b", 32)+`"`)
	if _, err := compare(opts); err == nil || !strings.Contains(err.Error(), "collection IDs differ") {
		t.Fatalf("mixed collection error = %v", err)
	}

	preTranche = writeMetricRecordsWithCollection(t, "pre-tranche-duplicate.jsonl", []int64{120, 121}, 1_500, 200, strings.Repeat("3", 64), strings.Repeat("c", 40), false, collectionID, []int{2, 6})
	opts.preTranchePath = preTranche
	if _, err := compare(opts); err == nil || !strings.Contains(err.Error(), "sample sequence 2") {
		t.Fatalf("duplicate sequence error = %v", err)
	}
}

func TestCompareAllocationRatiosHandleZeroBaselineWithoutBreakingUngatedReports(t *testing.T) {
	baseline := writeMetricRecords(t, "baseline.jsonl", []int64{100, 101, 99}, 0, 0, "baseline-binary", strings.Repeat("a", 40), false)
	candidate := writeMetricRecords(t, "candidate.jsonl", []int64{90, 91, 89}, 1, 1, "candidate-binary", strings.Repeat("b", 40), false)
	report, err := compare(options{baselinePath: baseline, candidatePath: candidate, minSamples: 3, bootstrap: 100})
	if err != nil {
		t.Fatalf("ungated zero-baseline comparison failed: %v", err)
	}
	if report.TotalAllocRatio != nil || report.MallocRatio != nil {
		t.Fatalf("unbounded ratios = %v/%v, want nil/nil", report.TotalAllocRatio, report.MallocRatio)
	}
	var jsonReport strings.Builder
	if err := emit(&jsonReport, "json", report); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"total_alloc_ratio": null`, `"malloc_ratio": null`} {
		if !strings.Contains(jsonReport.String(), field) {
			t.Fatalf("JSON report does not explicitly represent unbounded ratio %s:\n%s", field, jsonReport.String())
		}
	}
	var markdown strings.Builder
	if err := emit(&markdown, "markdown", report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown.String(), "Allocation ratio unbounded") {
		t.Fatalf("markdown report lacks unbounded-ratio note:\n%s", markdown.String())
	}
	if err := evaluate(&report, options{}); err != nil {
		t.Fatalf("ungated zero-baseline report failed: %v", err)
	}
	if err := evaluate(&report, options{maxTotalAllocRatio: 1}); err == nil || !strings.Contains(err.Error(), "unavailable or unbounded") {
		t.Fatalf("zero-baseline allocated-byte gate error = %v", err)
	}
	if err := evaluate(&report, options{maxMallocRatio: 1}); err == nil || !strings.Contains(err.Error(), "unavailable or unbounded") {
		t.Fatalf("zero-baseline malloc gate error = %v", err)
	}

	zeroCandidate := writeMetricRecords(t, "zero-candidate.jsonl", []int64{90, 91, 89}, 0, 0, "zero-candidate-binary", strings.Repeat("d", 40), false)
	zeroReport, err := compare(options{baselinePath: baseline, candidatePath: zeroCandidate, minSamples: 3, bootstrap: 100})
	if err != nil {
		t.Fatal(err)
	}
	if zeroReport.TotalAllocRatio == nil || zeroReport.MallocRatio == nil || *zeroReport.TotalAllocRatio != 0 || *zeroReport.MallocRatio != 0 {
		t.Fatalf("zero/zero ratios = %v/%v, want 0/0", zeroReport.TotalAllocRatio, zeroReport.MallocRatio)
	}
	if err := evaluate(&zeroReport, options{maxTotalAllocRatio: 0.01, maxMallocRatio: 0.01}); err != nil {
		t.Fatalf("zero/zero allocation gates failed: %v", err)
	}
}

func TestCompareEnforcesFrozenBaselineAndCleanBuilds(t *testing.T) {
	baselineHash := strings.Repeat("1", 64)
	baselineRevision := strings.Repeat("a", 40)
	baseline := writeMetricRecords(t, "baseline.jsonl", []int64{100, 101, 99}, 1_000, 100, baselineHash, baselineRevision, false)
	candidate := writeMetricRecords(t, "candidate.jsonl", []int64{90, 91, 89}, 900, 90, strings.Repeat("2", 64), strings.Repeat("b", 40), false)
	opts := options{
		baselinePath: baseline, candidatePath: candidate, minSamples: 3, bootstrap: 100,
		expectBaselineSHA256: baselineHash, expectBaselineRevision: baselineRevision,
		requireClean: true,
	}
	if _, err := compare(opts); err != nil {
		t.Fatalf("authenticated clean evidence failed: %v", err)
	}
	wrongHash := opts
	wrongHash.expectBaselineSHA256 = strings.Repeat("3", 64)
	if _, err := compare(wrongHash); err == nil || !strings.Contains(err.Error(), "does not match required") {
		t.Fatalf("wrong baseline hash error = %v", err)
	}
	replaceInFile(t, candidate, `"revision_modified":false`, `"revision_modified":true`)
	if _, err := compare(opts); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("modified candidate error = %v", err)
	}
}

func TestExcessRemovalGateRequiresPreTrancheEvidence(t *testing.T) {
	opts := options{minExcessMallocRemoval: 0.8}
	if err := validateGateOptions(&opts); err == nil || !strings.Contains(err.Error(), "requires -pre-tranche") {
		t.Fatalf("missing pre-tranche error = %v", err)
	}
	opts = options{expectPreTrancheRevision: strings.Repeat("a", 40)}
	if err := validateGateOptions(&opts); err == nil || !strings.Contains(err.Error(), "identity expectations require -pre-tranche") {
		t.Fatalf("orphaned pre-tranche identity error = %v", err)
	}
}

func writeRecords(t *testing.T, name string, elapsed []int64) string {
	identity := strings.TrimSuffix(name, filepath.Ext(name))
	revisionByte := "d"
	hashByte := "4"
	switch {
	case strings.Contains(identity, "baseline"):
		revisionByte = "a"
		hashByte = "1"
	case strings.Contains(identity, "candidate"):
		revisionByte = "b"
		hashByte = "2"
	case strings.Contains(identity, "pre-tranche"):
		revisionByte = "c"
		hashByte = "3"
	}
	return writeMetricRecords(
		t, name, elapsed, 1_000, 100,
		strings.Repeat(hashByte, 64), strings.Repeat(revisionByte, 40), false,
	)
}

func writeMetricRecords(
	t *testing.T,
	name string,
	elapsed []int64,
	totalAlloc, mallocs uint64,
	binarySHA256, revision string,
	revisionModified bool,
) string {
	return writeMetricRecordsWithCollection(
		t, name, elapsed, totalAlloc, mallocs, binarySHA256, revision,
		revisionModified, "", nil,
	)
}

func writeMetricRecordsWithCollection(
	t *testing.T,
	name string,
	elapsed []int64,
	totalAlloc, mallocs uint64,
	binarySHA256, revision string,
	revisionModified bool,
	collectionID string,
	sequences []int,
) string {
	t.Helper()
	if sequences != nil && len(sequences) != len(elapsed) {
		t.Fatalf("sequence count = %d, want %d", len(sequences), len(elapsed))
	}
	path := filepath.Join(t.TempDir(), name)
	implementation := "candidate"
	if strings.Contains(name, "pre-tranche") {
		implementation = "pre-tranche"
	} else if strings.Contains(name, "baseline") {
		implementation = "baseline"
	}
	var contents strings.Builder
	for index, value := range elapsed {
		contents.WriteString(`{"schema_version":2,"mode":"load","measurement":"timing","preset":"small","execution":"raw","context_check_interval":0,"elapsed_ns":`)
		contents.WriteString(integerString(value))
		contents.WriteString(`,"sample_run":`)
		contents.WriteString(integerString(int64(index + 1)))
		contents.WriteString(`,"sample_sequence":`)
		sequence := index + 1
		if sequences != nil {
			sequence = sequences[index]
		}
		contents.WriteString(integerString(int64(sequence)))
		if collectionID != "" {
			contents.WriteString(`,"collection_id":"`)
			contents.WriteString(collectionID)
			contents.WriteString(`"`)
		}
		contents.WriteString(`,"implementation":"`)
		contents.WriteString(implementation)
		contents.WriteString(`"`)
		contents.WriteString(`,"total_alloc_delta":`)
		contents.WriteString(integerString(int64(totalAlloc)))
		contents.WriteString(`,"mallocs_delta":`)
		contents.WriteString(integerString(int64(mallocs)))
		contents.WriteString(`,"go_version":"go1.25.1","goos":"darwin","goarch":"arm64","runtime_version":"Lua 5.1","revision":"`)
		contents.WriteString(revision)
		contents.WriteString(`","revision_modified":`)
		if revisionModified {
			contents.WriteString("true")
		} else {
			contents.WriteString("false")
		}
		contents.WriteString(`,"binary_sha256":"`)
		contents.WriteString(binarySHA256)
		contents.WriteString(`","input_sha256":"`)
		contents.WriteString(strings.Repeat("d", 64))
		contents.WriteString(`","codec_sha256":"`)
		contents.WriteString(strings.Repeat("e", 64))
		contents.WriteString(`","workload_sha256":"`)
		contents.WriteString(strings.Repeat("f", 64))
		contents.WriteString(`","oracle":{"digest":"`)
		contents.WriteString(strings.Repeat("c", 64))
		contents.WriteString(`","areas":1,"rooms":2,"exits":3,"tables":4,"entries":5,"encoded_bytes":6}}`)
		contents.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(contents.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func replaceInFile(t *testing.T, path, old, replacement string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.ReplaceAll(contents, []byte(old), []byte(replacement))
	if bytes.Equal(updated, contents) {
		t.Fatalf("fixture %s does not contain %q", path, old)
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		t.Fatal(err)
	}
}

func floatPointer(value float64) *float64 {
	return &value
}

func findGate(t *testing.T, gates []gateResult, name string) gateResult {
	t.Helper()
	for _, gate := range gates {
		if gate.Name == name {
			return gate
		}
	}
	t.Fatalf("gate %q not found in %+v", name, gates)
	return gateResult{}
}

func integerString(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte(value%10) + '0'
		value /= 10
	}
	return string(digits[position:])
}

func repeatedElapsed(value int64, count int) []int64 {
	values := make([]int64, count)
	for index := range values {
		values[index] = value
	}
	return values
}

func pairedSequences(count int) ([]int, []int) {
	baseline := make([]int, count)
	candidate := make([]int, count)
	for index := 0; index < count; index++ {
		baseline[index] = index*2 + 1
		candidate[index] = index*2 + 2
	}
	return baseline, candidate
}
