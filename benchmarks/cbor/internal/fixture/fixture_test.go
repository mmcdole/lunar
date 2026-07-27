package fixture

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLargePresetHasPublishedStructuralScale(t *testing.T) {
	preset, ok := Lookup("large")
	if !ok {
		t.Fatal("large preset is missing")
	}
	want := (Summary{Areas: 341, Rooms: 36_705, Exits: 109_742, Tables: 183_513, Entries: 938_452})
	if got := preset.Expected(); got != want {
		t.Fatalf("large summary = %+v, want %+v", got, want)
	}
}

func TestPresetRecordShapesCoverAllRecords(t *testing.T) {
	for name, preset := range presets {
		records := preset.Areas + preset.Rooms + preset.Exits
		shapes := preset.FourFields + preset.TenFields + preset.SixFields + preset.SevenFields + preset.EightFields
		if shapes != records {
			t.Errorf("%s has %d records but %d shapes", name, records, shapes)
		}
	}
}

func TestSmallPresetEncodingIsDeterministic(t *testing.T) {
	preset, _ := Lookup("small")
	directory := t.TempDir()
	first := filepath.Join(directory, "first.dat")
	second := filepath.Join(directory, "second.dat")
	codecRoot := fixturePath(t)
	if _, err := WriteCBOR(codecRoot, first, preset); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteCBOR(codecRoot, second, preset); err != nil {
		t.Fatal(err)
	}
	firstData, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstData, secondData) {
		t.Fatal("small preset produced different CBOR bytes")
	}
	if len(firstData) != 8_011 {
		t.Fatalf("small preset encoded %d bytes, want 8011", len(firstData))
	}
}

func fixturePath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate codec fixture")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "testdata"))
}
