package lua

import (
	"fmt"
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
	start     uint32
	end       uint32
	line      uint32
	kind      tokenKind
	ownedText bool
}

type lexer struct {
	sourceName string
	source     string
	scratch    []byte
	offset     int
	line       uint32
	lookahead  token
	hasLook    bool
}

func newLexer(sourceName, source string) *lexer {
	return &lexer{
		sourceName: sourceName,
		source:     source,
		line:       1,
	}
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

func (lex *lexer) scan() (token, error) {
	for {
		for lex.offset < len(lex.source) {
			switch lex.source[lex.offset] {
			case ' ', '\t', '\v', '\f':
				lex.offset++
			case '\n', '\r':
				if err := lex.consumeNewline(); err != nil {
					return token{}, err
				}
			default:
				goto skippedSpace
			}
		}

	skippedSpace:
		if lex.offset+1 < len(lex.source) &&
			lex.source[lex.offset] == '-' &&
			lex.source[lex.offset+1] == '-' {
			lex.offset += 2
			if level, after, ok := lex.longDelimiter(lex.offset, '['); ok {
				lex.offset = after
				if _, _, err := lex.readLong(level, false); err != nil {
					return token{}, err
				}
			} else {
				for lex.offset < len(lex.source) &&
					lex.source[lex.offset] != '\n' &&
					lex.source[lex.offset] != '\r' {
					lex.offset++
				}
			}
			continue
		}
		break
	}

	start := lex.offset
	line := lex.line
	if start == len(lex.source) {
		return lex.token(tokenEOF, start, line), nil
	}

	switch current := lex.source[lex.offset]; current {
	case '[':
		if level, after, ok := lex.longDelimiter(start, '['); ok {
			lex.offset = after
			text, owned, err := lex.readLong(level, true)
			if err != nil {
				return token{}, err
			}
			value := lex.token(tokenString, start, line)
			value.text = text
			value.ownedText = owned
			return value, nil
		}
		if start+1 < len(lex.source) && lex.source[start+1] == '=' {
			return token{}, lex.errorf(line, "invalid long string delimiter")
		}
	case '\'', '"':
		text, owned, err := lex.readQuoted(current)
		if err != nil {
			return token{}, err
		}
		value := lex.token(tokenString, start, line)
		value.text = text
		value.ownedText = owned
		return value, nil
	case '.':
		if lex.match("...") {
			return lex.token(tokenDots, start, line), nil
		}
		if lex.match("..") {
			return lex.token(tokenConcat, start, line), nil
		}
		if start+1 < len(lex.source) && isDigit(lex.source[start+1]) {
			return lex.readNumber(start, line)
		}
	case '=':
		if lex.match("==") {
			return lex.token(tokenEqual, start, line), nil
		}
	case '>':
		if lex.match(">=") {
			return lex.token(tokenGreaterEqual, start, line), nil
		}
	case '<':
		if lex.match("<=") {
			return lex.token(tokenLessEqual, start, line), nil
		}
	case '~':
		if lex.match("~=") {
			return lex.token(tokenNotEqual, start, line), nil
		}
	default:
		if isDigit(current) {
			return lex.readNumber(start, line)
		}
		if isNameStart(current) {
			lex.offset++
			for lex.offset < len(lex.source) &&
				isNameContinue(lex.source[lex.offset]) {
				lex.offset++
			}
			text := lex.source[start:lex.offset]
			kind := keyword(text)
			value := lex.token(kind, start, line)
			if kind == tokenName {
				value.text = text
			}
			return value, nil
		}
	}

	lex.offset++
	return lex.token(tokenKind(lex.source[start]), start, line), nil
}

func (lex *lexer) readNumber(start int, line uint32) (token, error) {
	offset := start
	if lex.source[offset] == '.' {
		offset++
	}
	for offset < len(lex.source) &&
		(isDigit(lex.source[offset]) || lex.source[offset] == '.') {
		offset++
	}
	if offset < len(lex.source) &&
		(lex.source[offset] == 'e' || lex.source[offset] == 'E') {
		offset++
		if offset < len(lex.source) &&
			(lex.source[offset] == '+' || lex.source[offset] == '-') {
			offset++
		}
	}
	for offset < len(lex.source) && isNameContinue(lex.source[offset]) {
		offset++
	}

	literal := lex.source[start:offset]
	number, err := parseNumber(literal)
	if err != nil {
		return token{}, lex.errorf(line, "malformed number near %q", literal)
	}
	lex.offset = offset
	value := lex.token(tokenNumber, start, line)
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

func (lex *lexer) readQuoted(delimiter byte) (string, bool, error) {
	lex.offset++
	contentStart := lex.offset
	lex.scratch = lex.scratch[:0]
	escaped := false

	for lex.offset < len(lex.source) {
		current := lex.source[lex.offset]
		switch current {
		case delimiter:
			end := lex.offset
			lex.offset++
			if !escaped {
				return lex.source[contentStart:end], false, nil
			}
			return string(lex.scratch), true, nil
		case '\n', '\r':
			return "", false, lex.errorf(lex.line, "unfinished string")
		case '\\':
			if !escaped {
				lex.scratch = append(
					lex.scratch,
					lex.source[contentStart:lex.offset]...,
				)
				escaped = true
			}
			lex.offset++
			if lex.offset == len(lex.source) {
				return "", false, lex.errorf(lex.line, "unfinished string")
			}
			escape := lex.source[lex.offset]
			switch escape {
			case 'a':
				lex.scratch = append(lex.scratch, '\a')
				lex.offset++
			case 'b':
				lex.scratch = append(lex.scratch, '\b')
				lex.offset++
			case 'f':
				lex.scratch = append(lex.scratch, '\f')
				lex.offset++
			case 'n':
				lex.scratch = append(lex.scratch, '\n')
				lex.offset++
			case 'r':
				lex.scratch = append(lex.scratch, '\r')
				lex.offset++
			case 't':
				lex.scratch = append(lex.scratch, '\t')
				lex.offset++
			case 'v':
				lex.scratch = append(lex.scratch, '\v')
				lex.offset++
			case '\n', '\r':
				lex.scratch = append(lex.scratch, '\n')
				if err := lex.consumeNewline(); err != nil {
					return "", false, err
				}
			default:
				if !isDigit(escape) {
					lex.scratch = append(lex.scratch, escape)
					lex.offset++
					break
				}
				value := 0
				digits := 0
				for lex.offset < len(lex.source) &&
					digits < 3 &&
					isDigit(lex.source[lex.offset]) {
					value = value*10 + int(lex.source[lex.offset]-'0')
					lex.offset++
					digits++
				}
				if value > 255 {
					return "", false, lex.errorf(
						lex.line,
						"escape sequence too large",
					)
				}
				lex.scratch = append(lex.scratch, byte(value))
			}
		default:
			if escaped {
				lex.scratch = append(lex.scratch, current)
			}
			lex.offset++
		}
	}
	return "", false, lex.errorf(lex.line, "unfinished string")
}

func (lex *lexer) readLong(level int, keepText bool) (string, bool, error) {
	if lex.offset < len(lex.source) &&
		(lex.source[lex.offset] == '\n' || lex.source[lex.offset] == '\r') {
		if err := lex.consumeNewline(); err != nil {
			return "", false, err
		}
	}

	contentStart := lex.offset
	segmentStart := contentStart
	lex.scratch = lex.scratch[:0]
	normalizing := false

	for lex.offset < len(lex.source) {
		if level == 0 &&
			lex.offset+1 < len(lex.source) &&
			lex.source[lex.offset] == '[' &&
			lex.source[lex.offset+1] == '[' {
			return "", false, lex.errorf(
				lex.line,
				"nesting of [[...]] is deprecated",
			)
		}
		if lex.source[lex.offset] == ']' {
			_, after, ok := lex.longDelimiter(lex.offset, ']')
			if ok && after-lex.offset == level+2 {
				end := lex.offset
				lex.offset = after
				if !keepText {
					return "", false, nil
				}
				if !normalizing {
					return lex.source[contentStart:end], false, nil
				}
				lex.scratch = append(
					lex.scratch,
					lex.source[segmentStart:end]...,
				)
				return string(lex.scratch), true, nil
			}
		}

		switch lex.source[lex.offset] {
		case '\n', '\r':
			current := lex.source[lex.offset]
			mixedPair := lex.offset+1 < len(lex.source) &&
				(lex.source[lex.offset+1] == '\n' ||
					lex.source[lex.offset+1] == '\r') &&
				lex.source[lex.offset+1] != current
			normalize := current == '\r' || mixedPair
			if keepText && normalize {
				if !normalizing {
					lex.scratch = append(
						lex.scratch,
						lex.source[contentStart:lex.offset]...,
					)
					normalizing = true
				} else {
					lex.scratch = append(
						lex.scratch,
						lex.source[segmentStart:lex.offset]...,
					)
				}
				lex.scratch = append(lex.scratch, '\n')
			}
			if err := lex.consumeNewline(); err != nil {
				return "", false, err
			}
			if normalize {
				segmentStart = lex.offset
			}
		default:
			lex.offset++
		}
	}
	if keepText {
		return "", false, lex.errorf(lex.line, "unfinished long string")
	}
	return "", false, lex.errorf(lex.line, "unfinished long comment")
}

func (lex *lexer) longDelimiter(
	offset int,
	delimiter byte,
) (level, after int, ok bool) {
	if offset >= len(lex.source) || lex.source[offset] != delimiter {
		return 0, offset, false
	}
	cursor := offset + 1
	for cursor < len(lex.source) && lex.source[cursor] == '=' {
		cursor++
	}
	if cursor >= len(lex.source) || lex.source[cursor] != delimiter {
		return cursor - offset - 1, cursor, false
	}
	return cursor - offset - 1, cursor + 1, true
}

func (lex *lexer) consumeNewline() error {
	first := lex.source[lex.offset]
	lex.offset++
	if lex.offset < len(lex.source) &&
		(lex.source[lex.offset] == '\n' || lex.source[lex.offset] == '\r') &&
		lex.source[lex.offset] != first {
		lex.offset++
	}
	if lex.line == ^uint32(0) {
		return lex.errorf(lex.line, "chunk has too many lines")
	}
	lex.line++
	return nil
}

func (lex *lexer) match(text string) bool {
	if len(lex.source)-lex.offset < len(text) ||
		lex.source[lex.offset:lex.offset+len(text)] != text {
		return false
	}
	lex.offset += len(text)
	return true
}

func (lex *lexer) token(kind tokenKind, start int, line uint32) token {
	return token{
		start: uint32(start),
		end:   uint32(lex.offset),
		line:  line,
		kind:  kind,
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
