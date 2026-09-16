package lua

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
	"weak"
)

func TestCanonicalObjectsAndOwnership(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	table, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	tableValue := table.Value()
	gotTable, ok := tableValue.AsTable()
	if !ok || gotTable != table {
		t.Fatalf("table round trip = (%p, %v), want %p", gotTable, ok, table)
	}
	if same, applicable := tableValue.SameObject(table.Value()); !applicable || !same {
		t.Fatalf("SameObject = (%v, %v), want (true, true)", same, applicable)
	}

	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	gotData, ok := data.Value().AsUserData()
	if !ok || gotData != data || gotData.Data() != "payload" {
		t.Fatalf("userdata round trip failed")
	}

	main := state.MainThread()
	gotThread, ok := main.Value().AsThread()
	if !ok || gotThread != main || !main.IsMain() || main.State() != state {
		t.Fatal("main thread is not canonical")
	}

	runtime.GC()
	if got, ok := tableValue.AsTable(); !ok || got != table {
		t.Fatal("Value did not retain its canonical object across GC")
	}

	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherTable, err := other.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString("foreign", otherTable.Value()); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign table error = %v, want ErrForeignValue", err)
	}
	shared := other.String("x")
	if err := table.RawSetString("shared-string", shared); err != nil {
		t.Fatalf("state-neutral string rejected: %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if got := rawStr(table, "shared-string"); got.String() != "x" {
		t.Fatalf("state-neutral string = %v, want x", got)
	}
	if equal, err := state.RawEqual(shared, state.String("x")); err != nil || !equal {
		t.Fatalf("state-neutral equality after origin close = (%v, %v)", equal, err)
	}
	if err := table.RawSetString("number", Number(4)); err != nil {
		t.Fatalf("state-independent scalar rejected: %v", err)
	}
}

func TestUserDataOwningHandleRepresentation(t *testing.T) {
	if unsafe.Sizeof(UserData{}) != unsafe.Sizeof(hostToken{}) {
		t.Fatalf(
			"UserData size = %d; host token size = %d",
			unsafe.Sizeof(UserData{}),
			unsafe.Sizeof(hostToken{}),
		)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	token := data.token()
	if unsafe.Pointer(data) != unsafe.Pointer(token) {
		t.Fatal("UserData is not an offset-zero host-token view")
	}
	object := data.runtimeObject()
	if object == nil {
		t.Fatal("userdata handle has no compact object")
	}
	key := weak.Make(&object.objectHeader)
	if state.runtime.hosts.entries[key].Value() != token {
		t.Fatal("userdata object does not have its live token in the directory")
	}

	public := data.Value()
	compact := slotFromValue(public)
	if compact.ref != unsafe.Pointer(object) {
		t.Fatal("compact userdata slot does not point directly at its object")
	}
	if compact.ref == public.ref {
		t.Fatal("public userdata Value exposed the compact object pointer")
	}

	table, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString("data", public); err != nil {
		t.Fatal(err)
	}
	fromTable, ok := rawStr(table, "data").AsUserData()
	if !ok || fromTable != data {
		t.Fatalf(
			"re-published userdata = (%p, %v); want (%p, true)",
			fromTable,
			ok,
			data,
		)
	}
	runtime.KeepAlive(data)
}

func TestTableOwningHandleRepresentation(t *testing.T) {
	if unsafe.Sizeof(Table{}) != unsafe.Sizeof(hostToken{}) {
		t.Fatalf(
			"Table size = %d; host token size = %d",
			unsafe.Sizeof(Table{}),
			unsafe.Sizeof(hostToken{}),
		)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	token := table.token()
	if unsafe.Pointer(table) != unsafe.Pointer(token) {
		t.Fatal("Table is not an offset-zero host-token view")
	}
	object := table.runtimeObject()
	if object == nil {
		t.Fatal("table handle has no compact object")
	}
	if unsafe.Pointer(&object.objectHeader) != unsafe.Pointer(object) {
		t.Fatal("table object header is not at offset zero")
	}
	key := weak.Make(&object.objectHeader)
	if state.runtime.hosts.entries[key].Value() != token {
		t.Fatal("table object does not have its live token in the directory")
	}

	public := table.Value()
	compact := slotFromValue(public)
	if compact.ref != unsafe.Pointer(object) {
		t.Fatal("compact table slot does not point directly at its object")
	}
	if compact.ref == public.ref {
		t.Fatal("public table Value exposed the compact object pointer")
	}
	published, ok := compact.owningValue().AsTable()
	if !ok || published != table {
		t.Fatalf(
			"re-published table = (%p, %v); want (%p, true)",
			published,
			ok,
			table,
		)
	}
	runtime.KeepAlive(table)
}

func TestWarmTablePublicationDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	object := newTable(state, 0, 0)
	first := object.owningHandle()
	compact := slotFromTableObject(object)

	var published *Table
	allocations := testing.AllocsPerRun(1_000, func() {
		value := compact.owningValue()
		published, _ = value.AsTable()
	})
	if allocations != 0 {
		t.Fatalf(
			"warm table publication allocated %.2f times",
			allocations,
		)
	}
	if published != first {
		t.Fatalf(
			"warm table publication = %p; want %p",
			published,
			first,
		)
	}
	runtime.KeepAlive(first)
}

func TestTableRepublishAfterOwningTokenDies(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	object, token := rootedTableWithoutHandle(t, state)
	index := newTable(state, 0, 1)
	index.rawSetSlot(slotFromTableObject(object), numberSlot(91))
	waitForWeakTableToken(t, object, token)

	first, ok := state.registry.rawGetStringValue("rooted table").AsTable()
	if !ok || first.runtimeObject() != object {
		t.Fatal("re-publication changed compact table identity")
	}
	second, ok := state.registry.rawGetStringValue("rooted table").AsTable()
	if !ok || second != first {
		t.Fatalf(
			"second re-publication = (%p, %v); want (%p, true)",
			second,
			ok,
			first,
		)
	}
	stored, err := index.rawGetValue(first.Value())
	if err != nil {
		t.Fatal(err)
	}
	if number, ok := stored.AsNumber(); !ok || number != 91 {
		t.Fatalf(
			"table-key lookup after token replacement = (%v, %v); want 91",
			number,
			ok,
		)
	}
	entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		TableKind,
	)
	if entries != 1 || keys != 1 || stale != 0 {
		t.Fatalf(
			"table directory = entries:%d keys:%d stale:%d; want 1/1/0",
			entries,
			keys,
			stale,
		)
	}
	runtime.KeepAlive(first)
}

