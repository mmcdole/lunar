//go:build windows

package lua

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	path := t.TempDir() + `\popen-output`
	command := `more > "` + path + `"`
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
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if text != "first\nsecond\n" {
		t.Fatalf("Windows popen input = %q", content)
	}
}
