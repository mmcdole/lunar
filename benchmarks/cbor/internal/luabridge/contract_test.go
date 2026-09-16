package luabridge

import (
	"errors"
	"testing"
)

func TestForEachVisitsNestedTablesAndStopsOnError(t *testing.T) {
	state, err := NewState(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.DoString("@iteration.lua", `items = {first = {value = 1}, second = {value = 2}}`); err != nil {
		t.Fatal(err)
	}
	value, err := state.Global("items")
	if err != nil {
		t.Fatal(err)
	}
	table, ok := ValueTable(value)
	if !ok {
		t.Fatal("items is not a table")
	}
	seen := make(map[string]float64)
	err = state.ForEach(table, func(key, value Value) error {
		name, ok := ValueString(key)
		if !ok {
			t.Fatal("unexpected key type")
		}
		child, ok := ValueTable(value)
		if !ok {
			t.Fatal("unexpected value type")
		}
		return state.ForEach(child, func(_, value Value) error {
			number, ok := ValueNumber(value)
			if !ok {
				t.Fatal("nested value is not a number")
			}
			if _, exists := seen[name]; exists {
				t.Fatal("visited a field twice")
			}
			seen[name] = number
			return nil
		})
	})
	if err != nil || len(seen) != 2 || seen["first"] != 1 || seen["second"] != 2 {
		t.Fatalf("nested traversal = %v, %v", seen, err)
	}
	stop := errors.New("stop traversal")
	visits := 0
	err = state.ForEach(table, func(_, _ Value) error {
		visits++
		return stop
	})
	if !errors.Is(err, stop) || visits != 1 {
		t.Fatalf("early stop = %v after %d visits", err, visits)
	}
}

func TestCallOneAppliesLuaFixedResultAdjustment(t *testing.T) {
	state, err := NewState(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.DoString("@call-one-contract.lua", `
function no_results(_) end
function many_results(_) return 41, 42 end
`); err != nil {
		t.Fatal(err)
	}

	noResults, err := state.Global("no_results")
	if err != nil {
		t.Fatal(err)
	}
	result, err := state.CallOne(noResults, Number(0))
	if err != nil {
		t.Fatal(err)
	}
	if kind := ValueKind(result); kind != NilKind {
		t.Fatalf("zero-result adjustment kind = %v; want nil", kind)
	}

	manyResults, err := state.Global("many_results")
	if err != nil {
		t.Fatal(err)
	}
	result, err = state.CallOne(manyResults, Number(0))
	if err != nil {
		t.Fatal(err)
	}
	if number, ok := ValueNumber(result); !ok || number != 41 {
		t.Fatalf("many-result adjustment = (%v, %v); want 41", number, ok)
	}
}
