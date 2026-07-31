package lua_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmcdole/lunar"
)

func TestFriendlyObjectInterface(t *testing.T) {
	state, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	config, err := state.NewTableWithCapacity(0, 4)
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
	sameTable, ok := global.AsTable()
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

func TestPublicUserDataRoundTripKeepsIdentityAndOwnership(t *testing.T) {
	state, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}

	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("data", data.Value()); err != nil {
		t.Fatal(err)
	}
	global, err := state.Global("data")
	if err != nil {
		t.Fatal(err)
	}
	published, ok := global.AsUserData()
	if !ok || published != data {
		t.Fatalf(
			"global userdata = (%p, %v); want (%p, true)",
			published,
			ok,
			data,
		)
	}
	if same, applicable := global.SameObject(data.Value()); !applicable || !same {
		t.Fatalf(
			"userdata identity = (%v, %v); want (true, true)",
			same,
			applicable,
		)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if payload := published.Data(); payload != "payload" {
		t.Fatalf("post-close payload = %v; want payload", payload)
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
	state, err := lua.New(lua.Options{Source: lua.OSSource()})
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

func TestPublicStandardStreams(t *testing.T) {
	var output, diagnostic bytes.Buffer
	state, err := lua.New(lua.Options{
		Source: lua.OSSource(),
		Stdin:  strings.NewReader(`return 41`),
		Stdout: &output,
		Stderr: &diagnostic,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	chunk, err := state.LoadString(
		"@standard-streams.lua",
		`local loaded=assert(loadfile()); print(loaded())`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(chunk.Value()); err != nil {
		t.Fatal(err)
	}
	if output.String() != "41\n" {
		t.Fatalf("standard output = %q", output.String())
	}
	if diagnostic.Len() != 0 {
		t.Fatalf("standard error = %q; want empty", diagnostic.String())
	}
}

func TestPublicPackageLoadingAndOrdinaryNativeAssignment(t *testing.T) {
	state, err := lua.New(lua.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenPackage(); err != nil {
		t.Fatal(err)
	}

	target, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	assign, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
		table, _ := frame.Argument(0)
		key, _ := frame.Argument(1)
		value, _ := frame.Argument(2)
		if err := frame.SetIndex(table, key, value); err != nil {
			frame.ThrowString(err.Error())
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(
		assign.Value(),
		target.Value(),
		state.String("answer"),
		lua.Number(42),
	); err != nil {
		t.Fatal(err)
	}
	value := target.RawGetString("answer")
	number, ok := value.AsNumber()
	if !ok || number != 42 {
		t.Fatalf("assigned value = %v; want 42", value)
	}

	if err := state.PreloadModule("host.module", func(frame lua.Frame) lua.Outcome {
		return frame.ReturnString("loaded")
	}); err != nil {
		t.Fatal(err)
	}
	require, err := state.Global("require")
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(require, state.String("host.module"))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("require results = %v; want one value", results)
	}
	text, ok := results[0].AsString()
	if !ok || text != "loaded" {
		t.Fatalf("required value = (%q, %v); want loaded", text, ok)
	}
}
