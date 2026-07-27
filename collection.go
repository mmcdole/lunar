package lua

import (
	"runtime"
	"unsafe"
)

type objectMark uint8

const objectMarked objectMark = 1

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

	tableWork    []*tableObject
	functionWork []*functionObject
	threadWork   []*threadObject
	userDataWork []*userDataObject
	upvalues     map[*upvalue]struct{}

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
	bytes     uint64
	tables    int
	functions int
	threads   int
	userData  int
	upvalues  int
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

// collectUnreachable performs one synchronous semantic mark/sweep pass.
// It remains internal until weak tables and Lua finalization can be exposed
// atomically with collectgarbage.
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

	var result collectionResult
	ledger.phase = collectionSweeping
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
	for _, value := range table.array.values() {
		state.markSlot(value)
	}
	for _, entry := range table.store.entries.values() {
		if entry.hash == entryHashEmpty {
			continue
		}
		// Tombstone keys remain conservative edges until the weak-table
		// tranche introduces a non-owning dead-key representation.
		state.markSlot(entry.key)
		if !entry.value.isNil() {
			state.markSlot(entry.value)
		}
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
}

const maximumRetainedCollectionWork = 1024

func clearCollectionWork[T any](work *[]*T) {
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

// semanticHeap reports deterministic logical storage owned by registered Lua
// objects. Opaque userdata payloads, host tokens, strings, Prototypes, and Go
// allocator size-class rounding are intentionally outside this first
// accounting boundary.
func (state *State) semanticHeap() semanticHeapSummary {
	if state == nil {
		return semanticHeapSummary{}
	}
	ledger := &state.objects
	if ledger.upvalues != nil {
		clear(ledger.upvalues)
	}
	var summary semanticHeapSummary
	defer func() {
		if summary.upvalues > maximumRetainedCollectionWork {
			ledger.upvalues = nil
		} else if ledger.upvalues != nil {
			clear(ledger.upvalues)
		}
	}()

	ledgerEntryBytes := uint64(unsafe.Sizeof((*tableObject)(nil)))
	for _, table := range ledger.tables {
		summary.tables++
		summary.bytes += uint64(unsafe.Sizeof(*table)) +
			ledgerEntryBytes
		summary.bytes += uint64(table.array.cap()) *
			uint64(unsafe.Sizeof(slot{}))
		summary.bytes += uint64(table.store.entries.cap()) *
			uint64(unsafe.Sizeof(tableEntry{}))
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
		} else {
			summary.bytes += uint64(unsafe.Sizeof(*function)) +
				ledgerEntryBytes
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
		for upvalue := thread.openUpvalues; upvalue != nil; upvalue = upvalue.next {
			summary.addUpvalue(ledger, upvalue)
		}
	}
	for _, data := range ledger.userData {
		summary.userData++
		summary.bytes += uint64(unsafe.Sizeof(*data)) +
			ledgerEntryBytes
	}
	return summary
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
}
