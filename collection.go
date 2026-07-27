package lua

import (
	"runtime"
	"unsafe"
)

type objectMark uint8

const objectMarked objectMark = 1

type userDataFlags uint8

const userDataFinalized userDataFlags = 1

type weakMode uint8

const (
	weakKeys weakMode = 1 << iota
	weakValues
)

type weakTable struct {
	table *tableObject
	mode  weakMode
}

type threadFlags uint8

const (
	threadFlagMarked threadFlags = 1 << iota
	threadFlagMain
)

type collectionPhase uint8

const (
	collectionIdle collectionPhase = iota
	collectionMarking
	collectionSweeping
	collectionBroken
)

const (
	defaultCollectionPause          = 200
	defaultCollectionStepMultiplier = 200
)

// collectionControl is scheduling policy, kept separate from the object
// ledger and its transient mark/sweep state. A stopped collector still
// accepts explicit collection.
type collectionControl struct {
	pause          int
	stepMultiplier int
	stopped        bool
}

type stringBacking struct {
	data   unsafe.Pointer
	length int
}

func defaultCollectionControl() collectionControl {
	return collectionControl{
		pause:          defaultCollectionPause,
		stepMultiplier: defaultCollectionStepMultiplier,
	}
}

// objectLedger is State-owned. Compact objects point only at runtimeState, so
// retaining an ordinary object does not retain this ledger or its peers.
//
// Separate typed vectors avoid storing a redundant kind or link in every
// object. Separate typed work stacks also halve mark-work storage compared
// with tagged heterogeneous records.
type objectLedger struct {
	tables    []*tableObject
	functions []*functionObject
	threads   []*threadObject
	userData  []*userDataObject

	tableWork     []*tableObject
	functionWork  []*functionObject
	threadWork    []*threadObject
	userDataWork  []*userDataObject
	weakTables    []weakTable
	finalizers    []*userDataObject
	finalizerHead int
	upvalues      map[*upvalue]struct{}
	prototypes    map[*Prototype]struct{}
	names         map[*internedText]struct{}
	longStrings   map[*longString]struct{}
	stringBacking map[stringBacking]struct{}
	prototypeWork []*Prototype

	phase collectionPhase
}

type collectionResult struct {
	tables    int
	functions int
	threads   int
	userData  int
}

func (result collectionResult) total() int {
	return result.tables +
		result.functions +
		result.threads +
		result.userData
}

type semanticHeapSummary struct {
	bytes        uint64
	tables       int
	functions    int
	threads      int
	userData     int
	upvalues     int
	prototypes   int
	textBackings int
}

func (summary semanticHeapSummary) objects() int {
	return summary.tables +
		summary.functions +
		summary.threads +
		summary.userData
}

func (thread *threadObject) collectionMark() objectMark {
	return objectMark(thread.flags & threadFlagMarked)
}

func (thread *threadObject) setCollectionMark(mark objectMark) {
	thread.flags = thread.flags&^threadFlagMarked |
		threadFlags(mark&objectMarked)
}

func (thread *threadObject) isMain() bool {
	return thread != nil && thread.flags&threadFlagMain != 0
}

func (state *State) registerTable(table *tableObject) {
	if state == nil ||
		state.runtime == nil ||
		state.runtime.closed.Load() ||
		state.objects.phase != collectionIdle ||
		table == nil ||
		table.owner != nil ||
		table.gcMark != 0 {
		panic("lua: invalid table registration")
	}
	tables := appendObjectVector(state.objects.tables, table)
	table.owner = state.runtime
	state.objects.tables = tables
}

func (state *State) registerFunction(function *functionObject) {
	if state == nil ||
		state.runtime == nil ||
		state.runtime.closed.Load() ||
		state.objects.phase != collectionIdle ||
		function == nil ||
		function.owner != nil ||
		function.gcMark != 0 {
		panic("lua: invalid function registration")
	}
	functions := appendObjectVector(
		state.objects.functions,
		function,
	)
	function.owner = state.runtime
	state.objects.functions = functions
}

func (state *State) registerThread(thread *threadObject) {
	if state == nil ||
		state.runtime == nil ||
		state.runtime.closed.Load() ||
		state.objects.phase != collectionIdle ||
		thread == nil ||
		thread.owner != nil ||
		thread.collectionMark() != 0 {
		panic("lua: invalid thread registration")
	}
	threads := appendObjectVector(state.objects.threads, thread)
	thread.owner = state.runtime
	state.objects.threads = threads
}

func (state *State) registerUserData(data *userDataObject) {
	if state == nil ||
		state.runtime == nil ||
		state.runtime.closed.Load() ||
		state.objects.phase != collectionIdle ||
		data == nil ||
		data.owner != nil ||
		data.gcMark != 0 {
		panic("lua: invalid userdata registration")
	}
	userData := appendObjectVector(state.objects.userData, data)
	data.owner = state.runtime
	state.objects.userData = userData
}

const initialObjectVectorCapacity = 4

