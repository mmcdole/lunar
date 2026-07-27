// Command run collects randomized fresh-process CBOR workload samples.
package main

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type options struct {
	baseline                 string
	candidate                string
	preTranche               string
	baselineOutput           string
	candidateOutput          string
	preTrancheOutput         string
	expectBaselineSHA256     string
	expectBaselineRevision   string
	expectPreTrancheSHA256   string
	expectPreTrancheRevision string
	requireClean             bool
	preset                   string
	mode                     string
	measurement              string
	fixture                  string
	data                     string
	runs                     int
	warmups                  int
	seed                     int64
	comparisonMode           string
	guarded                  bool
	contextCheckInterval     int
	timeout                  time.Duration
	overwrite                bool
}

type implementation struct {
	label      string
	binary     string
	sha256     string
	outputPath string
	output     *os.File
}

func main() {
	var opts options
	flag.StringVar(&opts.baseline, "baseline", "", "pinned baseline CBOR worker binary")
	flag.StringVar(&opts.candidate, "candidate", "", "candidate CBOR worker binary")
	flag.StringVar(&opts.preTranche, "pre-tranche", "", "optional pre-tranche CBOR worker binary")
	flag.StringVar(&opts.baselineOutput, "baseline-output", "", "new baseline JSONL evidence path")
	flag.StringVar(&opts.candidateOutput, "candidate-output", "", "new candidate JSONL evidence path")
	flag.StringVar(&opts.preTrancheOutput, "pre-tranche-output", "", "new pre-tranche JSONL evidence path")
	flag.StringVar(&opts.expectBaselineSHA256, "expect-baseline-sha256", "", "required SHA-256 of the pinned baseline binary")
	flag.StringVar(&opts.expectBaselineRevision, "expect-baseline-revision", "", "required VCS revision reported by the pinned baseline")
	flag.StringVar(&opts.expectPreTrancheSHA256, "expect-pre-tranche-sha256", "", "required SHA-256 of the pinned pre-tranche binary")
	flag.StringVar(&opts.expectPreTrancheRevision, "expect-pre-tranche-revision", "", "required VCS revision reported by the pinned pre-tranche")
	flag.BoolVar(&opts.requireClean, "require-clean", false, "require every worker to report a clean VCS build")
	flag.StringVar(&opts.preset, "preset", "large", "CBOR workload preset: small or large")
	flag.StringVar(&opts.mode, "mode", "load", "CBOR workload operation: load or save")
	flag.StringVar(&opts.measurement, "measurement", "timing", "CBOR workload protocol: timing or retained")
	flag.StringVar(&opts.fixture, "fixture", "testdata", "Lua workload and codec directory")
	flag.StringVar(&opts.data, "data", "", "pre-generated CBOR input shared by both workers (required)")
	flag.IntVar(&opts.runs, "runs", 15, "recorded paired samples")
	flag.IntVar(&opts.warmups, "warmups", 2, "discarded paired warmups")
	flag.Int64Var(&opts.seed, "seed", 1, "runtime-order randomization seed")
	flag.StringVar(&opts.comparisonMode, "comparison-mode", "implementations", "comparison policy: implementations or context-tax")
	flag.BoolVar(&opts.guarded, "guarded", false, "exercise the worker's context-guarded path")
	flag.IntVar(&opts.contextCheckInterval, "context-check-interval", 0, "guarded VM polling interval")
	flag.DurationVar(&opts.timeout, "timeout", 10*time.Minute, "timeout for each fresh process")
	flag.BoolVar(&opts.overwrite, "overwrite", false, "replace existing evidence files")
	flag.Parse()

	if err := execute(opts); err != nil {
		fmt.Fprintln(os.Stderr, "cbor-run:", err)
		os.Exit(1)
	}
}

