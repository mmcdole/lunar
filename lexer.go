package lua

import (
	"fmt"
	"io"
	"strconv"
)

type tokenKind uint16

const (
	tokenAnd tokenKind = 256 + iota
	tokenBreak
	tokenDo
	tokenElse
	tokenElseIf
	tokenEnd
	tokenFalse
	tokenFor
	tokenFunction
	tokenIf
	tokenIn
	tokenLocal
	tokenNil
	tokenNot
	tokenOr
	tokenRepeat
	tokenReturn
	tokenThen
	tokenTrue
	tokenUntil
	tokenWhile
	tokenConcat
	tokenDoubleColon
	tokenDots
	tokenEqual
	tokenGreaterEqual
	tokenLessEqual
	tokenNotEqual
	tokenNumber
	tokenName
	tokenString
	tokenEOF
)

var tokenNames = [...]string{
	tokenAnd - 256:          "and",
	tokenBreak - 256:        "break",
	tokenDo - 256:           "do",
	tokenElse - 256:         "else",
	tokenElseIf - 256:       "elseif",
	tokenEnd - 256:          "end",
	tokenFalse - 256:        "false",
	tokenFor - 256:          "for",
	tokenFunction - 256:     "function",
	tokenIf - 256:           "if",
	tokenIn - 256:           "in",
	tokenLocal - 256:        "local",
	tokenNil - 256:          "nil",
	tokenNot - 256:          "not",
	tokenOr - 256:           "or",
	tokenRepeat - 256:       "repeat",
	tokenReturn - 256:       "return",
	tokenThen - 256:         "then",
	tokenTrue - 256:         "true",
	tokenUntil - 256:        "until",
	tokenWhile - 256:        "while",
	tokenConcat - 256:       "..",
	tokenDoubleColon - 256:  "::",
	tokenDots - 256:         "...",
	tokenEqual - 256:        "==",
	tokenGreaterEqual - 256: ">=",
	tokenLessEqual - 256:    "<=",
	tokenNotEqual - 256:     "~=",
	tokenNumber - 256:       "<number>",
	tokenName - 256:         "<name>",
	tokenString - 256:       "<string>",
	tokenEOF - 256:          "<eof>",
}

func (kind tokenKind) String() string {
	if kind < 256 {
		if kind >= 0x20 && kind <= 0x7e {
			return fmt.Sprintf("%q", byte(kind))
		}
		return fmt.Sprintf("byte(%d)", kind)
	}
	index := int(kind - 256)
	if index < 0 || index >= len(tokenNames) {
		return "<invalid token>"
	}
	return tokenNames[index]
}

type token struct {
	// text may borrow the source buffer. ownedText records decoded text whose
	// backing storage may be adopted by the compilation string table.
	text      string
	number    float64
	line      uint32
	kind      tokenKind
	ownedText bool
}

type lexer struct {
	sourceName string
	window     string
	input      *chunkInput
	scratch    []byte
	offset     int
	line       uint32
	lookahead  token
	hasLook    bool
}

func newLexer(sourceName, source string) *lexer {
	return &lexer{
		sourceName: sourceName,
		window:     source,
		line:       1,
	}
}

func newInputLexer(sourceName string, input *chunkInput) *lexer {
	if input == nil {
		panic("lua: lexer requires chunk input")
	}
	return &lexer{
		sourceName: sourceName,
		input:      input,
		line:       1,
	}
}

func (lex *lexer) inputPiece() string {
	if lex.input == nil {
		return lex.window
	}
	return lex.input.piece
}

func (lex *lexer) inputGeneration() uint64 {
	if lex.input == nil {
		return 1
	}
	return lex.input.pieceGeneration
}

func (lex *lexer) inputOffset() int {
	if lex.input == nil {
		return lex.offset
	}
	return lex.input.offset + lex.offset
}

func (lex *lexer) inputWindow() string {
	if lex.offset != len(lex.window) {
		return lex.window[lex.offset:]
	}
	return lex.refillWindow()
}

func (lex *lexer) refillWindow() string {
	if lex.input == nil {
		return ""
	}
	if len(lex.window) != 0 {
		if err := lex.input.advance(len(lex.window)); err != nil {
			return ""
		}
		lex.window = ""
		lex.offset = 0
	}
	window := lex.input.window()
	if len(window) == 0 {
		return ""
	}
	lex.window = window
	lex.offset = 0
	return window
}