func appendObjectVector[T any](objects []*T, object *T) []*T {
	if len(objects) < cap(objects) {
		return append(objects, object)
	}
	capacity := initialObjectVectorCapacity
	if current := cap(objects); current != 0 {
		maxInt := int(^uint(0) >> 1)
		if current > maxInt/2 {
			panic("lua: object ledger capacity overflow")
		}
		capacity = current * 2
	}
	grown := make([]*T, len(objects), capacity)
	copy(grown, objects)
	return append(grown, object)
}

// collectUnreachable performs one synchronous semantic mark/sweep pass. It
// classifies and retains finalizable userdata but deliberately does not enter
// Lua; collectAndFinalize invokes the persistent queue only after the
// collector has returned to its idle phase.
func (state *State) collectUnreachable() collectionResult {
	if state == nil ||
		state.runtime == nil ||
		state.runtime.closed.Load() {
		return collectionResult{}
	}
	ledger := &state.objects
	if ledger.phase == collectionBroken {
		panic("lua: semantic collector is unusable after a failed sweep")
	}
	if ledger.phase != collectionIdle {
		panic("lua: recursive semantic collection")
	}
	succeeded := false
	ledger.phase = collectionMarking
	defer func() {
		ledger.clearWork()
		if !succeeded {
			switch ledger.phase {
			case collectionMarking:
				state.clearCollectionMarks()
				ledger.phase = collectionIdle
			case collectionSweeping:
				ledger.phase = collectionBroken
			}
		}
	}()

	state.validateCollectionLedger()
	state.markCollectionRoots()
	state.drainCollectionWork()
	state.separateFinalizers(false)
	state.markPendingFinalizers()
	state.drainCollectionWork()

	var result collectionResult
	ledger.phase = collectionSweeping
	state.clearWeakTables()
	result.threads = state.sweepThreads()
	result.functions = state.sweepFunctions()
	result.tables = state.sweepTables()
	result.userData = state.sweepUserData()
	state.runtime.hosts.prune()
	ledger.phase = collectionIdle
	succeeded = true
	return result
}

func (state *State) validateCollectionLedger() {
	for _, table := range state.objects.tables {
		if table == nil ||
			table.owner != state.runtime ||
			table.gcMark != 0 {
			panic("lua: invalid table in object ledger")
		}
	}
	for _, function := range state.objects.functions {
		if function == nil ||
			function.owner != state.runtime ||
			function.gcMark != 0 {
			panic("lua: invalid function in object ledger")
		}
	}
	for _, thread := range state.objects.threads {
		if thread == nil ||
			thread.owner != state.runtime ||
			thread.collectionMark() != 0 {
			panic("lua: invalid thread in object ledger")
		}
	}
	for _, data := range state.objects.userData {
		if data == nil ||
			data.owner != state.runtime ||
			data.gcMark != 0 {
			panic("lua: invalid userdata in object ledger")
		}
	}
	ledger := &state.objects
	if ledger.finalizerHead < 0 ||
		ledger.finalizerHead > len(ledger.finalizers) {
		panic("lua: invalid finalizer queue")
	}
	for _, data := range ledger.finalizers[:ledger.finalizerHead] {
		if data != nil {
			panic("lua: consumed finalizer retained its userdata")
		}
	}
	for _, data := range ledger.finalizers[ledger.finalizerHead:] {
		if data == nil ||
			data.owner != state.runtime ||
			data.flags&userDataFinalized == 0 {
			panic("lua: invalid pending finalizer")
		}
	}
}

func (state *State) clearCollectionMarks() {
	for _, table := range state.objects.tables {
		if table != nil {
			table.gcMark = 0
		}
	}
	for _, function := range state.objects.functions {
		if function != nil {
			function.gcMark = 0
		}
	}
	for _, thread := range state.objects.threads {
		if thread != nil {
			thread.setCollectionMark(0)
		}
	}
	for _, data := range state.objects.userData {
		if data != nil {
			data.gcMark = 0
		}
	}
}

func (state *State) markCollectionRoots() {
	state.markThread(state.main)
	state.markThread(state.active)
	state.markTable(state.registry)
	state.markUserData(state.packageSentinel)
	for _, metatable := range state.typeMetatables {
		state.markTable(metatable)
	}
	state.markErrorValue(state.execution.failure)
	state.markErrorValue(state.execution.pendingExit)
	state.runtime.hosts.markCollectionRoots(state)
}

func (state *State) markPendingFinalizers() {
	ledger := &state.objects
	for _, data := range ledger.finalizers[ledger.finalizerHead:] {
		state.markUserData(data)
	}
}

// separateFinalizers classifies userdata in reverse creation order. A normal
// collection considers only dead userdata after the ordinary root graph has
// drained. Close considers every remaining userdata exactly once.
//
// Lua 5.1 marks each considered userdata finalized at this point. A current
// raw __gc value, callable or not, moves it into the persistent call-order
// queue. The handler itself is deliberately not captured: Lua looks it up
// again immediately before the call.
func (state *State) separateFinalizers(all bool) {
	ledger := &state.objects
	ledger.compactFinalizerQueue()
	for index := len(ledger.userData) - 1; index >= 0; index-- {
		data := ledger.userData[index]
		if data.flags&userDataFinalized != 0 ||
			!all && data.gcMark == objectMarked {
			continue
		}
		data.flags |= userDataFinalized
		if _, found := metatableEventSlot(data.metatable, metaGC); found {
			ledger.appendFinalizer(data)
		}
	}
}

