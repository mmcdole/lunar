package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"time"

	"github.com/mmcdole/lunik/benchmarks/cbor/internal/fixture"
	lua "github.com/mmcdole/lunik/benchmarks/cbor/internal/luabridge"
)

const stagedData = "data/graph.cbor"

var fixtureFiles = []string{
	"workload.lua",
	"cbor.lua",
}

type options struct {
	mode                 string
	measurement          string
	preset               string
	fixture              string
	data                 string
	format               string
	cpuProfile           string
	heapProfile          string
	keepWorkdir          bool
	verboseLua           bool
	guarded              bool
	contextCheckInterval int
}

type oracle struct {
	Digest       string `json:"digest"`
	Areas        int    `json:"areas"`
	Rooms        int    `json:"rooms"`
	Exits        int    `json:"exits"`
	Tables       int    `json:"tables"`
	Entries      int    `json:"entries"`
	EncodedBytes int    `json:"encoded_bytes"`
}

type result struct {
	SchemaVersion        int    `json:"schema_version"`
	Mode                 string `json:"mode"`
	Measurement          string `json:"measurement"`
	Preset               string `json:"preset"`
	Execution            string `json:"execution"`
	ContextCheckInterval int    `json:"context_check_interval"`
	ElapsedNS            int64  `json:"elapsed_ns"`
	HeapBefore           uint64 `json:"heap_before,omitempty"`
	HeapRetained         uint64 `json:"heap_retained,omitempty"`
	HeapDelta            int64  `json:"heap_delta,omitempty"`
	TotalAllocDelta      uint64 `json:"total_alloc_delta"`
	MallocsDelta         uint64 `json:"mallocs_delta"`
	GoVersion            string `json:"go_version"`
	GOOS                 string `json:"goos"`
	GOARCH               string `json:"goarch"`
	Revision             string `json:"revision,omitempty"`
	RevisionModified     bool   `json:"revision_modified,omitempty"`
	InputSHA256          string `json:"input_sha256"`
	CodecSHA256          string `json:"codec_sha256"`
	WorkloadSHA256       string `json:"workload_sha256"`
	RuntimeVersion       string `json:"runtime_version"`
	Workdir              string `json:"workdir,omitempty"`
	Oracle               oracle `json:"oracle"`
}

func main() {
	var opts options
	flag.StringVar(&opts.mode, "mode", "save", "operation to measure: load or save")
	flag.StringVar(&opts.measurement, "measurement", "timing", "measurement protocol: timing, retained, or profile")
	flag.StringVar(&opts.preset, "preset", "small", "input preset: small or large")
	flag.StringVar(&opts.fixture, "fixture", "testdata", "Lua workload and codec directory")
	flag.StringVar(&opts.data, "data", "", "pre-generated CBOR input shared by every implementation (required)")
	flag.StringVar(&opts.format, "format", "text", "output format: text or jsonl")
	flag.StringVar(&opts.cpuProfile, "cpu-profile", "", "CPU profile path; valid only with -measurement profile")
	flag.StringVar(&opts.heapProfile, "heap-profile", "", "retained-heap profile path; valid only with -measurement profile")
	flag.BoolVar(&opts.keepWorkdir, "keep-workdir", false, "retain the staged writable fixture")
	flag.BoolVar(&opts.verboseLua, "verbose-lua", false, "print Lua workload log messages")
	flag.BoolVar(&opts.guarded, "guarded", false, "run with a live context and exercise context polling")
	flag.IntVar(&opts.contextCheckInterval, "context-check-interval", 0, "guarded bytecode polling interval; 0 preserves per-instruction checks")
	flag.Parse()

	measured, err := execute(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cbor-workload:", err)
		os.Exit(1)
	}
	if err := emit(os.Stdout, opts.format, measured); err != nil {
		fmt.Fprintln(os.Stderr, "cbor-workload:", err)
		os.Exit(1)
	}
}

