package lua

import "testing"

func TestNewTableFromConvertsNestedMapAndSequence(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	owned, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	config, err := state.NewTableFrom(map[string]any{
		"enabled": true,
		"payload": []byte{'a', 0, 'b'},
		"filters": []any{
			"combat",
			nil,
			map[string]any{"name": "chat"},
		},
		"owned": owned.Value(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if enabled, ok := config.RawGetString("enabled").AsBool(); !ok || !enabled {
		t.Fatalf("enabled = (%v, %v); want true", enabled, ok)
	}
	if payload, ok := config.RawGetString("payload").AsString(); !ok ||
		payload != "a\x00b" {
		t.Fatalf("payload = (%q, %v); want binary string", payload, ok)
	}
	filters, ok := config.RawGetString("filters").AsTable()
	if !ok {
		t.Fatal("filters is not a table")
	}
	if first, ok := filters.RawGetInt(1).AsString(); !ok || first != "combat" {
		t.Fatalf("filters[1] = (%q, %v); want combat", first, ok)
	}
	if !filters.RawGetInt(2).IsNil() {
		t.Fatalf("filters[2] = %v; want nil", filters.RawGetInt(2))
	}
	nested, ok := filters.RawGetInt(3).AsTable()
	if !ok {
		t.Fatal("filters[3] is not a table")
	}
	if name, ok := nested.RawGetString("name").AsString(); !ok || name != "chat" {
		t.Fatalf("filters[3].name = (%q, %v); want chat", name, ok)
	}
	if same, applicable := config.RawGetString("owned").SameObject(
		owned.Value(),
	); !applicable || !same {
		t.Fatalf("owned identity = (%v, %v); want (true, true)", same, applicable)
	}
}
