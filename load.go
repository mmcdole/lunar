package lua

import "errors"

// ErrInvalidPrototype reports a nil or invalid Prototype.
var ErrInvalidPrototype = errors.New("lua: invalid prototype")

// Compile compiles source as a Lua 5.1 chunk into an immutable, State-neutral
// Prototype.
//
// The returned Prototype may be shared by multiple States. Compile does not
// retain source. sourceName is retained for diagnostics and debug information;
// names beginning with '@' or '=' follow Lua 5.1's file-name and literal-name
// conventions. Syntax failures are returned as *Error values with category
// SyntaxError.
func Compile(sourceName, source string) (*Prototype, error) {
	prototype, syntaxError := compileSource(sourceName, source)
	if syntaxError != nil {
		return nil, syntaxError
	}
	return prototype, nil
}

// LoadString compiles source and returns a new Lua Function in the executing
// Thread's global environment. Outside a callback, that is the main Thread's
// environment. LoadString does not execute the resulting chunk.
func (state *State) LoadString(
	sourceName string,
	source string,
) (*Function, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	prototype, err := Compile(sourceName, source)
	if err != nil {
		return nil, err
	}
	return state.loadPrototype(prototype), nil
}

// LoadPrototype returns a new Lua Function over prototype in the executing
// Thread's global environment. Outside a callback, that is the main Thread's
// environment.
//
// Prototype is immutable and State-neutral. Loading the same Prototype in
// multiple States creates distinct Functions while sharing executable
// metadata. Root upvalues are initialized to Lua nil, matching Lua 5.1's
// loader.
func (state *State) LoadPrototype(
	prototype *Prototype,
) (*Function, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	if prototype == nil || !prototype.sealed {
		return nil, ErrInvalidPrototype
	}
	return state.loadPrototype(prototype), nil
}

func (state *State) loadPrototype(prototype *Prototype) *Function {
	count := int(prototype.upvalues)
	upvalues := make([]*upvalue, count)
	if count != 0 {
		cells := make([]upvalue, count)
		for index := range cells {
			cells[index].storage = nilSlot
			cells[index].cell = &cells[index].storage
			upvalues[index] = &cells[index]
		}
	}
	return newLuaFunctionOwned(
		state.runtime,
		prototype,
		state.globalEnvironment(),
		upvalues,
	)
}