// collectAndFinalize performs a complete collection from an idle State and
// runs its pending finalizers on the main thread. The current finalizer is
// consumed before invocation, so an error leaves only later work queued.
func (state *State) collectAndFinalize() (collectionResult, *Error) {
	if state == nil ||
		state.runtime == nil ||
		state.runtime.closed.Load() {
		return collectionResult{}, nil
	}
	if state.active != nil {
		panic("lua: idle collection requested during execution")
	}
	result := state.collectUnreachable()
	return result, state.runPendingFinalizers(nil)
}

// Collect performs one complete semantic collection and runs pending userdata
// finalizers. Finalizers may execute arbitrary non-yielding Lua. Collect
// resumes automatic collection after success. It returns a finalizer's Lua
// error when one occurs; later pending finalizers remain queued for another
// collection.
//
// Collect requires an idle State. A NativeFunc can use Frame.Collect while Lua
// is executing.
func (state *State) Collect() error {
	if _, err := state.prepareReadyMainThread(); err != nil {
		return err
	}
	state.runtime.collection.stopped = false
	_, failure := state.collectAndFinalize()
	if failure != nil {
		return failure.exposeValue()
	}
	state.runtime.collection.stopped = false
	return nil
}

// HeapBytes reports the State's target-architecture logical Lua heap size.
//
// The count covers registered Lua objects and their owned execution and table
// storage, unique retained string-backing views, and reachable immutable Prototypes.
// It is not process RSS or Go allocator usage; opaque userdata payloads, host
// ownership tokens, collector scratch, State infrastructure, and allocator
// rounding are not attributed to it. HeapBytes scans the live object ledger;
// it is a measurement operation rather than a cheap per-allocation counter.
func (state *State) HeapBytes() (uint64, error) {
	if err := state.checkOpen(); err != nil {
		return 0, err
	}
	return state.semanticHeap().bytes, nil
}

// collectAndFinalize performs the same operation from a live native Frame.
// Finalizers use the existing nested-call checkpoint and therefore cannot
// yield across the collecting callback.
func (frame Frame) collectAndFinalize() (collectionResult, *Error) {
	frame.activation()
	state := frame.thread.state
	result := state.collectUnreachable()
	return result, state.runPendingFinalizers(&frame)
}

// Collect performs one complete semantic collection from a NativeFunc and
// runs pending userdata finalizers before returning. Finalizers may execute
// arbitrary non-yielding Lua. Collect resumes automatic collection after
// success. A finalizer's Lua error is returned as an *Error through the error
// interface.
func (frame Frame) Collect() error {
	frame.activation()
	frame.thread.owner.collection.stopped = false
	_, failure := frame.collectAndFinalize()
	if failure != nil {
		return failure.exposeValue()
	}
	frame.thread.owner.collection.stopped = false
	return nil
}

// HeapBytes reports the target-architecture logical Lua heap size of frame's
// State. It has the same accounting boundary as State.HeapBytes.
func (frame Frame) HeapBytes() uint64 {
	frame.activation()
	return frame.thread.state.semanticHeap().bytes
}

func (state *State) runPendingFinalizers(frame *Frame) *Error {
	for {
		handler, argument, pending, invoke :=
			state.nextPendingFinalizer()
		if !pending {
			return nil
		}
		if !invoke {
			continue
		}
		arguments := [1]slot{argument}
		var failure *Error
		if frame != nil {
			failure = frame.callCompactNone(handler, arguments[:])
		} else {
			failure = state.callMainCompactNone(handler, arguments[:])
		}
		if failure != nil {
			return failure
		}
	}
}

func (state *State) nextPendingFinalizer() (
	handler slot,
	argument slot,
	pending bool,
	invoke bool,
) {
	data, found := state.objects.takeFinalizer()
	if !found {
		return nilSlot, nilSlot, false, false
	}
	if data == nil ||
		data.owner != state.runtime ||
		data.flags&userDataFinalized == 0 {
		panic("lua: invalid dequeued finalizer")
	}
	handler, found = metatableEventSlot(data.metatable, metaGC)
	if !found {
		return nilSlot, nilSlot, true, false
	}
	return handler, slotFromUserDataObject(data), true, true
}

// finalizeForClose separates every remaining userdata once and invokes every
// queued handler while the State is still fully usable. Lua failures are
// ignored as required by lua_close. A panic from host code is remembered,
// cleanup continues deterministically, and Close re-panics after teardown.
func (state *State) finalizeForClose() (any, bool) {
	state.separateFinalizers(true)
	var firstPanic any
	panicked := false
	for {
		handler, argument, pending, invoke :=
			state.nextPendingFinalizer()
		if !pending {
			return firstPanic, panicked
		}
		if !invoke {
			continue
		}
		func() {
			completed := false
			defer func() {
				if completed {
					return
				}
				current := recover()
				if !panicked {
					firstPanic = current
					panicked = true
				}
			}()
			arguments := [1]slot{argument}
			_ = state.callMainCompactNone(handler, arguments[:])
			completed = true
		}()
	}
}