func execute(opts options) error {
	if err := validateOptions(&opts); err != nil {
		return err
	}
	implementations, err := prepareImplementations(opts)
	if err != nil {
		return err
	}
	implementations, stagingDirectory, err := stageImplementations(implementations)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingDirectory)
	collectionID, err := newCollectionID()
	if err != nil {
		return fmt.Errorf("create collection ID: %w", err)
	}
	for index := range implementations {
		output, err := createEvidenceFile(implementations[index].outputPath, opts.overwrite)
		if err != nil {
			return fmt.Errorf("%s output: %w", implementations[index].label, err)
		}
		implementations[index].output = output
		defer output.Close()
	}
	random := rand.New(rand.NewSource(opts.seed))
	sequence := 0
	for round := -opts.warmups; round < opts.runs; round++ {
		ordered := append([]implementation(nil), implementations...)
		random.Shuffle(len(ordered), func(left, right int) {
			ordered[left], ordered[right] = ordered[right], ordered[left]
		})
		for _, current := range ordered {
			raw, err := runWorkload(opts, current.label, current.binary)
			if err != nil {
				phase := fmt.Sprintf("run %d", round+1)
				if round < 0 {
					phase = fmt.Sprintf("warmup %d", round+opts.warmups+1)
				}
				return fmt.Errorf("%s %s: %w", current.label, phase, err)
			}
			if err := validateWorkerIdentity(opts, current.label, raw); err != nil {
				return fmt.Errorf("%s identity: %w", current.label, err)
			}
			if round < 0 {
				continue
			}
			sequence++
			record, err := augmentRecord(raw, current.label, current.sha256, collectionID, round+1, sequence)
			if err != nil {
				return fmt.Errorf("%s run %d output: %w", current.label, round+1, err)
			}
			if _, err := current.output.Write(append(record, '\n')); err != nil {
				return fmt.Errorf("write %s run %d: %w", current.label, round+1, err)
			}
		}
	}
	for _, current := range implementations {
		if err := current.output.Sync(); err != nil {
			return fmt.Errorf("sync %s output: %w", current.label, err)
		}
	}
	if err := verifyStagedImplementations(implementations); err != nil {
		return err
	}
	paths := make([]string, len(implementations))
	for index, current := range implementations {
		paths[index] = current.outputPath
	}
	fmt.Printf("recorded collection %s with %d randomized samples per implementation in %s\n", collectionID, opts.runs, strings.Join(paths, ", "))
	return nil
}

func stageImplementations(implementations []implementation) ([]implementation, string, error) {
	directory, err := os.MkdirTemp("", "lugo-cbor-workers-")
	if err != nil {
		return nil, "", fmt.Errorf("create worker staging directory: %w", err)
	}
	staged := append([]implementation(nil), implementations...)
	for index := range staged {
		extension := filepath.Ext(staged[index].binary)
		target := filepath.Join(directory, staged[index].label+extension)
		if err := copyWorkerBinary(staged[index].binary, target); err != nil {
			os.RemoveAll(directory)
			return nil, "", fmt.Errorf("stage %s worker: %w", staged[index].label, err)
		}
		hash, err := hashFile(target)
		if err != nil {
			os.RemoveAll(directory)
			return nil, "", fmt.Errorf("hash staged %s worker: %w", staged[index].label, err)
		}
		if hash != staged[index].sha256 {
			os.RemoveAll(directory)
			return nil, "", fmt.Errorf(
				"%s worker changed while staging: before=%s staged=%s",
				staged[index].label, staged[index].sha256, hash,
			)
		}
		staged[index].binary = target
	}
	return staged, directory, nil
}

func copyWorkerBinary(sourcePath, targetPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	mode := info.Mode().Perm() & 0o555
	if mode&0o100 == 0 {
		mode |= 0o500
	}
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	if err := target.Sync(); err != nil {
		target.Close()
		return err
	}
	if err := target.Close(); err != nil {
		return err
	}
	return os.Chmod(targetPath, mode)
}

func verifyStagedImplementations(implementations []implementation) error {
	for _, current := range implementations {
		hash, err := hashFile(current.binary)
		if err != nil {
			return fmt.Errorf("rehash staged %s worker: %w", current.label, err)
		}
		if hash != current.sha256 {
			return fmt.Errorf(
				"staged %s worker changed during collection: before=%s after=%s",
				current.label, current.sha256, hash,
			)
		}
	}
	return nil
}

