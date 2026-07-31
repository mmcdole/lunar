package lua

// MarshalBinary serializes the Prototype as a Lua 5.1 binary chunk.
//
// Together with Compile and LoadPrototype this completes a build-time
// pipeline: compile once without a State, keep the bytes, and load them
// into as many States as needed without paying to parse again.
//
// Lua 5.1 binary chunks describe the host ABI in their header rather than
// defining a portable encoding, so the result is loadable only by a
// runtime built for the same byte order, pointer width, and number format.
// Treat the bytes as a cache keyed by architecture, not as a distribution
// format, and keep the source available to recompile from.
//
// MarshalBinary implements encoding.BinaryMarshaler. It reports
// ErrInvalidPrototype for a Prototype that is not sealed.
func (prototype *Prototype) MarshalBinary() ([]byte, error) {
	if prototype == nil {
		return nil, ErrInvalidPrototype
	}
	dumped, err := dumpPrototype(prototype)
	if err != nil {
		return nil, err
	}
	return []byte(dumped), nil
}

// UnmarshalPrototype decodes a Lua 5.1 binary chunk produced by
// MarshalBinary or by string.dump, returning a Prototype that LoadPrototype
// can install into any State.
//
// sourceName is retained for diagnostics; names beginning with '@' or '='
// follow Lua 5.1's file-name and literal-name conventions. Input that is
// not a binary chunk, or one built for another ABI, is rejected with a
// *Error categorized SyntaxError.
//
// Binary chunks encode structure that a decoder must trust, so decoding
// applies the default load limit to projected retained storage the way
// Options.MaxLoadBytes does. Load untrusted chunks through a State whose
// MaxLoadBytes reflects the policy the host wants.
//
// There is no UnmarshalBinary counterpart because a Prototype is immutable
// once sealed; decoding produces a new one.
func UnmarshalPrototype(sourceName string, data []byte) (*Prototype, error) {
	control, failure := newLoadControl(nil, defaultMaxLoadBytes)
	if failure != nil {
		return nil, failure
	}
	input := newStringChunkInput(string(data), &control)
	return decodeBinaryChunk(sourceName, input, &control)
}
