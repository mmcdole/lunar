package lua_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	lua "github.com/mmcdole/badger-lua"
)

func TestFriendlyObjectInterface(t *testing.T) {
	state, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	config, err := state.NewTable(0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.RawSetString("host", state.String("aardmud.org")); err != nil {
		t.Fatal(err)
	}
	if err := config.RawSetString("port", lua.Number(4000)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("config", config.Value()); err != nil {
		t.Fatal(err)
	}

	global, err := state.Global("config")
	if err != nil {
		t.Fatal(err)
	}
	sameTable, ok := global.Table()
	if !ok || sameTable != config {
		t.Fatalf("global table = (%p, %v), want %p", sameTable, ok, config)
	}
	if host, ok := config.RawGetString("host").AsString(); !ok || host != "aardmud.org" {
		t.Fatalf("host = (%q, %v)", host, ok)
	}
	if port, ok := config.RawGetString("port").AsNumber(); !ok || port != 4000 {
		t.Fatalf("port = (%v, %v)", port, ok)
	}
}

func TestZeroPublicObjectsAreNotCanonical(t *testing.T) {
	if (lua.Value{}).Valid() {
		t.Fatal("zero Value must be invalid")
	}
	if (&lua.Table{}).Value().Valid() {
		t.Fatal("zero Table must not manufacture a valid canonical identity")
	}
	if (&lua.UserData{}).Value().Valid() {
		t.Fatal("zero UserData must not manufacture a valid canonical identity")
	}
	if (&lua.Thread{}).Value().Valid() {
		t.Fatal("zero Thread must not manufacture a valid canonical identity")
	}
	if (&lua.Function{}).Value().Valid() {
		t.Fatal("zero Function must not manufacture a valid canonical identity")
	}
}

func TestPublicBaseLibraryProtectedCalls(t *testing.T) {
	state, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	chunk, err := state.LoadString("@public-base.lua", `
local ok, value = pcall(function() return 42 end)
return ok, value
`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || !results[0].Truth() {
		t.Fatalf("pcall results = %v", results)
	}
	number, ok := results[1].AsNumber()
	if !ok || number != 42 {
		t.Fatalf("pcall value = (%v, %v); want 42", number, ok)
	}
}

func TestPublicReaderAndFileLoading(t *testing.T) {
	state, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	fromReader, err := state.Load(
		"@reader.lua",
		strings.NewReader(`return 41`),
	)
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(fromReader.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("Reader results = %v", results)
	}
	number, ok := results[0].AsNumber()
	if !ok || number != 41 {
		t.Fatalf("Reader value = (%v, %v); want 41", number, ok)
	}

	path := filepath.Join(t.TempDir(), "public.lua")
	if err := os.WriteFile(
		path,
		[]byte("#!/usr/bin/env lua\nreturn 42"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	fromFile, err := state.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	results, err = state.Call(fromFile.Value())
	if err != nil {
		t.Fatal(err)
	}
	number, ok = results[0].AsNumber()
	if !ok || number != 42 {
		t.Fatalf("file value = (%v, %v); want 42", number, ok)
	}
}