func (state *State) markErrorValue(failure *Error) {
	if value, valid := failure.valueSlot(); valid {
		state.markSlot(value)
	}
}

func (directory *hostDirectory) markCollectionRoots(state *State) {
	directory.mutex.Lock()
	defer directory.mutex.Unlock()

	for objectReference, tokenReference := range directory.entries {
		object := objectReference.Value()
		token := tokenReference.Value()
		if object == nil || token == nil {
			continue
		}
		if token.owner != state.runtime ||
			token.object != unsafe.Pointer(object) {
			panic("lua: corrupt host-token root")
		}
		state.markObject(token.kind, token.object)
		runtime.KeepAlive(token)
	}
}

func (state *State) markSlot(value slot) {
	switch value.kind() {
	case TableKind:
		state.markTable((*tableObject)(value.ref))
	case FunctionKind:
		state.markFunction((*functionObject)(value.ref))
	case ThreadKind:
		state.markThread((*threadObject)(value.ref))
	case UserDataKind:
		state.markUserData((*userDataObject)(value.ref))
	}
}

func (state *State) markObject(kind Kind, object unsafe.Pointer) {
	switch kind {
	case TableKind:
		state.markTable((*tableObject)(object))
	case FunctionKind:
		state.markFunction((*functionObject)(object))
	case ThreadKind:
		state.markThread((*threadObject)(object))
	case UserDataKind:
		state.markUserData((*userDataObject)(object))
	default:
		panic("lua: invalid collected-object kind")
	}
}

func (state *State) markTable(table *tableObject) {
	if table == nil {
		return
	}
	if table.owner != state.runtime {
		panic("lua: unregistered table in Lua graph")
	}
	if table.gcMark == objectMarked {
		return
	}
	table.gcMark = objectMarked
	state.objects.tableWork = appendObjectVector(
		state.objects.tableWork,
		table,
	)
}

func (state *State) markFunction(function *functionObject) {
	if function == nil {
		return
	}
	if function.owner != state.runtime {
		panic("lua: unregistered function in Lua graph")
	}
	if function.gcMark == objectMarked {
		return
	}
	function.gcMark = objectMarked
	state.objects.functionWork = appendObjectVector(
		state.objects.functionWork,
		function,
	)
}

func (state *State) markThread(thread *threadObject) {
	if thread == nil {
		return
	}
	if thread.owner != state.runtime {
		panic("lua: unregistered thread in Lua graph")
	}
	if thread.collectionMark() == objectMarked {
		return
	}
	thread.setCollectionMark(objectMarked)
	state.objects.threadWork = appendObjectVector(
		state.objects.threadWork,
		thread,
	)
}

func (state *State) markUserData(data *userDataObject) {
	if data == nil {
		return
	}
	if data.owner != state.runtime {
		panic("lua: unregistered userdata in Lua graph")
	}
	if data.gcMark == objectMarked {
		return
	}
	data.gcMark = objectMarked
	state.objects.userDataWork = appendObjectVector(
		state.objects.userDataWork,
		data,
	)
}

func (state *State) drainCollectionWork() {
	ledger := &state.objects
	for len(ledger.tableWork) != 0 ||
		len(ledger.functionWork) != 0 ||
		len(ledger.threadWork) != 0 ||
		len(ledger.userDataWork) != 0 {
		if table := popObject(&ledger.tableWork); table != nil {
			state.traceTable(table)
		}
		if function := popObject(&ledger.functionWork); function != nil {
			state.traceFunction(function)
		}
		if thread := popObject(&ledger.threadWork); thread != nil {
			state.traceThread(thread)
		}
		if data := popObject(&ledger.userDataWork); data != nil {
			state.traceUserData(data)
		}
	}
}

func popObject[T any](work *[]*T) *T {
	if len(*work) == 0 {
		return nil
	}
	last := len(*work) - 1
	object := (*work)[last]
	(*work)[last] = nil
	*work = (*work)[:last]
	return object
}

func (state *State) traceTable(table *tableObject) {
	state.markTable(table.metatable)
	mode := tableWeakMode(table)
	if mode != 0 {
		state.objects.weakTables = append(
			state.objects.weakTables,
			weakTable{table: table, mode: mode},
		)
	}
	if mode&weakValues == 0 {
		for _, value := range table.array.values() {
			state.markSlot(value)
		}
	}
	entries := table.store.entries.values()
	for index := range entries {
		entry := &entries[index]
		if entry.hash == entryHashEmpty {
			continue
		}
		if entry.value.isNil() {
			retireDeletedReferenceKey(entry)
			continue
		}
		if mode&weakKeys == 0 {
			state.markSlot(entry.key)
		}
		if mode&weakValues == 0 {
			state.markSlot(entry.value)
		}
	}
}

func tableWeakMode(table *tableObject) weakMode {
	if table == nil || table.metatable == nil {
		return 0
	}
	value, found := metatableEventSlot(table.metatable, metaMode)
	if !found || !value.isString() {
		return 0
	}
	var mode weakMode
	text := stringSlotText(value)
	for index := 0; index < len(text) && text[index] != 0; index++ {
		switch text[index] {
		case 'k':
			mode |= weakKeys
		case 'v':
			mode |= weakValues
		}
		if mode == (weakKeys | weakValues) {
			return mode
		}
	}
	return mode
}

