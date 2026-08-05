// Command shapes measures retained heap for controlled table and string
// shapes. Each process builds one shape, anchors it in a global, and reports
// the live heap added after garbage collection, using the same stabilization
// protocol as the CBOR workload worker.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"time"

	lua "github.com/mmcdole/lunar/benchmarks/cbor/internal/luabridge"
)

// shape describes one controlled table topology. distinctKeys zero means
// every key in the shape is globally unique; a positive count means that many
// distinct keys are reused across every table, mirroring record field names.
type shape struct {
	name         string
	tables       int
	entries      int
	keyBytes     int
	distinctKeys int
	description  string
}

var shapes = []shape{
	{"tables-repeated-16", 25000, 4, 16, 4,
		"25,000 four-field tables, four 16-byte keys repeated throughout"},
	{"tables-unique-16", 25000, 4, 16, 0,
		"25,000 four-field tables, globally unique 16-byte keys"},
	{"tables-repeated-80", 25000, 4, 80, 4,
		"25,000 four-field tables, four 80-byte keys repeated throughout"},
	{"one-unique-16", 1, 100000, 16, 0,
		"one table, 100,000 unique 16-byte keys"},
	{"one-unique-64", 1, 100000, 64, 0,
		"one table, 100,000 unique 64-byte keys"},
	{"one-unique-256", 1, 100000, 256, 0,
		"one table, 100,000 unique 256-byte keys"},
	{"one-unique-1024", 1, 100000, 1024, 0,
		"one table, 100,000 unique 1,024-byte keys"},
}

type result struct {
	SchemaVersion   int    `json:"schema_version"`
	Case            string `json:"case"`
	Tables          int    `json:"tables"`
	EntriesPerTable int    `json:"entries_per_table"`
	KeyBytes        int    `json:"key_bytes"`
	DistinctKeys    int    `json:"distinct_keys"`
	ElapsedNS       int64  `json:"elapsed_ns"`
	HeapBefore      uint64 `json:"heap_before"`
	HeapRetained    uint64 `json:"heap_retained"`
	HeapDelta       int64  `json:"heap_delta"`
	TotalAllocDelta uint64 `json:"total_alloc_delta"`
	MallocsDelta    uint64 `json:"mallocs_delta"`
	GoVersion       string `json:"go_version"`
	GOOS            string `json:"goos"`
	GOARCH          string `json:"goarch"`
	Revision        string `json:"revision,omitempty"`
	RevisionDirty   bool   `json:"revision_modified,omitempty"`
	RuntimeVersion  string `json:"runtime_version"`
}

func main() {
	var caseName, format string
	var list bool
	flag.StringVar(&caseName, "case", "", "shape case to measure")
	flag.StringVar(&format, "format", "text", "output format: text or jsonl")
	flag.BoolVar(&list, "list", false, "print the available cases and exit")
	flag.Parse()

	if list {
		for _, s := range shapes {
			fmt.Printf("%-20s %s\n", s.name, s.description)
		}
		return
	}
	measured, err := execute(caseName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shapes:", err)
		os.Exit(1)
	}
	if err := emit(os.Stdout, format, measured); err != nil {
		fmt.Fprintln(os.Stderr, "shapes:", err)
		os.Exit(1)
	}
}

func lookup(name string) (shape, error) {
	for _, s := range shapes {
		if s.name == name {
			return s, nil
		}
	}
	return shape{}, fmt.Errorf("unknown -case %q; use -list", name)
}

func execute(caseName string) (result, error) {
	selected, err := lookup(caseName)
	if err != nil {
		return result{}, err
	}
	L, err := lua.NewState(lua.Options{})
	if err != nil {
		return result{}, fmt.Errorf("create Lua state: %w", err)
	}
	defer func() {
		_ = L.Close()
	}()

	// Repeated-key templates are byte slices so each insertion converts
	// through string([]byte), forcing a fresh backing array. Any retained
	// sharing is then attributable to the VM, never to the Go harness.
	templates := repeatedTemplates(selected)

	stabilizeHeap()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	started := time.Now()
	if err := build(L, selected, templates); err != nil {
		return result{}, err
	}
	elapsed := time.Since(started)

	var immediate runtime.MemStats
	runtime.ReadMemStats(&immediate)
	templates = nil
	stabilizeHeap()
	var retained runtime.MemStats
	runtime.ReadMemStats(&retained)

	// The verification walk runs only after every measured sample has been
	// captured, so its allocations never contaminate the result.
	if err := verify(L, selected); err != nil {
		return result{}, err
	}

	revision, dirty := buildRevision()
	return result{
		SchemaVersion: 1,
		Case:          selected.name,
		Tables:        selected.tables, EntriesPerTable: selected.entries,
		KeyBytes: selected.keyBytes, DistinctKeys: selected.distinctKeys,
		ElapsedNS:       elapsed.Nanoseconds(),
		HeapBefore:      before.HeapAlloc,
		HeapRetained:    retained.HeapAlloc,
		HeapDelta:       int64(retained.HeapAlloc) - int64(before.HeapAlloc),
		TotalAllocDelta: immediate.TotalAlloc - before.TotalAlloc,
		MallocsDelta:    immediate.Mallocs - before.Mallocs,
		GoVersion:       runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Revision: revision, RevisionDirty: dirty,
		RuntimeVersion: L.RuntimeVersion(),
	}, nil
}

