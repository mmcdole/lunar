//go:build windows

package lua

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	windowsPopenCopyHelperEnv = "LUNAR_TEST_WINDOWS_POPEN_COPY"
	windowsPopenCopyOutputEnv = "LUNAR_TEST_WINDOWS_POPEN_COPY_OUTPUT"
)

func TestIOPopenWindowsCopyHelper(t *testing.T) {
	if os.Getenv(windowsPopenCopyHelperEnv) != "1" {
		return
	}
	output, err := os.Create(os.Getenv(windowsPopenCopyOutputEnv))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, os.Stdin); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOSExecuteWindowsStatusAndShellFallback(t *testing.T) {
	state := newStateWithOS(t)
	defer state.Close()
	results, err := state.Call(mustLoadString(
		t,
		state,
		"@execute-windows.lua",
		`return os.execute("exit /b 0"),
  os.execute("exit /b 7"),
  os.execute("exit /b -1")`,
	).Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Number(0),
		Number(7),
		Number(-1),
	)

	t.Setenv(
		"COMSPEC",
		filepath.Join(t.TempDir(), "missing-cmd.exe"),
	)
	if !hostShellAvailable() {
		t.Fatal("cmd.exe PATH fallback is unavailable")
	}
}

func TestIOPopenWindowsReadAndCloseStatus(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()

	results := runIOChunk(t, state, `
local reader=assert(io.popen("echo abc","r"))
local text=reader:read("*a")
local readClose=reader:close()
local exited=assert(io.popen("exit /b 7","r"))
local exitClose=exited:close()
return text,readClose,exitClose
`)
	text, ok := results[0].AsString()
	if !ok || strings.TrimSpace(text) != "abc" {
		t.Fatalf("Windows popen output = %q", text)
	}
	assertTestValues(
		t,
		results[1:],
		Bool(true),
		Bool(true),
	)
}

func TestIOPopenWindowsWriteClosesBeforeWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "popen-output")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(windowsPopenCopyHelperEnv, "1")
	t.Setenv(windowsPopenCopyOutputEnv, path)
	command := `"` + executable +
		`" -test.run=TestIOPopenWindowsCopyHelper`
	state := newStateWithIO(t, Options{})
	defer state.Close()

	results := runIOChunk(t, state, `
local writer=assert(io.popen(`+luaTestQuote(command)+`,"w"))
local written=writer:write("first\nsecond\n")
local closed=writer:close()
return written,closed
`)
	assertTestValues(t, results, Bool(true), Bool(true))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first\nsecond\n" {
		t.Fatalf("Windows popen input = %q", content)
	}
}
