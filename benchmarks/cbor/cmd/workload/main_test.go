package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mmcdole/lunik/benchmarks/cbor/internal/fixture"
	lua "github.com/mmcdole/lunik/benchmarks/cbor/internal/luabridge"
)

func TestSmallPresetLoadRoundTripsWithStableOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the Lua CBOR codec")
	}

	input := generatedInput(t, "small")
	measured, err := execute(options{
		mode: "load", measurement: "timing", preset: "small",
		fixture: fixturePath(t), data: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if measured.SchemaVersion != 2 {
		t.Fatalf("schema version = %d, want 2", measured.SchemaVersion)
	}
	if measured.Oracle.Digest == "" || measured.InputSHA256 == "" || measured.CodecSHA256 == "" || measured.WorkloadSHA256 == "" {
		t.Fatalf("missing reproducibility metadata: %+v", measured)
	}
	if measured.RuntimeVersion == "" {
		t.Fatal("runtime version is empty")
	}
	if measured.Oracle.Areas != 3 || measured.Oracle.Rooms != 32 || measured.Oracle.Exits != 96 || measured.Oracle.Tables != 172 {
		t.Fatalf("unexpected small-preset oracle: %+v", measured.Oracle)
	}
}

func TestSmallPresetSaveRoundTripsWithoutModifyingSharedInput(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the Lua CBOR codec")
	}

	input := generatedInput(t, "small")
	before, err := hashFile(input)
	if err != nil {
		t.Fatal(err)
	}
	measured, err := execute(options{
		mode: "save", measurement: "timing", preset: "small",
		fixture: fixturePath(t), data: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := hashFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || measured.InputSHA256 != before {
		t.Fatalf("shared input changed: before=%s after=%s recorded=%s", before, after, measured.InputSHA256)
	}
}

func TestCorruptInputIsRejected(t *testing.T) {
	input := filepath.Join(t.TempDir(), "corrupt.cbor")
	if err := os.WriteFile(input, []byte("not cbor"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := execute(options{mode: "load", measurement: "timing", preset: "small", fixture: fixturePath(t), data: input})
	if err == nil {
		t.Fatal("corrupt input was accepted")
	}
}

func TestTruncatedInputIsRejected(t *testing.T) {
	input := generatedInput(t, "small")
	encoded, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	truncated := filepath.Join(t.TempDir(), "truncated.cbor")
	if err := os.WriteFile(truncated, encoded[:len(encoded)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = execute(options{mode: "load", measurement: "timing", preset: "small", fixture: fixturePath(t), data: truncated})
	if err == nil {
		t.Fatal("truncated input was accepted")
	}
}

func TestDataIsRequired(t *testing.T) {
	_, err := validateOptions(options{mode: "load", measurement: "timing", preset: "small"})
	if err == nil {
		t.Fatal("missing shared input was accepted")
	}
}

func TestTimingMeasurementRejectsProfiles(t *testing.T) {
	_, err := validateOptions(options{
		mode: "load", measurement: "timing", preset: "small", data: "input.cbor",
		cpuProfile: filepath.Join(t.TempDir(), "cpu.pprof"),
	})
	if err == nil {
		t.Fatal("timing measurement accepted a CPU profile")
	}
}

func TestProfileMeasurementSeparatesCPUAndHeapProfiles(t *testing.T) {
	profileDir := t.TempDir()
	_, err := validateOptions(options{
		mode: "load", measurement: "profile", preset: "small", data: "input.cbor",
		cpuProfile:  filepath.Join(profileDir, "cpu.pprof"),
		heapProfile: filepath.Join(profileDir, "heap.pprof"),
	})
	if err == nil {
		t.Fatal("profile measurement accepted combined CPU and heap profiles")
	}
}

func TestGuardedMeasurementRecordsContextPolicy(t *testing.T) {
	input := generatedInput(t, "small")
	measured, err := execute(options{
		mode: "load", measurement: "timing", preset: "small", fixture: fixturePath(t), data: input,
		guarded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if measured.Execution != "guarded" || measured.ContextCheckInterval != 0 {
		t.Fatalf("context policy = %q/%d, want guarded/0", measured.Execution, measured.ContextCheckInterval)
	}
}

func TestConfigurableContextIntervalIsRejected(t *testing.T) {
	state, err := lua.NewState(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = state.Close()
	}()
	if err := state.ConfigureExecution(true, 256); err == nil {
		t.Fatal("runtime accepted an unsupported context polling interval")
	}
}

func TestRawMeasurementRejectsContextInterval(t *testing.T) {
	_, err := validateOptions(options{
		mode: "load", measurement: "timing", preset: "small", data: "input.cbor", contextCheckInterval: 256,
	})
	if err == nil {
		t.Fatal("raw measurement accepted a context-check interval")
	}
}

func TestInvalidPresetFailsBeforeStaging(t *testing.T) {
	_, err := validateOptions(options{mode: "load", measurement: "timing", preset: "unknown", data: "input.cbor"})
	if err == nil {
		t.Fatal("unknown preset was accepted")
	}
}

func generatedInput(t *testing.T, name string) string {
	t.Helper()
	preset, ok := fixture.Lookup(name)
	if !ok {
		t.Fatalf("unknown fixture preset %q", name)
	}
	path := filepath.Join(t.TempDir(), name+".cbor")
	if _, err := fixture.WriteCBOR(fixturePath(t), path, preset); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixturePath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test fixture")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "testdata"))
}
