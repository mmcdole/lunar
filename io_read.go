package lua

import (
	"bufio"
	"errors"
	"io"
)

const (
	defaultIOReadLimit     = maximumConstructedStringBytes
	maximumIONumberBytes   = 64 << 10
	ioReadContextPollBytes = 64 << 10
	maximumIOReadSlack     = 4 << 10
)

var (
	errIOReadTooLarge  = errors.New("lua: resulting string too large")
	errIONumberTooLong = errors.New("lua: numeric input is too long")
)

// ioReadEngine implements the input formats shared by io.read and file:read.
// It consumes one inputEndpoint, so every user of a file observes one logical
// cursor even when the endpoint has read ahead.
type ioReadEngine struct {
	input   *inputEndpoint
	strings *stringPool
	limit   int

	contextThread *threadObject
	contextBytes  uint32
}

func newIOReadEngine(
	input *inputEndpoint,
	strings *stringPool,
) ioReadEngine {
	return ioReadEngine{
		input:   input,
		strings: strings,
		limit:   defaultIOReadLimit,
	}
}

// bindContext makes a potentially long native read cooperative with the
// active public call. Raw calls leave contextThread nil, so their byte loops
// pay only one predictable branch.
func (engine *ioReadEngine) bindContext(thread *threadObject) bool {
	if engine.input == nil ||
		thread == nil ||
		thread.state.ambientDone == nil {
		return false
	}
	engine.contextThread = thread
	engine.contextBytes = ioReadContextPollBytes
	engine.input.source.contextThread = thread
	return true
}

func (engine *ioReadEngine) unbindContext() {
	if engine.contextThread == nil {
		return
	}
	if engine.input != nil &&
		engine.input.source.contextThread == engine.contextThread {
		engine.input.source.contextThread = nil
	}
	engine.contextThread = nil
	engine.contextBytes = 0
}

func (engine *ioReadEngine) consumeContextBytes(count int) *Error {
	if engine.contextThread == nil || count <= 0 {
		return nil
	}
	consumed := uint64(count)
	budget := uint64(engine.contextBytes)
	if consumed < budget {
		engine.contextBytes -= uint32(consumed)
		return nil
	}
	if failure := pollExecutionContext(engine.contextThread); failure != nil {
		return failure
	}
	remainder := consumed % ioReadContextPollBytes
	if remainder == 0 {
		engine.contextBytes = ioReadContextPollBytes
	} else {
		engine.contextBytes = ioReadContextPollBytes - uint32(remainder)
	}
	return nil
}

func (engine *ioReadEngine) readRequestSize(available int) int {
	if engine.contextThread == nil ||
		available <= int(engine.contextBytes) {
		return available
	}
	return int(engine.contextBytes)
}

func (engine *ioReadEngine) checkEmptyRead() *Error {
	if engine.contextThread == nil {
		return nil
	}
	return pollExecutionContext(engine.contextThread)
}

// readLine reads through the next line feed and returns the bytes before it.
// A carriage return is ordinary data. The final unterminated line is a value;
// only an EOF encountered before any byte reports no value.
func (engine *ioReadEngine) readLine() (slot, bool, error) {
	var collected []byte
	for {
		piece, err := engine.input.ReadSlice('\n')
		consumed := len(piece)
		if failure := engine.consumeContextBytes(consumed); failure != nil {
			return nilSlot, false, failure
		}
		terminated := len(piece) != 0 &&
			piece[len(piece)-1] == '\n'
		if terminated {
			piece = piece[:len(piece)-1]
		}
		if len(collected)+len(piece) > engine.readLimit() {
			return nilSlot, false,
				engine.finish(errIOReadTooLarge)
		}

		if len(collected) != 0 {
			collected = append(collected, piece...)
		}

		switch {
		case terminated:
			if failure := engine.finish(err); failure != nil {
				return nilSlot, false, failure
			}
			if collected != nil {
				return engine.ownedStringSlot(collected), true, nil
			}
			return engine.copiedStringSlot(piece), true, nil
		case err == bufio.ErrBufferFull:
			if collected == nil {
				capacity := len(piece) * 2
				if capacity > engine.readLimit() {
					capacity = engine.readLimit()
				}
				collected = make([]byte, 0, capacity)
				collected = append(collected, piece...)
			}
			continue
		case err == io.EOF:
			if failure := engine.finish(err); failure != nil &&
				failure != io.EOF {
				return nilSlot, false, failure
			}
			if collected == nil && len(piece) == 0 {
				return nilSlot, false, nil
			}
			if collected != nil {
				return engine.ownedStringSlot(collected), true, nil
			}
			return engine.copiedStringSlot(piece), true, nil
		case err != nil:
			return nilSlot, false, engine.finish(err)
		default:
			panic("lua: input line read made no progress")
		}
	}
}