func execute(opts options) (result, error) {
	expected, err := validateOptions(opts)
	if err != nil {
		return result{}, err
	}

	source, err := filepath.Abs(opts.fixture)
	if err != nil {
		return result{}, fmt.Errorf("resolve fixture: %w", err)
	}
	sharedInputSHA256, err := hashFile(opts.data)
	if err != nil {
		return result{}, fmt.Errorf("hash shared input: %w", err)
	}
	workdir, err := os.MkdirTemp("", "lunik-cbor-bench-")
	if err != nil {
		return result{}, fmt.Errorf("create work directory: %w", err)
	}
	if !opts.keepWorkdir {
		defer os.RemoveAll(workdir)
	}
	if err := stageFixture(source, workdir); err != nil {
		return result{}, err
	}

	dataPath := filepath.Join(workdir, filepath.FromSlash(stagedData))
	if err := copyFile(opts.data, dataPath); err != nil {
		return result{}, fmt.Errorf("stage shared input: %w", err)
	}
	baselinePath := filepath.Join(workdir, "benchmark-input.dat")
	if err := copyFile(dataPath, baselinePath); err != nil {
		return result{}, fmt.Errorf("preserve oracle input: %w", err)
	}

	L, err := lua.NewState(lua.Options{
		FixedUnixTime: fixture.FixedTimestamp,
	})
	if err != nil {
		return result{}, fmt.Errorf("create Lua state: %w", err)
	}
	defer func() {
		_ = L.Close()
	}()
	if err := L.ConfigureExecution(
		opts.guarded,
		opts.contextCheckInterval,
	); err != nil {
		return result{}, err
	}
	if err := installFixture(L, workdir, opts.verboseLua); err != nil {
		return result{}, err
	}

	if opts.mode == "save" {
		if err := callTrue(L, "loadBenchmarkGraph"); err != nil {
			return result{}, fmt.Errorf("prepare graph: %w", err)
		}
	}
	measureHeap := opts.measurement == "retained" || opts.heapProfile != ""
	if measureHeap {
		stabilizeHeap()
	}
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	stopCPU, err := startCPUProfile(opts.cpuProfile)
	if err != nil {
		return result{}, err
	}
	started := time.Now()
	err = callTrue(L, map[string]string{"load": "loadBenchmarkGraph", "save": "saveBenchmarkGraph"}[opts.mode])
	elapsed := time.Since(started)
	stopCPU()
	if err != nil {
		return result{}, fmt.Errorf("%s graph: %w", opts.mode, err)
	}

	var immediate runtime.MemStats
	runtime.ReadMemStats(&immediate)
	var retained runtime.MemStats
	if measureHeap {
		stabilizeHeap()
		runtime.ReadMemStats(&retained)
		if opts.heapProfile != "" {
			if err := writeHeapProfile(opts.heapProfile); err != nil {
				return result{}, err
			}
		}
	}

	// A load is round-tripped through the unchanged save path only after all measured
	// memory samples have been captured. A save has already produced this output.
	if opts.mode == "load" {
		if err := callTrue(L, "saveBenchmarkGraph"); err != nil {
			return result{}, fmt.Errorf("round-trip loaded graph: %w", err)
		}
	}

	sourceOracle, err := inspectGraphFile(workdir, baselinePath)
	if err != nil {
		return result{}, fmt.Errorf("inspect source oracle: %w", err)
	}
	if err := verifySummary("source", sourceOracle, expected); err != nil {
		return result{}, err
	}
	// Reclaim the verifier state before decoding the output. This is outside every
	// reported measurement and prevents two full graphs overlapping.
	runtime.GC()
	outputOracle, err := inspectGraphFile(workdir, dataPath)
	if err != nil {
		return result{}, fmt.Errorf("decode round-trip output: %w", err)
	}
	if err := verifySummary("round-trip", outputOracle, expected); err != nil {
		return result{}, err
	}
	if sourceOracle.Digest != outputOracle.Digest {
		return result{}, fmt.Errorf("round-trip structural digest changed: source=%s output=%s", sourceOracle.Digest, outputOracle.Digest)
	}
	revision, modified := buildRevision()
	stagedInputSHA256, err := hashFile(baselinePath)
	if err != nil {
		return result{}, fmt.Errorf("hash staged input: %w", err)
	}
	if stagedInputSHA256 != sharedInputSHA256 {
		return result{}, fmt.Errorf("staged input hash changed: source=%s staged=%s", sharedInputSHA256, stagedInputSHA256)
	}
	currentInputSHA256, err := hashFile(opts.data)
	if err != nil {
		return result{}, fmt.Errorf("rehash shared input: %w", err)
	}
	if currentInputSHA256 != sharedInputSHA256 {
		return result{}, fmt.Errorf("shared input changed during the run: before=%s after=%s", sharedInputSHA256, currentInputSHA256)
	}
	codecSHA256, err := hashFile(filepath.Join(workdir, "cbor.lua"))
	if err != nil {
		return result{}, fmt.Errorf("hash codec: %w", err)
	}
	workloadSHA256, err := hashFile(filepath.Join(workdir, "workload.lua"))
	if err != nil {
		return result{}, fmt.Errorf("hash workload: %w", err)
	}

	execution := "raw"
	if opts.guarded {
		execution = "guarded"
	}
	measured := result{
		SchemaVersion: 2,
		Mode:          opts.mode, Measurement: opts.measurement, Preset: opts.preset,
		Execution: execution, ContextCheckInterval: opts.contextCheckInterval,
		ElapsedNS:       elapsed.Nanoseconds(),
		TotalAllocDelta: immediate.TotalAlloc - before.TotalAlloc,
		MallocsDelta:    immediate.Mallocs - before.Mallocs,
		GoVersion:       runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Revision: revision, RevisionModified: modified, InputSHA256: sharedInputSHA256,
		CodecSHA256: codecSHA256, WorkloadSHA256: workloadSHA256,
		RuntimeVersion: L.RuntimeVersion(),
		Oracle:         outputOracle,
	}
	if opts.keepWorkdir {
		measured.Workdir = workdir
	}
	if measureHeap {
		measured.HeapBefore = before.HeapAlloc
		measured.HeapRetained = retained.HeapAlloc
		measured.HeapDelta = int64(retained.HeapAlloc) - int64(before.HeapAlloc)
	}
	return measured, nil
}