func TestHostDirectoryDoesNotPinCyclicTable(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	object, token := weakTablePublication(t, state)
	waitForWeakTable(t, state, object, token)
	state.runtime.hosts.prune()
	entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		TableKind,
	)
	if entries != 0 || keys != 0 || stale != 0 {
		t.Fatalf(
			"dead table remains in host directory: entries=%d keys=%d stale=%d",
			entries,
			keys,
			stale,
		)
	}
	runtime.KeepAlive(state)
}

func TestTableHandleSupportsNestedPublicationAfterClose(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	outer, err := state.NewTableWithCapacity(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	outerObject := outer.runtimeObject()
	inner := newTable(state, 0, 1)
	inner.rawSetIntegerSlot(1, numberSlot(17))
	outerObject.rawSetIntegerSlot(1, numberSlot(11))
	if err := outerObject.rawSetStringSlot(
		"inner",
		slotFromTableObject(inner),
	); err != nil {
		t.Fatal(err)
	}
	longText := strings.Repeat("post-close-string-", 8)
	if err := outerObject.rawSetStringSlot(
		"long",
		stringSlot(state.runtime.strings.make(longText)),
	); err != nil {
		t.Fatal(err)
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if rawLen(outer) != 1 {
		t.Fatalf("post-close outer length = %d; want 1", rawLen(outer))
	}
	if number, ok := rawInt(outer, 1).AsNumber(); !ok || number != 11 {
		t.Fatalf("post-close scalar = (%v, %v); want 11", number, ok)
	}
	first, ok := rawStr(outer, "inner").AsTable()
	if !ok || first.runtimeObject() != inner {
		t.Fatal("post-close nested table was not published")
	}
	second, ok := rawStr(outer, "inner").AsTable()
	if !ok || second != first {
		t.Fatal("post-close nested table publication was not canonical")
	}
	if text, ok := rawStr(outer, "long").AsString(); !ok ||
		text != longText {
		t.Fatalf("post-close string = (%q, %v)", text, ok)
	}
	if state.runtime.collection.attributedStrings != nil ||
		state.runtime.collection.attributedStringHighWater != 0 {
		t.Fatal("post-close string read recreated collection attribution")
	}
	if err := outer.RawSetString("blocked", Bool(true)); !errors.Is(
		err,
		ErrClosed,
	) {
		t.Fatalf("post-close mutation = %v; want ErrClosed", err)
	}
	runtime.KeepAlive(outer)
	runtime.KeepAlive(first)
}

func TestLuaOnlyLibrariesDoNotPublishTableHandles(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	for _, open := range []func() error{
		state.OpenBase,
		state.OpenPackage,
		state.OpenTable,
		state.OpenString,
		state.OpenMath,
		state.OpenIO,
		state.OpenOS,
	} {
		if err := open(); err != nil {
			t.Fatal(err)
		}
	}
	if entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		TableKind,
	); entries != 0 || keys != 0 || stale != 0 {
		t.Fatalf(
			"opening libraries published tables: entries=%d keys=%d stale=%d",
			entries,
			keys,
			stale,
		)
	}

	chunk := mustLoadString(t, state, "@compact-tables.lua", `
local sequence={1,2,3}
table.insert(sequence,4)
package.preload.compact=function()
	return {answer=40}
end
local module=require("compact")
local file=assert(io.tmpfile())
assert(file:write("compact"))
assert(file:close())
local protected,caught=pcall(function()
	error(sequence,0)
end)
local thread=coroutine.create(function()
	error({thread=true},0)
end)
local resumed,raised=coroutine.resume(thread)
return module.answer+math.floor(2.9),
	not protected and caught==sequence,
	not resumed and type(raised)=="table"
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Number(42),
		Bool(true),
		Bool(true),
	)
	if entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		TableKind,
	); entries != 0 || keys != 0 || stale != 0 {
		t.Fatalf(
			"Lua-only execution published tables: entries=%d keys=%d stale=%d",
			entries,
			keys,
			stale,
		)
	}
}

func TestUserDataOwningHandleEnforcesStateOwnership(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}

	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	foreign, err := other.NewUserData("foreign")
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString(
		"foreign",
		foreign.Value(),
	); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign userdata table value = %v; want ErrForeignValue", err)
	}

	var zero UserData
	for name, data := range map[string]*UserData{
		"nil":  nil,
		"zero": &zero,
	} {
		if data.Value().Valid() {
			t.Fatalf("%s userdata manufactured a valid Value", name)
		}
		if _, err := userDataEnvironment(
			data,
		); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("%s userdata environment = %v; want ErrInvalidValue", name, err)
		}
	}
}

func TestWarmUserDataPublicationDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	data, err := state.NewUserData(nil)
	if err != nil {
		t.Fatal(err)
	}
	compact := slotFromValue(data.Value())
	var published *UserData
	allocations := testing.AllocsPerRun(1000, func() {
		value := compact.owningValue()
		published, _ = value.AsUserData()
	})
	if allocations != 0 {
		t.Fatalf(
			"warm userdata publication allocated %.2f times",
			allocations,
		)
	}
	if published != data {
		t.Fatalf("warm userdata publication = %p; want %p", published, data)
	}
	runtime.KeepAlive(data)
}

func TestUserDataHandleIdentitySurvivesStateClose(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	table, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString("data", data.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	published, ok := rawStr(table, "data").AsUserData()
	if !ok || published != data {
		t.Fatalf(
			"post-close userdata = (%p, %v); want (%p, true)",
			published,
			ok,
			data,
		)
	}
	if got := published.Data(); got != "payload" {
		t.Fatalf("post-close userdata payload = %v; want payload", got)
	}
}

func TestHostDirectoryDoesNotPinUserData(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	object, token := weakUserDataPublication(t, state)
	waitForWeakUserData(t, state, []weak.Pointer[userDataObject]{object},
		[]weak.Pointer[hostToken]{token})
	state.runtime.hosts.prune()
	if len(state.runtime.hosts.entries) != 0 {
		t.Fatal("dead userdata publication remains in host directory")
	}
	runtime.KeepAlive(state)
}

func TestUserDataRepublishAfterOwningTokenDies(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	table, object, token := rootedUserDataWithoutHandle(t, state)
	waitForWeakUserDataToken(t, object, token)

	first, ok := table.rawGetStringValue("data").AsUserData()
	if !ok {
		t.Fatal("re-published compact userdata is not userdata")
	}
	if first.runtimeObject() != object.Value() {
		t.Fatal("re-publication changed compact userdata identity")
	}
	second, ok := table.rawGetStringValue("data").AsUserData()
	if !ok || second != first {
		t.Fatalf(
			"second re-publication = (%p, %v); want (%p, true)",
			second,
			ok,
			first,
		)
	}

	state.runtime.hosts.mutex.Lock()
	entryCount := len(state.runtime.hosts.entries)
	keyCount := len(state.runtime.hosts.keys)
	state.runtime.hosts.mutex.Unlock()
	if entryCount != 1 || keyCount != 1 {
		t.Fatalf(
			"re-published directory size = entries:%d keys:%d; want 1/1",
			entryCount,
			keyCount,
		)
	}
	runtime.KeepAlive(first)
	runtime.KeepAlive(table)
}

func TestConcurrentUserDataRepublishAfterStateClose(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	table, object, token := rootedUserDataWithoutHandle(t, state)
	waitForWeakUserDataToken(t, object, token)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	const workers = 32
	start := make(chan struct{})
	published := make([]*UserData, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for index := range published {
		go func() {
			defer group.Done()
			<-start
			value := table.rawGetStringValue("data")
			data, ok := value.AsUserData()
			if !ok {
				return
			}
			published[index] = data
		}()
	}
	close(start)
	group.Wait()

	first := published[0]
	if first == nil {
		t.Fatal("concurrent re-publication did not return userdata")
	}
	for index, data := range published {
		if data != first {
			t.Fatalf(
				"concurrent re-publication %d = %p; want %p",
				index,
				data,
				first,
			)
		}
	}
	if first.runtimeObject() != object.Value() {
		t.Fatal("post-close re-publication changed compact object identity")
	}
	runtime.KeepAlive(table)
}

func TestHostDirectoryIncrementalMaintenanceBoundsStaleMetadata(
	t *testing.T,
) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	const abandoned = 256
	objects := make([]weak.Pointer[userDataObject], abandoned)
	tokens := make([]weak.Pointer[hostToken], abandoned)
	for index := range objects {
		objects[index], tokens[index] = weakUserDataPublication(t, state)
	}
	waitForWeakUserData(t, state, objects, tokens)

	live := make([]*UserData, 0, abandoned*2)
	previousStale := abandoned
	for index := 0; index < abandoned*2 && previousStale != 0; index++ {
		data, createErr := state.NewUserData(index)
		if createErr != nil {
			t.Fatal(createErr)
		}
		live = append(live, data)

		entryCount, keyCount, stale := hostDirectoryCounts(
			&state.runtime.hosts,
		)
		if stale > previousStale {
			t.Fatalf(
				"maintenance step %d increased stale entries from %d to %d",
				index,
				previousStale,
				stale,
			)
		}
		if entryCount > len(live)+abandoned ||
			keyCount > len(live)+abandoned {
			t.Fatalf(
				"maintenance step %d left unbounded metadata entries:%d keys:%d live:%d",
				index,
				entryCount,
				keyCount,
				len(live),
			)
		}
		previousStale = stale
	}

	entryCount, keyCount, stale := hostDirectoryCounts(
		&state.runtime.hosts,
	)
	if stale != 0 {
		t.Fatalf(
			"incremental maintenance left %d stale entries after %d publications",
			stale,
			len(live),
		)
	}
	if entryCount != len(live) || keyCount != len(live) {
		t.Fatalf(
			"maintained directory size = entries:%d keys:%d; want %d/%d",
			entryCount,
			keyCount,
			len(live),
			len(live),
		)
	}
	runtime.KeepAlive(live)
}

func hostDirectoryCounts(
	directory *hostDirectory,
) (entries, keys, stale int) {
	directory.mutex.Lock()
	defer directory.mutex.Unlock()
	for object, token := range directory.entries {
		if object.Value() == nil || token.Value() == nil {
			stale++
		}
	}
	return len(directory.entries), len(directory.keys), stale
}

func hostDirectoryKindCounts(
	directory *hostDirectory,
	kind Kind,
) (entries, keys, staleAllKinds int) {
	directory.mutex.Lock()
	defer directory.mutex.Unlock()
	for object, reference := range directory.entries {
		token := reference.Value()
		if object.Value() == nil || token == nil {
			// A dead weak endpoint no longer carries enough information to
			// attribute the entry to one object kind.
			staleAllKinds++
			continue
		}
		if token.kind == kind {
			entries++
		}
	}
	for _, object := range directory.keys {
		reference, found := directory.entries[object]
		if !found {
			continue
		}
		token := reference.Value()
		if object.Value() != nil &&
			token != nil &&
			token.kind == kind {
			keys++
		}
	}
	return entries, keys, staleAllKinds
}

func rootedTableWithoutHandle(
	t *testing.T,
	state *State,
) (*tableObject, weak.Pointer[hostToken]) {
	t.Helper()
	object := newTable(state, 0, 0)
	if err := state.registry.rawSetStringSlot(
		"rooted table",
		slotFromTableObject(object),
	); err != nil {
		t.Fatal(err)
	}
	handle := object.owningHandle()
	token := weak.Make(handle.token())
	runtime.KeepAlive(handle)
	return object, token
}

func weakTablePublication(
	t *testing.T,
	state *State,
) (weak.Pointer[tableObject], weak.Pointer[hostToken]) {
	t.Helper()
	object := newTable(state, 0, 1)
	if err := object.rawSetStringSlot(
		"self",
		slotFromTableObject(object),
	); err != nil {
		t.Fatal(err)
	}
	handle := object.owningHandle()
	objectReference := weak.Make(object)
	tokenReference := weak.Make(handle.token())
	runtime.KeepAlive(handle)
	return objectReference, tokenReference
}

func waitForWeakTable(
	t *testing.T,
	state *State,
	object weak.Pointer[tableObject],
	token weak.Pointer[hostToken],
) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		if token.Value() == nil {
			state.collectUnreachable()
		}
		if object.Value() == nil && token.Value() == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("weak host directory pinned a discarded cyclic table")
		case <-ticker.C:
		}
	}
}

func waitForWeakTableToken(
	t *testing.T,
	object *tableObject,
	token weak.Pointer[hostToken],
) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		if token.Value() == nil {
			if object == nil || object.owner == nil {
				t.Fatal("Lua-rooted compact table disappeared with its token")
			}
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("discarded table owning token remained reachable")
		case <-ticker.C:
		}
	}
}

func rootedUserDataWithoutHandle(
	t *testing.T,
	state *State,
) (
	*tableObject,
	weak.Pointer[userDataObject],
	weak.Pointer[hostToken],
) {
	t.Helper()
	table := state.registry
	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	object := data.runtimeObject()
	token := data.token()
	if err := table.rawSetStringValue("data", data.Value()); err != nil {
		t.Fatal(err)
	}
	return table, weak.Make(object), weak.Make(token)
}

func waitForWeakUserDataToken(
	t *testing.T,
	object weak.Pointer[userDataObject],
	token weak.Pointer[hostToken],
) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		if token.Value() == nil {
			if object.Value() == nil {
				t.Fatal("Lua-rooted compact userdata was collected with its token")
			}
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("discarded userdata owning token remained reachable")
		case <-ticker.C:
		}
	}
}

func waitForWeakUserData(
	t *testing.T,
	state *State,
	objects []weak.Pointer[userDataObject],
	tokens []weak.Pointer[hostToken],
) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		allTokensDead := true
		for _, token := range tokens {
			if token.Value() != nil {
				allTokensDead = false
				break
			}
		}
		if allTokensDead {
			state.collectUnreachable()
		}
		allDead := true
		for index := range objects {
			if objects[index].Value() != nil ||
				tokens[index].Value() != nil {
				allDead = false
				break
			}
		}
		if allDead {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("weak host directory pinned discarded userdata")
		case <-ticker.C:
		}
	}
}

func weakUserDataPublication(
	t *testing.T,
	state *State,
) (
	weak.Pointer[userDataObject],
	weak.Pointer[hostToken],
) {
	t.Helper()
	data, err := state.NewUserData(nil)
	if err != nil {
		t.Fatal(err)
	}
	return weak.Make(data.runtimeObject()), weak.Make(data.token())
}

func BenchmarkWarmUserDataPublication(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()
	data, err := state.NewUserData(nil)
	if err != nil {
		b.Fatal(err)
	}
	compact := slotFromValue(data.Value())

	var published Value
	b.ReportAllocs()
	for range b.N {
		published = compact.owningValue()
	}
	runtime.KeepAlive(published)
	runtime.KeepAlive(data)
}

func BenchmarkWarmTablePublication(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()
	object := newTable(state, 0, 0)
	first := object.owningHandle()
	compact := slotFromTableObject(object)

	var published Value
	b.ReportAllocs()
	for range b.N {
		published = compact.owningValue()
	}
	runtime.KeepAlive(published)
	runtime.KeepAlive(first)
}
