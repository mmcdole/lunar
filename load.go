package lua

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ErrInvalidPrototype reports a nil or invalid Prototype.
var ErrInvalidPrototype = errors.New("lua: invalid prototype")

const loadContextPollBytes = 64 << 10

// loadControl bounds one load independently of the runtime value stack. Input
// and storage charged by a loader each receive the full configured allowance;
// neither can hide the other crossing its limit.
type loadControl struct {
	context      context.Context
	done         <-chan struct{}
	limit        uint64
	inputBytes   uint64
	storageBytes uint64
	nextPoll     uint64
}

func newLoadControl(
	ctx context.Context,
	limit int,
) (loadControl, *Error) {
	if limit < 0 {
		return loadControl{}, newLoadResourceError(
			"load byte limit is negative",
		)
	}
	control := loadControl{
		context:  ctx,
		limit:    uint64(limit),
		nextPoll: loadContextPollBytes,
	}
	if ctx != nil {
		control.done = ctx.Done()
		if failure := control.check(); failure != nil {
			return loadControl{}, failure
		}
	}
	return control, nil
}

func (control *loadControl) consume(count uint64) *Error {
	if control == nil {
		return nil
	}
	if count > ^uint64(0)-control.inputBytes {
		return newLoadResourceError("load input size overflows")
	}
	total := control.inputBytes + count
	if control.limit != 0 && total > control.limit {
		return newLoadResourceError(
			"load input exceeds the configured %d-byte limit",
			control.limit,
		)
	}
	control.inputBytes = total
	if total < control.nextPoll {
		return nil
	}
	control.nextPoll = total + loadContextPollBytes
	if control.nextPoll < total {
		control.nextPoll = ^uint64(0)
	}
	return control.check()
}

func (control *loadControl) reserve(count uint64) *Error {
	if control == nil {
		return nil
	}
	if count > ^uint64(0)-control.storageBytes {
		return newLoadResourceError("load storage size overflows")
	}
	total := control.storageBytes + count
	if control.limit != 0 && total > control.limit {
		return newLoadResourceError(
			"load storage exceeds the configured %d-byte limit",
			control.limit,
		)
	}
	control.storageBytes = total
	return control.check()
}

func (control *loadControl) release(count uint64) {
	if control == nil || count > control.storageBytes {
		panic("lua: invalid load-storage release")
	}
	control.storageBytes -= count
}

func (control *loadControl) check() *Error {
	if control == nil ||
		control.done == nil ||
		!contextChannelClosed(control.done) {
		return nil
	}
	return newContextError(control.context, false)
}

func newLoadResourceError(
	format string,
	arguments ...any,
) *Error {
	message := fmt.Sprintf(format, arguments...)
	return &Error{
		value:       errorStringValue(message),
		description: message,
		category:    ResourceError,
	}
}

type chunkRefill func() (string, error)

// chunkInput is the one sequential input used by source and binary loaders.
// Refill pieces are immutable strings; a span within one piece may therefore
// be borrowed until its caller either consumes or interns it.
type chunkInput struct {
	piece    string
	offset   int
	position uint64
	refill   chunkRefill
	control  *loadControl
	failure  error
	ended    bool
}

func newStringChunkInput(
	text string,
	control *loadControl,
) *chunkInput {
	return &chunkInput{
		piece:   text,
		control: control,
		ended:   true,
	}
}

func newRefillableChunkInput(
	refill chunkRefill,
	control *loadControl,
) *chunkInput {
	return &chunkInput{
		refill:  refill,
		control: control,
	}
}

func (input *chunkInput) peekByte() (byte, error) {
	if err := input.ensurePiece(); err != nil {
		return 0, err
	}
	return input.piece[input.offset], nil
}

func (input *chunkInput) readByte() (byte, error) {
	if err := input.ensurePiece(); err != nil {
		return 0, err
	}
	if failure := input.control.consume(1); failure != nil {
		return 0, failure
	}
	value := input.piece[input.offset]
	input.offset++
	input.position++
	return value, nil
}

func (input *chunkInput) readFull(destination []byte) error {
	for len(destination) != 0 {
		if err := input.ensurePiece(); err != nil {
			return err
		}
		available := len(input.piece) - input.offset
		count := len(destination)
		if count > available {
			count = available
		}
		if failure := input.control.consume(
			uint64(count),
		); failure != nil {
			return failure
		}
		copy(
			destination[:count],
			input.piece[input.offset:input.offset+count],
		)
		input.offset += count
		input.position += uint64(count)
		destination = destination[count:]
	}
	return nil
}

// readSpan returns count consecutive bytes. owned reports whether the input
// assembled a cross-piece span into storage the caller may adopt.
func (input *chunkInput) readSpan(
	count int,
) (text string, owned bool, err error) {
	if count < 0 {
		return "", false, newLoadResourceError(
			"negative load span",
		)
	}
	if count == 0 {
		return "", false, nil
	}
	if err := input.ensurePiece(); err != nil {
		return "", false, err
	}
	if count <= len(input.piece)-input.offset {
		if failure := input.control.consume(
			uint64(count),
		); failure != nil {
			return "", false, failure
		}
		start := input.offset
		input.offset += count
		input.position += uint64(count)
		return input.piece[start:input.offset], false, nil
	}

	if failure := input.control.reserve(
		uint64(count),
	); failure != nil {
		return "", false, failure
	}
	bytes := make([]byte, count)
	if err := input.readFull(bytes); err != nil {
		return "", false, err
	}
	return stringFromOwnedBytes(bytes), true, nil
}

func (input *chunkInput) ensurePiece() error {
	for input.offset == len(input.piece) {
		if input.failure != nil {
			return input.failure
		}
		if input.ended || input.refill == nil {
			return io.EOF
		}
		if failure := input.control.check(); failure != nil {
			input.failure = failure
			return failure
		}
		piece, err := input.refill()
		if err != nil {
			input.failure = err
			return err
		}
		if piece == "" {
			input.ended = true
			return io.EOF
		}
		input.piece = piece
		input.offset = 0
	}
	return nil
}

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

// LoadString loads a Lua 5.1 source or native binary chunk and returns a new
// Lua Function in the executing Thread's global environment. Outside a
// callback, that is the main Thread's environment. LoadString does not execute
// the resulting chunk.
func (state *State) LoadString(
	sourceName string,
	source string,
) (*Function, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	control, failure := newLoadControl(
		nil,
		state.options.MaxLoadBytes,
	)
	if failure != nil {
		return nil, failure
	}
	prototype, err := loadStringPrototype(
		sourceName,
		source,
		&control,
	)
	if err != nil {
		return nil, err
	}
	return state.loadPrototype(prototype), nil
}

func loadStringPrototype(
	sourceName string,
	source string,
	control *loadControl,
) (*Prototype, error) {
	if len(source) != 0 && source[0] == 0x1b {
		return decodeBinaryChunk(
			sourceName,
			newStringChunkInput(source, control),
			control,
		)
	}
	if failure := control.consume(uint64(len(source))); failure != nil {
		return nil, failure
	}
	prototype, syntaxError := compileSource(sourceName, source)
	if syntaxError != nil {
		return nil, syntaxError
	}
	return prototype, nil
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
