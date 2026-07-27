package lua

import (
	"runtime"
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
		state.objects.upvalues == nil {
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
	if state.objects.tableWork != nil ||
		state.objects.functionWork != nil ||
		state.objects.threadWork != nil ||
		state.objects.userDataWork != nil ||
		state.objects.upvalues != nil {
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
