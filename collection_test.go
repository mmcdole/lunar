package lua

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"
	"weak"
)

func TestSemanticLedgerRegistersEveryCanonicalObject(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	initial := state.semanticHeap()
	if initial.tables != 2 ||
		initial.threads != 1 ||
		initial.functions != 0 ||
		initial.userData != 0 {
		t.Fatalf("initial semantic heap = %+v", initial)
	}

	table, err := state.NewTable(3, 5)
	if err != nil {
		t.Fatal(err)
	}
	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	native, err := state.NewNativeFunction(
		func(frame Frame) Outcome { return frame.Return() },
	)
	if err != nil {
		t.Fatal(err)
	}
	luaFunction := mustLoadString(
		t,
		state,
		"@collector-registration.lua",
		"return 1",
	)
	thread, err := state.NewThread(luaFunction.Value())
	if err != nil {
		t.Fatal(err)
	}

	current := state.semanticHeap()
	if current.tables != initial.tables+1 ||
		current.userData != initial.userData+1 ||
		current.functions != initial.functions+2 ||
		current.threads != initial.threads+1 {
		t.Fatalf(
			"semantic heap after construction = %+v; initial %+v",
			current,
			initial,
		)
	}
	assertSemanticLedgerWellFormed(t, state)
	runtime.KeepAlive(table)
	runtime.KeepAlive(data)
	runtime.KeepAlive(native)
	runtime.KeepAlive(luaFunction)
	runtime.KeepAlive(thread)
}

func TestSemanticRegistrationOwnsObjectsAndRejectsUnsafePhases(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	table := &tableObject{}
	state.registerTable(table)
	if table.owner != state.runtime ||
		state.objects.tables[len(state.objects.tables)-1] != table {
		t.Fatal("table registration did not establish canonical ownership")
	}
	assertCollectorPanic(t, func() {
		state.registerTable(table)
	})

	candidate := &tableObject{}
	state.objects.phase = collectionMarking
	assertCollectorPanic(t, func() {
		state.registerTable(candidate)
	})
	state.objects.phase = collectionIdle
	if candidate.owner != nil {
		t.Fatal("rejected registration changed object ownership")
	}

	closed := newCollectorTestState(t)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	closedCandidate := &tableObject{}
	assertCollectorPanic(t, func() {
		closed.registerTable(closedCandidate)
	})
	if closedCandidate.owner != nil {
		t.Fatal("closed-State registration changed object ownership")
	}
}

func TestSemanticTracerCoversEveryObjectEdge(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		parent := newTable(state, 2, 3)
		arrayValue := newTable(state, 0, 0)
		recordKey := newTable(state, 0, 0)
		recordValue := newTable(state, 0, 0)
		metatable := newTable(state, 0, 0)
		parent.rawSetIntegerSlot(1, slotFromTableObject(arrayValue))
		if status := parent.rawSetSlot(
			slotFromTableObject(recordKey),
			slotFromTableObject(recordValue),
		); status != tableKeyValid {
			t.Fatalf("record insertion status = %d", status)
		}
		parent.metatable = metatable
		mustRootCollectorObject(
			t,
			state,
			"table parent",
			slotFromTableObject(parent),
		)

		if swept := state.collectUnreachable(); swept.total() != 0 {
			t.Fatalf("rooted table graph swept %+v", swept)
		}
		for name, object := range map[string]*tableObject{
			"parent":       parent,
			"array value":  arrayValue,
			"record key":   recordKey,
			"record value": recordValue,
			"metatable":    metatable,
		} {
			if object.owner != state.runtime {
				t.Fatalf("%s was not traced", name)
			}
		}

		unrootCollectorObject(t, state, "table parent")
		state.collectUnreachable()
		for name, object := range map[string]*tableObject{
			"parent":       parent,
			"array value":  arrayValue,
			"record key":   recordKey,
			"record value": recordValue,
			"metatable":    metatable,
		} {
			if object.owner != nil || object.gcMark != 0 {
				t.Fatalf("%s survived without a root", name)
			}
		}
	})

	t.Run("functions", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		environment := newTable(state, 0, 0)
		upvalueTarget := newTable(state, 0, 0)
		prototype := collectorPrototype(t, 1)
		cell := newClosedUpvalue(slotFromTableObject(upvalueTarget))
		luaFunction := newLuaFunction(
			state,
			prototype,
			environment,
			[]*upvalue{cell},
		)
		captureTarget := newTable(state, 0, 0)
		nativeFunction := newNativeFunctionOwned(
			state,
			state.main.globals,
			func(frame Frame) Outcome { return frame.Return() },
			[]slot{slotFromTableObject(captureTarget)},
		)
		mustRootCollectorObject(
			t,
			state,
			"lua function",
			slotFromFunctionObject(luaFunction),
		)
		mustRootCollectorObject(
			t,
			state,
			"native function",
			slotFromFunctionObject(nativeFunction),
		)

		state.collectUnreachable()
		for name, object := range map[string]*tableObject{
			"environment": environment,
			"upvalue":     upvalueTarget,
			"capture":     captureTarget,
		} {
			if object.owner != state.runtime {
				t.Fatalf("%s edge was not traced", name)
			}
		}

		unrootCollectorObject(t, state, "lua function")
		unrootCollectorObject(t, state, "native function")
		state.collectUnreachable()
		if luaFunction.owner != nil ||
			nativeFunction.owner != nil ||
			environment.owner != nil ||
			upvalueTarget.owner != nil ||
			captureTarget.owner != nil {
			t.Fatal("unrooted function graph survived")
		}
	})

	t.Run("thread", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		entry := newLuaFunction(
			state,
			collectorPrototype(t, 0),
			state.main.globals,
			nil,
		)
		thread, err := state.newThreadObject(
			slotFromFunctionObject(entry),
		)
		if err != nil {
			t.Fatal(err)
		}
		globals := newTable(state, 0, 0)
		registerValue := newTable(state, 0, 0)
		frameFunction := newLuaFunction(
			state,
			collectorPrototype(t, 0),
			state.main.globals,
			nil,
		)
		thread.globals = globals
		thread.values = append(
			thread.values,
			slotFromTableObject(registerValue),
		)
		thread.top = len(thread.values)
		thread.frameExtent = thread.top
		thread.frames = append(
			thread.frames,
			activation{function: frameFunction},
		)
		thread.captureUpvalue(1)
		mustRootCollectorObject(
			t,
			state,
			"thread",
			slotFromThreadObject(thread),
		)

		state.collectUnreachable()
		if globals.owner != state.runtime ||
			registerValue.owner != state.runtime ||
			frameFunction.owner != state.runtime {
			t.Fatal("thread edge was not traced")
		}

		unrootCollectorObject(t, state, "thread")
		state.collectUnreachable()
		if thread.owner != nil ||
			globals.owner != nil ||
			registerValue.owner != nil ||
			frameFunction.owner != nil {
			t.Fatal("unrooted thread graph survived")
		}
	})

	t.Run("userdata", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		environment := newTable(state, 0, 0)
		metatable := newTable(state, 0, 0)
		data := newUserDataObject(state, nil, environment, nil)
		data.metatable = metatable
		mustRootCollectorObject(
			t,
			state,
			"userdata",
			slotFromUserDataObject(data),
		)

		state.collectUnreachable()
		if environment.owner != state.runtime ||
			metatable.owner != state.runtime {
			t.Fatal("userdata edge was not traced")
		}

		unrootCollectorObject(t, state, "userdata")
		state.collectUnreachable()
		if data.owner != nil ||
			environment.owner != nil ||
			metatable.owner != nil {
			t.Fatal("unrooted userdata graph survived")
		}
	})
}

func TestSemanticCollectorRetiresDeletedReferenceKeys(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	table := newTable(state, 0, 4)
	key := newTable(state, 0, 0)
	keySlot := slotFromTableObject(key)
	hash, err := hashTableKey(keySlot)
	if err != nil {
		t.Fatal(err)
	}
	if status := table.rawSetSlot(keySlot, numberSlot(1)); status !=
		tableKeyValid {
		t.Fatalf("reference-key insertion status = %d", status)
	}
	if status := table.rawSetSlot(keySlot, nilSlot); status != tableKeyValid {
		t.Fatalf("reference-key deletion status = %d", status)
	}
	mustRootCollectorObject(
		t,
		state,
		"dead-key table",
		slotFromTableObject(table),
	)
	mustRootCollectorObject(t, state, "dead-key object", keySlot)

	state.collectUnreachable()
	index, found := table.store.findContinuation(keySlot, hash)
	if !found {
		t.Fatal("collector discarded a valid next continuation")
	}
	entry := table.store.entries.at(index)
	if !entry.key.isDeadReferenceKey() {
		t.Fatal("deleted reference key remained a strong slot")
	}
	if _, _, _, nextErr := table.next(keySlot); nextErr != nil {
		t.Fatalf("next rejected a collector-retired key: %v", nextErr)
	}

	if changed := table.set(keySlot, 0, false, hash, nilSlot); changed {
		t.Fatal("absent nil write changed a dead-key tombstone")
	}
	if !entry.key.isDeadReferenceKey() {
		t.Fatal("absent nil write restored a strong reference key")
	}
	if changed := table.set(
		keySlot,
		0,
		false,
		hash,
		numberSlot(2),
	); !changed {
		t.Fatal("dead reference key did not revive")
	}
	value, found := table.rawSlot(keySlot)
	if !found || !rawSlotEqual(value, numberSlot(2)) {
		t.Fatalf("revived reference value = (%v, %v)", value, found)
	}
	index, found = table.store.findStored(keySlot, hash)
	if !found || table.store.entries.at(index).key.isDeadReferenceKey() {
		t.Fatal("revival did not restore the canonical key slot")
	}

	if status := table.rawSetSlot(keySlot, nilSlot); status != tableKeyValid {
		t.Fatalf("second reference-key deletion status = %d", status)
	}
	headerReference := weak.Make(&key.objectHeader)
	unrootCollectorObject(t, state, "dead-key object")
	state.collectUnreachable()
	index, found = table.store.findContinuation(keySlot, hash)
	if !found {
		t.Fatal("unreachable key lost its tombstone before Go reclamation")
	}
	dead := (*deadReferenceKey)(table.store.entries.at(index).key.ref)
	if dead.target != headerReference {
		t.Fatal("dead key did not preserve weak identity")
	}
	if key.owner != nil {
		t.Fatal("deleted key remained semantically reachable")
	}
	key = nil
	keySlot = slot{}
	waitForWeakObjectHeader(t, headerReference)
	if dead.target != headerReference || dead.target.Value() != nil {
		t.Fatal("dead-key identity changed after Go reclamation")
	}
}

