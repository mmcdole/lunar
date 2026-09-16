package lua

import (
	"runtime"
	"unsafe"
)

type stringBacking struct {
	data   unsafe.Pointer
	length int
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

const collectedObjectLedgerBytes = uint64(
	unsafe.Sizeof((*objectHeader)(nil)),
)

func tableRetainedBytes(table *tableObject) uint64 {
	if table == nil {
		return 0
	}
	return uint64(unsafe.Sizeof(*table)) +
		collectedObjectLedgerBytes +
		tableStorageRetainedBytes(table)
}

func tableStorageRetainedBytes(table *tableObject) uint64 {
	if table == nil {
		return 0
	}
	return uint64(table.array.cap())*uint64(unsafe.Sizeof(slot{})) +
		uint64(table.store.entries.cap())*
			uint64(unsafe.Sizeof(tableEntry{}))
}

func functionRetainedBytes(function *functionObject) uint64 {
	if function == nil {
		return 0
	}
	if function.prototype == nil {
		body := function.nativeBodyUnchecked()
		return uint64(unsafe.Sizeof(nativeFunctionAllocation{})) +
			collectedObjectLedgerBytes +
			uint64(cap(body.captures))*
				uint64(unsafe.Sizeof(slot{}))
	}
	return uint64(unsafe.Sizeof(*function)) +
		collectedObjectLedgerBytes +
		uint64(function.prototype.upvalues)*
			uint64(unsafe.Sizeof((*upvalue)(nil)))
}

func threadRetainedBytes(thread *threadObject) uint64 {
	if thread == nil {
		return 0
	}
	return uint64(unsafe.Sizeof(*thread)) +
		collectedObjectLedgerBytes +
		uint64(cap(thread.values))*uint64(unsafe.Sizeof(slot{})) +
		uint64(cap(thread.frames))*uint64(unsafe.Sizeof(activation{})) +
		uint64(cap(thread.continuations))*
			uint64(unsafe.Sizeof(executionContinuation{}))
}

func userDataRetainedBytes(data *userDataObject) uint64 {
	if data == nil {
		return 0
	}
	return uint64(unsafe.Sizeof(*data)) + collectedObjectLedgerBytes
}

func upvalueCellsRetainedBytes(count int) uint64 {
	if count <= 0 {
		return 0
	}
	return uint64(count) * uint64(unsafe.Sizeof(upvalue{}))
}

func stringRefRetainedBytes(value stringRef) uint64 {
	if !value.valid() {
		return 0
	}
	length := stringLength(value.ref, value.bits)
	if length <= 1 {
		return 0
	}
	bytes := uint64(length)
	if int(value.bits>>stringLengthShift&stringLengthSentinel) ==
		stringLengthSentinel {
		bytes += uint64(unsafe.Sizeof(longString{}))
	}
	return bytes
}

// prototypeTreeRetainedBytes is cached when immutable executable metadata is
// sealed. It deliberately uses a conservative, allocation-free sum: repeated
// name or string backing inside one tree may be counted more than once. Debt
// only schedules a collection; semanticHeap remains the exact deduplicated
// reporting source.
func prototypeTreeRetainedBytes(prototype *Prototype) uint64 {
	if prototype == nil {
		return 0
	}
	var bytes uint64
	add := func(value uint64) {
		limit := uint64(prototypeRetainedMask)
		if bytes >= limit || value >= limit-bytes {
			bytes = limit
			return
		}
		bytes += value
	}
	addVector := func(count int, size uintptr) {
		if count <= 0 {
			return
		}
		value := uint64(count) * uint64(size)
		if uint64(count) != 0 &&
			value/uint64(count) != uint64(size) {
			value = uint64(prototypeRetainedMask)
		}
		add(value)
	}
	addName := func(name *internedText) {
		if name == nil {
			return
		}
		add(uint64(unsafe.Sizeof(*name)))
		add(uint64(len(name.text)))
	}

	add(uint64(unsafe.Sizeof(*prototype)))
	addVector(cap(prototype.code), unsafe.Sizeof(instruction(0)))
	addVector(cap(prototype.constants), unsafe.Sizeof(slot{}))
	addVector(cap(prototype.children), unsafe.Sizeof((*Prototype)(nil)))
	addName(prototype.sourceName)
	for _, constant := range prototype.constants {
		if constant.isString() {
			add(stringRefRetainedBytes(stringRef{
				ref:  constant.ref,
				bits: constant.bits,
			}))
		}
	}
	for _, child := range prototype.children {
		add(child.schedulingBytes())
	}

	debug := prototype.debug
	if debug == nil {
		return bytes
	}
	add(uint64(unsafe.Sizeof(*debug)))
	addVector(cap(debug.lines), unsafe.Sizeof(uint32(0)))
	addVector(cap(debug.locals), unsafe.Sizeof(localInfo{}))
	addVector(cap(debug.upvalues), unsafe.Sizeof((*internedText)(nil)))
	for _, local := range debug.locals {
		addName(local.name)
	}
	for _, name := range debug.upvalues {
		addName(name)
	}
	return bytes
}

// semanticHeap reports the target-architecture logical storage retained by
// this State's Lua graph. Strings and immutable Prototypes are State-neutral
// representations, but each is attributed once when this State retains it.
// Opaque userdata payloads, host tokens, and Go allocator size-class rounding
// remain outside the accounting boundary.
func (state *State) semanticHeap() semanticHeapSummary {
	return state.measureSemanticHeap(false)
}

func (state *State) semanticHeapForCollection() semanticHeapSummary {
	return state.measureSemanticHeap(true)
}

func (state *State) measureSemanticHeap(
	reconcileStrings bool,
) semanticHeapSummary {
	if state == nil {
		return semanticHeapSummary{}
	}
	ledger := &state.objects
	var summary semanticHeapSummary
	defer ledger.releaseSemanticHeapScratch()

	for _, table := range ledger.tables {
		summary.tables++
		summary.bytes += tableRetainedBytes(table)
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
		summary.bytes += functionRetainedBytes(function)
		if function.prototype == nil {
			body := function.nativeBodyUnchecked()
			for _, capture := range body.captures {
				summary.addSlot(ledger, capture)
			}
		} else {
			summary.addPrototype(ledger, function.prototype)
			count := int(function.prototype.upvalues)
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
		summary.bytes += threadRetainedBytes(thread)
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
		summary.bytes += userDataRetainedBytes(data)
	}
	summary.addError(ledger, state.execution.failure)
	summary.addError(ledger, state.execution.pendingExit)
	summary.addStringPool(ledger, &state.runtime.strings)
	if reconcileStrings {
		state.runtime.collection.sweepAttributedStrings(ledger)
	}
	return summary
}

// releaseSemanticHeapScratch leaves scratch empty for the next scan, including
// when a scan panics. Heap measurement invokes no Lua or host callbacks, so
// these State-local workspaces cannot be reentered while populated.
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

func (ledger *objectLedger) retainsString(value stringRef) bool {
	if ledger == nil || !value.valid() {
		return false
	}
	encodedLength := int(
		value.bits >> stringLengthShift & stringLengthSentinel,
	)
	if encodedLength == stringLengthSentinel {
		_, found := ledger.longStrings[(*longString)(value.ref)]
		return found
	}
	if encodedLength <= 1 {
		return true
	}
	_, found := ledger.stringBacking[stringBacking{
		data:   value.ref,
		length: encodedLength,
	}]
	return found
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

// chargePrototypeTree records the conservative logical weight cached when
// immutable executable metadata was sealed. Repeated loading may deliberately
// charge shared metadata again: debt is a scheduling signal, and keeping an
// exact per-State attribution map would add allocation and lookup cost to
// every load. semanticHeap remains the exact deduplicated reporting source.
func (state *State) chargePrototypeTree(root *Prototype) {
	if state == nil || root == nil {
		return
	}
	state.runtime.collection.charge(root.schedulingBytes())
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