func (state *State) clearWeakTables() {
	for _, weak := range state.objects.weakTables {
		table := weak.table
		if table == nil ||
			table.owner != state.runtime ||
			table.gcMark != objectMarked ||
			weak.mode == 0 {
			panic("lua: invalid weak table")
		}
		if weak.mode&weakValues != 0 {
			array := table.array.values()
			for index := range array {
				if array[index].isNil() ||
					!state.weakSlotCleared(array[index], false) {
					continue
				}
				writeSlot(&array[index], nilSlot)
				table.arrayUsed--
			}
		}
		entries := table.store.entries.values()
		for index := range entries {
			entry := &entries[index]
			// Lua 5.1 checks both record sides. During ordinary marking
			// the configured strong side is already marked; finalization
			// additionally clears finalized userdata values while keeping
			// them as keys for one cycle.
			if entry.hash == entryHashEmpty ||
				entry.value.isNil() ||
				!state.weakSlotCleared(entry.key, true) &&
					!state.weakSlotCleared(entry.value, false) {
				continue
			}
			table.store.deleteAt(index)
			table.recordIntegerDeleted()
			retireDeletedReferenceKey(entry)
		}
	}
}

func (state *State) weakSlotCleared(value slot, key bool) bool {
	switch value.kind() {
	case TableKind:
		table := (*tableObject)(value.ref)
		if table.owner != state.runtime {
			panic("lua: foreign table in weak table")
		}
		return table.gcMark != objectMarked
	case FunctionKind:
		function := (*functionObject)(value.ref)
		if function.owner != state.runtime {
			panic("lua: foreign function in weak table")
		}
		return function.gcMark != objectMarked
	case ThreadKind:
		thread := (*threadObject)(value.ref)
		if thread.owner != state.runtime {
			panic("lua: foreign thread in weak table")
		}
		return thread.collectionMark() != objectMarked
	case UserDataKind:
		data := (*userDataObject)(value.ref)
		if data.owner != state.runtime {
			panic("lua: foreign userdata in weak table")
		}
		return data.gcMark != objectMarked ||
			!key && data.flags&userDataFinalized != 0
	default:
		return false
	}
}

func (state *State) traceFunction(function *functionObject) {
	state.markTable(function.environment)
	if function.prototype == nil {
		for _, capture := range function.nativeBodyUnchecked().captures {
			state.markSlot(capture)
		}
		return
	}
	for index := 0; index < int(function.prototype.upvalues); index++ {
		upvalue := function.luaUpvalueUnchecked(index)
		if upvalue == nil || upvalue.cell == nil {
			panic("lua: invalid registered function upvalue")
		}
		state.markSlot(upvalue.read())
	}
}

func (state *State) traceThread(thread *threadObject) {
	state.markTable(thread.globals)
	extent := thread.liveValueExtent()
	if extent < 0 || extent > len(thread.values) {
		panic("lua: invalid live thread extent")
	}
	for _, value := range thread.values[:extent] {
		state.markSlot(value)
	}
	for _, frame := range thread.frames {
		state.markFunction(frame.function)
	}
	for upvalue := thread.openUpvalues; upvalue != nil; upvalue = upvalue.next {
		if upvalue.cell == nil {
			panic("lua: invalid open upvalue")
		}
		state.markSlot(upvalue.read())
	}
}

func (state *State) traceUserData(data *userDataObject) {
	state.markTable(data.metatable)
	state.markTable(data.environment)
}

func (state *State) sweepThreads() int {
	ledger := &state.objects
	live := ledger.threads[:0]
	for _, thread := range ledger.threads {
		if thread.collectionMark() == objectMarked {
			thread.setCollectionMark(0)
			live = append(live, thread)
		} else {
			thread.releaseCollected()
		}
	}
	swept := len(ledger.threads) - len(live)
	clear(ledger.threads[len(live):])
	ledger.threads = retainObjectVector(live)
	return swept
}

func (state *State) sweepFunctions() int {
	ledger := &state.objects
	live := ledger.functions[:0]
	for _, function := range ledger.functions {
		if function.gcMark == objectMarked {
			function.gcMark = 0
			live = append(live, function)
		} else {
			function.releaseCollected()
		}
	}
	swept := len(ledger.functions) - len(live)
	clear(ledger.functions[len(live):])
	ledger.functions = retainObjectVector(live)
	return swept
}

func (state *State) sweepTables() int {
	ledger := &state.objects
	live := ledger.tables[:0]
	for _, table := range ledger.tables {
		if table.gcMark == objectMarked {
			table.gcMark = 0
			live = append(live, table)
		} else {
			*table = tableObject{}
		}
	}
	swept := len(ledger.tables) - len(live)
	clear(ledger.tables[len(live):])
	ledger.tables = retainObjectVector(live)
	return swept
}

func (state *State) sweepUserData() int {
	ledger := &state.objects
	live := ledger.userData[:0]
	for _, data := range ledger.userData {
		if data.gcMark == objectMarked {
			data.gcMark = 0
			live = append(live, data)
		} else {
			_, _ = releaseCollectedResource(state, data)
			*data = userDataObject{}
		}
	}
	swept := len(ledger.userData) - len(live)
	clear(ledger.userData[len(live):])
	ledger.userData = retainObjectVector(live)
	return swept
}