func TestSemanticCollectorRetiresEveryReferenceKeyKind(t *testing.T) {
	tests := []struct {
		name string
		make func(*testing.T, *State) slot
	}{
		{
			name: "table",
			make: func(_ *testing.T, state *State) slot {
				return slotFromTableObject(newTable(state, 0, 0))
			},
		},
		{
			name: "native function",
			make: func(_ *testing.T, state *State) slot {
				function := newNativeFunctionOwned(
					state,
					state.main.globals,
					func(frame Frame) Outcome {
						return frame.Return()
					},
					nil,
				)
				return slotFromFunctionObject(function)
			},
		},
		{
			name: "userdata",
			make: func(_ *testing.T, state *State) slot {
				data := newUserDataObject(state, nil, nil, nil)
				return slotFromUserDataObject(data)
			},
		},
		{
			name: "thread",
			make: func(t *testing.T, state *State) slot {
				entry := newNativeFunctionOwned(
					state,
					state.main.globals,
					func(frame Frame) Outcome {
						return frame.Return()
					},
					nil,
				)
				thread, err := state.newThreadObject(
					slotFromFunctionObject(entry),
				)
				if err != nil {
					t.Fatal(err)
				}
				return slotFromThreadObject(thread)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newCollectorTestState(t)
			defer state.Close()
			table := newTable(state, 0, 1)
			key := test.make(t, state)
			if unsafe.Pointer(referenceSlotHeader(key)) != key.ref {
				t.Fatal("object header is not at the canonical object base")
			}
			hash, err := hashTableKey(key)
			if err != nil {
				t.Fatal(err)
			}
			if status := table.rawSetSlot(
				key,
				numberSlot(1),
			); status != tableKeyValid {
				t.Fatalf("insertion status = %d", status)
			}
			if status := table.rawSetSlot(
				key,
				nilSlot,
			); status != tableKeyValid {
				t.Fatalf("deletion status = %d", status)
			}
			mustRootCollectorObject(
				t,
				state,
				"dead-key-table",
				slotFromTableObject(table),
			)
			mustRootCollectorObject(t, state, "dead-key-object", key)

			state.collectUnreachable()
			index, found := table.store.findContinuation(key, hash)
			if !found {
				t.Fatal("collector discarded the continuation")
			}
			dead := table.store.entries.at(index).key
			if !dead.isDeadReferenceKey() ||
				!deadReferenceKeyMatches(dead, key) {
				t.Fatal("collector did not preserve weak key identity")
			}
			state.collectUnreachable()
			if table.store.entries.at(index).key.ref != dead.ref {
				t.Fatal("repeated collection replaced the dead-key holder")
			}
		})
	}
}

func TestDeadReferenceKeyRevivalCompactsTombstones(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()
	table := newTable(state, 0, 8)
	key := newTable(state, 0, 0)
	keySlot := slotFromTableObject(key)
	if status := table.rawSetSlot(
		keySlot,
		numberSlot(1),
	); status != tableKeyValid {
		t.Fatalf("reference insertion status = %d", status)
	}
	names := [...]string{"discard-a", "discard-b", "discard-c"}
	for _, name := range names {
		if err := table.rawSetStringSlot(name, numberSlot(1)); err != nil {
			t.Fatal(err)
		}
	}
	if status := table.rawSetSlot(
		keySlot,
		nilSlot,
	); status != tableKeyValid {
		t.Fatalf("reference deletion status = %d", status)
	}
	for _, name := range names {
		if err := table.rawSetStringSlot(name, nilSlot); err != nil {
			t.Fatal(err)
		}
	}
	if !table.store.shouldCompact() {
		t.Fatal("test did not establish a compactable store")
	}
	mustRootCollectorObject(
		t,
		state,
		"compact-table",
		slotFromTableObject(table),
	)
	mustRootCollectorObject(t, state, "compact-key", keySlot)
	before := state.semanticHeap()
	state.collectUnreachable()
	retired := state.semanticHeap()
	if retired.bytes-before.bytes !=
		uint64(unsafe.Sizeof(deadReferenceKey{})) {
		t.Fatalf(
			"dead-key accounting delta = %d; want %d",
			retired.bytes-before.bytes,
			unsafe.Sizeof(deadReferenceKey{}),
		)
	}

	hash, err := hashTableKey(keySlot)
	if err != nil {
		t.Fatal(err)
	}
	if changed := table.set(
		keySlot,
		0,
		false,
		hash,
		numberSlot(2),
	); !changed {
		t.Fatal("reference-key revival did not change the table")
	}
	if table.store.dead != 0 ||
		table.store.live != 1 ||
		table.store.shouldCompact() {
		t.Fatalf(
			"compacted store = live:%d dead:%d compact:%v",
			table.store.live,
			table.store.dead,
			table.store.shouldCompact(),
		)
	}
	index, found := table.store.findStored(keySlot, hash)
	if !found || table.store.entries.at(index).key.isDeadReferenceKey() {
		t.Fatal("compaction retained the dead-key holder")
	}
	revived := state.semanticHeap()
	if retired.bytes-revived.bytes !=
		uint64(unsafe.Sizeof(deadReferenceKey{})) {
		t.Fatalf(
			"compaction accounting delta = %d; want %d",
			retired.bytes-revived.bytes,
			unsafe.Sizeof(deadReferenceKey{}),
		)
	}
}

func TestSemanticCollectorSweepsCyclesAndIsolatesStates(t *testing.T) {
	first := newCollectorTestState(t)
	defer first.Close()
	second := newCollectorTestState(t)
	defer second.Close()

	table := newTable(first, 0, 1)
	if err := table.rawSetStringSlot(
		"self",
		slotFromTableObject(table),
	); err != nil {
		t.Fatal(err)
	}

	function := newLuaFunction(
		first,
		collectorPrototype(t, 1),
		first.main.globals,
		[]*upvalue{newClosedUpvalue(nilSlot)},
	)
	testFunctionUpvalue(function, 0).write(
		slotFromFunctionObject(function),
	)

	entry := newLuaFunction(
		first,
		collectorPrototype(t, 0),
		first.main.globals,
		nil,
	)
	thread, err := first.newThreadObject(slotFromFunctionObject(entry))
	if err != nil {
		t.Fatal(err)
	}
	thread.values = append(thread.values, slotFromThreadObject(thread))
	thread.top = len(thread.values)

	data := newUserDataObject(first, nil, nil, nil)
	userCycle := newTable(first, 0, 1)
	data.environment = userCycle
	userCycle.rawSetIntegerSlot(1, slotFromUserDataObject(data))

	foreign := newTable(second, 0, 0)
	beforeSecond := second.semanticHeap()
	result := first.collectUnreachable()
	if result.tables < 2 ||
		result.functions < 2 ||
		result.threads < 1 ||
		result.userData < 1 {
		t.Fatalf("cycle sweep = %+v", result)
	}
	if table.owner != nil ||
		function.owner != nil ||
		thread.owner != nil ||
		data.owner != nil ||
		userCycle.owner != nil {
		t.Fatal("semantic cycle remained registered")
	}
	afterSecond := second.semanticHeap()
	if afterSecond != beforeSecond ||
		foreign.owner != second.runtime {
		t.Fatalf(
			"collection crossed States: before=%+v after=%+v",
			beforeSecond,
			afterSecond,
		)
	}
}

func TestSemanticCollectorRootsStateAndHostObjects(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	typeMetatable := newTable(state, 0, 0)
	state.typeMetatables[NumberKind] = typeMetatable
	sentinel := state.ensurePackageSentinel()
	state.collectUnreachable()
	if typeMetatable.owner != state.runtime ||
		sentinel.owner != state.runtime ||
		state.main.owner != state.runtime ||
		state.registry.owner != state.runtime {
		t.Fatal("State root was not traced")
	}

	objectReference, tokenReference := liveHostTableRoot(t, state)
	state.collectUnreachable()
	if objectReference.Value() == nil {
		t.Fatal("live owning token did not root its table")
	}

	state.typeMetatables[NumberKind] = nil
	state.packageSentinel = nil
	waitForDiscardedHostTable(
		t,
		state,
		objectReference,
		tokenReference,
	)
	state.collectUnreachable()
	if typeMetatable.owner != nil || sentinel.owner != nil {
		t.Fatal("cleared State roots survived")
	}
}

func TestSemanticCollectorRootsExecutionErrors(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	failureTarget := newTable(state, 0, 0)
	exitTarget := newTable(state, 0, 0)
	state.execution.failure = &Error{
		compactValue:    slotFromTableObject(failureTarget),
		hasCompactValue: true,
	}
	state.execution.pendingExit = &Error{
		compactValue:    slotFromTableObject(exitTarget),
		hasCompactValue: true,
	}
	state.collectUnreachable()
	if failureTarget.owner != state.runtime ||
		exitTarget.owner != state.runtime {
		t.Fatal("execution error value was not traced")
	}

	state.execution = executionControl{}
	state.collectUnreachable()
	if failureTarget.owner != nil || exitTarget.owner != nil {
		t.Fatal("cleared execution error retained its Lua value")
	}
}

