package lua

import (
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

	table, err := state.NewTableWithCapacity(3, 5)
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

func TestObjectVectorRetentionSlackPolicies(t *testing.T) {
	const wantTableSlack = 16 * 1024
	if maximumRetainedTableVectorSlack != wantTableSlack {
		t.Fatalf(
			"table-vector fixed slack = %d; want %d",
			maximumRetainedTableVectorSlack,
			wantTableSlack,
		)
	}

	table := &tableObject{}
	for _, test := range []struct {
		name   string
		live   int
		slack  int
		retain bool
	}{
		{name: "empty at limit", slack: wantTableSlack, retain: true},
		{name: "empty over limit", slack: wantTableSlack + 1},
		{
			name:   "live at limit",
			live:   1,
			slack:  wantTableSlack,
			retain: true,
		},
		{name: "live over limit", live: 1, slack: wantTableSlack + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			objects := make(
				[]*tableObject,
				test.live,
				test.live+test.slack,
			)
			if test.live != 0 {
				objects[0] = table
			}
			retained := retainTableObjectVector(objects)
			if len(retained) != test.live {
				t.Fatalf(
					"retained length = %d; want %d",
					len(retained),
					test.live,
				)
			}
			if test.live != 0 && retained[0] != table {
				t.Fatal("compaction discarded the live table")
			}
			sameCapacity := cap(retained) == cap(objects)
			if sameCapacity != test.retain {
				t.Fatalf(
					"capacity retained = %v; want %v",
					sameCapacity,
					test.retain,
				)
			}
		})
	}

	emptyOverGeneralLimit := make(
		[]*functionObject,
		0,
		maximumRetainedObjectVectorSlack+1,
	)
	if retained := retainObjectVector(
		emptyOverGeneralLimit,
	); retained != nil {
		t.Fatalf(
			"non-table vector over general limit retained capacity %d",
			cap(retained),
		)
	}
}

func TestWarmDeadTableChurnAllocatesOnlyTableObjects(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newCollectorTestState(t)
	defer state.Close()

	const tablesPerCycle = 1024
	runCycle := func() {
		for range tablesPerCycle {
			newTable(state, 0, 0)
		}
		result, failure := state.collectAndFinalize()
		if failure != nil {
			t.Fatal(failure)
		}
		if result.tables != tablesPerCycle {
			t.Fatalf(
				"semantic table sweep = %d; want %d",
				result.tables,
				tablesPerCycle,
			)
		}
	}

	runCycle()
	if allocations := testing.AllocsPerRun(
		100,
		runCycle,
	); allocations != tablesPerCycle {
		t.Fatalf(
			"warm dead-table churn allocations = %v; want %d",
			allocations,
			tablesPerCycle,
		)
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
	if number, ok := rawInt(table, 1).AsNumber(); !ok || number != 23 {
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
	if err := state.RawSetGlobal("collect_now", collect.Value()); err != nil {
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
	table, err := state.NewTableWithCapacity(1, 0)
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
	if number, ok := rawInt(table, 1).AsNumber(); !ok || number != 19 {
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
	table, err := state.NewTableWithCapacity(0, 0)
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
	table, err := state.NewTableWithCapacity(0, 0)
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