// readBytes returns at most count bytes. EOF before any byte reports no value;
// a short read at EOF returns the bytes that were available. A zero-byte read
// probes EOF without advancing the logical cursor.
func (engine *ioReadEngine) readBytes(
	count uint64,
) (slot, bool, error) {
	if count == 0 {
		next, err := engine.input.Peek(1)
		if failure := engine.finish(err); failure != nil &&
			failure != io.EOF {
			return nilSlot, false, failure
		}
		if len(next) == 0 {
			return nilSlot, false, nil
		}
		return engine.emptyStringSlot(), true, nil
	}

	limit := engine.readLimit()
	target := uint64(limit)
	if count < target {
		target = count
	}
	if value, present, err, handled :=
		engine.readBorrowedBytes(target); handled {
		if err == nil && present && count > uint64(limit) {
			next, peekErr := engine.input.Peek(1)
			if failure := engine.finish(peekErr); failure != nil &&
				failure != io.EOF {
				return nilSlot, false, failure
			}
			if len(next) != 0 {
				return nilSlot, false, errIOReadTooLarge
			}
		}
		return value, present, err
	}

	reader, err := engine.input.reader()
	if err != nil {
		return nilSlot, false, engine.finish(err)
	}
	initial := reader.Size()
	if uint64(initial) > target {
		initial = int(target)
	}
	initial = engine.readRequestSize(initial)
	if initial < 1 {
		initial = 1
	}
	buffer := make([]byte, 0, initial)
	emptyReads := 0

	for uint64(len(buffer)) < target {
		if len(buffer) == cap(buffer) {
			if engine.contextThread == nil {
				next, err := engine.input.Peek(1)
				if failure := engine.finish(err); failure != nil &&
					failure != io.EOF {
					return nilSlot, false, failure
				}
				if len(next) == 0 {
					break
				}
			}
			nextCapacity := cap(buffer) * 2
			if nextCapacity == 0 {
				nextCapacity = 1
			}
			if uint64(nextCapacity) > target {
				nextCapacity = int(target)
			}
			grown := make([]byte, len(buffer), nextCapacity)
			copy(grown, buffer)
			buffer = grown
		}

		end := cap(buffer)
		if uint64(end) > target {
			end = int(target)
		}
		start := len(buffer)
		request := engine.readRequestSize(end - start)
		end = start + request
		buffer = buffer[:end]
		read, err := engine.input.Read(buffer[start:])
		buffer = buffer[:start+read]
		if read == 0 && err == nil {
			emptyReads++
			if failure := engine.checkEmptyRead(); failure != nil {
				return nilSlot, false, failure
			}
			if emptyReads == maxConsecutiveEmptyRead {
				return nilSlot, false, io.ErrNoProgress
			}
			continue
		}
		emptyReads = 0
		if failure := engine.consumeContextBytes(read); failure != nil {
			return nilSlot, false, failure
		}
		if err == io.EOF {
			if failure := engine.finish(err); failure != nil &&
				failure != io.EOF {
				return nilSlot, false, failure
			}
			break
		}
		if err != nil {
			return nilSlot, false, engine.finish(err)
		}
		if failure := engine.finish(nil); failure != nil {
			return nilSlot, false, failure
		}
	}

	if count > uint64(limit) && len(buffer) == limit {
		next, err := engine.input.Peek(1)
		if failure := engine.finish(err); failure != nil &&
			failure != io.EOF {
			return nilSlot, false, failure
		}
		if len(next) != 0 {
			return nilSlot, false, errIOReadTooLarge
		}
	}
	if len(buffer) == 0 {
		return nilSlot, false, nil
	}
	return engine.ownedStringSlot(buffer), true, nil
}