const maximumRetainedObjectVectorSlack = 1024

func retainObjectVector[T any](objects []*T) []*T {
	if len(objects) == 0 {
		if cap(objects) <= maximumRetainedObjectVectorSlack {
			return objects
		}
		return nil
	}
	slackCapacity := cap(objects) - len(objects)
	if slackCapacity <= len(objects) ||
		slackCapacity <= maximumRetainedObjectVectorSlack {
		return objects
	}
	slack := len(objects) / 4
	if slack < 16 {
		slack = 16
	}
	if slack > maximumRetainedObjectVectorSlack {
		slack = maximumRetainedObjectVectorSlack
	}
	compacted := make([]*T, len(objects), len(objects)+slack)
	copy(compacted, objects)
	clear(objects)
	return compacted
}

func (thread *threadObject) releaseCollected() {
	thread.closeUpvalues(0)
	clear(thread.values)
	clear(thread.frames)
	clear(thread.continuations)
	*thread = threadObject{}
}

func (function *functionObject) releaseCollected() {
	if function.prototype == nil {
		body := function.nativeBodyUnchecked()
		clear(body.captures)
		*body = nativeFunctionData{}
	} else if function.body != nil {
		upvalues := unsafe.Slice(
			(**upvalue)(function.body),
			int(function.prototype.upvalues),
		)
		clear(upvalues)
	}
	*function = functionObject{}
}

func (ledger *objectLedger) clearWork() {
	clearCollectionWork(&ledger.tableWork)
	clearCollectionWork(&ledger.functionWork)
	clearCollectionWork(&ledger.threadWork)
	clearCollectionWork(&ledger.userDataWork)
	clearCollectionWork(&ledger.weakTables)
}

func (ledger *objectLedger) appendFinalizer(data *userDataObject) {
	if data == nil {
		panic("lua: cannot queue a nil finalizer")
	}
	ledger.finalizers = appendObjectVector(ledger.finalizers, data)
}

func (ledger *objectLedger) compactFinalizerQueue() {
	if ledger.finalizerHead == 0 {
		return
	}
	remaining := len(ledger.finalizers) - ledger.finalizerHead
	if remaining != 0 {
		copy(
			ledger.finalizers[:remaining],
			ledger.finalizers[ledger.finalizerHead:],
		)
	}
	clear(ledger.finalizers[remaining:])
	ledger.finalizers = ledger.finalizers[:remaining]
	ledger.finalizerHead = 0
	if remaining == 0 &&
		cap(ledger.finalizers) > maximumRetainedCollectionWork {
		ledger.finalizers = nil
	}
}

func (ledger *objectLedger) takeFinalizer() (*userDataObject, bool) {
	if ledger.finalizerHead == len(ledger.finalizers) {
		ledger.compactFinalizerQueue()
		return nil, false
	}
	data := ledger.finalizers[ledger.finalizerHead]
	ledger.finalizers[ledger.finalizerHead] = nil
	ledger.finalizerHead++
	if ledger.finalizerHead == len(ledger.finalizers) {
		ledger.compactFinalizerQueue()
	}
	return data, true
}

const maximumRetainedCollectionWork = 1024

func clearCollectionWork[T any](work *[]T) {
	clear(*work)
	if cap(*work) > maximumRetainedCollectionWork {
		*work = nil
		return
	}
	*work = (*work)[:0]
}

// detachObjectsForClose releases State-owned roots and scratch. Live object
// contents are otherwise preserved for post-close observation through owning
// handles.
func (state *State) detachObjectsForClose() {
	ledger := &state.objects
	for _, thread := range ledger.threads {
		thread.closeUpvalues(0)
		clear(thread.values)
		clear(thread.frames)
		clear(thread.continuations)
		thread.values = nil
		thread.frames = nil
		thread.continuations = nil
		thread.openUpvalues = nil
		thread.top = 0
		thread.frameExtent = 0
		thread.globals = nil
		thread.status = ThreadClosed
		thread.setCollectionMark(0)
	}
	for _, function := range ledger.functions {
		function.gcMark = 0
	}
	for _, table := range ledger.tables {
		table.gcMark = 0
	}
	for _, data := range ledger.userData {
		data.gcMark = 0
	}
	state.objects = objectLedger{}
}