func TestSemanticSweepCompactsStableVectorsAndClearsTail(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	expected := append([]*tableObject(nil), state.objects.tables...)
	for index := 0; index < 16; index++ {
		table := newTable(state, 0, 0)
		if index%2 == 0 {
			name := "live table " + string(rune('a'+index))
			mustRootCollectorObject(
				t,
				state,
				name,
				slotFromTableObject(table),
			)
			expected = append(expected, table)
		}
	}
	state.collectUnreachable()
	if len(state.objects.tables) != len(expected) {
		t.Fatalf(
			"stable table vector length = %d; want %d",
			len(state.objects.tables),
			len(expected),
		)
	}
	for index, table := range expected {
		if state.objects.tables[index] != table {
			t.Fatalf("table survivor %d moved out of stable order", index)
		}
	}
	backing := state.objects.tables[:cap(state.objects.tables)]
	for index, table := range backing[len(state.objects.tables):] {
		if table != nil {
			t.Fatalf("table vector retained dead tail entry %d", index)
		}
	}
}

func TestOwningTableDoesNotRetainStateLedgerPeers(t *testing.T) {
	table, stateReference, peerReference := tableFromDiscardedState(t)
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for stateReference.Value() != nil || peerReference.Value() != nil {
		runtime.GC()
		select {
		case <-deadline.C:
			t.Fatal("owning table retained its State or an unrelated peer")
		case <-ticker.C:
		}
	}

	if err := table.RawSetInt(1, Number(23)); err != nil {
		t.Fatal(err)
	}
	if number, ok := table.RawGetInt(1).AsNumber(); !ok || number != 23 {
		t.Fatalf("retained table value = (%v, %v); want 23", number, ok)
	}
	runtime.KeepAlive(table)
}

func TestSemanticCollectorRecoversMarksAfterTracingPanic(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	parent := newTable(state, 1, 0)
	rogue := &tableObject{}
	parent.rawSetIntegerSlot(1, slot{
		ref:  unsafe.Pointer(rogue),
		bits: uint64(TableKind),
	})
	mustRootCollectorObject(
		t,
		state,
		"panic parent",
		slotFromTableObject(parent),
	)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("collector accepted an unregistered graph edge")
			}
		}()
		state.collectUnreachable()
	}()
	if state.objects.phase != collectionIdle ||
		parent.gcMark != 0 {
		t.Fatal("failed collection did not preserve retry state")
	}

	parent.rawSetIntegerSlot(1, nilSlot)
	if swept := state.collectUnreachable(); swept.total() != 0 {
		t.Fatalf("retry swept a live object: %+v", swept)
	}
	if state.objects.phase != collectionIdle ||
		parent.gcMark != 0 ||
		parent.owner != state.runtime {
		t.Fatal("collector did not recover after a tracing panic")
	}
}