// readBorrowedBytes serves fixed reads already small enough for bufio's
// reusable storage. The result is interned or copied before another reader
// operation can reuse that storage, eliminating a per-call temporary buffer.
func (engine *ioReadEngine) readBorrowedBytes(
	target uint64,
) (slot, bool, error, bool) {
	if engine.contextThread != nil {
		return nilSlot, false, nil, false
	}
	reader, err := engine.input.reader()
	if err != nil {
		return nilSlot, false, engine.finish(err), true
	}
	request := reader.Size()
	if target < uint64(request) {
		request = int(target)
	}
	request = engine.readRequestSize(request)
	if request < 1 {
		return nilSlot, false, nil, false
	}

	text, peekErr := reader.Peek(request)
	complete := uint64(len(text)) == target
	if !complete && peekErr == nil {
		return nilSlot, false, nil, false
	}

	var value slot
	if peekErr == nil || peekErr == io.EOF {
		value = engine.copiedStringSlot(text)
	}
	discarded, discardErr := reader.Discard(len(text))
	if discarded != len(text) {
		if discardErr == nil {
			discardErr = io.ErrNoProgress
		}
		return nilSlot, false, engine.finish(discardErr), true
	}
	if failure := engine.consumeContextBytes(discarded); failure != nil {
		return nilSlot, false, failure, true
	}
	if failure := engine.finish(peekErr); failure != nil &&
		failure != io.EOF {
		return nilSlot, false, failure, true
	}
	if len(text) == 0 {
		return nilSlot, false, nil, true
	}
	return value, true, nil, true
}

// readAll consumes input through EOF. Unlike the other formats, an empty input
// is a successful empty string.
func (engine *ioReadEngine) readAll() (slot, bool, error) {
	limit := engine.readLimit()
	if value, err, handled := engine.readBorrowedAll(); handled {
		return value, true, err
	}

	reader, err := engine.input.reader()
	if err != nil {
		return nilSlot, false, engine.finish(err)
	}
	initial := engine.readAllInitialCapacity(reader.Size(), limit)
	buffer := make([]byte, 0, initial)
	emptyReads := 0

	for {
		if len(buffer) == limit {
			next, err := engine.input.Peek(1)
			if failure := engine.finish(err); failure != nil &&
				failure != io.EOF {
				return nilSlot, false, failure
			}
			if len(next) != 0 {
				return nilSlot, false, errIOReadTooLarge
			}
			break
		}

		if len(buffer) == cap(buffer) {
			if engine.contextThread == nil {
				next, err := engine.input.Peek(1)
				if failure := engine.finish(err); failure != nil &&
					failure != io.EOF {
					return nilSlot, false, failure
				}
				if len(next) == 0 {
					break
				}
			}
			nextCapacity := cap(buffer) * 2
			if nextCapacity == 0 {
				nextCapacity = 1
			}
			if nextCapacity > limit {
				nextCapacity = limit
			}
			grown := make([]byte, len(buffer), nextCapacity)
			copy(grown, buffer)
			buffer = grown
		}

		target := cap(buffer)
		if target > limit {
			target = limit
		}
		start := len(buffer)
		request := engine.readRequestSize(target - start)
		target = start + request
		buffer = buffer[:target]
		read, err := engine.input.Read(buffer[start:])
		buffer = buffer[:start+read]
		if read == 0 && err == nil {
			emptyReads++
			if failure := engine.checkEmptyRead(); failure != nil {
				return nilSlot, false, failure
			}
			if emptyReads == maxConsecutiveEmptyRead {
				return nilSlot, false, io.ErrNoProgress
			}
			continue
		}
		emptyReads = 0
		if failure := engine.consumeContextBytes(read); failure != nil {
			return nilSlot, false, failure
		}

		if err == io.EOF {
			if failure := engine.finish(err); failure != nil &&
				failure != io.EOF {
				return nilSlot, false, failure
			}
			break
		}
		if err != nil {
			return nilSlot, false, engine.finish(err)
		}
		if failure := engine.finish(nil); failure != nil {
			return nilSlot, false, failure
		}
	}

	return engine.ownedStringSlot(buffer), true, nil
}

func (engine *ioReadEngine) readAllInitialCapacity(
	fallback int,
	limit int,
) int {
	initial := fallback
	// Context-aware input retains bounded admission: reserving a complete
	// large file before the first poll would make cancellation responsive in
	// bytes read but not in memory committed.
	if engine.contextThread == nil {
		if remaining, known :=
			engine.input.remainingRegularBytes(); known &&
			remaining <= int64(limit) {
			initial = int(remaining)
		}
	}
	if initial > limit {
		initial = limit
	}
	initial = engine.readRequestSize(initial)
	if initial < 1 {
		initial = 1
	}
	return initial
}

