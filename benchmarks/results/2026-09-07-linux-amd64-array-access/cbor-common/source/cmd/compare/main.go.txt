// Command compare compares CBOR workload JSONL sample sets.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const minimumQualificationSamples = 15

type options struct {
	baselinePath             string
	candidatePath            string
	preTranchePath           string
	minSamples               int
	maxCV                    float64
	minSpeedup               float64
	maxElapsedRatio          float64
	maxTotalAllocRatio       float64
	maxMallocRatio           float64
	minExcessMallocRemoval   float64
	expectBaselineSHA256     string
	expectBaselineRevision   string
	expectPreTrancheSHA256   string
	expectPreTrancheRevision string
	requireClean             bool
	bootstrap                int
	seed                     int64
	format                   string
	comparisonMode           string
	outputPath               string
	overwrite                bool
}

type sampleRecord struct {
	SchemaVersion        int    `json:"schema_version"`
	Mode                 string `json:"mode"`
	Measurement          string `json:"measurement"`
	Preset               string `json:"preset"`
	Execution            string `json:"execution"`
	ContextCheckInterval int    `json:"context_check_interval"`
	ElapsedNS            int64  `json:"elapsed_ns"`
	SampleRun            int    `json:"sample_run"`
	SampleSequence       int    `json:"sample_sequence"`
	CollectionID         string `json:"collection_id"`
	Implementation       string `json:"implementation"`
	HeapBefore           uint64 `json:"heap_before"`
	HeapRetained         uint64 `json:"heap_retained"`
	HeapDelta            int64  `json:"heap_delta"`
	TotalAllocDelta      uint64 `json:"total_alloc_delta"`
	MallocsDelta         uint64 `json:"mallocs_delta"`
	GoVersion            string `json:"go_version"`
	GOOS                 string `json:"goos"`
	GOARCH               string `json:"goarch"`
	Revision             string `json:"revision"`
	RevisionModified     bool   `json:"revision_modified"`
	BinarySHA256         string `json:"binary_sha256"`
	InputSHA256          string `json:"input_sha256"`
	CodecSHA256          string `json:"codec_sha256"`
	WorkloadSHA256       string `json:"workload_sha256"`
	RuntimeVersion       string `json:"runtime_version"`
	Workdir              string `json:"workdir"`
	Oracle               struct {
		Digest       string `json:"digest"`
		Areas        int    `json:"areas"`
		Rooms        int    `json:"rooms"`
		Exits        int    `json:"exits"`
		Tables       int    `json:"tables"`
		Entries      int    `json:"entries"`
		EncodedBytes int    `json:"encoded_bytes"`
	} `json:"oracle"`
}

type workloadSignature struct {
	SchemaVersion        int    `json:"schema_version"`
	Mode                 string `json:"mode"`
	Measurement          string `json:"measurement"`
	Preset               string `json:"preset"`
	Execution            string `json:"execution"`
	ContextCheckInterval int    `json:"context_check_interval"`
	GoVersion            string `json:"go_version"`
	GOOS                 string `json:"goos"`
	GOARCH               string `json:"goarch"`
	InputSHA256          string `json:"input_sha256"`
	CodecSHA256          string `json:"codec_sha256"`
	WorkloadSHA256       string `json:"workload_sha256"`
	RuntimeVersion       string `json:"runtime_version"`
	OracleDigest         string `json:"oracle_digest"`
	OracleAreas          int    `json:"oracle_areas"`
	OracleRooms          int    `json:"oracle_rooms"`
	OracleExits          int    `json:"oracle_exits"`
	OracleTables         int    `json:"oracle_tables"`
	OracleEntries        int    `json:"oracle_entries"`
	OracleEncodedBytes   int    `json:"oracle_encoded_bytes"`
}

type distribution struct {
	Median float64 `json:"median"`
	MAD    float64 `json:"mad"`
	P95    float64 `json:"p95"`
	Mean   float64 `json:"mean"`
	CV     float64 `json:"cv"`
}

type sampleSummary struct {
	Samples          int          `json:"samples"`
	Revision         string       `json:"revision"`
	RevisionModified bool         `json:"revision_modified"`
	BinarySHA256     string       `json:"binary_sha256"`
	Elapsed          distribution `json:"elapsed_ns"`
	TotalAlloc       distribution `json:"total_alloc_bytes"`
	Mallocs          distribution `json:"mallocs"`
	HeapDelta        distribution `json:"heap_delta_bytes"`
}

type comparisonReport struct {
	SchemaVersion                 int               `json:"schema_version"`
	CollectionID                  string            `json:"collection_id,omitempty"`
	ComparisonMode                string            `json:"comparison_mode"`
	Signature                     workloadSignature `json:"workload"`
	CandidateRuntimeVersion       string            `json:"candidate_runtime_version"`
	CandidateExecution            string            `json:"candidate_execution"`
	CandidateContextCheckInterval int               `json:"candidate_context_check_interval"`
	Baseline                      sampleSummary     `json:"baseline"`
	Candidate                     sampleSummary     `json:"candidate"`
	PreTranche                    *sampleSummary    `json:"pre_tranche,omitempty"`
	ElapsedReduction              float64           `json:"elapsed_reduction"`
	ElapsedReductionLow           float64           `json:"elapsed_reduction_ci_low"`
	ElapsedReductionHigh          float64           `json:"elapsed_reduction_ci_high"`
	TotalAllocReduction           float64           `json:"total_alloc_reduction"`
	TotalAllocRatio               *float64          `json:"total_alloc_ratio"`
	MallocReduction               float64           `json:"malloc_reduction"`
	MallocRatio                   *float64          `json:"malloc_ratio"`
	ExcessMallocRemoval           *float64          `json:"excess_malloc_removal,omitempty"`
	HeapDeltaReduction            float64           `json:"heap_delta_reduction"`
	Qualification                 bool              `json:"qualification"`
	Policy                        comparisonPolicy  `json:"policy"`
	Gates                         []gateResult      `json:"gates"`
}

type comparisonPolicy struct {
	MinSamples                 int    `json:"min_samples"`
	Bootstrap                  int    `json:"bootstrap"`
	Seed                       int64  `json:"seed"`
	StrictEvidence             bool   `json:"strict_evidence"`
	CollectionRequired         bool   `json:"collection_required"`
	RequiredMeasurement        string `json:"required_measurement"`
	ExpectedBaselineSHA256     string `json:"expected_baseline_sha256"`
	ExpectedBaselineRevision   string `json:"expected_baseline_revision"`
	ExpectedPreTrancheSHA256   string `json:"expected_pre_tranche_sha256"`
	ExpectedPreTrancheRevision string `json:"expected_pre_tranche_revision"`
	RequireClean               bool   `json:"require_clean"`
	OutputPath                 string `json:"output_path"`
	Overwrite                  bool   `json:"overwrite"`
	Format                     string `json:"format"`
}