func (lex *lexer) inputFailure() error {
	if lex.input == nil || lex.input.failure == nil {
		return io.EOF
	}
	return lex.input.failure
}

func (lex *lexer) inputError() error {
	err := lex.inputFailure()
	if err == io.EOF {
		return nil
	}
	return err
}

func (lex *lexer) commitInput() error {
	if lex.input == nil || lex.offset == 0 {
		return nil
	}
	if err := lex.input.advance(lex.offset); err != nil {
		return err
	}
	lex.window = lex.window[lex.offset:]
	lex.offset = 0
	return nil
}

func (lex *lexer) next() (token, error) {
	if lex.hasLook {
		value := lex.lookahead
		lex.lookahead = token{}
		lex.hasLook = false
		return value, nil
	}
	return lex.scan()
}

func (lex *lexer) peek() (token, error) {
	if !lex.hasLook {
		value, err := lex.scan()
		if err != nil {
			return token{}, err
		}
		lex.lookahead = value
		lex.hasLook = true
	}
	return lex.lookahead, nil
}

// currentCode follows Lua's reader convention: a byte is returned as a
// non-negative integer, while -1 means either EOF or an input failure. The
// failure, when present, is retained by chunkInput.
func (lex *lexer) currentCode() int {
	window := lex.inputWindow()
	if len(window) == 0 {
		return -1
	}
	return int(window[0])
}

func (lex *lexer) capturedCode(
	capture *lexerTextCapture,
) int {
	code := lex.currentCode()
	if code >= 0 {
		capture.sync(lex)
	}
	return code
}

func (lex *lexer) advance(count int) {
	lex.offset += count
}

type lexerScanClass uint8

const (
	horizontalSpaceClass lexerScanClass = iota
	lineBodyClass
	numberBodyClass
	nameBodyClass
)

func (lex *lexer) advanceWhile(
	capture *lexerTextCapture,
	class lexerScanClass,
) error {
	for {
		window := lex.inputWindow()
		if len(window) == 0 {
			err := lex.inputFailure()
			if err == io.EOF {
				return nil
			}
			return err
		}
		if capture != nil {
			capture.sync(lex)
		}
		count := 0
		switch class {
		case horizontalSpaceClass:
			for count < len(window) {
				value := window[count]
				if value != ' ' &&
					value != '\t' &&
					value != '\v' &&
					value != '\f' {
					break
				}
				count++
			}
		case lineBodyClass:
			for count < len(window) &&
				window[count] != '\n' &&
				window[count] != '\r' {
				count++
			}
		case numberBodyClass:
			for count < len(window) &&
				(isDigit(window[count]) || window[count] == '.') {
				count++
			}
		case nameBodyClass:
			for count < len(window) &&
				isNameContinue(window[count]) {
				count++
			}
		default:
			panic("lua: invalid lexer scan class")
		}
		lex.advance(count)
		if count != len(window) {
			return nil
		}
	}
}

func (lex *lexer) skipLine() error {
	return lex.advanceWhile(nil, lineBodyClass)
}

