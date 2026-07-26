package lua

import "io"

type lexerTextMark struct {
	generation uint64
	offset     int
}

// lexerTextCapture borrows text while a token remains inside one immutable
// refill piece. Crossing a real piece boundary promotes the token into the
// lexer's reusable scratch buffer. Polling-window boundaries within one piece
// do not promote it.
type lexerTextCapture struct {
	piece      string
	generation uint64
	start      int
	owned      bool
}

func (lex *lexer) beginTextCapture() lexerTextCapture {
	lex.scratch = lex.scratch[:0]
	return lexerTextCapture{
		piece:      lex.inputPiece(),
		generation: lex.inputGeneration(),
		start:      lex.inputOffset(),
	}
}

func (capture *lexerTextCapture) sync(lex *lexer) {
	generation := lex.inputGeneration()
	if capture.generation == generation {
		return
	}
	if capture.start != len(capture.piece) {
		lex.scratch = append(
			lex.scratch,
			capture.piece[capture.start:]...,
		)
		capture.owned = true
	}
	capture.piece = lex.inputPiece()
	capture.generation = generation
	capture.start = lex.inputOffset()
}

func (capture *lexerTextCapture) mark(lex *lexer) lexerTextMark {
	capture.sync(lex)
	return lexerTextMark{
		generation: capture.generation,
		offset:     lex.inputOffset(),
	}
}

func (capture *lexerTextCapture) flushTo(
	lex *lexer,
	mark lexerTextMark,
) {
	if mark.generation != capture.generation ||
		mark.offset < capture.start ||
		mark.offset > len(capture.piece) {
		panic("lua: invalid lexer text mark")
	}
	lex.scratch = append(
		lex.scratch,
		capture.piece[capture.start:mark.offset]...,
	)
	capture.start = mark.offset
	capture.owned = true
}

func (capture *lexerTextCapture) reset(lex *lexer) {
	capture.piece = lex.inputPiece()
	capture.generation = lex.inputGeneration()
	capture.start = lex.inputOffset()
}

func (capture *lexerTextCapture) appendByte(lex *lexer, value byte) {
	lex.scratch = append(lex.scratch, value)
	capture.owned = true
}

func (capture *lexerTextCapture) appendRepeated(
	lex *lexer,
	value byte,
	count int,
) {
	for ; count > 0; count-- {
		lex.scratch = append(lex.scratch, value)
	}
	capture.owned = true
}

func (capture *lexerTextCapture) finishAt(
	lex *lexer,
	mark lexerTextMark,
) (string, bool) {
	if mark.generation != capture.generation ||
		mark.offset < capture.start ||
		mark.offset > len(capture.piece) {
		panic("lua: invalid lexer text finish")
	}
	if !capture.owned {
		return capture.piece[capture.start:mark.offset], false
	}
	lex.scratch = append(
		lex.scratch,
		capture.piece[capture.start:mark.offset]...,
	)
	return string(lex.scratch), true
}

func (capture *lexerTextCapture) finish(lex *lexer) (string, bool) {
	capture.sync(lex)
	return capture.finishAt(lex, lexerTextMark{
		generation: capture.generation,
		offset:     lex.inputOffset(),
	})
}

func (lex *lexer) readQuoted(delimiter byte) (string, bool, error) {
	capture := lex.beginTextCapture()
	for {
		window := lex.inputWindow()
		if len(window) == 0 {
			err := lex.inputFailure()
			if err == io.EOF {
				return "", false, lex.errorf(
					lex.line,
					"unfinished string",
				)
			}
			return "", false, err
		}
		capture.sync(lex)
		count := 0
		for count < len(window) {
			current := window[count]
			if current == delimiter ||
				current == '\n' ||
				current == '\r' ||
				current == '\\' {
				break
			}
			count++
		}
		lex.advance(count)
		if count == len(window) {
			continue
		}

		current := window[count]
		switch current {
		case delimiter:
			mark := capture.mark(lex)
			text, owned := capture.finishAt(lex, mark)
			lex.advance(1)
			return text, owned, nil
		case '\n', '\r':
			return "", false, lex.errorf(lex.line, "unfinished string")
		case '\\':
			mark := capture.mark(lex)
			capture.flushTo(lex, mark)
			lex.advance(1)
			escapeCode := lex.currentCode()
			if escapeCode < 0 {
				if err := lex.inputError(); err != nil {
					return "", false, err
				}
				return "", false, lex.errorf(lex.line, "unfinished string")
			}

			escape := byte(escapeCode)
			var decoded byte
			switch escape {
			case 'a':
				decoded = '\a'
				lex.advance(1)
			case 'b':
				decoded = '\b'
				lex.advance(1)
			case 'f':
				decoded = '\f'
				lex.advance(1)
			case 'n':
				decoded = '\n'
				lex.advance(1)
			case 'r':
				decoded = '\r'
				lex.advance(1)
			case 't':
				decoded = '\t'
				lex.advance(1)
			case 'v':
				decoded = '\v'
				lex.advance(1)
			case '\n', '\r':
				decoded = '\n'
				if _, err := lex.consumeNewline(); err != nil {
					return "", false, err
				}
			default:
				if !isDigit(escape) {
					decoded = escape
					lex.advance(1)
					break
				}
				value := 0
				for digits := 0; digits < 3; digits++ {
					digitCode := lex.currentCode()
					if digitCode < 0 {
						if err := lex.inputError(); err != nil {
							return "", false, err
						}
						break
					}
					digit := byte(digitCode)
					if !isDigit(digit) {
						break
					}
					value = value*10 + int(digit-'0')
					lex.advance(1)
				}
				if value > 255 {
					return "", false, lex.errorf(
						lex.line,
						"escape sequence too large",
					)
				}
				decoded = byte(value)
			}
			capture.reset(lex)
			capture.appendByte(lex, decoded)
		}
	}
}