type gateResult struct {
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Relation  string   `json:"relation,omitempty"`
	Threshold float64  `json:"threshold"`
	Actual    *float64 `json:"actual,omitempty"`
	Passed    *bool    `json:"passed,omitempty"`
	Failure   string   `json:"failure,omitempty"`
}

func main() {
	var opts options
	flag.StringVar(&opts.baselinePath, "baseline", "", "CBOR worker baseline JSONL")
	flag.StringVar(&opts.candidatePath, "candidate", "", "CBOR worker candidate JSONL")
	flag.StringVar(&opts.preTranchePath, "pre-tranche", "", "optional pre-tranche JSONL collected in the same rounds")
	flag.IntVar(&opts.minSamples, "min-samples", 15, "minimum samples required in each set")
	flag.Float64Var(&opts.maxCV, "max-cv", 0, "maximum timing coefficient of variation; zero disables")
	flag.Float64Var(&opts.minSpeedup, "min-speedup", 0, "legacy minimum median elapsed-time reduction; zero disables")
	flag.Float64Var(&opts.maxElapsedRatio, "max-elapsed-ratio", 0, "maximum candidate/baseline median elapsed-time ratio; zero disables")
	flag.Float64Var(&opts.maxTotalAllocRatio, "max-total-alloc-ratio", 0, "maximum candidate/baseline allocated-byte ratio; zero disables")
	flag.Float64Var(&opts.maxMallocRatio, "max-malloc-ratio", 0, "maximum candidate/baseline malloc-count ratio; zero disables")
	flag.Float64Var(&opts.minExcessMallocRemoval, "min-excess-malloc-removal", 0, "required removal of pre-tranche malloc excess over baseline; zero disables")
	flag.StringVar(&opts.expectBaselineSHA256, "expect-baseline-sha256", "", "required SHA-256 of the baseline evidence binary")
	flag.StringVar(&opts.expectBaselineRevision, "expect-baseline-revision", "", "required VCS revision of the baseline evidence")
	flag.StringVar(&opts.expectPreTrancheSHA256, "expect-pre-tranche-sha256", "", "required SHA-256 of the pre-tranche evidence binary")
	flag.StringVar(&opts.expectPreTrancheRevision, "expect-pre-tranche-revision", "", "required VCS revision of the pre-tranche evidence")
	flag.BoolVar(&opts.requireClean, "require-clean", false, "require every evidence set to report a clean VCS build")
	flag.IntVar(&opts.bootstrap, "bootstrap", 10_000, "deterministic bootstrap resamples")
	flag.Int64Var(&opts.seed, "seed", 1, "bootstrap random seed")
	flag.StringVar(&opts.format, "format", "markdown", "output format: markdown or json")
	flag.StringVar(&opts.comparisonMode, "comparison-mode", "implementations", "comparison policy: implementations or context-tax")
	flag.StringVar(&opts.outputPath, "output", "", "exclusive report archive path; required for qualification")
	flag.BoolVar(&opts.overwrite, "overwrite", false, "replace an existing report archive")
	flag.Parse()

	if exitCode := runComparison(opts, os.Stdout, os.Stderr); exitCode != 0 {
		os.Exit(exitCode)
	}
}

func runComparison(opts options, stdout, stderr io.Writer) int {
	report, err := compare(opts)
	if err != nil {
		fmt.Fprintln(stderr, "cbor-compare:", err)
		return 1
	}
	opts.outputPath = report.Policy.OutputPath
	opts.overwrite = report.Policy.Overwrite
	opts.format = report.Policy.Format
	gateErr := evaluate(&report, opts)
	if err := emitReport(opts, stdout, report); err != nil {
		fmt.Fprintln(stderr, "cbor-compare:", err)
		return 1
	}
	if gateErr != nil {
		fmt.Fprintln(stderr, "cbor-compare:", gateErr)
		return 2
	}
	return 0
}

