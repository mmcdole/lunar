package lua

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

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
		table, err := state.NewTableWithCapacity(1, 0)
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
		peerTable, err := peer.NewTableWithCapacity(1, 0)
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

		table, err := state.NewTableWithCapacity(1, 0)
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
	table, err := state.NewTableWithCapacity(0, 3)
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
			frame.ThrowString(indexErr.Error())
		}
		shortResult, indexErr := frame.Index(lookupTarget, shortProbe)
		if indexErr != nil {
			nestedError = indexErr
			frame.ThrowString(indexErr.Error())
		}
		longNumber, longOK := longResult.AsNumber()
		shortNumber, shortOK := shortResult.AsNumber()
		if !longOK || !shortOK {
			frame.ThrowString("non-numeric lookup result")
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

	lookupProxy, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	lookupMetatable, err := state.NewTableWithCapacity(0, 1)
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
	if got, ok := rawStr(table, longText).AsNumber(); !ok || got != 17 {
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
	setProxy, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	setMetatable, err := state.NewTableWithCapacity(0, 1)
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
			frame.ThrowString(setError.Error())
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
		foreign, err := peer.NewTableWithCapacity(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		rejected := longValue(state, "foreign-tree-leaf")
		state.resetCollectionDebt()
		if _, err := state.NewTableFrom(map[string]any{
			"kept":    rejected,
			"foreign": foreign.Value(),
		}); err == nil {
			t.Fatal("NewTableFrom accepted a foreign value")
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
		target, err := state.NewTableWithCapacity(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		metatable, err := state.NewTableWithCapacity(0, 2)
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
			frame.ThrowString("unexpected metamethod target")
		}
		switch frame.ArgumentCount() {
		case 2:
			return frame.ReturnNumber(29)
		case 3:
			return frame.Return()
		default:
			frame.ThrowString("unexpected metamethod argument count")
		}
		// Unreachable: the throw above does not return.
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}

	stringMetatable, err := state.NewTableWithCapacity(0, 2)
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

	source, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	sourceMetatable, err := state.NewTableWithCapacity(0, 2)
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
			frame.ThrowString(failure.Error())
		}
		number, ok := result.AsNumber()
		if !ok || number != 29 {
			frame.ThrowString("unexpected __index result")
		}
		setFailure = frame.SetIndex(source.Value(), setKey, setValue)
		if setFailure != nil {
			frame.ThrowString(setFailure.Error())
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

	table, err := state.NewTableWithCapacity(2, 0)
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
