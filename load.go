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

// chunkRefillFailure distinguishes a callback failure from errors produced by
// the loader itself. Public loading boundaries unwrap it and return the
// callback's original error.
type chunkRefillFailure struct {
	cause error
}

func (failure *chunkRefillFailure) Error() string {
	return failure.cause.Error()
}

func (failure *chunkRefillFailure) Unwrap() error {
	return failure.cause
}

func originalChunkRefillError(err error) error {
	if failure, ok := err.(*chunkRefillFailure); ok {
		return failure.cause
	}
	return err
}

// chunkInput is the one sequential input used by source and binary loaders.
// Refill pieces are immutable strings; a span within one piece may therefore
// be borrowed until its caller either consumes or interns it.
type chunkInput struct {
	piece           string
	offset          int
	windowEnd       int
	position        uint64
	pieceGeneration uint64
	refill          chunkRefill
	control         *loadControl
	failure         error
	pendingFailure  error
	ended           bool
}

func newStringChunkInput(
	text string,
	control *loadControl,
) *chunkInput {
	windowEnd := len(text)
	if control != nil && windowEnd > loadContextPollBytes {
		windowEnd = loadContextPollBytes
	}
	return &chunkInput{
		piece:           text,
		windowEnd:       windowEnd,
		pieceGeneration: 1,
		control:         control,
		ended:           true,
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
	window := input.window()
	if len(window) == 0 {
		return 0, input.windowError()
	}
	return window[0], nil
}

func (input *chunkInput) readByte() (byte, error) {
	window := input.window()
	if len(window) == 0 {
		return 0, input.windowError()
	}
	value := window[0]
	if err := input.advance(1); err != nil {
		return 0, err
	}
	return value, nil
}

func (input *chunkInput) readFull(destination []byte) error {
	for len(destination) != 0 {
		window := input.window()
		if len(window) == 0 {
			return input.windowError()
		}
		count := len(destination)
		if count > len(window) {
			count = len(window)
		}
		segment := window[:count]
		if err := input.advance(count); err != nil {
			return err
		}
		copy(destination[:count], segment)
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
	if len(input.window()) == 0 {
		return "", false, input.windowError()
	}
	if count <= len(input.piece)-input.offset {
		piece := input.piece
		start := input.offset
		remaining := count
		for remaining != 0 {
			window := input.window()
			if len(window) == 0 {
				return "", false, input.windowError()
			}
			step := remaining
			if step > len(window) {
				step = len(window)
			}
			if advanceErr := input.advance(step); advanceErr != nil {
				return "", false, advanceErr
			}
			remaining -= step
		}
		return piece[start : start+count], false, nil
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

// window returns the current contiguous input span. Controlled loads expose at
// most one polling interval at a time so scanners cannot defer cancellation
// while walking a very large refill piece.
func (input *chunkInput) window() string {
	if input.offset != input.windowEnd {
		return input.piece[input.offset:input.windowEnd]
	}
	return input.windowSlow()
}

func (input *chunkInput) windowSlow() string {
	if input.failure != nil {
		return ""
	}
	if input.offset < len(input.piece) {
		input.setWindowEnd()
		return input.piece[input.offset:input.windowEnd]
	}
	if err := input.ensurePiece(); err != nil {
		return ""
	}
	input.setWindowEnd()
	return input.piece[input.offset:input.windowEnd]
}

func (input *chunkInput) windowError() error {
	if input.failure != nil {
		return input.failure
	}
	return io.EOF
}

func (input *chunkInput) setWindowEnd() {
	input.windowEnd = len(input.piece)
	if input.control != nil &&
		input.windowEnd-input.offset > loadContextPollBytes {
		input.windowEnd = input.offset + loadContextPollBytes
	}
}

// advance consumes bytes from the current window. All input accounting and
// absolute-position updates pass through this method.
func (input *chunkInput) advance(count int) error {
	if input.control == nil {
		input.offset += count
		input.position += uint64(count)
		return nil
	}
	return input.advanceSlow(count)
}

func (input *chunkInput) advanceSlow(count int) error {
	available := input.windowEnd - input.offset
	if count < 0 || count > available {
		panic("lua: chunk input advanced beyond its current window")
	}
	if input.failure != nil {
		return input.failure
	}
	if input.control != nil {
		if failure := input.control.consume(uint64(count)); failure != nil {
			input.failure = failure
			return failure
		}
	}
	input.offset += count
	input.position += uint64(count)
	return nil
}

func (input *chunkInput) ensurePiece() error {
	for input.offset == len(input.piece) {
		if input.failure != nil {
			return input.failure
		}
		if input.pendingFailure != nil {
			failure := input.pendingFailure
			input.pendingFailure = nil
			if failure == io.EOF {
				input.ended = true
				return io.EOF
			}
			input.failure = failure
			return failure
		}
		if input.ended || input.refill == nil {
			return io.EOF
		}
		if failure := input.control.check(); failure != nil {
			input.failure = failure
			return failure
		}
		piece, err := input.refill()
		if piece == "" {
			if err != nil && err != io.EOF {
				input.failure = &chunkRefillFailure{cause: err}
				return input.failure
			}
			input.ended = true
			return io.EOF
		}
		input.piece = piece
		input.offset = 0
		input.windowEnd = 0
		input.pieceGeneration++
		if err == io.EOF {
			input.pendingFailure = io.EOF
		} else if err != nil {
			input.pendingFailure = &chunkRefillFailure{cause: err}
		}
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
	return state.loadString(nil, sourceName, source)
}

// LoadStringContext loads source like LoadString while observing ctx during
// compilation or binary decoding.
//
// A nil context returns ErrNilContext. Cancellation is returned as a *Error
// categorized ContextError. The resulting Function does not retain ctx.
func (state *State) LoadStringContext(
	ctx context.Context,
	sourceName string,
	source string,
) (*Function, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	return state.loadString(ctx, sourceName, source)
}

func (state *State) loadString(
	ctx context.Context,
	sourceName string,
	source string,
) (*Function, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	control, failure := newLoadControl(
		ctx,
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
	return state.loadPrototypeObject(prototype).owningHandle(), nil
}

func loadStringPrototype(
	sourceName string,
	source string,
	control *loadControl,
) (*Prototype, error) {
	if len(source) == 0 || source[0] != 0x1b {
		// An ordinary fixed string has already been materialized by its
		// caller. Charge it once, then use the lexer's direct window instead
		// of routing every byte through the refillable cursor. A cancellable
		// context retains bounded polling through chunkInput.
		if control.done == nil {
			if failure := control.consume(uint64(len(source))); failure != nil {
				return nil, failure
			}
			prototype, syntaxError := compileSource(sourceName, source)
			if syntaxError != nil {
				return nil, syntaxError
			}
			return prototype, nil
		}
	}
	return loadInputPrototype(
		sourceName,
		newStringChunkInput(source, control),
		control,
	)
}

func loadInputPrototype(
	sourceName string,
	input *chunkInput,
	control *loadControl,
) (*Prototype, error) {
	if input == nil || control == nil {
		panic("lua: loader requires input and load control")
	}
	if input.control != control {
		panic("lua: loader and input use different load controls")
	}
	first, err := input.peekByte()
	if err != nil && err != io.EOF {
		return nil, originalChunkRefillError(err)
	}
	if err == nil && first == 0x1b {
		return decodeBinaryChunk(
			sourceName,
			input,
			control,
		)
	}
	return compileInput(sourceName, input)
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
	return state.loadPrototypeObject(prototype).owningHandle(), nil
}

func (state *State) loadPrototypeObject(
	prototype *Prototype,
) *functionObject {
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
		state,
		prototype,
		state.globalEnvironment(),
		upvalues,
	)
}
