package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateOptionsRequiresProvisionedRuntime(t *testing.T) {
	opts := options{mode: "load", runs: 1, format: "jsonl"}
	err := validateOptions(&opts)
	if err == nil || !strings.Contains(err.Error(), "never downloaded") {
		t.Fatalf("validation error = %v, want explicit offline-runtime requirement", err)
	}
}

func TestValidateOptionsSelectsModeScriptAndAbsolutePaths(t *testing.T) {
	temporary := t.TempDir()
	fixture := filepath.Join(temporary, "fixture")
	mustWriteTestFile(t, filepath.Join(fixture, "workload.lua"), "return true")
	mustWriteTestFile(t, filepath.Join(fixture, "cbor.lua"), "return {}")
	data := filepath.Join(temporary, "input.dat")
	mustWriteTestFile(t, data, "fixture")
	script := filepath.Join(temporary, "measure_save.lua")
	mustWriteTestFile(t, script, "return true")

	opts := options{
		mode: "save", fixture: fixture, data: data, script: script,
		lua51: "provided-elsewhere", runs: 1, format: "jsonl",
	}
	if err := validateOptions(&opts); err != nil {
		t.Fatal(err)
	}
	for label, path := range map[string]string{"fixture": opts.fixture, "data": opts.data, "script": opts.script} {
		if !filepath.IsAbs(path) {
			t.Fatalf("%s path %q is not absolute", label, path)
		}
	}
}

func TestValidateOptionsNormalizesRuntimePinsAndProtectsEvidence(t *testing.T) {
	temporary := t.TempDir()
	fixture := filepath.Join(temporary, "fixture")
	mustWriteTestFile(t, filepath.Join(fixture, "workload.lua"), "return true")
	mustWriteTestFile(t, filepath.Join(fixture, "cbor.lua"), "return {}")
	data := filepath.Join(temporary, "input.dat")
	mustWriteTestFile(t, data, "fixture")
	script := filepath.Join(temporary, "load.lua")
	mustWriteTestFile(t, script, "return true")
	output := filepath.Join(temporary, "native.jsonl")

	opts := options{
		mode: "load", fixture: fixture, data: data, script: script, output: output,
		luajit: "provided-elsewhere", runs: 1, format: "jsonl",
		expectLuaJITSHA256: strings.Repeat("A", 64),
	}
	if err := validateOptions(&opts); err != nil {
		t.Fatal(err)
	}
	if opts.expectLuaJITSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("normalized LuaJIT SHA = %q", opts.expectLuaJITSHA256)
	}
	if !filepath.IsAbs(opts.output) {
		t.Fatalf("output path %q is not absolute", opts.output)
	}
	if err := os.WriteFile(output, []byte("existing evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateOptions(&opts); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing evidence validation error = %v", err)
	}

	opts.output = ""
	opts.expectLuaJITSHA256 = "not-a-digest"
	if err := validateOptions(&opts); err == nil || !strings.Contains(err.Error(), "-expect-luajit-sha256") {
		t.Fatalf("invalid LuaJIT SHA validation error = %v", err)
	}
}