func compare(opts options) (comparisonReport, error) {
	if opts.baselinePath == "" || opts.candidatePath == "" {
		return comparisonReport{}, errors.New("-baseline and -candidate are required")
	}
	if opts.minSamples < 1 {
		return comparisonReport{}, errors.New("-min-samples must be at least 1")
	}
	if opts.bootstrap <= 0 {
		opts.bootstrap = 10_000
	}
	if err := validateGateOptions(&opts); err != nil {
		return comparisonReport{}, err
	}
	comparisonMode := opts.comparisonMode
	if comparisonMode == "" {
		comparisonMode = "implementations"
	}
	qualification := gatesEnabled(opts)
	collectionRequired := qualification || comparisonMode == "context-tax" || opts.preTranchePath != ""
	strictEvidence := collectionRequired
	baseline, err := readSampleSetPolicy(opts.baselinePath, opts.minSamples, strictEvidence, "baseline")
	if err != nil {
		return comparisonReport{}, fmt.Errorf("baseline: %w", err)
	}
	candidate, err := readSampleSetPolicy(opts.candidatePath, opts.minSamples, strictEvidence, "candidate")
	if err != nil {
		return comparisonReport{}, fmt.Errorf("candidate: %w", err)
	}
	if err := validateSignaturePair(comparisonMode, baseline.signature, candidate.signature); err != nil {
		return comparisonReport{}, err
	}
	if qualification && baseline.signature.Measurement != "timing" {
		return comparisonReport{}, fmt.Errorf(
			"qualification gates require timing evidence, got measurement %q",
			baseline.signature.Measurement,
		)
	}
	if err := validateEvidenceIdentities(opts, comparisonMode, baseline, candidate); err != nil {
		return comparisonReport{}, err
	}
	pairedBaseline, pairedCandidate, err := pairedElapsedSamples(baseline.records, candidate.records)
	if err != nil {
		return comparisonReport{}, err
	}

	report := comparisonReport{
		SchemaVersion: 2, CollectionID: baseline.collectionID, ComparisonMode: comparisonMode,
		Signature:                     baseline.signature,
		CandidateRuntimeVersion:       candidate.signature.RuntimeVersion,
		CandidateExecution:            candidate.signature.Execution,
		CandidateContextCheckInterval: candidate.signature.ContextCheckInterval,
		Baseline:                      summarize(baseline.records),
		Candidate:                     summarize(candidate.records),
		Qualification:                 qualification,
		Policy:                        policyOf(opts, strictEvidence, collectionRequired),
	}
	if report.Baseline.Elapsed.Median <= 0 {
		return comparisonReport{}, errors.New("baseline median elapsed time must be positive")
	}
	report.ElapsedReduction = reduction(report.Baseline.Elapsed.Median, report.Candidate.Elapsed.Median)
	report.TotalAllocReduction = reduction(report.Baseline.TotalAlloc.Median, report.Candidate.TotalAlloc.Median)
	report.TotalAllocRatio = metricRatio(report.Baseline.TotalAlloc.Median, report.Candidate.TotalAlloc.Median)
	report.MallocReduction = reduction(report.Baseline.Mallocs.Median, report.Candidate.Mallocs.Median)
	report.MallocRatio = metricRatio(report.Baseline.Mallocs.Median, report.Candidate.Mallocs.Median)
	report.HeapDeltaReduction = reduction(report.Baseline.HeapDelta.Median, report.Candidate.HeapDelta.Median)
	report.ElapsedReductionLow, report.ElapsedReductionHigh = bootstrapPairedReduction(
		pairedBaseline, pairedCandidate, opts.bootstrap, opts.seed,
	)
	if opts.preTranchePath != "" {
		preTranche, err := readSampleSetPolicy(opts.preTranchePath, opts.minSamples, true, "pre-tranche")
		if err != nil {
			return comparisonReport{}, fmt.Errorf("pre-tranche: %w", err)
		}
		if err := validateSignaturePair("implementations", baseline.signature, preTranche.signature); err != nil {
			return comparisonReport{}, fmt.Errorf("pre-tranche: %w", err)
		}
		if err := validatePreTrancheIdentity(opts, baseline, candidate, preTranche); err != nil {
			return comparisonReport{}, err
		}
		if err := validateCollectionSession([]namedSampleSet{
			{name: "baseline", set: baseline},
			{name: "candidate", set: candidate},
			{name: "pre-tranche", set: preTranche},
		}, true); err != nil {
			return comparisonReport{}, fmt.Errorf("three-way pairing: %w", err)
		}
		summary := summarize(preTranche.records)
		report.PreTranche = &summary
		excess := summary.Mallocs.Median - report.Baseline.Mallocs.Median
		if excess <= 0 {
			return comparisonReport{}, fmt.Errorf(
				"pre-tranche malloc median %.0f has no positive excess over baseline %.0f",
				summary.Mallocs.Median, report.Baseline.Mallocs.Median,
			)
		}
		removal := (summary.Mallocs.Median - report.Candidate.Mallocs.Median) / excess
		report.ExcessMallocRemoval = &removal
	} else if err := validateCollectionSession([]namedSampleSet{
		{name: "baseline", set: baseline},
		{name: "candidate", set: candidate},
	}, collectionRequired); err != nil {
		return comparisonReport{}, fmt.Errorf("pairing: %w", err)
	}
	return report, nil
}

func validateGateOptions(opts *options) error {
	for name, value := range map[string]float64{
		"-max-cv":                    opts.maxCV,
		"-min-speedup":               opts.minSpeedup,
		"-max-elapsed-ratio":         opts.maxElapsedRatio,
		"-max-total-alloc-ratio":     opts.maxTotalAllocRatio,
		"-max-malloc-ratio":          opts.maxMallocRatio,
		"-min-excess-malloc-removal": opts.minExcessMallocRemoval,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("%s must be a finite nonnegative number", name)
		}
	}
	if opts.minExcessMallocRemoval > 1 {
		return errors.New("-min-excess-malloc-removal cannot exceed 1")
	}
	if opts.minSpeedup > 1 {
		return errors.New("-min-speedup cannot exceed 1")
	}
	if opts.format == "" {
		opts.format = "markdown"
	}
	if opts.format != "markdown" && opts.format != "json" {
		return fmt.Errorf("invalid -format %q: expected markdown or json", opts.format)
	}
	if opts.overwrite && opts.outputPath == "" {
		return errors.New("-overwrite requires -output")
	}
	comparisonMode := opts.comparisonMode
	if comparisonMode == "" {
		comparisonMode = "implementations"
	}
	if comparisonMode == "context-tax" && opts.preTranchePath != "" {
		return errors.New("context-tax comparison does not accept -pre-tranche")
	}
	if opts.minExcessMallocRemoval > 0 && opts.preTranchePath == "" {
		return errors.New("-min-excess-malloc-removal requires -pre-tranche")
	}
	if opts.preTranchePath == "" && (opts.expectPreTrancheSHA256 != "" || opts.expectPreTrancheRevision != "") {
		return errors.New("pre-tranche identity expectations require -pre-tranche")
	}
	if err := normalizeExpectedHash(&opts.expectBaselineSHA256); err != nil {
		return fmt.Errorf("-expect-baseline-sha256: %w", err)
	}
	if err := normalizeExpectedHash(&opts.expectPreTrancheSHA256); err != nil {
		return fmt.Errorf("-expect-pre-tranche-sha256: %w", err)
	}
	if err := validateExpectedRevision(opts.expectBaselineRevision); err != nil {
		return fmt.Errorf("-expect-baseline-revision: %w", err)
	}
	if err := validateExpectedRevision(opts.expectPreTrancheRevision); err != nil {
		return fmt.Errorf("-expect-pre-tranche-revision: %w", err)
	}
	if gatesEnabled(*opts) {
		if opts.minSamples < minimumQualificationSamples {
			return fmt.Errorf("qualification gates require -min-samples of at least %d", minimumQualificationSamples)
		}
		var missing []string
		if opts.expectBaselineSHA256 == "" {
			missing = append(missing, "-expect-baseline-sha256")
		}
		if opts.expectBaselineRevision == "" {
			missing = append(missing, "-expect-baseline-revision")
		}
		if !opts.requireClean {
			missing = append(missing, "-require-clean")
		}
		if opts.outputPath == "" {
			missing = append(missing, "-output")
		}
		if len(missing) > 0 {
			return fmt.Errorf("qualification gates require %s", strings.Join(missing, ", "))
		}
	}
	if opts.outputPath != "" {
		absoluteOutput, err := filepath.Abs(opts.outputPath)
		if err != nil {
			return fmt.Errorf("resolve -output: %w", err)
		}
		for label, inputPath := range map[string]string{
			"-baseline":    opts.baselinePath,
			"-candidate":   opts.candidatePath,
			"-pre-tranche": opts.preTranchePath,
		} {
			if inputPath == "" {
				continue
			}
			same, err := sameFilePath(absoluteOutput, inputPath)
			if err != nil {
				return fmt.Errorf("compare -output with %s: %w", label, err)
			}
			if same {
				return fmt.Errorf("-output must differ from %s evidence", label)
			}
		}
		opts.outputPath = absoluteOutput
	}
	if opts.minExcessMallocRemoval > 0 {
		var missing []string
		if opts.expectPreTrancheSHA256 == "" {
			missing = append(missing, "-expect-pre-tranche-sha256")
		}
		if opts.expectPreTrancheRevision == "" {
			missing = append(missing, "-expect-pre-tranche-revision")
		}
		if len(missing) > 0 {
			return fmt.Errorf("-min-excess-malloc-removal requires %s", strings.Join(missing, ", "))
		}
	}
	return nil
}