func validateOptions(opts options) (fixture.Summary, error) {
	if opts.mode != "load" && opts.mode != "save" {
		return fixture.Summary{}, fmt.Errorf("invalid -mode %q: expected load or save", opts.mode)
	}
	if opts.measurement != "timing" && opts.measurement != "retained" && opts.measurement != "profile" {
		return fixture.Summary{}, fmt.Errorf("invalid -measurement %q: expected timing, retained, or profile", opts.measurement)
	}
	if opts.measurement == "profile" && opts.cpuProfile == "" && opts.heapProfile == "" {
		return fixture.Summary{}, fmt.Errorf("-measurement profile requires -cpu-profile or -heap-profile")
	}
	if opts.cpuProfile != "" && opts.heapProfile != "" {
		return fixture.Summary{}, fmt.Errorf("CPU and heap profiles must be captured in separate runs")
	}
	if opts.measurement != "profile" && (opts.cpuProfile != "" || opts.heapProfile != "") {
		return fixture.Summary{}, fmt.Errorf("profile outputs require -measurement profile")
	}
	if opts.contextCheckInterval < 0 {
		return fixture.Summary{}, fmt.Errorf("-context-check-interval cannot be negative")
	}
	if !opts.guarded && opts.contextCheckInterval != 0 {
		return fixture.Summary{}, fmt.Errorf("-context-check-interval requires -guarded")
	}
	if opts.data == "" {
		return fixture.Summary{}, fmt.Errorf("-data is required so every implementation uses the same input file")
	}
	if preset, ok := fixture.Lookup(opts.preset); ok {
		return preset.Expected(), nil
	}
	return fixture.Summary{}, fmt.Errorf("invalid -preset %q: expected small or large", opts.preset)
}

