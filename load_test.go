package lua

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateDoStringReturnsOwnedResults(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	results, err := state.DoString(
		"@values.lua",
		`return nil, true, 42, "lunar"`,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Nil(),
		Bool(true),
		Number(42),
		state.String("lunar"),
	)

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Nil(),
		Bool(true),
		Number(42),
		state.String("lunar"),
	)
}

func TestStateDoStringPreservesLoadAndExecutionErrors(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	if results, doErr := state.DoString(
		"@broken.lua",
		`local =`,
	); results != nil || doErr == nil {
		t.Fatalf("syntax result = (%v, %v)", results, doErr)
	} else {
		var failure *Error
		if !errors.As(doErr, &failure) ||
			failure.Category() != SyntaxError ||
			!strings.Contains(failure.Error(), "broken.lua") {
			t.Fatalf("syntax error = %T %v", doErr, doErr)
		}
	}

	if results, doErr := state.DoString(
		"@runtime.lua",
		`error("failure")`,
	); results != nil || doErr == nil {
		t.Fatalf("runtime result = (%v, %v)", results, doErr)
	} else {
		var failure *Error
		if !errors.As(doErr, &failure) ||
			failure.Category() != RuntimeError ||
			!strings.Contains(failure.Error(), "runtime.lua") {
			t.Fatalf("runtime error = %T %v", doErr, doErr)
		}
	}
}

func TestStateDoFileReturnsResults(t *testing.T) {
	state, err := New(Options{ScriptLoader: HostLoader()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	path := filepath.Join(t.TempDir(), "answer.lua")
	if err := os.WriteFile(
		path,
		[]byte("#!/usr/bin/env lua\nreturn 6 * 7"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	results, err := state.DoFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))
}

func TestStateDoContextCoversLoadingAndExecution(t *testing.T) {
	state, err := New(Options{ScriptLoader: HostLoader()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	type contextKey struct{}
	ctx := context.WithValue(
		context.Background(),
		contextKey{},
		"request",
	)
	var seen context.Context
	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		seen = frame.Context()
		return frame.ReturnNumber(42)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("probe", probe.Value()); err != nil {
		t.Fatal(err)
	}

	results, err := doStringWithContext(t, state, ctx, "@context.lua",
		`return probe()`,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))
	if seen != ctx || seen.Value(contextKey{}) != "request" {
		t.Fatalf("callback context = %v; want supplied context", seen)
	}

	path := filepath.Join(t.TempDir(), "context.lua")
	if err := os.WriteFile(path, []byte(`return 1`), 0o600); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if results, doErr := doFileWithContext(t, state, cancelled, path); results != nil || doErr == nil {
		t.Fatalf("cancelled file result = (%v, %v)", results, doErr)
	} else {
		assertContextFailure(
			t,
			doErr,
			context.Canceled,
			context.Canceled,
			"context canceled",
		)
	}
}

func doStringWithContext(
	t testing.TB,
	state *State,
	ctx context.Context,
	sourceName string,
	source string,
) (results []Value, err error) {
	t.Helper()
	if setErr := state.SetContext(ctx); setErr != nil {
		t.Fatalf("SetContext: %v", setErr)
	}
	defer func() { _ = state.RemoveContext() }()
	return state.DoString(sourceName, source)
}

func doFileWithContext(
	t testing.TB,
	state *State,
	ctx context.Context,
	path string,
) (results []Value, err error) {
	t.Helper()
	if setErr := state.SetContext(ctx); setErr != nil {
		t.Fatalf("SetContext: %v", setErr)
	}
	defer func() { _ = state.RemoveContext() }()
	return state.DoFile(path)
}
