// Command generate writes a deterministic public nested-graph CBOR corpus.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/mmcdole/lugo/benchmarks/cbor/internal/fixture"
)

func main() {
	presetName := flag.String("preset", "small", "public preset to generate: small or large")
	fixtureRoot := flag.String("fixture", "testdata", "directory containing cbor.lua")
	output := flag.String("output", "", "output CBOR file (required)")
	flag.Parse()

	if *output == "" {
		fmt.Fprintln(os.Stderr, "cbor-generate: -output is required")
		os.Exit(2)
	}
	preset, ok := fixture.Lookup(*presetName)
	if !ok {
		fmt.Fprintf(os.Stderr, "cbor-generate: invalid -preset %q: expected small or large\n", *presetName)
		os.Exit(2)
	}
	summary, err := fixture.WriteCBOR(*fixtureRoot, *output, preset)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cbor-generate:", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Preset string `json:"preset"`
		Output string `json:"output"`
		fixture.Summary
	}{Preset: preset.Name, Output: *output, Summary: summary}); err != nil {
		fmt.Fprintln(os.Stderr, "cbor-generate:", err)
		os.Exit(1)
	}
}