func emit(w io.Writer, format string, measured result) error {
	switch format {
	case "jsonl":
		return json.NewEncoder(w).Encode(measured)
	case "text":
		if measured.Measurement == "timing" {
			_, err := fmt.Fprintf(w,
				"mode=%s measurement=%s preset=%s execution=%s context_check_interval=%d elapsed=%s total_alloc_delta=%d mallocs_delta=%d digest=%s tables=%d entries=%d encoded_bytes=%d\n",
				measured.Mode, measured.Measurement, measured.Preset, measured.Execution, measured.ContextCheckInterval, time.Duration(measured.ElapsedNS),
				measured.TotalAllocDelta, measured.MallocsDelta, measured.Oracle.Digest,
				measured.Oracle.Tables, measured.Oracle.Entries, measured.Oracle.EncodedBytes)
			return err
		}
		_, err := fmt.Fprintf(w,
			"mode=%s measurement=%s preset=%s execution=%s context_check_interval=%d elapsed=%s heap_before=%d heap_retained=%d heap_delta=%d total_alloc_delta=%d mallocs_delta=%d digest=%s tables=%d entries=%d encoded_bytes=%d\n",
			measured.Mode, measured.Measurement, measured.Preset, measured.Execution, measured.ContextCheckInterval, time.Duration(measured.ElapsedNS),
			measured.HeapBefore, measured.HeapRetained, measured.HeapDelta,
			measured.TotalAllocDelta, measured.MallocsDelta, measured.Oracle.Digest,
			measured.Oracle.Tables, measured.Oracle.Entries, measured.Oracle.EncodedBytes)
		return err
	default:
		return fmt.Errorf("invalid -format %q: expected text or jsonl", format)
	}
}

func stageFixture(source, destination string) error {
	for _, relative := range fixtureFiles {
		from := filepath.Join(source, filepath.FromSlash(relative))
		to := filepath.Join(destination, filepath.FromSlash(relative))
		if err := copyFile(from, to); err != nil {
			return fmt.Errorf("stage %s: %w", relative, err)
		}
	}
	return nil
}

func copyFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(destination, source); err != nil {
		destination.Close()
		return err
	}
	return destination.Close()
}

func installFixture(
	L *lua.State,
	fixtureRoot string,
	verbose bool,
) error {
	if err := L.SetGlobal(
		"LUNIK_CBOR_DATA_PATH",
		L.String(filepath.Join(
			fixtureRoot,
			filepath.FromSlash(stagedData),
		)),
	); err != nil {
		return fmt.Errorf("install data path: %w", err)
	}
	logger, err := L.NewLogFunction(verbose)
	if err != nil {
		return fmt.Errorf("construct logger: %w", err)
	}
	if err := L.SetGlobal("LUNIK_CBOR_LOG", logger); err != nil {
		return fmt.Errorf("install logger: %w", err)
	}
	if err := L.PrependPackagePath(fixtureRoot); err != nil {
		return fmt.Errorf("set package path: %w", err)
	}
	if err := L.DoFile(filepath.Join(fixtureRoot, "workload.lua")); err != nil {
		return fmt.Errorf("load wrapper: %w", err)
	}
	return nil
}

func callTrue(L *lua.State, name string) error {
	return L.CallGlobalBool(name)
}

func stabilizeHeap() {
	runtime.GC()
	runtime.GC()
}

func startCPUProfile(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create CPU profile: %w", err)
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("start CPU profile: %w", err)
	}
	return func() {
		pprof.StopCPUProfile()
		_ = file.Close()
	}, nil
}

func writeHeapProfile(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create heap profile: %w", err)
	}
	defer file.Close()
	if err := pprof.WriteHeapProfile(file); err != nil {
		return fmt.Errorf("write heap profile: %w", err)
	}
	return nil
}