// semanticHeap reports the target-architecture logical storage retained by
// this State's Lua graph. Strings and immutable Prototypes are State-neutral
// representations, but each is attributed once when this State retains it.
// Opaque userdata payloads, host tokens, and Go allocator size-class rounding
// remain outside the accounting boundary.
func (state *State) semanticHeap() semanticHeapSummary {
	if state == nil {
		return semanticHeapSummary{}
	}
	ledger := &state.objects
	ledger.resetSemanticHeapScratch()
	var summary semanticHeapSummary
	defer ledger.releaseSemanticHeapScratch()

	ledgerEntryBytes := uint64(unsafe.Sizeof((*tableObject)(nil)))
	for _, table := range ledger.tables {
		summary.tables++
		summary.bytes += uint64(unsafe.Sizeof(*table)) +
			ledgerEntryBytes
		summary.bytes += uint64(table.array.cap()) *
			uint64(unsafe.Sizeof(slot{}))
		summary.bytes += uint64(table.store.entries.cap()) *
			uint64(unsafe.Sizeof(tableEntry{}))
		for _, value := range table.array.values() {
			summary.addSlot(ledger, value)
		}
		for _, entry := range table.store.entries.values() {
			if entry.key.isDeadReferenceKey() {
				summary.bytes += uint64(unsafe.Sizeof(deadReferenceKey{}))
			}
			summary.addSlot(ledger, entry.key)
			summary.addSlot(ledger, entry.value)
		}
	}
	for _, function := range ledger.functions {
		summary.functions++
		if function.prototype == nil {
			summary.bytes += uint64(unsafe.Sizeof(
				nativeFunctionAllocation{},
			)) + ledgerEntryBytes
			body := function.nativeBodyUnchecked()
			summary.bytes += uint64(cap(body.captures)) *
				uint64(unsafe.Sizeof(slot{}))
			for _, capture := range body.captures {
				summary.addSlot(ledger, capture)
			}
		} else {
			summary.bytes += uint64(unsafe.Sizeof(*function)) +
				ledgerEntryBytes
			summary.addPrototype(ledger, function.prototype)
			count := int(function.prototype.upvalues)
			summary.bytes += uint64(count) *
				uint64(unsafe.Sizeof((*upvalue)(nil)))
			for index := 0; index < count; index++ {
				summary.addUpvalue(
					ledger,
					function.luaUpvalueUnchecked(index),
				)
			}
		}
	}
	for _, thread := range ledger.threads {
		summary.threads++
		summary.bytes += uint64(unsafe.Sizeof(*thread)) +
			ledgerEntryBytes
		summary.bytes += uint64(cap(thread.values)) *
			uint64(unsafe.Sizeof(slot{}))
		summary.bytes += uint64(cap(thread.frames)) *
			uint64(unsafe.Sizeof(activation{}))
		summary.bytes += uint64(cap(thread.continuations)) *
			uint64(unsafe.Sizeof(executionContinuation{}))
		extent := thread.liveValueExtent()
		if extent < 0 || extent > len(thread.values) {
			panic("lua: invalid live thread extent")
		}
		for _, value := range thread.values[:extent] {
			summary.addSlot(ledger, value)
		}
		for upvalue := thread.openUpvalues; upvalue != nil; upvalue = upvalue.next {
			summary.addUpvalue(ledger, upvalue)
		}
	}
	for _, data := range ledger.userData {
		summary.userData++
		summary.bytes += uint64(unsafe.Sizeof(*data)) +
			ledgerEntryBytes
	}
	summary.addError(ledger, state.execution.failure)
	summary.addError(ledger, state.execution.pendingExit)
	summary.addStringPool(ledger, &state.runtime.strings)
	return summary
}

func (ledger *objectLedger) resetSemanticHeapScratch() {
	clear(ledger.upvalues)
	clear(ledger.prototypes)
	clear(ledger.names)
	clear(ledger.longStrings)
	clear(ledger.stringBacking)
	clear(ledger.prototypeWork)
	ledger.prototypeWork = ledger.prototypeWork[:0]
}

func (ledger *objectLedger) releaseSemanticHeapScratch() {
	visited := len(ledger.upvalues) +
		len(ledger.prototypes) +
		len(ledger.names) +
		len(ledger.longStrings) +
		len(ledger.stringBacking)
	clear(ledger.prototypeWork)
	if cap(ledger.prototypeWork) > maximumRetainedCollectionWork {
		ledger.prototypeWork = nil
	} else {
		ledger.prototypeWork = ledger.prototypeWork[:0]
	}
	if visited > maximumRetainedCollectionWork {
		ledger.upvalues = nil
		ledger.prototypes = nil
		ledger.names = nil
		ledger.longStrings = nil
		ledger.stringBacking = nil
		return
	}
	clear(ledger.upvalues)
	clear(ledger.prototypes)
	clear(ledger.names)
	clear(ledger.longStrings)
	clear(ledger.stringBacking)
}

func (summary *semanticHeapSummary) addUpvalue(
	ledger *objectLedger,
	cell *upvalue,
) {
	if cell == nil {
		return
	}
	if ledger.upvalues == nil {
		ledger.upvalues = make(map[*upvalue]struct{})
	}
	if _, found := ledger.upvalues[cell]; found {
		return
	}
	ledger.upvalues[cell] = struct{}{}
	summary.upvalues++
	summary.bytes += uint64(unsafe.Sizeof(*cell))
	summary.addSlot(ledger, cell.read())
}

func (summary *semanticHeapSummary) addSlot(
	ledger *objectLedger,
	value slot,
) {
	if value.isString() {
		summary.addStringRef(ledger, stringRef{
			ref:  value.ref,
			bits: value.bits,
		})
	}
}

func (summary *semanticHeapSummary) addError(
	ledger *objectLedger,
	failure *Error,
) {
	if value, found := failure.valueSlot(); found {
		summary.addSlot(ledger, value)
	}
}