func policyOf(opts options, strictEvidence, collectionRequired bool) comparisonPolicy {
	requiredMeasurement := "any"
	if gatesEnabled(opts) {
		requiredMeasurement = "timing"
	}
	return comparisonPolicy{
		MinSamples:                 opts.minSamples,
		Bootstrap:                  opts.bootstrap,
		Seed:                       opts.seed,
		StrictEvidence:             strictEvidence,
		CollectionRequired:         collectionRequired,
		RequiredMeasurement:        requiredMeasurement,
		ExpectedBaselineSHA256:     opts.expectBaselineSHA256,
		ExpectedBaselineRevision:   opts.expectBaselineRevision,
		ExpectedPreTrancheSHA256:   opts.expectPreTrancheSHA256,
		ExpectedPreTrancheRevision: opts.expectPreTrancheRevision,
		RequireClean:               opts.requireClean,
		OutputPath:                 opts.outputPath,
		Overwrite:                  opts.overwrite,
		Format:                     opts.format,
	}
}

func sameFilePath(left, right string) (bool, error) {
	absoluteLeft, err := filepath.Abs(left)
	if err != nil {
		return false, err
	}
	absoluteRight, err := filepath.Abs(right)
	if err != nil {
		return false, err
	}
	if filepath.Clean(absoluteLeft) == filepath.Clean(absoluteRight) {
		return true, nil
	}
	leftInfo, leftErr := os.Stat(absoluteLeft)
	rightInfo, rightErr := os.Stat(absoluteRight)
	if leftErr == nil && rightErr == nil {
		return os.SameFile(leftInfo, rightInfo), nil
	}
	if leftErr != nil && !os.IsNotExist(leftErr) {
		return false, leftErr
	}
	if rightErr != nil && !os.IsNotExist(rightErr) {
		return false, rightErr
	}
	return false, nil
}

func gatesEnabled(opts options) bool {
	return opts.maxCV > 0 ||
		opts.minSpeedup > 0 ||
		opts.maxElapsedRatio > 0 ||
		opts.maxTotalAllocRatio > 0 ||
		opts.maxMallocRatio > 0 ||
		opts.minExcessMallocRemoval > 0
}

func validateEvidenceIdentities(opts options, comparisonMode string, baseline, candidate sampleSet) error {
	if opts.expectBaselineSHA256 != "" && baseline.identity.BinarySHA256 != opts.expectBaselineSHA256 {
		return fmt.Errorf(
			"baseline SHA-256 %s does not match required %s",
			baseline.identity.BinarySHA256, opts.expectBaselineSHA256,
		)
	}
	if opts.expectBaselineRevision != "" && baseline.identity.Revision != opts.expectBaselineRevision {
		return fmt.Errorf(
			"baseline revision %q does not match required %q",
			baseline.identity.Revision, opts.expectBaselineRevision,
		)
	}
	if opts.requireClean {
		if err := validateCleanIdentity("baseline", baseline.identity); err != nil {
			return err
		}
		if err := validateCleanIdentity("candidate", candidate.identity); err != nil {
			return err
		}
	}
	switch comparisonMode {
	case "implementations":
		if baseline.identity.BinarySHA256 == candidate.identity.BinarySHA256 {
			return fmt.Errorf(
				"baseline and candidate have identical binary SHA-256 %s",
				baseline.identity.BinarySHA256,
			)
		}
	case "context-tax":
		if baseline.identity.BinarySHA256 != candidate.identity.BinarySHA256 {
			return fmt.Errorf(
				"context-tax baseline and candidate binaries differ: %s != %s",
				baseline.identity.BinarySHA256, candidate.identity.BinarySHA256,
			)
		}
		if baseline.identity.Revision != candidate.identity.Revision ||
			baseline.identity.RevisionModified != candidate.identity.RevisionModified {
			return fmt.Errorf(
				"context-tax baseline and candidate archived build identities differ: revision=%q/%q modified=%t/%t",
				baseline.identity.Revision, candidate.identity.Revision,
				baseline.identity.RevisionModified, candidate.identity.RevisionModified,
			)
		}
	}
	return nil
}

func validatePreTrancheIdentity(opts options, baseline, candidate, preTranche sampleSet) error {
	if opts.expectPreTrancheSHA256 != "" && preTranche.identity.BinarySHA256 != opts.expectPreTrancheSHA256 {
		return fmt.Errorf(
			"pre-tranche SHA-256 %s does not match required %s",
			preTranche.identity.BinarySHA256, opts.expectPreTrancheSHA256,
		)
	}
	if opts.expectPreTrancheRevision != "" && preTranche.identity.Revision != opts.expectPreTrancheRevision {
		return fmt.Errorf(
			"pre-tranche revision %q does not match required %q",
			preTranche.identity.Revision, opts.expectPreTrancheRevision,
		)
	}
	if opts.requireClean {
		if err := validateCleanIdentity("pre-tranche", preTranche.identity); err != nil {
			return err
		}
	}
	sets := []struct {
		name string
		set  sampleSet
	}{
		{name: "baseline", set: baseline},
		{name: "candidate", set: candidate},
		{name: "pre-tranche", set: preTranche},
	}
	for left := range sets {
		for right := left + 1; right < len(sets); right++ {
			if sets[left].set.identity.BinarySHA256 == sets[right].set.identity.BinarySHA256 {
				return fmt.Errorf(
					"%s and %s have identical binary SHA-256 %s",
					sets[left].name, sets[right].name,
					sets[left].set.identity.BinarySHA256,
				)
			}
		}
	}
	return nil
}