func buildRevision() (string, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	var revision string
	var modified bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	return revision, modified
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

func inspectGraphFile(codecRoot, dataPath string) (oracle, error) {
	encoded, err := os.ReadFile(dataPath)
	if err != nil {
		return oracle{}, err
	}
	L, err := lua.NewState(lua.Options{
		FixedUnixTime: fixture.FixedTimestamp,
	})
	if err != nil {
		return oracle{}, err
	}
	defer func() {
		_ = L.Close()
	}()
	if err := L.PrependPackagePath(codecRoot); err != nil {
		return oracle{}, err
	}
	if err := L.DoString(
		"@load-cbor.lua",
		`__lunik_cbor_codec = require "cbor"`,
	); err != nil {
		return oracle{}, err
	}
	moduleValue, err := L.Global("__lunik_cbor_codec")
	if err != nil {
		return oracle{}, err
	}
	module, ok := lua.ValueTable(moduleValue)
	if !ok {
		return oracle{}, fmt.Errorf(
			"CBOR module is %s, not a table",
			lua.ValueTypeName(moduleValue),
		)
	}
	decode := lua.TableRawGetString(module, "decode")
	if lua.ValueKind(decode) != lua.FunctionKind {
		return oracle{}, fmt.Errorf(
			"CBOR decode is %s, not a function",
			lua.ValueTypeName(decode),
		)
	}
	decoded, err := L.CallOne(decode, L.String(string(encoded)))
	if err != nil {
		return oracle{}, err
	}
	root, ok := lua.ValueTable(decoded)
	if !ok {
		return oracle{}, fmt.Errorf(
			"decoded root is %s, not a table",
			lua.ValueTypeName(decoded),
		)
	}
	if err := lua.TableRawSetString(
		root,
		"timestamp",
		lua.Number(fixture.FixedTimestamp),
	); err != nil {
		return oracle{}, err
	}

	areas, err := requiredTable(root, "areas")
	if err != nil {
		return oracle{}, err
	}
	rooms, err := requiredTable(root, "rooms")
	if err != nil {
		return oracle{}, err
	}
	exits, err := requiredTable(root, "exits")
	if err != nil {
		return oracle{}, err
	}
	areaCount, err := countEntries(L, areas)
	if err != nil {
		return oracle{}, err
	}
	roomCount, err := countEntries(L, rooms)
	if err != nil {
		return oracle{}, err
	}
	observed := oracle{
		Areas: areaCount, Rooms: roomCount, EncodedBytes: len(encoded),
	}
	if err := L.ForEach(exits, func(_, value lua.Value) error {
		list, ok := lua.ValueTable(value)
		if !ok {
			return fmt.Errorf(
				"exit list is %s, not a table",
				lua.ValueTypeName(value),
			)
		}
		observed.Exits += lua.TableLen(list)
		return nil
	}); err != nil {
		return oracle{}, err
	}

	state := digestState{
		runtime: L,
		memo:    make(map[*lua.Table][]byte),
		stack:   make(map[*lua.Table]bool),
	}
	digest, err := state.value(lua.TableValue(root))
	if err != nil {
		return oracle{}, err
	}
	observed.Digest = hex.EncodeToString(digest)
	observed.Tables = state.tables
	observed.Entries = state.entries
	return observed, nil
}

func requiredTable(root *lua.Table, key string) (*lua.Table, error) {
	value := lua.TableRawGetString(root, key)
	table, ok := lua.ValueTable(value)
	if !ok {
		return nil, fmt.Errorf(
			"required field %s is %s, not a table",
			key,
			lua.ValueTypeName(value),
		)
	}
	return table, nil
}

func countEntries(L *lua.State, table *lua.Table) (int, error) {
	count := 0
	err := L.ForEach(table, func(_, _ lua.Value) error {
		count++
		return nil
	})
	return count, err
}

func verifySummary(label string, observed oracle, expected fixture.Summary) error {
	if observed.Areas != expected.Areas || observed.Rooms != expected.Rooms ||
		observed.Exits != expected.Exits || observed.Tables != expected.Tables ||
		observed.Entries != expected.Entries {
		return fmt.Errorf("%s structure mismatch: got areas=%d rooms=%d exits=%d tables=%d entries=%d; want areas=%d rooms=%d exits=%d tables=%d entries=%d",
			label, observed.Areas, observed.Rooms, observed.Exits, observed.Tables, observed.Entries,
			expected.Areas, expected.Rooms, expected.Exits, expected.Tables, expected.Entries)
	}
	return nil
}

type digestState struct {
	runtime *lua.State
	memo    map[*lua.Table][]byte
	stack   map[*lua.Table]bool
	tables  int
	entries int
}

type digestEntry struct {
	key   []byte
	value lua.Value
}

func (s *digestState) value(value lua.Value) ([]byte, error) {
	switch lua.ValueKind(value) {
	case lua.NilKind:
		return hashParts('n'), nil
	case lua.BoolKind:
		boolean, _ := lua.ValueBool(value)
		if boolean {
			return hashParts('b', []byte{1}), nil
		}
		return hashParts('b', []byte{0}), nil
	case lua.NumberKind:
		number, _ := lua.ValueNumber(value)
		bits := math.Float64bits(number)
		if number == 0 {
			bits = 0
		}
		var data [8]byte
		binary.BigEndian.PutUint64(data[:], bits)
		return hashParts('d', data[:]), nil
	case lua.StringKind:
		text, _ := lua.ValueString(value)
		return hashParts('s', []byte(text)), nil
	case lua.TableKind:
		table, _ := lua.ValueTable(value)
		return s.table(table)
	default:
		return nil, fmt.Errorf(
			"oracle does not support %s values",
			lua.ValueTypeName(value),
		)
	}
}

func (s *digestState) table(table *lua.Table) ([]byte, error) {
	if digest, ok := s.memo[table]; ok {
		return digest, nil
	}
	if s.stack[table] {
		return nil, fmt.Errorf("oracle input contains a table cycle")
	}
	s.stack[table] = true
	defer delete(s.stack, table)

	entries := make([]digestEntry, 0, lua.TableLen(table))
	if err := s.runtime.ForEach(table, func(key, value lua.Value) error {
		encoded, err := scalarEncoding(key)
		if err != nil {
			return err
		}
		entries = append(entries, digestEntry{key: encoded, value: value})
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return bytes.Compare(entries[i].key, entries[j].key) < 0 })

	h := sha256.New()
	h.Write([]byte{'t'})
	writeUint64(h, uint64(len(entries)))
	for _, entry := range entries {
		writeBytes(h, entry.key)
		valueDigest, err := s.value(entry.value)
		if err != nil {
			return nil, err
		}
		h.Write(valueDigest)
	}
	digest := h.Sum(nil)
	s.memo[table] = digest
	s.tables++
	s.entries += len(entries)
	return digest, nil
}

func scalarEncoding(value lua.Value) ([]byte, error) {
	var data []byte
	switch lua.ValueKind(value) {
	case lua.BoolKind:
		boolean, _ := lua.ValueBool(value)
		data = []byte{'b', 0}
		if boolean {
			data[1] = 1
		}
	case lua.NumberKind:
		number, _ := lua.ValueNumber(value)
		bits := math.Float64bits(number)
		if number == 0 {
			bits = 0
		}
		data = make([]byte, 9)
		data[0] = 'd'
		binary.BigEndian.PutUint64(data[1:], bits)
	case lua.StringKind:
		text, _ := lua.ValueString(value)
		data = append([]byte{'s'}, []byte(text)...)
	default:
		return nil, fmt.Errorf(
			"oracle does not support %s table keys",
			lua.ValueTypeName(value),
		)
	}
	return data, nil
}

func hashParts(tag byte, parts ...[]byte) []byte {
	h := sha256.New()
	h.Write([]byte{tag})
	for _, part := range parts {
		writeBytes(h, part)
	}
	return h.Sum(nil)
}

func writeBytes(h hash.Hash, data []byte) {
	writeUint64(h, uint64(len(data)))
	h.Write(data)
}

func writeUint64(h hash.Hash, value uint64) {
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], value)
	h.Write(data[:])
}