func TestPrepareRuntimeSpecsRejectsUnexpectedSHA(t *testing.T) {
	runtimePath := filepath.Join(t.TempDir(), "lua")
	if err := os.WriteFile(runtimePath, []byte("#!/bin/sh\nprintf 'Lua 5.1.5\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := prepareRuntimeSpecs(options{
		lua51: runtimePath, expectLua51: "Lua 5.1.5", expectLua51SHA256: strings.Repeat("0", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "does not match required") {
		t.Fatalf("unexpected runtime SHA error = %v", err)
	}
}

func TestNativeRuntimeUsesVerifiedPrivateCopy(t *testing.T) {
	runtimePath := filepath.Join(t.TempDir(), "luajit")
	if err := os.WriteFile(runtimePath, []byte("#!/bin/sh\nprintf 'LuaJIT 2.1 staged\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := hashFile(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	specs := []runtimeSpec{
		{name: "luajit-2.1-jit-off", binary: runtimePath, binarySHA256: digest, expectedVersion: "LuaJIT 2.1"},
		{name: "luajit-2.1-jit-on", binary: runtimePath, binarySHA256: digest, expectedVersion: "LuaJIT 2.1"},
	}
	directory, err := stageRuntimeBinaries(specs)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	if specs[0].executable == runtimePath || specs[0].executable == "" {
		t.Fatalf("native runtime was not staged: %+v", specs[0])
	}
	if specs[0].executable != specs[1].executable {
		t.Fatalf("LuaJIT modes did not share one staged binary: %q != %q", specs[0].executable, specs[1].executable)
	}
	info, err := os.Stat(specs[0].executable)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o500 {
		t.Fatalf("staged native runtime permissions = %#o, want 0500", permissions)
	}
	if err := os.WriteFile(runtimePath, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	stagedDigest, err := hashFile(specs[0].executable)
	if err != nil {
		t.Fatal(err)
	}
	if stagedDigest != digest {
		t.Fatalf("staged native runtime changed with source: got %s, want %s", stagedDigest, digest)
	}
}

func TestNativeRuntimeStagingRejectsHashMismatch(t *testing.T) {
	runtimePath := filepath.Join(t.TempDir(), "lua")
	if err := os.WriteFile(runtimePath, []byte("#!/bin/sh\nprintf 'Lua 5.1.5\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	specs := []runtimeSpec{{
		name: "puc-lua-5.1", binary: runtimePath, binarySHA256: strings.Repeat("0", 64), expectedVersion: "Lua 5.1.5",
	}}
	if _, err := stageRuntimeBinaries(specs); err == nil || !strings.Contains(err.Error(), "changed while it was staged") {
		t.Fatalf("native staging mismatch error = %v", err)
	}
}

func TestNativeRuntimeStagingRevalidatesEverySpecSharingOneSource(t *testing.T) {
	runtimePath := filepath.Join(t.TempDir(), "luajit")
	if err := os.WriteFile(runtimePath, []byte("#!/bin/sh\nprintf 'LuaJIT 2.1 test\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := hashFile(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	specs := []runtimeSpec{
		{name: "luajit-2.1-jit-off", binary: runtimePath, binarySHA256: digest, expectedVersion: "LuaJIT 2.1"},
		{name: "luajit-2.1-jit-on", binary: runtimePath, binarySHA256: strings.Repeat("0", 64), expectedVersion: "LuaJIT 2.1"},
	}
	if _, err := stageRuntimeBinaries(specs); err == nil ||
		!strings.Contains(err.Error(), "luajit-2.1-jit-on") || !strings.Contains(err.Error(), "changed while it was staged") {
		t.Fatalf("shared-source native staging mismatch error = %v", err)
	}
}

func TestEmitNativeJSONL(t *testing.T) {
	var output bytes.Buffer
	if err := emit(&output, "jsonl", result{
		RecordType: "sample", SchemaVersion: nativeSchemaVersion, Runtime: "puc-lua-5.1", Mode: "load", WallNS: 10, UserCPUNS: 4, SystemCPUNS: 2,
	}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"record_type":"sample"`, `"schema_version":2`, `"runtime":"puc-lua-5.1"`, `"wall_ns":10`, `"user_cpu_ns":4`} {
		if !strings.Contains(output.String(), field) {
			t.Fatalf("JSONL output %q does not contain %q", output.String(), field)
		}
	}
}

func TestQualificationPolicyRejectsIncompleteInvocation(t *testing.T) {
	tests := []struct {
		name      string
		want      string
		configure func(*options)
	}{
		{name: "missing PUC", want: "both -lua51 and -luajit", configure: func(opts *options) { opts.lua51 = "" }},
		{name: "missing LuaJIT", want: "both -lua51 and -luajit", configure: func(opts *options) { opts.luajit = "" }},
		{name: "missing PUC pin", want: "-expect-lua51-sha256", configure: func(opts *options) { opts.expectLua51SHA256 = "" }},
		{name: "missing LuaJIT pin", want: "-expect-luajit-sha256", configure: func(opts *options) { opts.expectLuaJITSHA256 = "" }},
		{name: "too few runs", want: "at least 15", configure: func(opts *options) { opts.runs = minimumNativeRuns - 1 }},
		{name: "text output", want: "-format jsonl", configure: func(opts *options) { opts.format = "text" }},
		{name: "stdout only", want: "explicit -output", configure: func(opts *options) { opts.output = "" }},
		{name: "JIT-off only", want: "JIT-off and JIT-on", configure: func(opts *options) { opts.includeLuaJITJIT = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := nativeQualificationOptions(t)
			test.configure(&opts)
			if err := validateOptions(&opts); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("qualification validation error = %v, want %q", err, test.want)
			}
		})
	}

	opts := nativeQualificationOptions(t)
	if err := validateOptions(&opts); err != nil {
		t.Fatalf("complete qualification policy rejected: %v", err)
	}
}

func TestDescriptiveSmokeModeRemainsAvailable(t *testing.T) {
	opts := nativeQualificationOptions(t)
	opts.qualification = false
	opts.lua51 = ""
	opts.expectLua51SHA256 = ""
	opts.expectLuaJITSHA256 = ""
	opts.runs = 1
	opts.format = "text"
	opts.output = ""
	opts.includeLuaJITJIT = false
	if err := validateOptions(&opts); err != nil {
		t.Fatalf("descriptive smoke mode rejected: %v", err)
	}
}

func TestQualificationArchiveHasPolicySamplesAndCompletion(t *testing.T) {
	opts := nativeQualificationOptions(t)
	temporary := filepath.Dir(opts.output)
	lua51 := filepath.Join(temporary, "lua51")
	luajit := filepath.Join(temporary, "luajit")
	writeFakeNativeRuntime(t, lua51, "Lua 5.1.5 fake", "puc")
	writeFakeNativeRuntime(t, luajit, "LuaJIT 2.1 fake", "jit")
	opts.lua51 = lua51
	opts.luajit = luajit
	var err error
	opts.expectLua51SHA256, err = hashFile(lua51)
	if err != nil {
		t.Fatal(err)
	}
	opts.expectLuaJITSHA256, err = hashFile(luajit)
	if err != nil {
		t.Fatal(err)
	}
	opts.expectLua51 = "Lua 5.1.5"
	opts.expectLuaJIT = "LuaJIT 2.1"
	opts.warmups = 0

	if err := execute(opts, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(opts.output)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	wantSamples := minimumNativeRuns * 3
	if len(lines) != wantSamples+2 {
		t.Fatalf("archive records = %d, want policy + %d samples + completion", len(lines), wantSamples)
	}
	var policy policyRecord
	if err := json.Unmarshal([]byte(lines[0]), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.RecordType != "policy" || !policy.Qualification || !policy.CompletionRequired || len(policy.Runtimes) != 3 {
		t.Fatalf("qualification policy = %+v", policy)
	}
	var completion completionRecord
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &completion); err != nil {
		t.Fatal(err)
	}
	if completion.RecordType != "completion" || !completion.Qualification || !completion.Completed ||
		completion.Samples != wantSamples || completion.ExpectedSamples != wantSamples {
		t.Fatalf("qualification completion = %+v", completion)
	}
	for _, runtimeName := range []string{"puc-lua-5.1", "luajit-2.1-jit-off", "luajit-2.1-jit-on"} {
		if completion.RuntimeSamples[runtimeName] != minimumNativeRuns {
			t.Fatalf("%s samples = %d, want %d", runtimeName, completion.RuntimeSamples[runtimeName], minimumNativeRuns)
		}
	}
}

func TestParseLuaMeasurementLoad(t *testing.T) {
	measurement, err := parseLuaMeasurement("CBOR_LUA_LOAD heap_kib=57035.75 graph_delta_kib=56963.25 cpu_seconds=0.704650")
	if err != nil {
		t.Fatal(err)
	}
	if measurement.OperationCPUNS != 704_650_000 {
		t.Fatalf("operation CPU = %d, want 704650000", measurement.OperationCPUNS)
	}
	if measurement.LuaHeapBytes != 58_404_608 || measurement.GraphDeltaBytes != 58_330_368 {
		t.Fatalf("heap fields = %d/%d", measurement.LuaHeapBytes, measurement.GraphDeltaBytes)
	}
}

func TestParseLuaMeasurementSave(t *testing.T) {
	measurement, err := parseLuaMeasurement("CBOR_LUA_SAVE cpu_seconds=0.679378 heap_kib=70156.446")
	if err != nil {
		t.Fatal(err)
	}
	if measurement.OperationCPUNS != 679_378_000 {
		t.Fatalf("operation CPU = %d, want 679378000", measurement.OperationCPUNS)
	}
	if measurement.LuaHeapBytes == 0 || measurement.GraphDeltaBytes != 0 {
		t.Fatalf("heap fields = %d/%d", measurement.LuaHeapBytes, measurement.GraphDeltaBytes)
	}
}

func TestParseLuaMeasurementRejectsMissingOperationTime(t *testing.T) {
	if _, err := parseLuaMeasurement("CBOR_LUA_SAVE heap_kib=12"); err == nil {
		t.Fatal("measurement without operation CPU was accepted")
	}
}

func mustWriteTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func nativeQualificationOptions(t *testing.T) options {
	t.Helper()
	temporary := t.TempDir()
	fixture := filepath.Join(temporary, "fixture")
	mustWriteTestFile(t, filepath.Join(fixture, "workload.lua"), "return true")
	mustWriteTestFile(t, filepath.Join(fixture, "cbor.lua"), "return {}")
	data := filepath.Join(temporary, "graph.cbor")
	mustWriteTestFile(t, data, "fixture")
	script := filepath.Join(temporary, "measure.lua")
	mustWriteTestFile(t, script, "return true")
	return options{
		mode: "load", fixture: fixture, data: data, script: script,
		lua51: "lua51", luajit: "luajit", expectLua51SHA256: strings.Repeat("a", 64),
		expectLuaJITSHA256: strings.Repeat("b", 64), runs: minimumNativeRuns, format: "jsonl",
		includeLuaJITJIT: true, output: filepath.Join(temporary, "native.jsonl"), qualification: true,
	}
}

func writeFakeNativeRuntime(t *testing.T, path, version, marker string) {
	t.Helper()
	contents := "#!/bin/sh\n" +
		"# " + marker + "\n" +
		"if [ \"$1\" = \"-v\" ]; then\n" +
		"  printf '" + version + "\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"printf 'CBOR_LUA_LOAD cpu_seconds=0.001 heap_kib=1 graph_delta_kib=1\\n'\n"
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
}
