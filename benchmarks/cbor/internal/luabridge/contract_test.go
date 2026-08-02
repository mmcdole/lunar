package luabridge

import "testing"

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