func build(L *lua.State, selected shape, templates [][]byte) error {
	anchor, err := L.NewTable(0, 0)
	if err != nil {
		return fmt.Errorf("create anchor: %w", err)
	}
	truth := lua.Bool(true)
	index := 0
	for t := 0; t < selected.tables; t++ {
		table, err := L.NewTable(0, 0)
		if err != nil {
			return fmt.Errorf("create table %d: %w", t, err)
		}
		for e := 0; e < selected.entries; e++ {
			var key string
			if selected.distinctKeys > 0 {
				key = string(templates[index%selected.distinctKeys])
			} else {
				key = string(uniqueKey(selected.name, index, selected.keyBytes))
			}
			if err := lua.TableRawSetString(table, key, truth); err != nil {
				return fmt.Errorf("set entry %d: %w", index, err)
			}
			index++
		}
		if err := lua.TableRawSetInt(anchor, t+1, lua.TableValue(table)); err != nil {
			return fmt.Errorf("anchor table %d: %w", t, err)
		}
	}
	return L.SetGlobal("SHAPE_ROOT", lua.TableValue(anchor))
}

func repeatedTemplates(selected shape) [][]byte {
	if selected.distinctKeys == 0 {
		return nil
	}
	templates := make([][]byte, selected.distinctKeys)
	for i := range templates {
		templates[i] = uniqueKey(selected.name+"/repeated", i, selected.keyBytes)
	}
	return templates
}

// uniqueKey derives keyBytes of hex text from chained SHA-256 blocks, so the
// distinguishing bytes are spread across the whole key rather than confined
// to a counter suffix.
func uniqueKey(salt string, index, keyBytes int) []byte {
	out := make([]byte, 0, keyBytes+sha256.Size*2)
	for block := 0; len(out) < keyBytes; block++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", salt, index, block)))
		var encoded [sha256.Size * 2]byte
		hex.Encode(encoded[:], sum[:])
		out = append(out, encoded[:]...)
	}
	return out[:keyBytes]
}

func verify(L *lua.State, selected shape) error {
	rootValue, err := L.Global("SHAPE_ROOT")
	if err != nil {
		return err
	}
	root, ok := lua.ValueTable(rootValue)
	if !ok {
		return fmt.Errorf("SHAPE_ROOT is %s, not a table", lua.ValueTypeName(rootValue))
	}
	// The Lunar adapter rejects nested traversal, so anchored tables are
	// collected first and walked one at a time.
	var anchored []*lua.Table
	if err := L.ForEach(root, func(_, value lua.Value) error {
		table, ok := lua.ValueTable(value)
		if !ok {
			return fmt.Errorf("anchored value is %s, not a table", lua.ValueTypeName(value))
		}
		anchored = append(anchored, table)
		return nil
	}); err != nil {
		return err
	}
	tables, entries, keyBytes := len(anchored), 0, 0
	var keys []string
	sampleKeys := selected.distinctKeys > 0
	for position, table := range anchored {
		if err := L.ForEach(table, func(key, entry lua.Value) error {
			text, ok := lua.ValueString(key)
			if !ok {
				return fmt.Errorf("key is %s, not a string", lua.ValueTypeName(key))
			}
			truth, ok := lua.ValueBool(entry)
			if !ok || !truth {
				return fmt.Errorf("entry value is not boolean true")
			}
			entries++
			keyBytes += len(text)
			if sampleKeys && position == 0 {
				keys = append(keys, text)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	expectedEntries := selected.tables * selected.entries
	if tables != selected.tables || entries != expectedEntries ||
		keyBytes != expectedEntries*selected.keyBytes {
		return fmt.Errorf(
			"shape mismatch: got tables=%d entries=%d key_bytes=%d; want tables=%d entries=%d key_bytes=%d",
			tables, entries, keyBytes,
			selected.tables, expectedEntries, expectedEntries*selected.keyBytes)
	}
	if sampleKeys {
		sort.Strings(keys)
		if len(keys) != selected.distinctKeys {
			return fmt.Errorf("first table holds %d keys; want %d", len(keys), selected.distinctKeys)
		}
		for i := 1; i < len(keys); i++ {
			if keys[i] == keys[i-1] {
				return fmt.Errorf("repeated-key case produced duplicate field names")
			}
		}
	}
	return nil
}

func emit(w io.Writer, format string, measured result) error {
	switch format {
	case "jsonl":
		return json.NewEncoder(w).Encode(measured)
	case "text":
		_, err := fmt.Fprintf(w,
			"case=%s tables=%d entries_per_table=%d key_bytes=%d distinct_keys=%d elapsed=%s heap_before=%d heap_retained=%d heap_delta=%d total_alloc_delta=%d mallocs_delta=%d runtime=%q\n",
			measured.Case, measured.Tables, measured.EntriesPerTable,
			measured.KeyBytes, measured.DistinctKeys,
			time.Duration(measured.ElapsedNS),
			measured.HeapBefore, measured.HeapRetained, measured.HeapDelta,
			measured.TotalAllocDelta, measured.MallocsDelta,
			measured.RuntimeVersion)
		return err
	default:
		return fmt.Errorf("invalid -format %q: expected text or jsonl", format)
	}
}

func stabilizeHeap() {
	runtime.GC()
	runtime.GC()
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