func validateCleanIdentity(label string, identity implementationSignature) error {
	if identity.Revision == "" {
		return fmt.Errorf("%s did not report a VCS revision", label)
	}
	if identity.RevisionModified {
		return fmt.Errorf("%s revision %s is modified", label, identity.Revision)
	}
	return nil
}

func normalizeExpectedHash(value *string) error {
	if *value == "" {
		return nil
	}
	*value = strings.ToLower(*value)
	if len(*value) != sha256.Size*2 {
		return fmt.Errorf("must contain %d hexadecimal characters", sha256.Size*2)
	}
	if _, err := hex.DecodeString(*value); err != nil {
		return fmt.Errorf("invalid SHA-256: %w", err)
	}
	return nil
}

func validateExpectedRevision(value string) error {
	if value == "" {
		return nil
	}
	if len(value) != 40 {
		return errors.New("must contain 40 hexadecimal characters")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("invalid revision: %w", err)
	}
	return nil
}

func validateSignaturePair(comparisonMode string, baseline, candidate workloadSignature) error {
	switch comparisonMode {
	case "implementations":
		normalized := candidate
		normalized.RuntimeVersion = baseline.RuntimeVersion
		if baseline != normalized {
			return fmt.Errorf("workload signature mismatch: baseline=%+v candidate=%+v", baseline, candidate)
		}
		return nil
	case "context-tax":
		if baseline.Execution != "raw" || baseline.ContextCheckInterval != 0 {
			return fmt.Errorf("context-tax baseline must be raw with interval 0, got execution=%s interval=%d", baseline.Execution, baseline.ContextCheckInterval)
		}
		if candidate.Execution != "guarded" || candidate.ContextCheckInterval <= 1 {
			return fmt.Errorf("context-tax candidate must be guarded with interval greater than 1, got execution=%s interval=%d", candidate.Execution, candidate.ContextCheckInterval)
		}
		normalized := candidate
		normalized.Execution = baseline.Execution
		normalized.ContextCheckInterval = baseline.ContextCheckInterval
		if baseline != normalized {
			return fmt.Errorf("workload signature mismatch outside context policy: baseline=%+v candidate=%+v", baseline, candidate)
		}
		return nil
	default:
		return fmt.Errorf("invalid comparison mode %q: expected implementations or context-tax", comparisonMode)
	}
}

type sampleSet struct {
	signature    workloadSignature
	identity     implementationSignature
	collectionID string
	records      []sampleRecord
}

type namedSampleSet struct {
	name string
	set  sampleSet
}

type implementationSignature struct {
	BinarySHA256     string
	Revision         string
	RevisionModified bool
}

func readSampleSet(path string, minSamples int) (sampleSet, error) {
	return readSampleSetPolicy(path, minSamples, false, "")
}

func readSampleSetPolicy(path string, minSamples int, strict bool, expectedImplementation string) (sampleSet, error) {
	file, err := os.Open(path)
	if err != nil {
		return sampleSet{}, err
	}
	defer file.Close()

	var set sampleSet
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		record, err := decodeSampleRecord(scanner.Text(), strict)
		if err != nil {
			return sampleSet{}, fmt.Errorf("line %d: %w", line, err)
		}
		if record.SchemaVersion != 2 {
			return sampleSet{}, fmt.Errorf("line %d: schema version %d, want 2", line, record.SchemaVersion)
		}
		if record.InputSHA256 == "" || record.CodecSHA256 == "" || record.WorkloadSHA256 == "" || record.BinarySHA256 == "" || record.Oracle.Digest == "" {
			return sampleSet{}, fmt.Errorf("line %d: missing reproducibility hash", line)
		}
		if strict {
			if err := validateStrictSampleRecord(record, expectedImplementation); err != nil {
				return sampleSet{}, fmt.Errorf("line %d: %w", line, err)
			}
		}
		signature := signatureOf(record)
		identity := identityOf(record)
		if len(set.records) == 0 {
			set.signature = signature
			set.identity = identity
			set.collectionID = record.CollectionID
		} else if set.signature != signature {
			return sampleSet{}, fmt.Errorf("line %d changes workload signature from %+v to %+v", line, set.signature, signature)
		} else if set.identity != identity {
			return sampleSet{}, fmt.Errorf("line %d changes implementation identity from %+v to %+v", line, set.identity, identity)
		} else if set.collectionID != record.CollectionID {
			return sampleSet{}, fmt.Errorf("line %d changes collection ID from %q to %q", line, set.collectionID, record.CollectionID)
		}
		set.records = append(set.records, record)
	}
	if err := scanner.Err(); err != nil {
		return sampleSet{}, err
	}
	if len(set.records) < minSamples {
		return sampleSet{}, fmt.Errorf("found %d samples, require at least %d", len(set.records), minSamples)
	}
	return set, nil
}

func decodeSampleRecord(line string, strict bool) (sampleRecord, error) {
	var record sampleRecord
	if !strict {
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return sampleRecord{}, err
		}
		return record, nil
	}
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return sampleRecord{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return sampleRecord{}, errors.New("record contains more than one JSON value")
		}
		return sampleRecord{}, fmt.Errorf("trailing JSON: %w", err)
	}
	return record, nil
}