func newCollectionID() (string, error) {
	var value [16]byte
	if _, err := cryptorand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func prepareImplementations(opts options) ([]implementation, error) {
	implementations := []implementation{
		{label: "baseline", binary: opts.baseline, outputPath: opts.baselineOutput},
		{label: "candidate", binary: opts.candidate, outputPath: opts.candidateOutput},
	}
	if opts.preTranche != "" {
		implementations = append(implementations, implementation{
			label: "pre-tranche", binary: opts.preTranche, outputPath: opts.preTrancheOutput,
		})
	}
	for index := range implementations {
		hash, err := hashFile(implementations[index].binary)
		if err != nil {
			return nil, fmt.Errorf("hash %s binary: %w", implementations[index].label, err)
		}
		implementations[index].sha256 = hash
	}
	if opts.expectBaselineSHA256 != "" && implementations[0].sha256 != opts.expectBaselineSHA256 {
		return nil, fmt.Errorf(
			"baseline SHA-256 %s does not match required %s",
			implementations[0].sha256, opts.expectBaselineSHA256,
		)
	}
	if opts.preTranche != "" && opts.expectPreTrancheSHA256 != "" && implementations[2].sha256 != opts.expectPreTrancheSHA256 {
		return nil, fmt.Errorf(
			"pre-tranche SHA-256 %s does not match required %s",
			implementations[2].sha256, opts.expectPreTrancheSHA256,
		)
	}
	switch opts.comparisonMode {
	case "implementations":
		for left := range implementations {
			for right := left + 1; right < len(implementations); right++ {
				if implementations[left].sha256 == implementations[right].sha256 {
					return nil, fmt.Errorf(
						"%s and %s binaries have identical SHA-256 %s",
						implementations[left].label, implementations[right].label,
						implementations[left].sha256,
					)
				}
			}
		}
	case "context-tax":
		if implementations[0].sha256 != implementations[1].sha256 {
			return nil, fmt.Errorf(
				"context-tax baseline and candidate binaries differ: %s != %s",
				implementations[0].sha256, implementations[1].sha256,
			)
		}
	}
	return implementations, nil
}

type workerBuildIdentity struct {
	Revision         string `json:"revision"`
	RevisionModified bool   `json:"revision_modified"`
}

func validateWorkerIdentity(opts options, label string, raw []byte) error {
	var identity workerBuildIdentity
	if err := json.Unmarshal(raw, &identity); err != nil {
		return fmt.Errorf("decode worker build identity: %w", err)
	}
	if opts.requireClean {
		if identity.Revision == "" {
			return errors.New("worker did not report a VCS revision")
		}
		if identity.RevisionModified {
			return fmt.Errorf("worker revision %s is modified", identity.Revision)
		}
	}
	if label == "baseline" && opts.expectBaselineRevision != "" && identity.Revision != opts.expectBaselineRevision {
		return fmt.Errorf(
			"baseline revision %q does not match required %q",
			identity.Revision, opts.expectBaselineRevision,
		)
	}
	if label == "pre-tranche" && opts.expectPreTrancheRevision != "" && identity.Revision != opts.expectPreTrancheRevision {
		return fmt.Errorf(
			"pre-tranche revision %q does not match required %q",
			identity.Revision, opts.expectPreTrancheRevision,
		)
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

func validateOptions(opts *options) error {
	if opts.baseline == "" || opts.candidate == "" {
		return errors.New("-baseline and -candidate binaries are required")
	}
	if opts.baselineOutput == "" || opts.candidateOutput == "" {
		return errors.New("-baseline-output and -candidate-output are required")
	}
	if (opts.preTranche == "") != (opts.preTrancheOutput == "") {
		return errors.New("-pre-tranche and -pre-tranche-output must be supplied together")
	}
	if opts.preTranche == "" && (opts.expectPreTrancheSHA256 != "" || opts.expectPreTrancheRevision != "") {
		return errors.New("pre-tranche identity expectations require -pre-tranche")
	}
	if opts.comparisonMode == "context-tax" && opts.preTranche != "" {
		return errors.New("context-tax comparison does not accept a pre-tranche implementation")
	}
	if opts.data == "" {
		return errors.New("-data is required so both workers use the same input file")
	}
	if opts.runs < 1 || opts.warmups < 0 {
		return errors.New("-runs must be positive and -warmups cannot be negative")
	}
	if opts.measurement != "timing" && opts.measurement != "retained" {
		return fmt.Errorf("invalid -measurement %q: expected timing or retained", opts.measurement)
	}
	if opts.comparisonMode != "implementations" && opts.comparisonMode != "context-tax" {
		return fmt.Errorf("invalid -comparison-mode %q: expected implementations or context-tax", opts.comparisonMode)
	}
	if opts.comparisonMode == "context-tax" {
		if opts.guarded {
			return errors.New("context-tax comparison keeps the baseline raw; omit -guarded")
		}
		if opts.contextCheckInterval <= 1 {
			return errors.New("context-tax comparison requires -context-check-interval greater than 1")
		}
	} else if opts.contextCheckInterval < 0 || (!opts.guarded && opts.contextCheckInterval != 0) {
		return errors.New("a nonnegative -context-check-interval requires -guarded")
	}
	if opts.timeout <= 0 {
		return errors.New("-timeout must be positive")
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
	for label, path := range map[string]*string{
		"baseline": &opts.baseline, "candidate": &opts.candidate,
		"pre-tranche": &opts.preTranche,
	} {
		if *path == "" {
			continue
		}
		resolved, err := exec.LookPath(*path)
		if err != nil {
			return fmt.Errorf("find %s binary %q: %w", label, *path, err)
		}
		absolute, err := filepath.Abs(resolved)
		if err != nil {
			return fmt.Errorf("resolve %s binary: %w", label, err)
		}
		*path = absolute
	}
	for label, path := range map[string]*string{
		"fixture": &opts.fixture, "data": &opts.data,
		"baseline output": &opts.baselineOutput, "candidate output": &opts.candidateOutput,
		"pre-tranche output": &opts.preTrancheOutput,
	} {
		if *path == "" {
			continue
		}
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", label, err)
		}
		*path = absolute
	}
	outputs := []string{opts.baselineOutput, opts.candidateOutput}
	if opts.preTrancheOutput != "" {
		outputs = append(outputs, opts.preTrancheOutput)
	}
	seenOutputs := make(map[string]bool, len(outputs))
	for _, output := range outputs {
		if seenOutputs[output] {
			return errors.New("baseline, candidate, and pre-tranche outputs must differ")
		}
		seenOutputs[output] = true
	}
	return nil
}

func workloadArguments(opts options, implementation string) []string {
	arguments := []string{
		"-preset", opts.preset,
		"-mode", opts.mode,
		"-measurement", opts.measurement,
		"-fixture", opts.fixture,
		"-data", opts.data,
		"-format", "jsonl",
	}
	guarded := opts.guarded
	contextCheckInterval := opts.contextCheckInterval
	if opts.comparisonMode == "context-tax" {
		guarded = implementation == "candidate"
		if !guarded {
			contextCheckInterval = 0
		}
	}
	if guarded {
		arguments = append(arguments, "-guarded")
		if contextCheckInterval != 0 {
			arguments = append(arguments, "-context-check-interval", fmt.Sprint(contextCheckInterval))
		}
	}
	return arguments
}

func runWorkload(opts options, implementation, binary string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, workloadArguments(opts, implementation)...)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("timed out after %s: %w", opts.timeout, ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("process failed: %w; output=%q", err, bytes.TrimSpace(output))
	}
	return output, nil
}

func augmentRecord(raw []byte, implementation, binarySHA256, collectionID string, run, sequence int) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var record map[string]json.RawMessage
	if err := decoder.Decode(&record); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("profiler emitted more than one JSON value")
		}
		return nil, fmt.Errorf("trailing profiler output: %w", err)
	}
	if record == nil {
		return nil, errors.New("profiler output is not a JSON object")
	}
	label, _ := json.Marshal(implementation)
	runJSON, _ := json.Marshal(run)
	sequenceJSON, _ := json.Marshal(sequence)
	record["implementation"] = label
	binaryHash, _ := json.Marshal(binarySHA256)
	record["binary_sha256"] = binaryHash
	collection, _ := json.Marshal(collectionID)
	record["collection_id"] = collection
	record["sample_run"] = runJSON
	record["sample_sequence"] = sequenceJSON
	return json.Marshal(record)
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func createEvidenceFile(path string, overwrite bool) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	flags := os.O_CREATE | os.O_WRONLY
	if overwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	return os.OpenFile(path, flags, 0o644)
}
