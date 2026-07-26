package lua

import (
	"bufio"
	"errors"
	"io"
)

const (
	defaultIOReadLimit = maximumConstructedStringBytes
	smallIOReadBytes   = 512
)

var errIOReadTooLarge = errors.New("lua: resulting string too large")

// ioReadEngine implements the input formats shared by io.read and file:read.
// It consumes one inputEndpoint, so every user of a file observes one logical
// cursor even when the endpoint has read ahead.
type ioReadEngine struct {
	input   *inputEndpoint
	strings *stringPool
	limit   int
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

// readLine reads through the next line feed and returns the bytes before it.
// A carriage return is ordinary data. The final unterminated line is a value;
// only an EOF encountered before any byte reports no value.
func (engine *ioReadEngine) readLine() (slot, bool, error) {
	var collected []byte
	for {
		piece, err := engine.input.ReadSlice('\n')
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
	var small [smallIOReadBytes]byte
	buffer := small[:0]
	owned := false

	for uint64(len(buffer)) < target {
		if len(buffer) == cap(buffer) {
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
			owned = true
		}

		end := cap(buffer)
		if uint64(end) > target {
			end = int(target)
		}
		start := len(buffer)
		buffer = buffer[:end]
		read, err := engine.input.Read(buffer[start:])
		buffer = buffer[:start+read]
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
	if owned {
		return engine.ownedStringSlot(buffer), true, nil
	}
	return engine.copiedStringSlot(buffer), true, nil
}

// readAll consumes input through EOF. Unlike the other formats, an empty input
// is a successful empty string.
func (engine *ioReadEngine) readAll() (slot, bool, error) {
	limit := engine.readLimit()
	var small [smallIOReadBytes]byte
	buffer := small[:0]
	owned := false

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
			owned = true
		}

		target := cap(buffer)
		if target > limit {
			target = limit
		}
		start := len(buffer)
		buffer = buffer[:target]
		read, err := engine.input.Read(buffer[start:])
		buffer = buffer[:start+read]

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

	if owned {
		return engine.ownedStringSlot(buffer), true, nil
	}
	return engine.copiedStringSlot(buffer), true, nil
}

// readNumber scans one deterministic Lua number. Leading Lua whitespace is
// consumed. A failed conversion leaves the first non-space byte untouched.
// Decimal numbers and the runtime's 0x-prefixed integer extension share the
// grammar used by parseLuaNumber.
func (engine *ioReadEngine) readNumber() (slot, bool, error) {
	var storage [64]byte
	token := storage[:0]

	peek := func(offset int) (byte, bool, error) {
		text, err := engine.input.Peek(offset + 1)
		if len(text) <= offset {
			return 0, false, err
		}
		return text[offset], true, err
	}
	take := func() error {
		if len(token) == engine.readLimit() {
			return errIOReadTooLarge
		}
		value, err := engine.input.ReadByte()
		if err != nil {
			return err
		}
		token = append(token, value)
		return nil
	}
	discard := func() error {
		_, err := engine.input.ReadByte()
		return err
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
	return stringSlot(
		engine.strings.make(stringFromOwnedBytes(text)),
	)
}

// finish gives one logical read operation ownership of any non-EOF failure
// recorded while bufio prefetched its data. Taking the failure here prevents
// the same Reader error from poisoning a later operation.
func (engine *ioReadEngine) finish(err error) error {
	if engine != nil && engine.input != nil {
		pending := engine.input.source.pending
		if pending != nil {
			// bufio retains the same error behind any prefetched bytes.
			// Preserve those bytes but rebuild the reader without its
			// deferred error, so this operation reports the failure once.
			buffered := engine.input.buffered
			if buffered != nil {
				unread, _ := buffered.Peek(buffered.Buffered())
				prefix := append([]byte(nil), unread...)
				buffered.Reset(&inputReplayReader{
					prefix: prefix,
					source: &engine.input.source,
				})
			}
			engine.input.source.pending = nil
			return pending.cause
		}
	}
	return err
}

// inputReplayReader restores bytes that bufio had prefetched alongside a
// non-EOF error. It is installed only on that rare path.
type inputReplayReader struct {
	prefix []byte
	source *readFailureRecorder
}

func (reader *inputReplayReader) Read(destination []byte) (int, error) {
	if len(reader.prefix) != 0 {
		count := copy(destination, reader.prefix)
		reader.prefix = reader.prefix[count:]
		return count, nil
	}
	return reader.source.Read(destination)
}