func validateStrictSampleRecord(record sampleRecord, expectedImplementation string) error {
	if record.Implementation != expectedImplementation {
		return fmt.Errorf("implementation %q, want %q", record.Implementation, expectedImplementation)
	}
	if record.Mode != "load" && record.Mode != "save" {
		return fmt.Errorf("invalid mode %q", record.Mode)
	}
	if record.Measurement != "timing" && record.Measurement != "retained" {
		return fmt.Errorf("invalid measurement %q", record.Measurement)
	}
	if record.Preset != "small" && record.Preset != "large" {
		return fmt.Errorf("invalid preset %q", record.Preset)
	}
	if record.Execution != "raw" && record.Execution != "guarded" {
		return fmt.Errorf("invalid execution %q", record.Execution)
	}
	if record.Execution == "raw" && record.ContextCheckInterval != 0 {
		return fmt.Errorf("raw execution has context interval %d", record.ContextCheckInterval)
	}
	if record.ContextCheckInterval < 0 {
		return fmt.Errorf("negative context interval %d", record.ContextCheckInterval)
	}
	if record.ElapsedNS <= 0 {
		return fmt.Errorf("elapsed_ns must be positive, got %d", record.ElapsedNS)
	}
	if record.SampleRun < 1 || record.SampleSequence < 1 {
		return fmt.Errorf("sample_run and sample_sequence must be positive, got %d/%d", record.SampleRun, record.SampleSequence)
	}
	if err := validateLowerHex("collection_id", record.CollectionID, 16); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"binary_sha256":   record.BinarySHA256,
		"input_sha256":    record.InputSHA256,
		"codec_sha256":    record.CodecSHA256,
		"workload_sha256": record.WorkloadSHA256,
		"oracle.digest":   record.Oracle.Digest,
	} {
		if err := validateLowerHex(name, value, sha256.Size); err != nil {
			return err
		}
	}
	if record.Revision != "" {
		if err := validateLowerHex("revision", record.Revision, 20); err != nil {
			return err
		}
	}
	if record.GoVersion == "" || record.GOOS == "" || record.GOARCH == "" || record.RuntimeVersion == "" {
		return errors.New("missing runtime identity field")
	}
	if record.Oracle.Areas <= 0 || record.Oracle.Rooms <= 0 || record.Oracle.Exits <= 0 ||
		record.Oracle.Tables <= 0 || record.Oracle.Entries <= 0 || record.Oracle.EncodedBytes <= 0 {
		return errors.New("invalid oracle counts")
	}
	return nil
}