// readBorrowedAll handles inputs that reach EOF within the reusable reader
// buffer. Larger inputs fall through to an owned growable buffer.
func (engine *ioReadEngine) readBorrowedAll() (
	slot,
	error,
	bool,
) {
	if engine.contextThread != nil {
		return nilSlot, nil, false
	}
	reader, err := engine.input.reader()
	if err != nil {
		return nilSlot, engine.finish(err), true
	}
	request := reader.Size()
	if limit := engine.readLimit(); request > limit {
		request = limit
	}
	if request < 1 {
		request = 1
	}
	request = engine.readRequestSize(request)
	if request < 1 {
		return nilSlot, nil, false
	}
	text, peekErr := reader.Peek(request)
	if peekErr == nil {
		return nilSlot, nil, false
	}

	var value slot
	if peekErr == io.EOF {
		value = engine.copiedStringSlot(text)
	}
	discarded, discardErr := reader.Discard(len(text))
	if discarded != len(text) {
		if discardErr == nil {
			discardErr = io.ErrNoProgress
		}
		return nilSlot, engine.finish(discardErr), true
	}
	if failure := engine.consumeContextBytes(discarded); failure != nil {
		return nilSlot, failure, true
	}
	if failure := engine.finish(peekErr); failure != nil &&
		failure != io.EOF {
		return nilSlot, failure, true
	}
	return value, nil, true
}

// readNumber scans one deterministic Lua number. Leading Lua whitespace is
// consumed. A failed conversion leaves the first non-space byte untouched.
// Decimal numbers and the runtime's 0x-prefixed integer extension share the
// grammar used by parseLuaNumber.
func (engine *ioReadEngine) readNumber() (slot, bool, error) {
	var storage [64]byte
	token := storage[:0]
	tokenLimit := maximumIONumberBytes
	tokenLimitError := errIONumberTooLong
	if limit := engine.readLimit(); limit < tokenLimit {
		tokenLimit = limit
		tokenLimitError = errIOReadTooLarge
	}

	peek := func(offset int) (byte, bool, error) {
		text, err := engine.input.Peek(offset + 1)
		if len(text) <= offset {
			return 0, false, err
		}
		return text[offset], true, err
	}
	take := func() error {
		if len(token) == tokenLimit {
			return tokenLimitError
		}
		value, err := engine.input.ReadByte()
		if err != nil {
			return err
		}
		token = append(token, value)
		if failure := engine.consumeContextBytes(1); failure != nil {
			return failure
		}
		return nil
	}
	discard := func() error {
		_, err := engine.input.ReadByte()
		if err != nil {
			return err
		}
		if failure := engine.consumeContextBytes(1); failure != nil {
			return failure
		}
		return nil
	}

	for {
		value, present, err := peek(0)
		if err != nil && err != io.EOF {
			return nilSlot, false, engine.finish(err)
		}
		if !present || !isLuaNumberSpace(value) {
			break
		}
		if err := discard(); err != nil {
			return nilSlot, false, engine.finish(err)
		}
	}

	offset := 0
	first, present, err := peek(0)
	if err != nil && err != io.EOF {
		return nilSlot, false, engine.finish(err)
	}
	if !present {
		if failure := engine.finish(err); failure != nil &&
			failure != io.EOF {
			return nilSlot, false, failure
		}
		return nilSlot, false, nil
	}
	if first == '+' || first == '-' {
		offset = 1
	}
	start, startPresent, startErr := peek(offset)
	if startErr != nil && startErr != io.EOF {
		return nilSlot, false, engine.finish(startErr)
	}
	validStart := startPresent && isDigit(start)
	if startPresent && start == '.' {
		following, followingPresent, followingErr := peek(offset + 1)
		if followingErr != nil && followingErr != io.EOF {
			return nilSlot, false, engine.finish(followingErr)
		}
		validStart = followingPresent && isDigit(following)
	}
	if !validStart {
		if failure := engine.finish(startErr); failure != nil &&
			failure != io.EOF {
			return nilSlot, false, failure
		}
		return nilSlot, false, nil
	}

	if offset != 0 {
		if err := take(); err != nil {
			return nilSlot, false, engine.finish(err)
		}
	}

	hexadecimal := false
	if start == '0' {
		marker, markerPresent, markerErr := peek(1)
		if markerErr != nil && markerErr != io.EOF {
			return nilSlot, false, engine.finish(markerErr)
		}
		digit, digitPresent, digitErr := peek(2)
		if digitErr != nil && digitErr != io.EOF {
			return nilSlot, false, engine.finish(digitErr)
		}
		hexadecimal = markerPresent &&
			(marker == 'x' || marker == 'X') &&
			digitPresent &&
			isHexDigit(digit)
	}

	if hexadecimal {
		for consumed := 0; consumed < 2; consumed++ {
			if err := take(); err != nil {
				return nilSlot, false, engine.finish(err)
			}
		}
		for {
			value, present, peekErr := peek(0)
			if peekErr != nil && peekErr != io.EOF {
				return nilSlot, false, engine.finish(peekErr)
			}
			if !present || !isHexDigit(value) {
				break
			}
			if err := take(); err != nil {
				return nilSlot, false, engine.finish(err)
			}
		}
	} else {
		for {
			value, present, peekErr := peek(0)
			if peekErr != nil && peekErr != io.EOF {
				return nilSlot, false, engine.finish(peekErr)
			}
			if !present || !isDigit(value) {
				break
			}
			if err := take(); err != nil {
				return nilSlot, false, engine.finish(err)
			}
		}

		value, present, peekErr := peek(0)
		if peekErr != nil && peekErr != io.EOF {
			return nilSlot, false, engine.finish(peekErr)
		}
		if present && value == '.' {
			if err := take(); err != nil {
				return nilSlot, false, engine.finish(err)
			}
			for {
				value, present, peekErr = peek(0)
				if peekErr != nil && peekErr != io.EOF {
					return nilSlot, false,
						engine.finish(peekErr)
				}
				if !present || !isDigit(value) {
					break
				}
				if err := take(); err != nil {
					return nilSlot, false,
						engine.finish(err)
				}
			}
		}

		value, present, peekErr = peek(0)
		if peekErr != nil && peekErr != io.EOF {
			return nilSlot, false, engine.finish(peekErr)
		}
		if present && (value == 'e' || value == 'E') {
			exponentOffset := 1
			sign, signPresent, signErr := peek(exponentOffset)
			if signErr != nil && signErr != io.EOF {
				return nilSlot, false, engine.finish(signErr)
			}
			if signPresent && (sign == '+' || sign == '-') {
				exponentOffset++
			}
			digit, digitPresent, digitErr := peek(exponentOffset)
			if digitErr != nil && digitErr != io.EOF {
				return nilSlot, false, engine.finish(digitErr)
			}
			if digitPresent && isDigit(digit) {
				for consumed := 0; consumed < exponentOffset; consumed++ {
					if err := take(); err != nil {
						return nilSlot, false,
							engine.finish(err)
					}
				}
				for {
					value, present, peekErr = peek(0)
					if peekErr != nil && peekErr != io.EOF {
						return nilSlot, false,
							engine.finish(peekErr)
					}
					if !present || !isDigit(value) {
						break
					}
					if err := take(); err != nil {
						return nilSlot, false,
							engine.finish(err)
					}
				}
			}
		}
	}

	if failure := engine.finish(nil); failure != nil {
		return nilSlot, false, failure
	}
	number, valid := parseLuaNumber(string(token))
	if !valid {
		panic("lua: IO number scanner produced an invalid token")
	}
	return numberSlot(number), true, nil
}