func (lex *lexer) scan() (token, error) {
	var tokenWindow string
	for {
		if err := lex.advanceWhile(
			nil,
			horizontalSpaceClass,
		); err != nil {
			return token{}, err
		}
		window := lex.inputWindow()
		if len(window) == 0 {
			if err := lex.inputFailure(); err != io.EOF {
				return token{}, err
			}
			return lex.token(tokenEOF, lex.line), nil
		}
		current := window[0]
		if current == '\n' || current == '\r' {
			if _, err := lex.consumeNewline(); err != nil {
				return token{}, err
			}
			continue
		}
		if current != '-' {
			tokenWindow = window
			break
		}

		line := lex.line
		lex.advance(1)
		code := lex.currentCode()
		if code < 0 {
			if err := lex.inputError(); err != nil {
				return token{}, err
			}
			return lex.token('-', line), nil
		}
		current = byte(code)
		if current != '-' {
			return lex.token('-', line), nil
		}
		lex.advance(1)
		code = lex.currentCode()
		if code < 0 {
			if err := lex.inputError(); err != nil {
				return token{}, err
			}
		} else {
			current = byte(code)
		}
		if code >= 0 && current == '[' {
			lex.advance(1)
			level, matched, _, delimiterErr := lex.longDelimiterTail('[')
			if delimiterErr != nil {
				return token{}, delimiterErr
			}
			if matched {
				if _, _, err := lex.readLong(level, false); err != nil {
					return token{}, err
				}
				continue
			}
		}
		if err := lex.skipLine(); err != nil {
			return token{}, err
		}
	}

	line := lex.line
	current := tokenWindow[0]

	switch current {
	case '[':
		lex.advance(1)
		level, matched, sawEquals, delimiterErr := lex.longDelimiterTail('[')
		if delimiterErr != nil {
			return token{}, delimiterErr
		}
		if matched {
			text, owned, err := lex.readLong(level, true)
			if err != nil {
				return token{}, err
			}
			value := lex.token(tokenString, line)
			value.text = text
			value.ownedText = owned
			return value, nil
		}
		if sawEquals {
			return token{}, lex.errorf(line, "invalid long string delimiter")
		}
		return lex.token('[', line), nil
	case '\'', '"':
		lex.advance(1)
		text, owned, err := lex.readQuoted(current)
		if err != nil {
			return token{}, err
		}
		value := lex.token(tokenString, line)
		value.text = text
		value.ownedText = owned
		return value, nil
	case '.':
		if len(tokenWindow) > 1 &&
			tokenWindow[1] != '.' &&
			isDigit(tokenWindow[1]) {
			if value, complete, numberErr := lex.readNumberWindow(
				line,
				tokenWindow,
			); complete || numberErr != nil {
				return value, numberErr
			}
		}
		capture := lex.beginTextCapture()
		lex.advance(1)
		nextCode := lex.currentCode()
		if nextCode < 0 && lex.inputError() != nil {
			return token{}, lex.inputError()
		}
		if nextCode >= 0 && byte(nextCode) == '.' {
			lex.advance(1)
			thirdCode := lex.currentCode()
			if thirdCode < 0 && lex.inputError() != nil {
				return token{}, lex.inputError()
			}
			if thirdCode >= 0 && byte(thirdCode) == '.' {
				lex.advance(1)
				return lex.token(tokenDots, line), nil
			}
			return lex.token(tokenConcat, line), nil
		}
		if nextCode >= 0 && isDigit(byte(nextCode)) {
			return lex.readNumber(line, &capture)
		}
		return lex.token('.', line), nil
	case ':':
		lex.advance(1)
		if matched, err := lex.acceptByte(':'); err != nil {
			return token{}, err
		} else if matched {
			return lex.token(tokenDoubleColon, line), nil
		}
		return lex.token(':', line), nil
	case '=':
		lex.advance(1)
		if matched, err := lex.acceptByte('='); err != nil {
			return token{}, err
		} else if matched {
			return lex.token(tokenEqual, line), nil
		}
		return lex.token('=', line), nil
	case '>':
		lex.advance(1)
		if matched, err := lex.acceptByte('='); err != nil {
			return token{}, err
		} else if matched {
			return lex.token(tokenGreaterEqual, line), nil
		}
		return lex.token('>', line), nil
	case '<':
		lex.advance(1)
		if matched, err := lex.acceptByte('='); err != nil {
			return token{}, err
		} else if matched {
			return lex.token(tokenLessEqual, line), nil
		}
		return lex.token('<', line), nil
	case '~':
		lex.advance(1)
		if matched, err := lex.acceptByte('='); err != nil {
			return token{}, err
		} else if matched {
			return lex.token(tokenNotEqual, line), nil
		}
		return lex.token('~', line), nil
	default:
		if isDigit(current) {
			if value, complete, numberErr := lex.readNumberWindow(
				line,
				tokenWindow,
			); complete || numberErr != nil {
				return value, numberErr
			}
			capture := lex.beginTextCapture()
			return lex.readNumber(line, &capture)
		}
		if isNameStart(current) {
			count := 1
			for count < len(tokenWindow) &&
				isNameContinue(tokenWindow[count]) {
				count++
			}
			if count != len(tokenWindow) {
				text := tokenWindow[:count]
				lex.advance(count)
				kind := keyword(text)
				value := lex.token(kind, line)
				if kind == tokenName {
					value.text = text
				}
				return value, nil
			}
			capture := lex.beginTextCapture()
			lex.advance(count)
			if err := lex.advanceWhile(
				&capture,
				nameBodyClass,
			); err != nil {
				return token{}, err
			}
			text, owned := capture.finish(lex)
			kind := keyword(text)
			value := lex.token(kind, line)
			if kind == tokenName {
				value.text = text
				value.ownedText = owned
			}
			return value, nil
		}
	}

	lex.advance(1)
	return lex.token(tokenKind(current), line), nil
}