func validateLowerHex(name, value string, byteLength int) error {
	if len(value) != byteLength*2 {
		return fmt.Errorf("%s must contain %d lowercase hexadecimal characters", name, byteLength*2)
	}
	if value != strings.ToLower(value) {
		return fmt.Errorf("%s must use lowercase hexadecimal", name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("invalid %s: %w", name, err)
	}
	return nil
}

func validateCollectionSession(sets []namedSampleSet, requireCollection bool) error {
	if len(sets) < 2 {
		return errors.New("collection validation requires at least two sample sets")
	}
	collectionID := sets[0].set.collectionID
	if requireCollection && collectionID == "" {
		return errors.New("comparison requires collection metadata")
	}
	for _, current := range sets[1:] {
		if current.set.collectionID != collectionID {
			return fmt.Errorf(
				"collection IDs differ: %s=%q %s=%q",
				sets[0].name, collectionID, current.name, current.set.collectionID,
			)
		}
	}
	// Legacy two-way evidence predates collection IDs and keeps its established
	// sample_run pairing behavior. Every newly collected session is stricter.
	if collectionID == "" {
		return nil
	}
	runs := len(sets[0].set.records)
	if runs == 0 {
		return errors.New("collection contains no samples")
	}
	byRun := make([]map[int]int, len(sets))
	sequences := make(map[int]string, runs*len(sets))
	for index, current := range sets {
		if len(current.set.records) != runs {
			return fmt.Errorf("collection sample counts differ: %s has %d, want %d", current.name, len(current.set.records), runs)
		}
		byRun[index] = make(map[int]int, runs)
		for _, record := range current.set.records {
			if record.SampleRun < 1 {
				return fmt.Errorf("%s record is missing a positive sample run", current.name)
			}
			if record.SampleSequence < 1 {
				return fmt.Errorf("%s run %d is missing a positive sample sequence", current.name, record.SampleRun)
			}
			if _, duplicate := byRun[index][record.SampleRun]; duplicate {
				return fmt.Errorf("%s repeats sample run %d", current.name, record.SampleRun)
			}
			byRun[index][record.SampleRun] = record.SampleSequence
			if previous, duplicate := sequences[record.SampleSequence]; duplicate {
				return fmt.Errorf("sample sequence %d is shared by %s and %s", record.SampleSequence, previous, current.name)
			}
			sequences[record.SampleSequence] = current.name
		}
	}
	width := len(sets)
	for run := 1; run <= runs; run++ {
		firstSequence := (run-1)*width + 1
		seen := make(map[int]bool, width)
		for index, current := range sets {
			sequence, found := byRun[index][run]
			if !found {
				return fmt.Errorf("%s is missing sample run %d", current.name, run)
			}
			if sequence < firstSequence || sequence >= firstSequence+width {
				return fmt.Errorf(
					"%s run %d has sequence %d outside expected range %d..%d",
					current.name, run, sequence, firstSequence, firstSequence+width-1,
				)
			}
			seen[sequence] = true
		}
		if len(seen) != width {
			return fmt.Errorf("run %d does not cover all %d randomized sequence positions", run, width)
		}
	}
	if len(sequences) != runs*width {
		return fmt.Errorf("collection has %d unique sequences, want %d", len(sequences), runs*width)
	}
	return nil
}

func identityOf(record sampleRecord) implementationSignature {
	return implementationSignature{
		BinarySHA256: record.BinarySHA256, Revision: record.Revision, RevisionModified: record.RevisionModified,
	}
}

func signatureOf(record sampleRecord) workloadSignature {
	return workloadSignature{
		SchemaVersion:        record.SchemaVersion,
		Mode:                 record.Mode,
		Measurement:          record.Measurement,
		Preset:               record.Preset,
		Execution:            record.Execution,
		ContextCheckInterval: record.ContextCheckInterval,
		GoVersion:            record.GoVersion,
		GOOS:                 record.GOOS,
		GOARCH:               record.GOARCH,
		InputSHA256:          record.InputSHA256,
		CodecSHA256:          record.CodecSHA256,
		WorkloadSHA256:       record.WorkloadSHA256,
		RuntimeVersion:       record.RuntimeVersion,
		OracleDigest:         record.Oracle.Digest,
		OracleAreas:          record.Oracle.Areas,
		OracleRooms:          record.Oracle.Rooms,
		OracleExits:          record.Oracle.Exits,
		OracleTables:         record.Oracle.Tables,
		OracleEntries:        record.Oracle.Entries,
		OracleEncodedBytes:   record.Oracle.EncodedBytes,
	}
}

func summarize(records []sampleRecord) sampleSummary {
	elapsed := make([]float64, len(records))
	allocated := make([]float64, len(records))
	mallocs := make([]float64, len(records))
	heap := make([]float64, len(records))
	for index, record := range records {
		elapsed[index] = float64(record.ElapsedNS)
		allocated[index] = float64(record.TotalAllocDelta)
		mallocs[index] = float64(record.MallocsDelta)
		heap[index] = float64(record.HeapDelta)
	}
	return sampleSummary{
		Samples: len(records), Revision: records[0].Revision,
		RevisionModified: records[0].RevisionModified, BinarySHA256: records[0].BinarySHA256,
		Elapsed: describe(elapsed), TotalAlloc: describe(allocated),
		Mallocs: describe(mallocs), HeapDelta: describe(heap),
	}
}

func describe(values []float64) distribution {
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	mean := 0.0
	for _, value := range values {
		mean += value
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, value := range values {
		difference := value - mean
		variance += difference * difference
	}
	variance /= float64(len(values))
	median := quantile(ordered, 0.5)
	deviations := make([]float64, len(values))
	for index, value := range values {
		deviations[index] = math.Abs(value - median)
	}
	sort.Float64s(deviations)
	cv := 0.0
	if mean != 0 {
		cv = math.Sqrt(variance) / math.Abs(mean)
	}
	return distribution{
		Median: median,
		MAD:    quantile(deviations, 0.5),
		P95:    quantile(ordered, 0.95),
		Mean:   mean,
		CV:     cv,
	}
}

func quantile(ordered []float64, probability float64) float64 {
	if len(ordered) == 1 {
		return ordered[0]
	}
	position := probability * float64(len(ordered)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return ordered[lower]
	}
	fraction := position - float64(lower)
	return ordered[lower]*(1-fraction) + ordered[upper]*fraction
}

func pairedElapsedSamples(baseline, candidate []sampleRecord) ([]float64, []float64, error) {
	baselineByRun := make(map[int]float64, len(baseline))
	candidateByRun := make(map[int]float64, len(candidate))
	for label, input := range map[string]struct {
		records []sampleRecord
		values  map[int]float64
	}{
		"baseline":  {records: baseline, values: baselineByRun},
		"candidate": {records: candidate, values: candidateByRun},
	} {
		for _, record := range input.records {
			if record.SampleRun < 1 {
				return nil, nil, fmt.Errorf("%s record is missing a positive paired sample run", label)
			}
			if _, duplicate := input.values[record.SampleRun]; duplicate {
				return nil, nil, fmt.Errorf("%s repeats paired sample run %d", label, record.SampleRun)
			}
			input.values[record.SampleRun] = float64(record.ElapsedNS)
		}
	}
	if len(baselineByRun) != len(candidateByRun) {
		return nil, nil, fmt.Errorf("paired sample runs differ: baseline has %d, candidate has %d", len(baselineByRun), len(candidateByRun))
	}
	runs := make([]int, 0, len(baselineByRun))
	for run := range baselineByRun {
		if _, found := candidateByRun[run]; !found {
			return nil, nil, fmt.Errorf("paired sample runs differ: candidate is missing run %d", run)
		}
		runs = append(runs, run)
	}
	sort.Ints(runs)
	pairedBaseline := make([]float64, len(runs))
	pairedCandidate := make([]float64, len(runs))
	for index, run := range runs {
		pairedBaseline[index] = baselineByRun[run]
		pairedCandidate[index] = candidateByRun[run]
	}
	return pairedBaseline, pairedCandidate, nil
}

func bootstrapPairedReduction(baseline, candidate []float64, iterations int, seed int64) (float64, float64) {
	random := rand.New(rand.NewSource(seed))
	reductions := make([]float64, iterations)
	baselineSample := make([]float64, len(baseline))
	candidateSample := make([]float64, len(candidate))
	for iteration := 0; iteration < iterations; iteration++ {
		for index := range baselineSample {
			pairedIndex := random.Intn(len(baseline))
			baselineSample[index] = baseline[pairedIndex]
			candidateSample[index] = candidate[pairedIndex]
		}
		sort.Float64s(baselineSample)
		sort.Float64s(candidateSample)
		reductions[iteration] = reduction(quantile(baselineSample, 0.5), quantile(candidateSample, 0.5))
	}
	sort.Float64s(reductions)
	return quantile(reductions, 0.025), quantile(reductions, 0.975)
}

func reduction(baseline, candidate float64) float64 {
	if baseline == 0 {
		return 0
	}
	return 1 - candidate/baseline
}

func metricRatio(baseline, candidate float64) *float64 {
	if baseline == 0 {
		if candidate == 0 {
			ratio := 0.0
			return &ratio
		}
		return nil
	}
	ratio := candidate / baseline
	return &ratio
}

func evaluate(report *comparisonReport, opts options) error {
	report.Qualification = gatesEnabled(opts)
	baselineCV := report.Baseline.Elapsed.CV
	candidateCV := report.Candidate.Elapsed.CV
	elapsedRatio := metricRatio(report.Baseline.Elapsed.Median, report.Candidate.Elapsed.Median)
	elapsedReduction := report.ElapsedReduction
	report.Gates = []gateResult{
		assessGate("baseline timing CV", opts.maxCV > 0, "<=", opts.maxCV, &baselineCV),
		assessGate("candidate timing CV", opts.maxCV > 0, "<=", opts.maxCV, &candidateCV),
		assessGate("median speedup", opts.minSpeedup > 0, ">=", opts.minSpeedup, &elapsedReduction),
		assessGate("elapsed ratio", opts.maxElapsedRatio > 0, "<=", opts.maxElapsedRatio, elapsedRatio),
		assessGate("allocated-byte ratio", opts.maxTotalAllocRatio > 0, "<=", opts.maxTotalAllocRatio, report.TotalAllocRatio),
		assessGate("malloc ratio", opts.maxMallocRatio > 0, "<=", opts.maxMallocRatio, report.MallocRatio),
		assessGate("excess malloc removal", opts.minExcessMallocRemoval > 0, ">=", opts.minExcessMallocRemoval, report.ExcessMallocRemoval),
	}
	var failures []string
	for _, gate := range report.Gates {
		if gate.Enabled && gate.Passed != nil && !*gate.Passed {
			failures = append(failures, gate.Failure)
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func assessGate(name string, enabled bool, relation string, threshold float64, actual *float64) gateResult {
	result := gateResult{
		Name: name, Enabled: enabled, Relation: relation,
		Threshold: threshold, Actual: actual,
	}
	if !enabled {
		return result
	}
	passed := false
	if actual != nil {
		switch relation {
		case "<=":
			passed = *actual <= threshold
		case ">=":
			passed = *actual >= threshold
		}
	}
	result.Passed = &passed
	if !passed {
		if actual == nil {
			result.Failure = fmt.Sprintf("%s is unavailable or unbounded", name)
		} else {
			result.Failure = fmt.Sprintf("%s %.4f violates required %s %.4f", name, *actual, relation, threshold)
		}
	}
	return result
}

func emitReport(opts options, stdout io.Writer, report comparisonReport) error {
	if opts.outputPath == "" {
		return emit(stdout, opts.format, report)
	}
	file, err := createReportFile(opts.outputPath, opts.overwrite)
	if err != nil {
		return fmt.Errorf("create report archive: %w", err)
	}
	if err := emit(file, opts.format, report); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync report archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close report archive: %w", err)
	}
	return nil
}

func createReportFile(path string, overwrite bool) (*os.File, error) {
	flags := os.O_WRONLY | os.O_CREATE
	if overwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		if os.IsExist(err) && !overwrite {
			return nil, fmt.Errorf("%s already exists; choose a new path or pass -overwrite", path)
		}
		return nil, err
	}
	return file, nil
}

func emit(output io.Writer, format string, report comparisonReport) error {
	switch format {
	case "json":
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	case "markdown":
		_, err := fmt.Fprintf(output,
			"| Metric | Baseline median | Candidate median | Reduction |\n|---|---:|---:|---:|\n"+
				"| Elapsed | %.3f ms | %.3f ms | %.2f%% (95%% bootstrap %.2f%%..%.2f%%) |\n"+
				"| Allocated | %.0f B | %.0f B | %.2f%% |\n"+
				"| Allocations | %.0f | %.0f | %.2f%% |\n"+
				"| Retained delta | %.0f B | %.0f B | %.2f%% |\n\n"+
				"Samples: %d baseline / %d candidate; timing CV: %.2f%% / %.2f%%.\n",
			report.Baseline.Elapsed.Median/1e6, report.Candidate.Elapsed.Median/1e6,
			report.ElapsedReduction*100, report.ElapsedReductionLow*100, report.ElapsedReductionHigh*100,
			report.Baseline.TotalAlloc.Median, report.Candidate.TotalAlloc.Median, report.TotalAllocReduction*100,
			report.Baseline.Mallocs.Median, report.Candidate.Mallocs.Median, report.MallocReduction*100,
			report.Baseline.HeapDelta.Median, report.Candidate.HeapDelta.Median, report.HeapDeltaReduction*100,
			report.Baseline.Samples, report.Candidate.Samples,
			report.Baseline.Elapsed.CV*100, report.Candidate.Elapsed.CV*100,
		)
		if err != nil {
			return err
		}
		if report.PreTranche != nil {
			_, err = fmt.Fprintf(output,
				"Pre-tranche median: %.3f ms, %.0f B, %.0f mallocs; excess malloc removal: %.2f%%.\n",
				report.PreTranche.Elapsed.Median/1e6,
				report.PreTranche.TotalAlloc.Median,
				report.PreTranche.Mallocs.Median,
				*report.ExcessMallocRemoval*100,
			)
			if err != nil {
				return err
			}
		}
		if err := emitUnboundedAllocationNote(output, report); err != nil {
			return err
		}
		if err := emitPolicyReport(output, report); err != nil {
			return err
		}
		return emitGateReport(output, report)
	default:
		return fmt.Errorf("invalid -format %q: expected markdown or json", format)
	}
}

func emitPolicyReport(output io.Writer, report comparisonReport) error {
	policy := report.Policy
	rows := [][2]string{
		{"Comparison mode", report.ComparisonMode},
		{"Baseline runtime", report.Signature.RuntimeVersion},
		{"Candidate runtime", report.CandidateRuntimeVersion},
		{"Minimum samples", fmt.Sprintf("%d", policy.MinSamples)},
		{"Bootstrap resamples", fmt.Sprintf("%d", policy.Bootstrap)},
		{"Bootstrap seed", fmt.Sprintf("%d", policy.Seed)},
		{"Strict evidence validation", fmt.Sprintf("%t", policy.StrictEvidence)},
		{"Collection metadata required", fmt.Sprintf("%t", policy.CollectionRequired)},
		{"Required measurement", policy.RequiredMeasurement},
		{"Expected baseline SHA-256", policyValue(policy.ExpectedBaselineSHA256)},
		{"Expected baseline revision", policyValue(policy.ExpectedBaselineRevision)},
		{"Expected pre-tranche SHA-256", policyValue(policy.ExpectedPreTrancheSHA256)},
		{"Expected pre-tranche revision", policyValue(policy.ExpectedPreTrancheRevision)},
		{"Clean builds required", fmt.Sprintf("%t", policy.RequireClean)},
		{"Report output", policyValue(policy.OutputPath)},
		{"Report overwrite allowed", fmt.Sprintf("%t", policy.Overwrite)},
		{"Report format", policy.Format},
	}
	if _, err := fmt.Fprintln(output, "\n| Qualification policy | Value |\n|---|---|"); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(output, "| %s | %s |\n", row[0], row[1]); err != nil {
			return err
		}
	}
	return nil
}

func policyValue(value string) string {
	if value == "" {
		return "not required"
	}
	return value
}

func emitGateReport(output io.Writer, report comparisonReport) error {
	if _, err := fmt.Fprintf(output, "\nQualification: %t\n\n| Gate | Enabled | Policy | Actual | Result |\n|---|---:|---:|---:|---:|\n", report.Qualification); err != nil {
		return err
	}
	for _, gate := range report.Gates {
		policy := "disabled"
		actual := "-"
		result := "-"
		if gate.Actual != nil {
			actual = fmt.Sprintf("%.4f", *gate.Actual)
		}
		if gate.Enabled {
			policy = fmt.Sprintf("%s %.4f", gate.Relation, gate.Threshold)
			if gate.Passed != nil && *gate.Passed {
				result = "pass"
			} else {
				result = "FAIL"
			}
		}
		if _, err := fmt.Fprintf(output, "| %s | %t | %s | %s | %s |\n", gate.Name, gate.Enabled, policy, actual, result); err != nil {
			return err
		}
	}
	return nil
}

func emitUnboundedAllocationNote(output io.Writer, report comparisonReport) error {
	var metrics []string
	if report.TotalAllocRatio == nil {
		metrics = append(metrics, "allocated bytes")
	}
	if report.MallocRatio == nil {
		metrics = append(metrics, "malloc count")
	}
	if len(metrics) == 0 {
		return nil
	}
	_, err := fmt.Fprintf(
		output,
		"Allocation ratio unbounded for %s: baseline median is zero while candidate median is positive.\n",
		strings.Join(metrics, " and "),
	)
	return err
}