func TestSemanticCollectionAtExecutionSafePointKeepsFrameExtent(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	collect, err := state.NewNativeFunction(func(frame Frame) Outcome {
		frame.thread.state.collectUnreachable()
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("collect_now", collect.Value()); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(
		t,
		state,
		"@collector-frame-extent.lua",
		`
local retained={answer=42}
collect_now()
return retained.answer
`,
	)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("result count = %d; want 1", len(results))
	}
	number, ok := results[0].AsNumber()
	if !ok || number != 42 {
		t.Fatalf("retained result = (%v, %v); want 42", number, ok)
	}
}

func TestSemanticSweepClosesEscapedUpvalueFromDeadThread(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	entry := newLuaFunction(
		state,
		collectorPrototype(t, 0),
		state.main.globals,
		nil,
	)
	thread, err := state.newThreadObject(slotFromFunctionObject(entry))
	if err != nil {
		t.Fatal(err)
	}
	thread.values = append(thread.values, numberSlot(37))
	thread.top = len(thread.values)
	cell := thread.captureUpvalue(1)
	closure := newLuaFunction(
		state,
		collectorPrototype(t, 1),
		state.main.globals,
		[]*upvalue{cell},
	)
	mustRootCollectorObject(
		t,
		state,
		"escaped closure",
		slotFromFunctionObject(closure),
	)

	state.collectUnreachable()
	if thread.owner != nil {
		t.Fatal("dead coroutine remained registered")
	}
	if testUpvalueIsOpen(cell) {
		t.Fatal("escaped upvalue still points into dead coroutine stack")
	}
	number, ok := cell.read().owningValue().AsNumber()
	if !ok || number != 37 {
		t.Fatalf("closed escaped value = (%v, %v); want 37", number, ok)
	}
}

func TestStateCloseDetachesLedgerWithoutBreakingOwningHandles(t *testing.T) {
	state := newCollectorTestState(t)
	table, err := state.NewTable(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetInt(1, Number(19)); err != nil {
		t.Fatal(err)
	}
	external := state.String(strings.Repeat("close-attribution-", 8))
	if err := table.RawSetString("external", external); err != nil {
		t.Fatal(err)
	}
	if len(state.runtime.collection.attributedStrings) == 0 {
		t.Fatal("test did not populate long-string attribution")
	}
	data, err := state.NewUserData("retained")
	if err != nil {
		t.Fatal(err)
	}
	function := mustLoadString(t, state, "@close-ledger.lua", "return")
	thread, err := state.NewThread(function.Value())
	if err != nil {
		t.Fatal(err)
	}
	tableObject := table.runtimeObject()
	dataObject := data.runtimeObject()
	functionObject := function.runtimeObject()
	threadObject := thread.runtimeObject()
	state.collectUnreachable()
	newLuaFunction(
		state,
		collectorPrototype(t, 1),
		state.main.globals,
		[]*upvalue{newClosedUpvalue(numberSlot(1))},
	)
	state.semanticHeap()
	if cap(state.objects.tableWork) == 0 ||
		cap(state.objects.functionWork) == 0 ||
		cap(state.objects.threadWork) == 0 ||
		state.objects.upvalues == nil ||
		state.objects.prototypes == nil ||
		state.objects.names == nil ||
		state.objects.stringBacking == nil {
		t.Fatal("test did not populate collector scratch")
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if state.objects.tables != nil ||
		state.objects.functions != nil ||
		state.objects.threads != nil ||
		state.objects.userData != nil {
		t.Fatal("Close retained an object-ledger head")
	}
	if state.runtime.collection.attributedStrings != nil ||
		state.runtime.collection.attributedStringHighWater != 0 ||
		state.runtime.collection.debt != 0 ||
		state.runtime.collection.budget != 0 ||
		state.runtime.collection.requested ||
		state.runtime.collection.runnable {
		t.Fatal("Close retained collection scheduling state")
	}
	if state.objects.tableWork != nil ||
		state.objects.functionWork != nil ||
		state.objects.threadWork != nil ||
		state.objects.userDataWork != nil ||
		state.objects.upvalues != nil ||
		state.objects.prototypes != nil ||
		state.objects.names != nil ||
		state.objects.longStrings != nil ||
		state.objects.stringBacking != nil ||
		state.objects.prototypeWork != nil {
		t.Fatal("Close retained collector scratch")
	}
	if tableObject.gcMark != 0 ||
		functionObject.gcMark != 0 ||
		threadObject.collectionMark() != 0 ||
		dataObject.gcMark != 0 {
		t.Fatal("Close retained a collection mark")
	}
	if number, ok := table.RawGetInt(1).AsNumber(); !ok || number != 19 {
		t.Fatalf("post-close table value = (%v, %v)", number, ok)
	}
	if data.Data() != "retained" {
		t.Fatalf("post-close userdata payload = %v", data.Data())
	}
	if function.Prototype() == nil {
		t.Fatal("post-close function lost its Prototype")
	}
	if thread.Status() != ThreadClosed ||
		threadObject.values != nil ||
		threadObject.frames != nil ||
		threadObject.continuations != nil {
		t.Fatal("Close did not release a child thread")
	}
}

func TestSemanticHeapAccountingAndWarmCollection(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()
	baseline := state.semanticHeap()

	table := newTable(state, 8, 5)
	withTable := state.semanticHeap()
	wantTableBytes := uint64(unsafe.Sizeof(tableObject{})) +
		uint64(unsafe.Sizeof((*tableObject)(nil))) +
		8*uint64(unsafe.Sizeof(slot{})) +
		8*uint64(unsafe.Sizeof(tableEntry{}))
	if delta := withTable.bytes - baseline.bytes; delta != wantTableBytes {
		t.Fatalf(
			"hinted table bytes = %d; want %d",
			delta,
			wantTableBytes,
		)
	}

	payload := make([]byte, 1<<20)
	data, err := state.NewUserData(payload)
	if err != nil {
		t.Fatal(err)
	}
	withData := state.semanticHeap()
	if delta := withData.bytes - withTable.bytes; delta !=
		uint64(unsafe.Sizeof(userDataObject{}))+
			uint64(unsafe.Sizeof((*userDataObject)(nil))) {
		t.Fatalf(
			"userdata bytes = %d; want object plus ledger %d",
			delta,
			unsafe.Sizeof(userDataObject{})+
				unsafe.Sizeof((*userDataObject)(nil)),
		)
	}

	state.collectUnreachable()
	afterSweep := state.semanticHeap()
	if table.owner != nil {
		t.Fatal("unrooted hinted table survived")
	}
	if afterSweep.tables != baseline.tables ||
		afterSweep.userData != baseline.userData+1 {
		t.Fatalf(
			"heap after sweep = %+v; baseline %+v",
			afterSweep,
			baseline,
		)
	}

	state.collectUnreachable()
	if allocations := testing.AllocsPerRun(1000, func() {
		if swept := state.collectUnreachable(); swept.total() != 0 {
			panic("stable collection swept a live object")
		}
	}); allocations != 0 {
		t.Fatalf("warm stable collection allocations = %v; want 0", allocations)
	}
	runtime.KeepAlive(data)
	runtime.KeepAlive(payload)
}

func TestSemanticHeapAccountingBoundary(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	beforeNative := state.semanticHeap()
	captures := []slot{numberSlot(1), trueSlot, nilSlot}
	native := newNativeFunctionOwned(
		state,
		state.main.globals,
		func(frame Frame) Outcome { return frame.Return() },
		captures,
	)
	afterNative := state.semanticHeap()
	wantNative := uint64(unsafe.Sizeof(nativeFunctionAllocation{})) +
		uint64(unsafe.Sizeof((*functionObject)(nil))) +
		uint64(len(captures))*uint64(unsafe.Sizeof(slot{}))
	if delta := afterNative.bytes - beforeNative.bytes; delta != wantNative {
		t.Fatalf("native function bytes = %d; want %d", delta, wantNative)
	}

	cell := newClosedUpvalue(numberSlot(7))
	prototype := collectorPrototype(t, 1)
	first := newLuaFunctionOwned(
		state,
		prototype,
		state.main.globals,
		[]*upvalue{cell},
	)
	afterFirst := state.semanticHeap()
	wantLua := uint64(unsafe.Sizeof(functionObject{})) +
		uint64(unsafe.Sizeof((*functionObject)(nil))) +
		uint64(unsafe.Sizeof((*upvalue)(nil)))
	wantPrototype := uint64(unsafe.Sizeof(*prototype)) +
		uint64(cap(prototype.code))*uint64(unsafe.Sizeof(instruction(0))) +
		uint64(cap(prototype.constants))*uint64(unsafe.Sizeof(slot{})) +
		uint64(cap(prototype.children))*
			uint64(unsafe.Sizeof((*Prototype)(nil)))
	if prototype.sourceName != nil {
		wantPrototype += uint64(unsafe.Sizeof(*prototype.sourceName)) +
			uint64(len(prototype.sourceName.text))
	}
	wantFirst := wantLua +
		uint64(unsafe.Sizeof(upvalue{})) +
		wantPrototype
	if delta := afterFirst.bytes - afterNative.bytes; delta != wantFirst {
		t.Fatalf("first shared-upvalue function bytes = %d; want %d", delta, wantFirst)
	}
	second := newLuaFunctionOwned(
		state,
		prototype,
		state.main.globals,
		[]*upvalue{cell},
	)
	afterSecond := state.semanticHeap()
	if delta := afterSecond.bytes - afterFirst.bytes; delta != wantLua {
		t.Fatalf("second shared-upvalue function bytes = %d; want %d", delta, wantLua)
	}
	if afterSecond.upvalues != afterFirst.upvalues {
		t.Fatal("one shared upvalue was counted more than once")
	}

	thread := &threadObject{
		state:         state,
		globals:       state.main.globals,
		values:        make([]slot, 0, 7),
		frames:        make([]activation, 0, 3),
		continuations: make([]executionContinuation, 0, 2),
		status:        ThreadSuspended,
	}
	state.registerThread(thread)
	afterThread := state.semanticHeap()
	wantThread := uint64(unsafe.Sizeof(threadObject{})) +
		uint64(unsafe.Sizeof((*threadObject)(nil))) +
		7*uint64(unsafe.Sizeof(slot{})) +
		3*uint64(unsafe.Sizeof(activation{})) +
		2*uint64(unsafe.Sizeof(executionContinuation{}))
	if delta := afterThread.bytes - afterSecond.bytes; delta != wantThread {
		t.Fatalf("thread backing bytes = %d; want %d", delta, wantThread)
	}

	table := newTable(state, 1, 0)
	beforeContent := state.semanticHeap()
	text := strings.Repeat("state-neutral-", 128)
	longString := state.String(text)
	table.rawSetIntegerSlot(1, slotFromValue(longString))
	afterString := state.semanticHeap()
	if delta := afterString.bytes - beforeContent.bytes; delta != uint64(len(text)) {
		t.Fatalf("retained string bytes = %d; want %d", delta, len(text))
	}
	table.owningHandle()
	afterContent := state.semanticHeap()
	if afterContent.bytes != afterString.bytes {
		t.Fatalf(
			"host token changed heap bytes: %d -> %d",
			afterString.bytes,
			afterContent.bytes,
		)
	}

	ledger := &state.objects
	tables := make([]*tableObject, len(ledger.tables), cap(ledger.tables)+64)
	copy(tables, ledger.tables)
	ledger.tables = tables
	ledger.tableWork = make([]*tableObject, 0, 64)
	ledger.functionWork = make([]*functionObject, 0, 64)
	ledger.finalizers = make([]*userDataObject, 0, 64)
	afterScratch := state.semanticHeap()
	if afterScratch.bytes != afterContent.bytes {
		t.Fatalf(
			"ledger slack or collector scratch changed heap bytes: %d -> %d",
			afterContent.bytes,
			afterScratch.bytes,
		)
	}
	if allocations := testing.AllocsPerRun(1000, func() {
		if state.semanticHeap().bytes != afterContent.bytes {
			panic("stable semantic heap changed")
		}
	}); allocations != 0 {
		t.Fatalf("warm HeapBytes accounting allocated %v times; want 0", allocations)
	}

	runtime.KeepAlive(native)
	runtime.KeepAlive(first)
	runtime.KeepAlive(second)
	runtime.KeepAlive(thread)
	runtime.KeepAlive(table)
	runtime.KeepAlive(longString)
}

func TestSemanticHeapAttributesPrototypeTreeBeforeClosureCreation(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	root := mustLoadString(
		t,
		state,
		"@prototype-accounting.lua",
		`return function() return "child constant" end`,
	)
	before := state.semanticHeap()
	if before.prototypes != 2 {
		t.Fatalf(
			"loaded prototype tree count = %d; want root and child",
			before.prototypes,
		)
	}
	if before.textBackings == 0 {
		t.Fatal("prototype strings were not attributed")
	}

	results, err := state.Call(root.Value())
	if err != nil {
		t.Fatal(err)
	}
	after := state.semanticHeap()
	if after.prototypes != before.prototypes {
		t.Fatalf(
			"creating child closure changed prototype attribution: %d -> %d",
			before.prototypes,
			after.prototypes,
		)
	}
	runtime.KeepAlive(results)
}

func TestCollectionHostSurfaceUsesTheSemanticCollector(t *testing.T) {
	state := newCollectorTestState(t)

	initial, err := state.HeapBytes()
	if err != nil {
		t.Fatal(err)
	}
	if initial == 0 || initial != state.semanticHeap().bytes {
		t.Fatalf(
			"initial HeapBytes = %d; semantic heap = %d",
			initial,
			state.semanticHeap().bytes,
		)
	}

	var stateCollectError error
	entry, err := state.NewNativeFunction(func(frame Frame) Outcome {
		before := frame.HeapBytes()
		stateCollectError = state.Collect()
		if err := frame.Collect(); err != nil {
			t.Fatal(err)
		}
		after := frame.HeapBytes()
		return frame.ReturnValues(Number(float64(before)), Number(float64(after)))
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(stateCollectError, ErrRunning) {
		t.Fatalf(
			"State.Collect during callback = %v; want ErrRunning",
			stateCollectError,
		)
	}
	if len(results) != 2 {
		t.Fatalf("Frame collector returned %d observations; want 2", len(results))
	}
	for index, result := range results {
		number, ok := result.AsNumber()
		if !ok || number <= 0 {
			t.Fatalf("Frame HeapBytes result %d = %v", index, result)
		}
	}

	if err := state.Collect(); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.HeapBytes(); !errors.Is(err, ErrClosed) {
		t.Fatalf("HeapBytes after Close = %v; want ErrClosed", err)
	}
	if err := state.Collect(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Collect after Close = %v; want ErrClosed", err)
	}
}

func TestAutomaticCollectionDebtTracksRetainedGrowth(t *testing.T) {
	t.Run("control arithmetic", func(t *testing.T) {
		control := collectionControl{budget: 10}
		control.charge(9)
		if control.debt != 9 || control.requested || control.runnable {
			t.Fatalf("debt before threshold = %+v", control)
		}
		control.charge(1)
		if control.debt != 10 ||
			!control.requested ||
			!control.runnable {
			t.Fatalf("debt at threshold = %+v", control)
		}
		control.setStopped(true)
		if !control.requested || control.runnable {
			t.Fatalf("stopped due cycle = %+v", control)
		}
		control.setStopped(false)
		if !control.runnable {
			t.Fatalf("restarted due cycle = %+v", control)
		}
		control.setServicing(true)
		if control.runnable {
			t.Fatalf("servicing due cycle = %+v", control)
		}
		control.setServicing(false)
		if !control.runnable {
			t.Fatalf("restored due cycle = %+v", control)
		}

		control.debt = ^uint64(0) - 1
		control.requested = false
		control.refreshRunnable()
		control.charge(2)
		if control.debt != ^uint64(0) ||
			!control.requested ||
			!control.runnable {
			t.Fatalf("saturated debt = %+v", control)
		}

		if got := automaticCollectionBudget(1, 200); got != minimumAutomaticCollectionDebt {
			t.Fatalf("small live-heap budget = %d", got)
		}
		if got := automaticCollectionBudget(1<<20, 200); got != 1<<20 {
			t.Fatalf("one-live-heap budget = %d; want %d", got, 1<<20)
		}
		if got := automaticCollectionBudget(^uint64(0), 300); got != ^uint64(0) {
			t.Fatalf("overflowing budget = %d; want saturation", got)
		}

		control = collectionControl{
			pause:     300,
			debt:      100,
			stopped:   true,
			requested: true,
			baseline:  1 << 20,
		}
		control.restoreAfterFinalizer()
		if control.stopped ||
			control.requested ||
			control.runnable ||
			control.debt != 100 ||
			control.budget != 2<<20 {
			t.Fatalf("successful finalizer restoration = %+v", control)
		}
	})

	t.Run("objects and capacity", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		state.resetCollectionDebt()

		table := newTable(state, 4, 4)
		if got, want := state.runtime.collection.debt,
			tableRetainedBytes(table); got != want {
			t.Fatalf("new-table debt = %d; want %d", got, want)
		}

		before := state.runtime.collection.debt
		table.rawSetIntegerSlot(1, numberSlot(1))
		table.rawSetIntegerSlot(1, numberSlot(2))
		table.rawSetIntegerSlot(1, nilSlot)
		if got := state.runtime.collection.debt; got != before {
			t.Fatalf(
				"in-capacity writes changed debt from %d to %d",
				before,
				got,
			)
		}

		for index := 5; index <= 12; index++ {
			table.rawSetIntegerSlot(index, numberSlot(float64(index)))
		}
		if got := state.runtime.collection.debt; got <= before {
			t.Fatalf(
				"capacity growth left debt at %d; want more than %d",
				got,
				before,
			)
		}

		if _, failure := state.collectAndFinalize(); failure != nil {
			t.Fatal(failure)
		}
		if got := state.runtime.collection.debt; got != 0 {
			t.Fatalf("completed cycle left %d bytes of old debt", got)
		}
	})

	t.Run("strings", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		state.resetCollectionDebt()

		long := strings.Repeat("x", shortStringLimit+1)
		external := state.String(long)
		if got := state.runtime.collection.debt; got != 0 {
			t.Fatalf("uncached external string charged %d bytes", got)
		}
		table, err := state.NewTable(1, 0)
		if err != nil {
			t.Fatal(err)
		}
		state.resetCollectionDebt()
		if err := table.RawSetInt(1, external); err != nil {
			t.Fatal(err)
		}
		if got, want := state.runtime.collection.debt,
			uint64(len(long)); got != want {
			t.Fatalf(
				"retained external string debt = %d; want %d",
				got,
				want,
			)
		}
		beforeRepeat := state.runtime.collection.debt
		if err := table.RawSetInt(1, external); err != nil {
			t.Fatal(err)
		}
		if got := state.runtime.collection.debt; got != beforeRepeat {
			t.Fatalf(
				"repeated external string changed debt from %d to %d",
				beforeRepeat,
				got,
			)
		}
		if _, failure := state.collectAndFinalize(); failure != nil {
			t.Fatal(failure)
		}
		if got := state.runtime.collection.debt; got != 0 {
			t.Fatalf("completed cycle left %d bytes of string debt", got)
		}
		if err := table.RawSetInt(1, external); err != nil {
			t.Fatal(err)
		}
		if got := state.runtime.collection.debt; got != 0 {
			t.Fatalf(
				"live external string was recharged after a cycle: %d",
				got,
			)
		}
		if err := table.RawSetInt(1, Nil()); err != nil {
			t.Fatal(err)
		}
		if _, failure := state.collectAndFinalize(); failure != nil {
			t.Fatal(failure)
		}
		if state.runtime.collection.attributedStrings != nil {
			t.Fatal("cycle retained attribution for a dead external string")
		}
		if err := table.RawSetInt(1, external); err != nil {
			t.Fatal(err)
		}
		if got, want := state.runtime.collection.debt,
			uint64(len(long)); got != want {
			t.Fatalf(
				"reimported dead string debt = %d; want %d",
				got,
				want,
			)
		}
		peer := newCollectorTestState(t)
		defer peer.Close()
		peerTable, err := peer.NewTable(1, 0)
		if err != nil {
			t.Fatal(err)
		}
		peer.resetCollectionDebt()
		if err := peerTable.RawSetInt(1, external); err != nil {
			t.Fatal(err)
		}
		if got, want := peer.runtime.collection.debt,
			uint64(len(long)); got != want {
			t.Fatalf(
				"cross-State external string debt = %d; want %d",
				got,
				want,
			)
		}

		_ = state.String("automatic-debt-cache-entry")
		first := state.runtime.collection.debt
		if first == 0 {
			t.Fatal("cached external string did not charge retained storage")
		}
		_ = state.String("automatic-debt-cache-entry")
		second := state.runtime.collection.debt
		_ = state.String("automatic-debt-cache-entry")
		if third := state.runtime.collection.debt; third != second {
			t.Fatalf(
				"warm string-cache hit changed debt from %d to %d",
				second,
				third,
			)
		}

		runtimeLong := strings.Repeat("y", shortStringLimit+1)
		state.resetCollectionDebt()
		runtimeString := state.runtime.strings.make(runtimeLong)
		if got, want := state.runtime.collection.debt,
			uint64(len(runtimeLong)); got != want {
			t.Fatalf("retained long-string debt = %d; want %d", got, want)
		}
		beforeExport := state.runtime.collection.debt
		beforeAttribution := len(
			state.runtime.collection.attributedStrings,
		)
		runtimeValue := stringValue(runtimeString)
		if got := state.runtime.collection.debt; got != beforeExport {
			t.Fatalf(
				"runtime string export changed debt from %d to %d",
				beforeExport,
				got,
			)
		}
		if got := len(state.runtime.collection.attributedStrings); got !=
			beforeAttribution {
			t.Fatalf(
				"runtime string export attribution count = %d; want %d",
				got,
				beforeAttribution,
			)
		}
		beforeIngress := state.runtime.collection.debt
		if err := table.RawSetInt(1, runtimeValue); err != nil {
			t.Fatal(err)
		}
		if got, want := state.runtime.collection.debt,
			beforeIngress+uint64(len(runtimeLong)); got != want {
			t.Fatalf(
				"runtime string re-entry debt = %d; want %d",
				got,
				want,
			)
		}
		afterIngress := state.runtime.collection.debt
		if err := table.RawSetInt(1, runtimeValue); err != nil {
			t.Fatal(err)
		}
		if got := state.runtime.collection.debt; got != afterIngress {
			t.Fatalf(
				"repeated runtime string re-entry changed debt from %d to %d",
				afterIngress,
				got,
			)
		}
	})

	t.Run("runtime string export", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		state.resetCollectionDebt()

		text := strings.Repeat("runtime-export-", 8)
		reference := state.runtime.strings.make(text)
		compact := stringSlot(reference)
		retainedBytes := stringRefRetainedBytes(reference)
		if got := state.runtime.collection.debt; got != retainedBytes {
			t.Fatalf(
				"runtime string debt = %d; want %d",
				got,
				retainedBytes,
			)
		}
		if state.runtime.collection.attributedStrings != nil {
			t.Fatal("internal runtime string entered the attribution set")
		}

		beforeHeap := state.semanticHeap().bytes
		beforeExport := state.runtime.collection.debt
		value := compact.owningValue()
		if got := state.runtime.collection.debt; got != beforeExport {
			t.Fatalf(
				"runtime string export changed debt from %d to %d",
				beforeExport,
				got,
			)
		}
		if state.runtime.collection.attributedStrings != nil {
			t.Fatal("runtime string export created attribution")
		}
		if afterHeap := state.semanticHeap().bytes; afterHeap != beforeHeap {
			t.Fatalf(
				"runtime string export changed heap from %d to %d",
				beforeHeap,
				afterHeap,
			)
		}

		beforeReentry := state.runtime.collection.debt
		reentered, err := state.runtime.importValue(value)
		if err != nil {
			t.Fatal(err)
		}
		if !rawSlotEqual(reentered, compact) {
			t.Fatal("same-State string re-entry changed compact identity")
		}
		if got, want := state.runtime.collection.debt,
			beforeReentry+retainedBytes; got != want {
			t.Fatalf(
				"same-State string re-entry debt = %d; want %d",
				got,
				want,
			)
		}
		if _, found := state.runtime.collection.attributedStrings[reference]; !found {
			t.Fatal("same-State string re-entry did not record attribution")
		}
		afterReentry := state.runtime.collection.debt
		if _, err := state.runtime.importValue(value); err != nil {
			t.Fatal(err)
		}
		if got := state.runtime.collection.debt; got != afterReentry {
			t.Fatalf(
				"repeated string re-entry changed debt from %d to %d",
				afterReentry,
				got,
			)
		}

		table, err := state.NewTable(1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := table.RawSetInt(1, value); err != nil {
			t.Fatal(err)
		}

		if _, failure := state.collectAndFinalize(); failure != nil {
			t.Fatal(failure)
		}
		if _, found := state.runtime.collection.attributedStrings[reference]; !found {
			t.Fatal("collection discarded live imported string attribution")
		}
		if got := state.runtime.collection.debt; got != 0 {
			t.Fatalf("completed cycle left string debt = %d", got)
		}
		if _, err := state.runtime.importValue(value); err != nil {
			t.Fatal(err)
		}
		if got := state.runtime.collection.debt; got != 0 {
			t.Fatalf("live post-cycle re-entry charged %d bytes", got)
		}

		if err := table.RawSetInt(1, Nil()); err != nil {
			t.Fatal(err)
		}
		if _, failure := state.collectAndFinalize(); failure != nil {
			t.Fatal(failure)
		}
		if state.runtime.collection.attributedStrings != nil {
			t.Fatal("collection retained a Go-only string attribution")
		}

		reentered, err = state.runtime.importValue(value)
		if err != nil {
			t.Fatal(err)
		}
		if !rawSlotEqual(reentered, compact) {
			t.Fatal("post-cycle string re-entry changed compact identity")
		}
		if got := state.runtime.collection.debt; got != retainedBytes {
			t.Fatalf(
				"post-cycle string re-entry debt = %d; want %d",
				got,
				retainedBytes,
			)
		}
	})

	t.Run("prototype trees", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		prototype, err := Compile(
			"@automatic-debt-prototype.lua",
			`return function() return "child constant" end`,
		)
		if err != nil {
			t.Fatal(err)
		}
		state.resetCollectionDebt()

		state.loadPrototypeObject(prototype)
		first := state.runtime.collection.debt
		beforeSecond := first
		state.loadPrototypeObject(prototype)
		second := state.runtime.collection.debt - beforeSecond
		if first != second {
			t.Fatalf(
				"first prototype load charged %d bytes; repeat charged %d",
				first,
				second,
			)
		}

		if _, failure := state.collectAndFinalize(); failure != nil {
			t.Fatal(failure)
		}
		state.loadPrototypeObject(prototype)
		if third := state.runtime.collection.debt; third != first {
			t.Fatalf(
				"reloaded swept prototype charged %d bytes; want %d",
				third,
				first,
			)
		}
	})
}

func TestNonRetainingBoundariesDoNotAdmitStrings(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	longText := strings.Repeat("boundary-read-key-", 5)
	shortText := "boundary-read-key"
	table, err := state.NewTable(0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString(longText, Number(11)); err != nil {
		t.Fatal(err)
	}

	storedShort := stateNeutralString(strings.Clone(shortText))
	shortProbe := stateNeutralString(strings.Clone(shortText))
	if storedShort.ref == shortProbe.ref {
		t.Fatal("short-string test backings unexpectedly match")
	}
	if status := table.runtimeObject().rawSetSlot(
		slotFromValue(storedShort),
		numberSlot(13),
	); status != tableKeyValid {
		t.Fatalf("short-key setup status = %d", status)
	}

	longProbe := state.String(strings.Clone(longText))
	storedLong, found := table.runtimeObject().rawSlot(
		slotFromValue(longProbe),
	)
	if !found || !rawSlotEqual(storedLong, numberSlot(11)) {
		t.Fatal("long-key setup is not readable by content")
	}
	key, _, found, err := table.runtimeObject().next(nilSlot)
	if err != nil || !found {
		t.Fatalf("first table key = (%v, %v)", found, err)
	}
	for found && (!key.isString() || stringSlotText(key) != longText) {
		key, _, found, err = table.runtimeObject().next(key)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatal("stored long key is absent")
	}
	if key.ref == longProbe.ref {
		t.Fatal("long-string test backings unexpectedly match")
	}
	storedLongKeyRef := key.ref

	enabled := false
	lookupTarget := table.Value()
	var nestedError error
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if !enabled {
			return frame.ReturnNumber(0)
		}
		longResult, indexErr := frame.Index(lookupTarget, longProbe)
		if indexErr != nil {
			nestedError = indexErr
			return frame.RaiseString(indexErr.Error())
		}
		shortResult, indexErr := frame.Index(lookupTarget, shortProbe)
		if indexErr != nil {
			nestedError = indexErr
			return frame.RaiseString(indexErr.Error())
		}
		longNumber, longOK := longResult.AsNumber()
		shortNumber, shortOK := shortResult.AsNumber()
		if !longOK || !shortOK {
			return frame.RaiseString("non-numeric lookup result")
		}
		return frame.ReturnNumber(longNumber + shortNumber)
	})
	if err != nil {
		t.Fatal(err)
	}
	var destination [1]Value
	if count, callErr := state.CallInto(
		host.Value(),
		nil,
		destination[:],
	); callErr != nil || count != 1 {
		t.Fatalf("warm native call = (%d, %v)", count, callErr)
	}

	enabled = true
	state.resetCollectionDebt()
	control := &state.runtime.collection
	if control.attributedStrings != nil {
		t.Fatal("setup retained a public long-string attribution")
	}
	hash := hashString(shortText)
	if state.runtime.strings.lookupProtected(shortText, hash).valid() {
		t.Fatal("setup admitted the short probe to the protected cache")
	}
	if value, _, _ := state.runtime.strings.lookupProbation(
		shortText,
		hash,
	); value.valid() {
		t.Fatal("setup admitted the short probe to the probation cache")
	}

	count, callErr := state.CallInto(
		host.Value(),
		nil,
		destination[:],
	)
	if callErr != nil || nestedError != nil || count != 1 {
		t.Fatalf(
			"read-only native lookup = (count %d, call %v, nested %v)",
			count,
			callErr,
			nestedError,
		)
	}
	if number, ok := destination[0].AsNumber(); !ok || number != 24 {
		t.Fatalf("read-only native lookup result = %v; want 24", destination[0])
	}
	if control.debt != 0 || control.attributedStrings != nil {
		t.Fatalf(
			"read-only native lookup changed collection state: debt=%d attributed=%d",
			control.debt,
			len(control.attributedStrings),
		)
	}
	if state.runtime.strings.lookupProtected(shortText, hash).valid() {
		t.Fatal("read-only lookup admitted the short probe to the protected cache")
	}
	if value, _, _ := state.runtime.strings.lookupProbation(
		shortText,
		hash,
	); value.valid() {
		t.Fatal("read-only lookup admitted the short probe to the probation cache")
	}

	lookupProxy, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	lookupMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := lookupMetatable.RawSetString("__index", table.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		lookupProxy.Value(),
		lookupMetatable,
	); err != nil {
		t.Fatal(err)
	}
	lookupTarget = lookupProxy.Value()
	state.resetCollectionDebt()
	count, callErr = state.CallInto(
		host.Value(),
		nil,
		destination[:],
	)
	if callErr != nil || nestedError != nil || count != 1 {
		t.Fatalf(
			"table-valued __index lookup = (count %d, call %v, nested %v)",
			count,
			callErr,
			nestedError,
		)
	}
	if number, ok := destination[0].AsNumber(); !ok || number != 24 {
		t.Fatalf("table-valued __index result = %v; want 24", destination[0])
	}
	if control.debt != 0 || control.attributedStrings != nil {
		t.Fatalf(
			"table-valued __index changed collection state: debt=%d attributed=%d",
			control.debt,
			len(control.attributedStrings),
		)
	}

	absent := state.String(
		strings.Clone(strings.Repeat("absent-boundary-key-", 4)),
	)
	if err := table.RawSet(absent, Nil()); err != nil {
		t.Fatal(err)
	}
	if control.debt != 0 || control.attributedStrings != nil {
		t.Fatalf(
			"absent nil write changed collection state: debt=%d attributed=%d",
			control.debt,
			len(control.attributedStrings),
		)
	}

	if err := table.RawSet(longProbe, Number(17)); err != nil {
		t.Fatal(err)
	}
	if got, ok := table.RawGetString(longText).AsNumber(); !ok || got != 17 {
		t.Fatalf("equal-content key update = %v; want 17", got)
	}
	if control.debt != 0 || control.attributedStrings != nil {
		t.Fatalf(
			"equal-content key update changed collection state: debt=%d attributed=%d",
			control.debt,
			len(control.attributedStrings),
		)
	}

	if err := table.RawSetString(longText, Nil()); err != nil {
		t.Fatal(err)
	}
	state.resetCollectionDebt()
	if err := table.RawSet(longProbe, Number(19)); err != nil {
		t.Fatal(err)
	}
	longKeySlot := slotFromValue(longProbe)
	longHash := uint32(stringSlotHash(longKeySlot))
	storeIndex, stored := table.runtimeObject().store.findStored(
		longKeySlot,
		longHash,
	)
	if !stored {
		t.Fatal("generic RawSet did not revive the long-key tombstone")
	}
	entry := table.runtimeObject().store.entries.at(storeIndex)
	if entry.key.ref != storedLongKeyRef {
		t.Fatal("generic RawSet replaced a retained tombstone key")
	}
	if control.debt != 0 || control.attributedStrings != nil {
		t.Fatalf(
			"generic tombstone revival changed collection state: debt=%d attributed=%d",
			control.debt,
			len(control.attributedStrings),
		)
	}

	if err := table.RawSetString(longText, Nil()); err != nil {
		t.Fatal(err)
	}
	setProxy, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	setMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := setMetatable.RawSetString("__newindex", table.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(setProxy.Value(), setMetatable); err != nil {
		t.Fatal(err)
	}
	setEnabled := false
	var setError error
	setter, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if !setEnabled {
			return frame.ReturnBool(true)
		}
		setError = frame.SetIndex(setProxy.Value(), longProbe, Number(23))
		if setError != nil {
			return frame.RaiseString(setError.Error())
		}
		return frame.ReturnBool(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, callErr := state.CallInto(
		setter.Value(),
		nil,
		destination[:],
	); callErr != nil || count != 1 {
		t.Fatalf("warm SetIndex call = (%d, %v)", count, callErr)
	}
	setEnabled = true
	state.resetCollectionDebt()
	if count, callErr := state.CallInto(
		setter.Value(),
		nil,
		destination[:],
	); callErr != nil || setError != nil || count != 1 {
		t.Fatalf(
			"tombstone SetIndex = (count %d, call %v, nested %v)",
			count,
			callErr,
			setError,
		)
	}
	storeIndex, stored = table.runtimeObject().store.findStored(
		longKeySlot,
		longHash,
	)
	if !stored {
		t.Fatal("table-valued __newindex did not revive the long-key tombstone")
	}
	entry = table.runtimeObject().store.entries.at(storeIndex)
	if entry.key.ref != storedLongKeyRef {
		t.Fatal("table-valued __newindex replaced a retained tombstone key")
	}
	if control.debt != 0 || control.attributedStrings != nil {
		t.Fatalf(
			"table-valued __newindex changed collection state: debt=%d attributed=%d",
			control.debt,
			len(control.attributedStrings),
		)
	}

	requireStableAllocationAccounting(t)
	indexAllocations := testing.AllocsPerRun(100, func() {
		control.attributedStrings = nil
		control.attributedStringHighWater = 0
		control.debt = 0
		control.requested = false
		control.refreshRunnable()
		count, callErr := state.CallInto(
			host.Value(),
			nil,
			destination[:],
		)
		if callErr != nil || count != 1 {
			panic("read-only native lookup failed")
		}
	})
	if indexAllocations != 0 {
		t.Fatalf(
			"warm read-only native lookup allocated %.2f objects",
			indexAllocations,
		)
	}
	absentAllocations := testing.AllocsPerRun(100, func() {
		control.attributedStrings = nil
		control.attributedStringHighWater = 0
		control.debt = 0
		control.requested = false
		control.refreshRunnable()
		if err := table.RawSet(absent, Nil()); err != nil {
			panic(err)
		}
	})
	if absentAllocations != 0 {
		t.Fatalf(
			"absent nil write allocated %.2f objects",
			absentAllocations,
		)
	}
}

func TestFailedBoundariesDoNotAdmitStrings(t *testing.T) {
	longValue := func(state *State, label string) Value {
		return state.String(strings.Clone(
			label + strings.Repeat("-external-backing", 5),
		))
	}
	assertNotAttributed := func(
		t *testing.T,
		state *State,
		value Value,
	) {
		t.Helper()
		compact := slotFromValue(value)
		reference := stringRef{ref: compact.ref, bits: compact.bits}
		if _, found := state.runtime.collection.attributedStrings[reference]; found {
			t.Fatalf("%q was attributed by a failed boundary", stringSlotText(compact))
		}
	}

	t.Run("constructors validate before admission", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		nonCallable := longValue(state, "thread-callable")
		state.resetCollectionDebt()
		if _, err := state.Call(nonCallable); err == nil {
			t.Fatal("Call accepted a non-callable string")
		}
		assertNotAttributed(t, state, nonCallable)

		state.resetCollectionDebt()
		if _, err := state.NewThread(nonCallable); err == nil {
			t.Fatal("NewThread accepted a non-callable string")
		}
		assertNotAttributed(t, state, nonCallable)

		peer := newCollectorTestState(t)
		defer peer.Close()
		foreign, err := peer.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		capture := longValue(state, "native-capture")
		state.resetCollectionDebt()
		if _, err := state.NewNativeFunction(
			func(frame Frame) Outcome { return frame.Return() },
			capture,
			foreign.Value(),
		); err == nil {
			t.Fatal("NewNativeFunction accepted a foreign capture")
		}
		assertNotAttributed(t, state, capture)
		if got := state.runtime.collection.debt; got != 0 {
			t.Fatalf("failed native construction charged %d bytes", got)
		}
	})

	t.Run("nested calls preflight before admission", func(t *testing.T) {
		state, err := New(Options{MaxFrames: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()

		handler, err := state.NewNativeFunction(
			func(frame Frame) Outcome { return frame.Return() },
		)
		if err != nil {
			t.Fatal(err)
		}
		target, err := state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		metatable, err := state.NewTable(0, 2)
		if err != nil {
			t.Fatal(err)
		}
		if err := metatable.RawSetString("__index", handler.Value()); err != nil {
			t.Fatal(err)
		}
		if err := metatable.RawSetString("__newindex", handler.Value()); err != nil {
			t.Fatal(err)
		}
		if err := state.SetMetatable(target.Value(), metatable); err != nil {
			t.Fatal(err)
		}

		nonCallable := longValue(state, "nested-callable")
		key := longValue(state, "nested-index-key")
		value := longValue(state, "nested-index-value")
		var callFailure, indexFailure, setFailure error
		host, err := state.NewNativeFunction(func(frame Frame) Outcome {
			_, callFailure = frame.Call(nonCallable)
			_, indexFailure = frame.Index(target.Value(), key)
			setFailure = frame.SetIndex(target.Value(), key, value)
			return frame.ReturnBool(true)
		})
		if err != nil {
			t.Fatal(err)
		}

		state.resetCollectionDebt()
		var destination [1]Value
		count, callErr := state.CallInto(
			host.Value(),
			nil,
			destination[:],
		)
		if callErr != nil || count != 1 {
			t.Fatalf("outer native call = (%d, %v)", count, callErr)
		}
		if callFailure == nil || indexFailure == nil || setFailure == nil {
			t.Fatalf(
				"nested failures = (call %v, index %v, set %v)",
				callFailure,
				indexFailure,
				setFailure,
			)
		}
		assertNotAttributed(t, state, nonCallable)
		assertNotAttributed(t, state, key)
		assertNotAttributed(t, state, value)
	})
}

func TestBoundaryMetamethodAdmissionTracksArgumentProvenance(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	internalText := strings.Repeat("runtime-created-chain-target-", 4)
	internalRef := state.runtime.strings.make(internalText)
	handlerCalls := 0
	handler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		handlerCalls++
		if target, ok := frame.String(0); !ok || target != internalText {
			return frame.RaiseString("unexpected metamethod target")
		}
		switch frame.ArgumentCount() {
		case 2:
			return frame.ReturnNumber(29)
		case 3:
			return frame.Return()
		default:
			return frame.RaiseString("unexpected metamethod argument count")
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	stringMetatable, err := state.NewTable(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := stringMetatable.RawSetString(
		"__index",
		handler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := stringMetatable.RawSetString(
		"__newindex",
		handler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		stringValue(internalRef),
		stringMetatable,
	); err != nil {
		t.Fatal(err)
	}

	source, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sourceMetatable, err := state.NewTable(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceMetatable.runtimeObject().rawSetStringSlot(
		"__index",
		stringSlot(internalRef),
	); err != nil {
		t.Fatal(err)
	}
	if err := sourceMetatable.runtimeObject().rawSetStringSlot(
		"__newindex",
		stringSlot(internalRef),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(source.Value(), sourceMetatable); err != nil {
		t.Fatal(err)
	}

	indexKey := state.String(strings.Clone(strings.Repeat(
		"external-index-key-",
		5,
	)))
	setKey := state.String(strings.Clone(strings.Repeat(
		"external-newindex-key-",
		5,
	)))
	setValue := state.String(strings.Clone(strings.Repeat(
		"external-newindex-value-",
		5,
	)))
	var indexFailure, setFailure error
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		result, failure := frame.Index(source.Value(), indexKey)
		indexFailure = failure
		if failure != nil {
			return frame.RaiseString(failure.Error())
		}
		number, ok := result.AsNumber()
		if !ok || number != 29 {
			return frame.RaiseString("unexpected __index result")
		}
		setFailure = frame.SetIndex(source.Value(), setKey, setValue)
		if setFailure != nil {
			return frame.RaiseString(setFailure.Error())
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}

	state.resetCollectionDebt()
	if _, err := state.Call(host.Value()); err != nil {
		t.Fatal(err)
	}
	if indexFailure != nil || setFailure != nil || handlerCalls != 2 {
		t.Fatalf(
			"metamethod chain = (index %v, set %v, calls %d); want two successful calls",
			indexFailure,
			setFailure,
			handlerCalls,
		)
	}

	control := &state.runtime.collection
	assertAttribution := func(value slot, want bool) {
		t.Helper()
		reference := stringRef{ref: value.ref, bits: value.bits}
		_, found := control.attributedStrings[reference]
		if found != want {
			t.Fatalf(
				"string %q attribution = %v; want %v",
				stringSlotText(value),
				found,
				want,
			)
		}
	}
	assertAttribution(stringSlot(internalRef), false)
	assertAttribution(slotFromValue(indexKey), true)
	assertAttribution(slotFromValue(setKey), true)
	assertAttribution(slotFromValue(setValue), true)
}

func TestLongStringAttributionCompactsAfterChurn(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	table, err := state.NewTable(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	survivor := state.String(
		"survivor-" + strings.Repeat("s", shortStringLimit),
	)
	if err := table.RawSetInt(1, survivor); err != nil {
		t.Fatal(err)
	}

	const attributedStringCount = minimumAttributedStringCompactionPeak * 4
	for index := 1; index < attributedStringCount; index++ {
		value := state.String(fmt.Sprintf(
			"discarded-%04d-%s",
			index,
			strings.Repeat("x", shortStringLimit),
		))
		if err := table.RawSetInt(2, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := table.RawSetInt(2, Nil()); err != nil {
		t.Fatal(err)
	}
	control := &state.runtime.collection
	if got := len(control.attributedStrings); got != attributedStringCount {
		t.Fatalf(
			"attribution size before collection = %d; want %d",
			got,
			attributedStringCount,
		)
	}
	if got := control.attributedStringHighWater; got !=
		attributedStringCount {
		t.Fatalf(
			"attribution high-water before collection = %d; want %d",
			got,
			attributedStringCount,
		)
	}

	if _, failure := state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if got := len(control.attributedStrings); got != 1 {
		t.Fatalf("attribution size after collection = %d; want 1", got)
	}
	reference := stringRef{
		ref:  slotFromValue(survivor).ref,
		bits: slotFromValue(survivor).bits,
	}
	if _, found := control.attributedStrings[reference]; !found {
		t.Fatal("compaction discarded the live string attribution")
	}
	if got := control.attributedStringHighWater; got != 1 {
		t.Fatalf(
			"attribution high-water after compaction = %d; want 1",
			got,
		)
	}

	if _, failure := state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if got := control.attributedStringHighWater; got != 1 {
		t.Fatalf(
			"stable collection changed attribution high-water to %d",
			got,
		)
	}
	runtime.KeepAlive(table)
}

func TestAutomaticCollectionRunsOnlyAtRootedExecutorSafePoints(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	target := mustLoadString(
		t,
		state,
		"@automatic-newtable.lua",
		`return 41, {answer = 42}`,
	)
	garbage := newTable(state, 0, 0)
	state.main.reserveValues(32)
	state.main.reserveFrames(4)
	state.resetCollectionDebt()
	state.runtime.collection.budget = 1
	if state.runtime.collection.requested {
		t.Fatal("fresh debt interval began with a due cycle")
	}

	results, err := state.Call(target.Value())
	if err != nil {
		t.Fatal(err)
	}
	if garbage.owner != nil {
		t.Fatal("automatic collection did not sweep prior garbage")
	}
	if len(results) != 2 {
		t.Fatalf("automatic collection changed result count to %d", len(results))
	}
	if number, ok := results[0].AsNumber(); !ok || number != 41 {
		t.Fatalf("first rooted result = %v; want 41", results[0])
	}
	table, ok := results[1].Table()
	if !ok {
		t.Fatalf("second rooted result = %v; want table", results[1])
	}
	assertTestValue(t, table.RawGetString("answer"), Number(42))
	if state.runtime.collection.requested {
		t.Fatal("completed automatic cycle remained requested")
	}
}

func TestAutomaticCollectionServicesPreexistingDebtAtRootEntry(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnNumber(42)
	})
	if err != nil {
		t.Fatal(err)
	}
	garbage := newTable(state, 0, 0)
	state.main.reserveValues(4)
	state.main.reserveFrames(1)
	state.resetCollectionDebt()
	state.runtime.collection.requestCycle()

	results, err := state.Call(target.Value())
	if err != nil {
		t.Fatal(err)
	}
	if garbage.owner != nil {
		t.Fatal("root-entry collection did not sweep prior garbage")
	}
	if len(results) != 1 {
		t.Fatalf("native result count = %d; want 1", len(results))
	}
	if number, ok := results[0].AsNumber(); !ok || number != 42 {
		t.Fatalf("native result = %v; want 42", results[0])
	}
	if state.runtime.collection.requested {
		t.Fatal("root-entry collection remained requested")
	}
}

func TestAutomaticCollectionRootsNativeReturnAtDepthZero(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	var returned *tableObject
	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		returned = newTable(state, 0, 1)
		if setErr := returned.rawSetStringSlot(
			"answer",
			numberSlot(42),
		); setErr != nil {
			return frame.RaiseString(setErr.Error())
		}
		return frame.returnOne(
			frame.activation(),
			slotFromTableObject(returned),
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	garbage := newTable(state, 0, 0)
	state.main.reserveValues(8)
	state.main.reserveFrames(2)
	state.resetCollectionDebt()
	state.runtime.collection.budget = 1

	results, err := state.Call(target.Value())
	if err != nil {
		t.Fatal(err)
	}
	if garbage.owner != nil {
		t.Fatal("native-return collection did not sweep prior garbage")
	}
	if returned == nil || returned.owner != state.runtime {
		t.Fatal("collection swept the compact native result")
	}
	if len(results) != 1 {
		t.Fatalf("native result count = %d; want 1", len(results))
	}
	table, ok := results[0].Table()
	if !ok || table.runtimeObject() != returned {
		t.Fatal("native return did not preserve canonical table identity")
	}
	assertTestValue(t, table.RawGetString("answer"), Number(42))
	if state.runtime.collection.requested {
		t.Fatal("native-return collection remained requested")
	}
}

func newCollectorTestState(t *testing.T) *State {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertCollectorPanic(t *testing.T, operation func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("operation did not panic")
		}
	}()
	operation()
}

func tableFromDiscardedState(
	t *testing.T,
) (*Table, weak.Pointer[State], weak.Pointer[tableObject]) {
	t.Helper()
	state := newCollectorTestState(t)
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	peer := newTable(state, 0, 0)
	stateReference := weak.Make(state)
	peerReference := weak.Make(peer)
	runtime.KeepAlive(peer)
	return table, stateReference, peerReference
}

func liveHostTableRoot(
	t *testing.T,
	state *State,
) (weak.Pointer[tableObject], weak.Pointer[hostToken]) {
	t.Helper()
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	object := table.runtimeObject()
	state.collectUnreachable()
	if object.owner != state.runtime {
		t.Fatal("owning table handle did not act as a root")
	}
	objectReference := weak.Make(object)
	tokenReference := weak.Make(table.token())
	runtime.KeepAlive(table)
	return objectReference, tokenReference
}

func waitForDiscardedHostTable(
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
		if token.Value() == nil && object.Value() == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("discarded host-rooted table remained reachable")
		case <-ticker.C:
		}
	}
}

func waitForWeakObjectHeader(
	t *testing.T,
	reference weak.Pointer[objectHeader],
) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for reference.Value() != nil {
		runtime.GC()
		select {
		case <-deadline.C:
			t.Fatal("semantically dead reference key remained Go-reachable")
		case <-ticker.C:
		}
	}
}

func collectorPrototype(t *testing.T, upvalues uint8) *Prototype {
	t.Helper()
	builder := testPrototypeBuilder(makeABC(opReturn, 0, 1, 0))
	builder.upvalues = int(upvalues)
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	return prototype
}

func mustRootCollectorObject(
	t *testing.T,
	state *State,
	name string,
	value slot,
) {
	t.Helper()
	if err := state.registry.rawSetStringSlot(name, value); err != nil {
		t.Fatal(err)
	}
}

func unrootCollectorObject(t *testing.T, state *State, name string) {
	t.Helper()
	if err := state.registry.rawSetStringSlot(name, nilSlot); err != nil {
		t.Fatal(err)
	}
}

func assertSemanticLedgerWellFormed(t *testing.T, state *State) {
	t.Helper()
	tables := make(map[*tableObject]struct{})
	for _, object := range state.objects.tables {
		if _, found := tables[object]; found {
			t.Fatal("table ledger contains a duplicate")
		}
		tables[object] = struct{}{}
		if object.owner != state.runtime ||
			object.gcMark != 0 {
			t.Fatal("table ledger contains an invalid object")
		}
	}
	functions := make(map[*functionObject]struct{})
	for _, object := range state.objects.functions {
		if _, found := functions[object]; found {
			t.Fatal("function ledger contains a duplicate")
		}
		functions[object] = struct{}{}
		if object.owner != state.runtime ||
			object.gcMark != 0 {
			t.Fatal("function ledger contains an invalid object")
		}
	}
	threads := make(map[*threadObject]struct{})
	for _, object := range state.objects.threads {
		if _, found := threads[object]; found {
			t.Fatal("thread ledger contains a duplicate")
		}
		threads[object] = struct{}{}
		if object.owner != state.runtime ||
			object.collectionMark() != 0 {
			t.Fatal("thread ledger contains an invalid object")
		}
	}
	userData := make(map[*userDataObject]struct{})
	for _, object := range state.objects.userData {
		if _, found := userData[object]; found {
			t.Fatal("userdata ledger contains a duplicate")
		}
		userData[object] = struct{}{}
		if object.owner != state.runtime ||
			object.gcMark != 0 {
			t.Fatal("userdata ledger contains an invalid object")
		}
	}
	summary := state.semanticHeap()
	if len(tables) != summary.tables ||
		len(functions) != summary.functions ||
		len(threads) != summary.threads ||
		len(userData) != summary.userData {
		t.Fatalf("ledger traversal and summary disagree: %+v", summary)
	}
}

func BenchmarkSemanticTableChurn(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()

	const collectionInterval = 1024
	uncollected := 0
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		newTable(state, 0, 0)
		uncollected++
		if uncollected == collectionInterval {
			result := state.collectUnreachable()
			if result.tables != uncollected {
				b.Fatalf(
					"semantic table sweep = %d; want %d",
					result.tables,
					uncollected,
				)
			}
			uncollected = 0
		}
	}
	if uncollected != 0 {
		result := state.collectUnreachable()
		if result.tables != uncollected {
			b.Fatalf(
				"final semantic table sweep = %d; want %d",
				result.tables,
				uncollected,
			)
		}
	}
}

func BenchmarkCompleteSemanticDeadTableChurn(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()

	const tablesPerCycle = 1024
	runCycle := func() {
		for range tablesPerCycle {
			newTable(state, 0, 0)
		}
		result, failure := state.collectAndFinalize()
		if failure != nil {
			b.Fatal(failure)
		}
		if result.tables != tablesPerCycle {
			b.Fatalf(
				"complete semantic table sweep = %d; want %d",
				result.tables,
				tablesPerCycle,
			)
		}
	}

	runCycle()
	b.ReportAllocs()
	b.ReportMetric(tablesPerCycle, "tables/cycle")
	b.ResetTimer()
	for range b.N {
		runCycle()
	}
}

func BenchmarkCompleteSemanticLiveTableCycle(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()

	const liveTables = 1024
	root := newTable(state, liveTables, 0)
	if err := state.registry.rawSetStringSlot(
		"collection-benchmark-root",
		slotFromTableObject(root),
	); err != nil {
		b.Fatal(err)
	}
	for index := 1; index <= liveTables; index++ {
		root.rawSetIntegerSlot(
			index,
			slotFromTableObject(newTable(state, 0, 0)),
		)
	}
	runCycle := func() {
		result, failure := state.collectAndFinalize()
		if failure != nil {
			b.Fatal(failure)
		}
		if result.total() != 0 {
			b.Fatalf(
				"live semantic cycle swept %d objects; want 0",
				result.total(),
			)
		}
	}

	runCycle()
	b.ReportAllocs()
	b.ReportMetric(liveTables, "tables/cycle")
	b.ResetTimer()
	for range b.N {
		runCycle()
	}
}

func BenchmarkCompleteSemanticMixedTableChurn(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()

	const (
		liveTables = 512
		deadTables = 512
	)
	root := newTable(state, liveTables, 0)
	if err := state.registry.rawSetStringSlot(
		"collection-benchmark-root",
		slotFromTableObject(root),
	); err != nil {
		b.Fatal(err)
	}
	for index := 1; index <= liveTables; index++ {
		root.rawSetIntegerSlot(
			index,
			slotFromTableObject(newTable(state, 0, 0)),
		)
	}
	runCycle := func() {
		for range deadTables {
			newTable(state, 0, 0)
		}
		result, failure := state.collectAndFinalize()
		if failure != nil {
			b.Fatal(failure)
		}
		if result.tables != deadTables ||
			result.functions != 0 ||
			result.threads != 0 ||
			result.userData != 0 {
			b.Fatalf(
				"mixed semantic sweep = %+v; want %d dead tables",
				result,
				deadTables,
			)
		}
	}

	runCycle()
	b.ReportAllocs()
	b.ReportMetric(liveTables, "live-tables/cycle")
	b.ReportMetric(deadTables, "dead-tables/cycle")
	b.ResetTimer()
	for range b.N {
		runCycle()
	}
}