func (engine *ioReadEngine) readLimit() int {
	if engine.limit < 0 {
		return 0
	}
	return engine.limit
}

func (engine *ioReadEngine) emptyStringSlot() slot {
	return stringSlot(engine.strings.make(""))
}

func (engine *ioReadEngine) copiedStringSlot(text []byte) slot {
	return stringSlot(engine.strings.makeBytes(text))
}

func (engine *ioReadEngine) ownedStringSlot(text []byte) slot {
	text = compactIOReadBuffer(text)
	return stringSlot(
		engine.strings.make(stringFromOwnedBytes(text)),
	)
}

// compactIOReadBuffer bounds capacity retained behind a long immutable
// string. Small absolute slack avoids a final copy; larger buffers retain at
// most one eighth of their logical length.
func compactIOReadBuffer(text []byte) []byte {
	slack := cap(text) - len(text)
	if slack <= maximumIOReadSlack || slack <= len(text)/8 {
		return text
	}
	compacted := make([]byte, len(text))
	copy(compacted, text)
	return compacted
}

// finish gives one logical read operation ownership of any non-EOF failure
// recorded while bufio prefetched its data. Taking the failure here prevents
// the same Reader error from poisoning a later operation.
func (engine *ioReadEngine) finish(err error) error {
	if err != nil {
		if luaFailure, ok := err.(*Error); ok &&
			luaFailure.Category() == ContextError {
			return luaFailure
		}
	}
	if engine != nil && engine.input != nil {
		if failure := engine.input.takeFailure(); failure != nil {
			return failure
		}
	}
	return err
}