func (summary *semanticHeapSummary) addStringRef(
	ledger *objectLedger,
	value stringRef,
) {
	length := stringLength(value.ref, value.bits)
	encodedLength := int(
		value.bits >> stringLengthShift & stringLengthSentinel,
	)
	if encodedLength == stringLengthSentinel {
		long := (*longString)(value.ref)
		if ledger.longStrings == nil {
			ledger.longStrings = make(map[*longString]struct{})
		}
		if _, found := ledger.longStrings[long]; !found {
			ledger.longStrings[long] = struct{}{}
			summary.bytes += uint64(unsafe.Sizeof(*long))
		}
		summary.addTextBacking(ledger, long.text)
		return
	}

	// Empty and single-byte runtime strings use process-wide static backing.
	if length <= 1 {
		return
	}
	summary.addStringBacking(
		ledger,
		stringBacking{data: value.ref, length: length},
	)
}

func (summary *semanticHeapSummary) addTextBacking(
	ledger *objectLedger,
	text string,
) {
	if len(text) == 0 {
		return
	}
	backing := stringBacking{
		data:   unsafe.Pointer(unsafe.StringData(text)),
		length: len(text),
	}
	summary.addStringBacking(ledger, backing)
	runtime.KeepAlive(text)
}

func (summary *semanticHeapSummary) addStringBacking(
	ledger *objectLedger,
	backing stringBacking,
) {
	if backing.data == nil || backing.length <= 0 {
		return
	}
	if ledger.stringBacking == nil {
		ledger.stringBacking = make(map[stringBacking]struct{})
	}
	if _, found := ledger.stringBacking[backing]; found {
		return
	}
	ledger.stringBacking[backing] = struct{}{}
	summary.textBackings++
	summary.bytes += uint64(backing.length)
}

func (summary *semanticHeapSummary) addName(
	ledger *objectLedger,
	name *internedText,
) {
	if name == nil {
		return
	}
	if ledger.names == nil {
		ledger.names = make(map[*internedText]struct{})
	}
	if _, found := ledger.names[name]; found {
		return
	}
	ledger.names[name] = struct{}{}
	summary.bytes += uint64(unsafe.Sizeof(*name))
	summary.addTextBacking(ledger, name.text)
}

func (summary *semanticHeapSummary) addPrototype(
	ledger *objectLedger,
	root *Prototype,
) {
	if root == nil {
		return
	}
	if ledger.prototypes == nil {
		ledger.prototypes = make(map[*Prototype]struct{})
	}
	enqueue := func(prototype *Prototype) {
		if prototype == nil {
			return
		}
		if _, found := ledger.prototypes[prototype]; found {
			return
		}
		ledger.prototypes[prototype] = struct{}{}
		ledger.prototypeWork = append(ledger.prototypeWork, prototype)
	}
	enqueue(root)
	for len(ledger.prototypeWork) != 0 {
		last := len(ledger.prototypeWork) - 1
		prototype := ledger.prototypeWork[last]
		ledger.prototypeWork[last] = nil
		ledger.prototypeWork = ledger.prototypeWork[:last]

		summary.prototypes++
		summary.bytes += uint64(unsafe.Sizeof(*prototype))
		summary.bytes += uint64(cap(prototype.code)) *
			uint64(unsafe.Sizeof(instruction(0)))
		summary.bytes += uint64(cap(prototype.constants)) *
			uint64(unsafe.Sizeof(slot{}))
		summary.bytes += uint64(cap(prototype.children)) *
			uint64(unsafe.Sizeof((*Prototype)(nil)))
		summary.addName(ledger, prototype.sourceName)
		for _, constant := range prototype.constants {
			summary.addSlot(ledger, constant)
		}
		for _, child := range prototype.children {
			enqueue(child)
		}

		debug := prototype.debug
		if debug == nil {
			continue
		}
		summary.bytes += uint64(unsafe.Sizeof(*debug))
		summary.bytes += uint64(cap(debug.lines)) *
			uint64(unsafe.Sizeof(uint32(0)))
		summary.bytes += uint64(cap(debug.locals)) *
			uint64(unsafe.Sizeof(localInfo{}))
		summary.bytes += uint64(cap(debug.upvalues)) *
			uint64(unsafe.Sizeof((*internedText)(nil)))
		for _, local := range debug.locals {
			summary.addName(ledger, local.name)
		}
		for _, name := range debug.upvalues {
			summary.addName(ledger, name)
		}
	}
}

func (summary *semanticHeapSummary) addStringPool(
	ledger *objectLedger,
	pool *stringPool,
) {
	if pool == nil {
		return
	}
	addShard := func(shard *stringSetShard) {
		if shard == nil {
			return
		}
		summary.bytes += uint64(unsafe.Sizeof(*shard))
		for setIndex := range shard.sets {
			set := &shard.sets[setIndex]
			for _, value := range set.entries {
				if value.valid() {
					summary.addStringRef(ledger, value)
				}
			}
		}
	}
	for _, shard := range pool.probation {
		addShard(shard)
	}
	for _, shard := range pool.protected {
		addShard(shard)
	}
}
