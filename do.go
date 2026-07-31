package lua

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
