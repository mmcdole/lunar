package lua

type metamethod uint8

const (
	metaIndex metamethod = iota
	metaNewIndex
	metaMode
	metaCall
	metaMetatable
	metaToString
	metaLength
	metaUnaryMinus
	metaAdd
	metaSub
	metaMul
	metaDiv
	metaMod
	metaPow
	metaConcat
	metaEqual
	metaLessThan
	metaLessEqual
	metaGC
	metamethodCount
)

var metamethodNames = [...]string{
	metaIndex:      "__index",
	metaNewIndex:   "__newindex",
	metaMode:       "__mode",
	metaCall:       "__call",
	metaMetatable:  "__metatable",
	metaToString:   "__tostring",
	metaLength:     "__len",
	metaUnaryMinus: "__unm",
	metaAdd:        "__add",
	metaSub:        "__sub",
	metaMul:        "__mul",
	metaDiv:        "__div",
	metaMod:        "__mod",
	metaPow:        "__pow",
	metaConcat:     "__concat",
	metaEqual:      "__eq",
	metaLessThan:   "__lt",
	metaLessEqual:  "__le",
	metaGC:         "__gc",
}

var metamethodNameHashes = func() (hashes [metamethodCount]uint32) {
	for event, name := range metamethodNames {
		hashes[event] = uint32(hashString(name))
	}
	return hashes
}()

func (event metamethod) name() string {
	if event >= metamethodCount {
		panic("lua: invalid metamethod")
	}
	return metamethodNames[event]
}

func (event metamethod) bit() uint32 {
	if event >= metamethodCount {
		panic("lua: invalid metamethod")
	}
	return uint32(1) << event
}

func metatableForSlot(thread *threadObject, value slot) *tableObject {
	switch value.kind() {
	case TableKind:
		return (*tableObject)(value.ref).metatable
	case UserDataKind:
		return userDataObjectFromSlot(value).metatable
	default:
		kind := value.kind()
		if kind < NilKind || kind > TableKind {
			return nil
		}
		return thread.state.typeMetatables[kind]
	}
}

func metamethodSlot(
	thread *threadObject,
	value slot,
	event metamethod,
) (slot, bool) {
	metatable := metatableForSlot(thread, value)
	if metatable == nil {
		return nilSlot, false
	}
	bit := event.bit()
	if metatable.absentMetamethods&bit != 0 {
		return nilSlot, false
	}
	result, found := metatable.store.getString(
		event.name(),
		metamethodNameHashes[event],
	)
	if !found || result.isNil() {
		metatable.absentMetamethods |= bit
		return nilSlot, false
	}
	return result, true
}

func binaryMetamethod(
	thread *threadObject,
	left slot,
	right slot,
	event metamethod,
) (slot, bool) {
	if method, found := metamethodSlot(thread, left, event); found {
		return method, true
	}
	return metamethodSlot(thread, right, event)
}

func matchingMetamethod(
	thread *threadObject,
	left slot,
	right slot,
	event metamethod,
) (slot, bool) {
	leftMethod, found := metamethodSlot(thread, left, event)
	if !found {
		return nilSlot, false
	}
	rightMethod, found := metamethodSlot(thread, right, event)
	if !found || !rawSlotEqual(leftMethod, rightMethod) {
		return nilSlot, false
	}
	return leftMethod, true
}

func callMetamethodFunction(thread *threadObject, value slot) *functionObject {
	method, found := metamethodSlot(thread, value, metaCall)
	if !found {
		return nil
	}
	function, _ := functionSlot(method)
	return function
}
