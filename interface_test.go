package lua_test

import (
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
