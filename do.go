package lua

import "context"

// DoString loads and executes a Lua 5.1 source or native binary chunk.
//
// sourceName is retained for diagnostics and debug information. The returned
// slice and its Values are owned by the caller and remain valid across later
// calls and after State.Close. DoString is equivalent to LoadString followed
// by Call.
func (state *State) DoString(
	sourceName string,
	source string,
) ([]Value, error) {
	chunk, err := state.LoadString(sourceName, source)
	if err != nil {
		return nil, err
	}
	return state.Call(chunk.Value())
}

// DoStringContext loads and executes a chunk like DoString while observing ctx
// during both loading and execution.
//
// A nil context returns ErrNilContext. Cancellation is returned as an *Error
// categorized ContextError. Lua-visible side effects completed before
// cancellation remain.
func (state *State) DoStringContext(
	ctx context.Context,
	sourceName string,
	source string,
) ([]Value, error) {
	chunk, err := state.LoadStringContext(ctx, sourceName, source)
	if err != nil {
		return nil, err
	}
	return state.CallContext(ctx, chunk.Value())
}

// DoFile opens path through the State's SourcePolicy, then loads and executes
// a Lua 5.1 source or native binary chunk.
//
// The source name is "@" followed by path. A leading Unix interpreter line is
// ignored in the same way as Lua 5.1's loadfile. The returned slice and its
// Values are owned by the caller and remain valid across later calls and after
// State.Close. DoFile is equivalent to LoadFile followed by Call.
func (state *State) DoFile(path string) ([]Value, error) {
	chunk, err := state.LoadFile(path)
	if err != nil {
		return nil, err
	}
	return state.Call(chunk.Value())
}

// DoFileContext loads and executes path like DoFile while observing ctx during
// both loading and execution.
//
// A nil context returns ErrNilContext. Cancellation is returned as an *Error
// categorized ContextError. A CustomSource opener receives ctx. Cancellation
// cannot interrupt a backend already blocked inside Open or Read unless that
// backend observes ctx. Lua-visible side effects completed before cancellation
// remain.
func (state *State) DoFileContext(
	ctx context.Context,
	path string,
) ([]Value, error) {
	chunk, err := state.LoadFileContext(ctx, path)
	if err != nil {
		return nil, err
	}
	return state.CallContext(ctx, chunk.Value())
}
