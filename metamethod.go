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

func (event metamethod) name() string {
	if event >= metamethodCount {
		panic("lua: invalid metamethod")
	}
	return metamethodNames[event]
}

func metatableForSlot(thread *Thread, value slot) *Table {
	switch value.kind() {
	case TableKind:
		return (*Table)(value.ref).metatable
	case UserDataKind:
		return (*UserData)(value.ref).metatable
	default:
		kind := value.kind()
		if kind < NilKind || kind > TableKind {
			return nil
		}
		return thread.state.typeMetatables[kind]
	}
}

func metamethodSlot(
	thread *Thread,
	value slot,
	event metamethod,
) (slot, bool) {
	metatable := metatableForSlot(thread, value)
	if metatable == nil {
		return nilSlot, false
	}
	result, found := metatable.rawStringSlot(event.name())
	if !found || result.kind() == NilKind {
		return nilSlot, false
	}
	return result, true
}

func binaryMetamethod(
	thread *Thread,
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
	thread *Thread,
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

func luaCallMetamethod(thread *Thread, value slot) *Function {
	method, found := metamethodSlot(thread, value, metaCall)
	if !found {
		return nil
	}
	function, _ := luaFunctionSlot(method)
	return function
}