func (lex *lexer) readLong(level int, keepText bool) (string, bool, error) {
	code := lex.currentCode()
	if code < 0 && lex.inputError() != nil {
		return "", false, lex.inputError()
	}
	if code == '\n' || code == '\r' {
		if _, err := lex.consumeNewline(); err != nil {
			return "", false, err
		}
	}

	var capture lexerTextCapture
	if keepText {
		capture = lex.beginTextCapture()
	}

	for {
		window := lex.inputWindow()
		if len(window) == 0 {
			err := lex.inputFailure()
			if err != io.EOF {
				return "", false, err
			}
			if keepText {
				return "", false, lex.errorf(
					lex.line,
					"unfinished long string",
				)
			}
			return "", false, lex.errorf(
				lex.line,
				"unfinished long comment",
			)
		}
		if keepText {
			capture.sync(lex)
		}

		count := 0
		for count < len(window) {
			value := window[count]
			if value == ']' ||
				value == '\n' ||
				value == '\r' ||
				level == 0 && value == '[' {
				break
			}
			count++
		}
		lex.advance(count)
		if count == len(window) {
			continue
		}

		switch window[count] {
		case '[':
			lex.advance(1)
			nextCode := lex.currentCode()
			if nextCode < 0 && lex.inputError() != nil {
				return "", false, lex.inputError()
			}
			if nextCode == '[' {
				return "", false, lex.errorf(
					lex.line,
					"nesting of [[...]] is deprecated",
				)
			}
		case ']':
			var mark lexerTextMark
			if keepText {
				mark = capture.mark(lex)
			}
			startGeneration := lex.inputGeneration()
			closed, equals, err := lex.tryLongClose(level)
			if err != nil {
				return "", false, err
			}
			if closed {
				if !keepText {
					return "", false, nil
				}
				text, owned := capture.finishAt(lex, mark)
				return text, owned, nil
			}
			if keepText &&
				lex.inputGeneration() != startGeneration {
				capture.flushTo(lex, mark)
				capture.appendByte(lex, ']')
				capture.appendRepeated(lex, '=', equals)
				capture.reset(lex)
			}
		case '\n', '\r':
			var mark lexerTextMark
			if keepText {
				mark = capture.mark(lex)
			}
			normalize, err := lex.consumeNewline()
			if err != nil {
				return "", false, err
			}
			if keepText && normalize {
				capture.flushTo(lex, mark)
				capture.reset(lex)
				capture.appendByte(lex, '\n')
			}
		}
	}
}

func (lex *lexer) longDelimiterTail(
	delimiter byte,
) (level int, matched bool, sawEquals bool, err error) {
	level, err = lex.consumeEquals()
	if err != nil {
		return 0, false, false, err
	}
	code := lex.currentCode()
	if code < 0 && lex.inputError() != nil {
		return 0, false, false, lex.inputError()
	}
	if code != int(delimiter) {
		return level, false, level != 0, nil
	}
	lex.advance(1)
	return level, true, level != 0, nil
}

func (lex *lexer) consumeEquals() (int, error) {
	total := 0
	for {
		window := lex.inputWindow()
		if len(window) == 0 {
			err := lex.inputFailure()
			if err == io.EOF {
				return total, nil
			}
			return 0, err
		}
		count := 0
		for count < len(window) && window[count] == '=' {
			count++
		}
		lex.advance(count)
		total += count
		if count != len(window) {
			return total, nil
		}
	}
}

func (lex *lexer) tryLongClose(
	level int,
) (closed bool, equals int, err error) {
	code := lex.currentCode()
	if code < 0 && lex.inputError() != nil {
		return false, 0, lex.inputError()
	}
	if code != ']' {
		panic("lua: long-close scan requires closing bracket")
	}
	lex.advance(1)
	equals, err = lex.consumeEquals()
	if err != nil {
		return false, 0, err
	}
	code = lex.currentCode()
	if code < 0 && lex.inputError() != nil {
		return false, 0, lex.inputError()
	}
	if code != ']' || equals != level {
		return false, equals, nil
	}
	lex.advance(1)
	return true, equals, nil
}

// PUC Lua 5.1 rejects a line number once it reaches MAX_INT, where MAX_INT is
// INT_MAX-2. Keep that signed bound even though the compact debug field is
// unsigned.
const maxSourceLine uint32 = 1<<31 - 3

func (lex *lexer) consumeNewline() (normalize bool, err error) {
	firstCode := lex.currentCode()
	if firstCode < 0 && lex.inputError() != nil {
		return false, lex.inputError()
	}
	if firstCode != '\n' && firstCode != '\r' {
		panic("lua: newline scan requires a newline")
	}
	first := byte(firstCode)
	lex.advance(1)
	mixedPair := false
	secondCode := lex.currentCode()
	if secondCode < 0 && lex.inputError() != nil {
		return false, lex.inputError()
	}
	if secondCode >= 0 &&
		(secondCode == '\n' || secondCode == '\r') &&
		byte(secondCode) != first {
		lex.advance(1)
		mixedPair = true
	}
	lex.line++
	if lex.line >= maxSourceLine {
		return false, lex.errorf(lex.line, "chunk has too many lines")
	}
	return first == '\r' || mixedPair, nil
}
