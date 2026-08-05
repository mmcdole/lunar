package main

import (
	"testing"

	lua "github.com/mmcdole/lunar/benchmarks/cbor/internal/luabridge"
)

func TestUniqueKeysHaveRequestedLengthAndDiffer(t *testing.T) {
	for _, length := range []int{16, 64, 65, 80, 256, 1024} {
		seen := make(map[string]bool)
		for index := 0; index < 200; index++ {
			key := string(uniqueKey("test", index, length))
			if len(key) != length {
				t.Fatalf("length %d index %d: got %d bytes", length, index, len(key))
			}
			if seen[key] {
				t.Fatalf("length %d: duplicate key at index %d", length, index)
			}
			seen[key] = true
		}
	}
}

func TestUniquenessIsNotConfinedToASuffix(t *testing.T) {
	first := uniqueKey("test", 1, 64)
	second := uniqueKey("test", 2, 64)
	if string(first[:32]) == string(second[:32]) {
		t.Fatalf("adjacent keys share their leading half: %q", first[:32])
	}
}

func TestBuildAndVerifyRoundTrip(t *testing.T) {
	cases := []shape{
		{"test-repeated", 8, 4, 16, 4, ""},
		{"test-unique", 3, 10, 32, 0, ""},
	}
	for _, selected := range cases {
		L, err := lua.NewState(lua.Options{})
		if err != nil {
			t.Fatalf("%s: create state: %v", selected.name, err)
		}
		if err := build(L, selected, repeatedTemplates(selected)); err != nil {
			t.Fatalf("%s: build: %v", selected.name, err)
		}
		if err := verify(L, selected); err != nil {
			t.Fatalf("%s: verify: %v", selected.name, err)
		}
		if err := L.Close(); err != nil {
			t.Fatalf("%s: close: %v", selected.name, err)
		}
	}
}

func TestVerifyRejectsAMissingEntry(t *testing.T) {
	selected := shape{"test-short", 2, 3, 16, 0, ""}
	L, err := lua.NewState(lua.Options{})
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() {
		_ = L.Close()
	}()
	if err := build(L, selected, nil); err != nil {
		t.Fatalf("build: %v", err)
	}
	selected.entries = 4
	if err := verify(L, selected); err == nil {
		t.Fatal("verify accepted a shape with missing entries")
	}
}

func TestEveryPublishedCaseIsResolvable(t *testing.T) {
	for _, s := range shapes {
		resolved, err := lookup(s.name)
		if err != nil {
			t.Fatalf("lookup %s: %v", s.name, err)
		}
		if resolved != s {
			t.Fatalf("lookup %s returned a different case", s.name)
		}
		if s.distinctKeys > 0 && s.distinctKeys > s.tables*s.entries {
			t.Fatalf("%s: more distinct keys than entries", s.name)
		}
	}
}