func (lex *lexer) readNumberWindow(
	line uint32,
	window string,
) (token, bool, error) {
	offset := 0
	if window[offset] == '.' {
		offset++
	}
	for offset < len(window) &&
		(isDigit(window[offset]) || window[offset] == '.') {
		offset++
	}
	if offset < len(window) &&
		(window[offset] == 'e' || window[offset] == 'E') {
		offset++
		if offset < len(window) &&
			(window[offset] == '+' || window[offset] == '-') {
			offset++
		}
	}
	for offset < len(window) && isNameContinue(window[offset]) {
		offset++
	}
	if offset == len(window) && lex.input != nil {
		return token{}, false, nil
	}

	literal := window[:offset]
	number, err := parseNumber(literal)
	if err != nil {
		return token{}, true, lex.errorf(
			line,
			"malformed number near %q",
			literal,
		)
	}
	lex.advance(offset)
	value := lex.token(tokenNumber, line)
	value.number = number
	return value, true, nil
}

func (lex *lexer) acceptByte(expected byte) (bool, error) {
	code := lex.currentCode()
	if code < 0 {
		return false, lex.inputError()
	}
	if byte(code) != expected {
		return false, nil
	}
	lex.advance(1)
	return true, nil
}

func (lex *lexer) readNumber(
	line uint32,
	capture *lexerTextCapture,
) (token, error) {
	if err := lex.advanceWhile(
		capture,
		numberBodyClass,
	); err != nil {
		return token{}, err
	}
	code := lex.capturedCode(capture)
	if code < 0 && lex.inputError() != nil {
		return token{}, lex.inputError()
	}
	if code == 'e' || code == 'E' {
		lex.advance(1)
		code = lex.capturedCode(capture)
		if code < 0 && lex.inputError() != nil {
			return token{}, lex.inputError()
		}
		if code == '+' || code == '-' {
			lex.advance(1)
		}
	}
	if err := lex.advanceWhile(
		capture,
		nameBodyClass,
	); err != nil {
		return token{}, err
	}

	literal, _ := capture.finish(lex)
	number, err := parseNumber(literal)
	if err != nil {
		return token{}, lex.errorf(line, "malformed number near %q", literal)
	}
	value := lex.token(tokenNumber, line)
	value.number = number
	return value, nil
}

func parseNumber(literal string) (float64, error) {
	number, ok := parseLuaNumber(literal)
	if !ok {
		return 0, strconv.ErrSyntax
	}
	return number, nil
}

func (lex *lexer) token(kind tokenKind, line uint32) token {
	return token{
		line: line,
		kind: kind,
	}
}

func (lex *lexer) errorf(line uint32, format string, arguments ...any) error {
	return newSourceSyntaxError(lex.sourceName, line, format, arguments...)
}

func keyword(text string) tokenKind {
	switch text {
	case "and":
		return tokenAnd
	case "break":
		return tokenBreak
	case "do":
		return tokenDo
	case "else":
		return tokenElse
	case "elseif":
		return tokenElseIf
	case "end":
		return tokenEnd
	case "false":
		return tokenFalse
	case "for":
		return tokenFor
	case "function":
		return tokenFunction
	case "if":
		return tokenIf
	case "in":
		return tokenIn
	case "local":
		return tokenLocal
	case "nil":
		return tokenNil
	case "not":
		return tokenNot
	case "or":
		return tokenOr
	case "repeat":
		return tokenRepeat
	case "return":
		return tokenReturn
	case "then":
		return tokenThen
	case "true":
		return tokenTrue
	case "until":
		return tokenUntil
	case "while":
		return tokenWhile
	default:
		return tokenName
	}
}

func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func isHexDigit(value byte) bool {
	return isDigit(value) ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}

func isNameStart(value byte) bool {
	return value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z'
}

func isNameContinue(value byte) bool {
	return isNameStart(value) || isDigit(value)
}
