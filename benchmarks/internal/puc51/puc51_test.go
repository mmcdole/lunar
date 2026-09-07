//go:build puc51 && cgo

package puc51

import (
	"strings"
	"testing"
)

func TestPreparedFunctionStaysReferenced(t *testing.T) {
	state, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	if err := state.Prepare("@cached.lua", `
local count = 0
function benchmark()
  benchmark = nil
  count = count + 1
  result = count
  text = "a\000b"
end
`, "benchmark"); err != nil {
		t.Fatal(err)
	}
	for want := 1; want <= 3; want++ {
		if err := state.Run(); err != nil {
			t.Fatal(err)
		}
		if got, err := state.GlobalNumber("result"); err != nil || got != float64(want) {
			t.Fatalf("result = %v, %v; want %d", got, err, want)
		}
		if got, err := state.GlobalString("text"); err != nil || got != "a\x00b" {
			t.Fatalf("text = %q, %v; want embedded NUL", got, err)
		}
	}
}

func TestProtectedCallRecovers(t *testing.T) {
	state, err := New(Base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	if err := state.Prepare("@error.lua", `
local count = 0
function benchmark()
  count = count + 1
  if count == 1 then error("expected failure") end
  result = count
end
`, "benchmark"); err != nil {
		t.Fatal(err)
	}
	if err := state.Run(); err == nil || !strings.Contains(err.Error(), "expected failure") {
		t.Fatalf("first call error = %v", err)
	}
	if err := state.Run(); err != nil {
		t.Fatal(err)
	}
	if got, err := state.GlobalNumber("result"); err != nil || got != 2 {
		t.Fatalf("result = %v, %v; want 2", got, err)
	}
}
