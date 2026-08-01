package lua

import (
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

type lexerTokenSnapshot struct {
	text       string
	numberBits uint64
	line       uint32
	kind       tokenKind
}

func testChunkInput(
	pieces []string,
	terminal error,
	refills *int,
) *chunkInput {
	index := 0
	return newRefillableChunkInput(func() (string, error) {
		if refills != nil {
			(*refills)++
		}
		if index == len(pieces) {
			return "", terminal
		}
		piece := pieces[index]
		index++
		if piece == "" {
			panic("empty test input piece")
		}
		return piece, nil
	}, nil)
}

func oneByteTestPieces(source string) []string {
	pieces := make([]string, len(source))
	for index := range source {
		pieces[index] = source[index : index+1]
	}
	return pieces
}

func snapshotLexer(lex *lexer) ([]lexerTokenSnapshot, error) {
	var tokens []lexerTokenSnapshot
	for {
		value, err := lex.next()
		if err != nil {
			return nil, err
		}
		numberBits := uint64(0)
		if value.kind == tokenNumber {
			numberBits = math.Float64bits(value.number)
		}
		tokens = append(tokens, lexerTokenSnapshot{
			text:       value.text,
			numberBits: numberBits,
			line:       value.line,
			kind:       value.kind,
		})
		if value.kind == tokenEOF {
			return tokens, nil
		}
	}
}

func TestLexerTokensAndLookahead(t *testing.T) {
	source := `
and break do else elseif end false for function if in local nil not or
repeat return then true until while
name_1 = 0x10 + .5e1 .. "x" ... 'y'
value ~= 3 and value <= 4 or value >= 2 == true
`
	lex := newLexer("tokens.lua", source)

	peeked, err := lex.peek()
	if err != nil {
		t.Fatal(err)
	}
	if peeked.kind != tokenAnd {
		t.Fatalf("peek = %s, want and", peeked.kind)
	}
	first, err := lex.next()
	if err != nil {
		t.Fatal(err)
	}
	if first != peeked {
		t.Fatal("next did not consume the lookahead token")
	}

	wantKinds := []tokenKind{
		tokenBreak, tokenDo, tokenElse, tokenElseIf, tokenEnd, tokenFalse,
		tokenFor, tokenFunction, tokenIf, tokenIn, tokenLocal, tokenNil,
		tokenNot, tokenOr, tokenRepeat, tokenReturn, tokenThen, tokenTrue,
		tokenUntil, tokenWhile,
		tokenName, '=', tokenNumber, '+', tokenNumber, tokenConcat,
		tokenString, tokenDots, tokenString,
		tokenName, tokenNotEqual, tokenNumber, tokenAnd, tokenName,
		tokenLessEqual, tokenNumber, tokenOr, tokenName, tokenGreaterEqual,
		tokenNumber, tokenEqual, tokenTrue, tokenEOF,
	}

	tokens := make([]token, 0, len(wantKinds))
	for _, want := range wantKinds {
		value, nextErr := lex.next()
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if value.kind != want {
			t.Fatalf(
				"token %d = %s, want %s",
				len(tokens),
				value.kind,
				want,
			)
		}
		tokens = append(tokens, value)
	}

	if tokens[20].text != "name_1" {
		t.Fatalf("name = %q, want name_1", tokens[20].text)
	}
	if tokens[22].number != 16 || tokens[24].number != 5 {
		t.Fatalf(
			"numbers = (%v, %v), want (16, 5)",
			tokens[22].number,
			tokens[24].number,
		)
	}
	if tokens[26].text != "x" || tokens[28].text != "y" {
		t.Fatalf(
			"strings = (%q, %q), want (x, y)",
			tokens[26].text,
			tokens[28].text,
		)
	}
	if lex.inputOffset() != len(source) {
		t.Fatal("lexer did not consume the complete source")
	}
}

func TestLexerFragmentationMatchesFixedInput(t *testing.T) {
	source := "-- heading\r\n" +
		"--[==[ignored\n\rcomment]==]\n" +
		"and identifier_123 = 0x1e + .5e1; " +
		"... .. . :: : == = >= > <= < ~= ~ " +
		"\"a\\n\\097\\255\\q\\\"continued\\\r\nline\" " +
		"'plain' [==[\r\nfirst\rsecond\n\rthird ]=] inner]==] " +
		"[=[left]===]=] tail 1e2..3"

	want, err := snapshotLexer(newLexer("@fragmented.lua", source))
	if err != nil {
		t.Fatal(err)
	}

	var stringsSeen []string
	var kindsSeen []tokenKind
	var numbersSeen []float64
	for _, value := range want {
		kindsSeen = append(kindsSeen, value.kind)
		if value.kind == tokenString {
			stringsSeen = append(stringsSeen, value.text)
		}
		if value.kind == tokenNumber {
			numbersSeen = append(
				numbersSeen,
				math.Float64frombits(value.numberBits),
			)
		}
	}
	wantStrings := []string{
		"a\na\xffq\"continued\nline",
		"plain",
		"first\nsecond\nthird ]=] inner",
		"left]===",
	}
	if !reflect.DeepEqual(stringsSeen, wantStrings) {
		t.Fatalf("strings = %#v, want %#v", stringsSeen, wantStrings)
	}
	wantNumbers := []float64{30, 5, 100, 3}
	if !reflect.DeepEqual(numbersSeen, wantNumbers) {
		t.Fatalf("numbers = %#v, want %#v", numbersSeen, wantNumbers)
	}
	wantOperators := []tokenKind{
		tokenDots,
		tokenConcat,
		'.',
		tokenDoubleColon,
		':',
		tokenEqual,
		'=',
		tokenGreaterEqual,
		'>',
		tokenLessEqual,
		'<',
		tokenNotEqual,
		'~',
	}
	if !containsTokenKinds(kindsSeen, wantOperators) {
		t.Fatalf("tokens do not contain maximal-munch sequence %#v", wantOperators)
	}

	check := func(label string, pieces []string) {
		t.Helper()
		input := testChunkInput(pieces, nil, nil)
		got, scanErr := snapshotLexer(
			newInputLexer("@fragmented.lua", input),
		)
		if scanErr != nil {
			t.Fatalf("%s: %v", label, scanErr)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s tokens differ:\n got: %#v\nwant: %#v", label, got, want)
		}
		if input.position != uint64(len(source)) {
			t.Fatalf(
				"%s consumed %d bytes, want %d",
				label,
				input.position,
				len(source),
			)
		}
	}
	for split := 1; split < len(source); split++ {
		check(
			"split "+strconv.Itoa(split),
			[]string{source[:split], source[split:]},
		)
	}
	check("one-byte pieces", oneByteTestPieces(source))
}

func containsTokenKinds(values, sequence []tokenKind) bool {
	if len(sequence) == 0 {
		return true
	}
	for start := 0; start+len(sequence) <= len(values); start++ {
		if reflect.DeepEqual(
			values[start:start+len(sequence)],
			sequence,
		) {
			return true
		}
	}
	return false
}

func TestLexerFragmentedTextOwnership(t *testing.T) {
	tests := []struct {
		name       string
		pieces     []string
		want       string
		kind       tokenKind
		borrowedAt int
		owned      bool
	}{
		{
			name:       "same-piece name",
			pieces:     []string{"borrowed "},
			want:       "borrowed",
			kind:       tokenName,
			borrowedAt: 0,
		},
		{
			name:       "same-piece quoted",
			pieces:     []string{`"borrowed" `},
			want:       "borrowed",
			kind:       tokenString,
			borrowedAt: 1,
		},
		{
			name:   "cross-piece name",
			pieces: []string{"cross", "piece "},
			want:   "crosspiece",
			kind:   tokenName,
			owned:  true,
		},
		{
			name:   "cross-piece quoted",
			pieces: []string{`"cross`, `piece" `},
			want:   "crosspiece",
			kind:   tokenString,
			owned:  true,
		},
		{
			name:   "decoded quoted",
			pieces: []string{`"line\n" `},
			want:   "line\n",
			kind:   tokenString,
			owned:  true,
		},
		{
			name:   "identical consecutive pieces",
			pieces: []string{"same", "same"},
			want:   "samesame",
			kind:   tokenName,
			owned:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := testChunkInput(test.pieces, nil, nil)
			value, err := newInputLexer("@ownership.lua", input).next()
			if err != nil {
				t.Fatal(err)
			}
			if value.kind != test.kind ||
				value.text != test.want ||
				value.ownedText != test.owned {
				t.Fatalf(
					"token = (%s, %q, owned %v), want (%s, %q, owned %v)",
					value.kind,
					value.text,
					value.ownedText,
					test.kind,
					test.want,
					test.owned,
				)
			}
			if !test.owned &&
				unsafe.StringData(value.text) !=
					unsafe.StringData(test.pieces[0][test.borrowedAt:]) {
				t.Fatal("same-piece token did not borrow its refill piece")
			}
		})
	}
}

func TestLexerRefillFailureIsStickyAndPeekDoesNotReadAhead(t *testing.T) {
	t.Run("peek", func(t *testing.T) {
		refills := 0
		input := testChunkInput(
			[]string{"alpha ", "beta"},
			nil,
			&refills,
		)
		lex := newInputLexer("@peek.lua", input)
		first, err := lex.peek()
		if err != nil {
			t.Fatal(err)
		}
		if first.kind != tokenName || first.text != "alpha" {
			t.Fatalf("peek = (%s, %q)", first.kind, first.text)
		}
		afterFirst := refills
		second, err := lex.peek()
		if err != nil {
			t.Fatal(err)
		}
		next, err := lex.next()
		if err != nil {
			t.Fatal(err)
		}
		if second != first || next != first {
			t.Fatal("peek and next did not preserve the same token")
		}
		if refills != afterFirst {
			t.Fatalf(
				"cached peek performed %d extra refills",
				refills-afterFirst,
			)
		}
	})

	t.Run("sticky failure", func(t *testing.T) {
		sentinel := errors.New("refill failed")
		refills := 0
		input := testChunkInput(
			[]string{"-- unfinished line"},
			sentinel,
			&refills,
		)
		lex := newInputLexer("@failure.lua", input)
		_, first := lex.next()
		afterFirst := refills
		_, second := lex.next()
		if !errors.Is(first, sentinel) || !errors.Is(second, sentinel) {
			t.Fatalf("errors = (%v, %v), want sentinel", first, second)
		}
		if refills != afterFirst {
			t.Fatal("sticky refill failure invoked the reader again")
		}
	})
}

func TestLexerUsesPUCSignedLineBound(t *testing.T) {
	lex := newLexer("@lines.lua", "\n\n")
	lex.line = maxSourceLine - 2
	if _, err := lex.consumeNewline(); err != nil {
		t.Fatalf("line below PUC bound: %v", err)
	}
	if lex.line != maxSourceLine-1 {
		t.Fatalf("line = %d, want %d", lex.line, maxSourceLine-1)
	}
	if _, err := lex.consumeNewline(); err == nil ||
		!strings.Contains(err.Error(), "chunk has too many lines") {
		t.Fatalf("signed line-bound error = %v", err)
	}
}

func TestLexerStringsCommentsAndLines(t *testing.T) {
	source := "-- heading\r\n" +
		"--[=[ignored\rcomment]=]\n" +
		"local a = \"a\\n\\097\\255\\q\\\\\\\"\"\n" +
		"local b = [==[\r\nfirst\rsecond\nthird]==]\n" +
		"local c = 'continued\\\r\nline'\n"
	lex := newLexer("@strings.lua", source)

	type expected struct {
		kind tokenKind
		text string
		line uint32
	}
	want := []expected{
		{tokenLocal, "", 4},
		{tokenName, "a", 4},
		{'=', "", 4},
		{tokenString, "a\na\xffq\\\"", 4},
		{tokenLocal, "", 5},
		{tokenName, "b", 5},
		{'=', "", 5},
		{tokenString, "first\nsecond\nthird", 5},
		{tokenLocal, "", 9},
		{tokenName, "c", 9},
		{'=', "", 9},
		{tokenString, "continued\nline", 9},
		{tokenEOF, "", 11},
	}

	for index, expected := range want {
		value, err := lex.next()
		if err != nil {
			t.Fatalf("token %d: %v", index, err)
		}
		if value.kind != expected.kind ||
			value.text != expected.text ||
			value.line != expected.line {
			t.Fatalf(
				"token %d = (%s, %q, line %d), want (%s, %q, line %d)",
				index,
				value.kind,
				value.text,
				value.line,
				expected.kind,
				expected.text,
				expected.line,
			)
		}
	}
}

func TestLexerLongDelimitersAndCommentFallback(t *testing.T) {
	source := "--[=not a long comment\n" +
		"[=[]=] [==[outer ]=] inner]==] " +
		"\"\\000\" \"\\1234\" --"
	lex := newLexer("long.lua", source)
	want := []struct {
		kind tokenKind
		text string
	}{
		{tokenString, ""},
		{tokenString, "outer ]=] inner"},
		{tokenString, "\x00"},
		{tokenString, "{4"},
		{tokenEOF, ""},
	}
	for index, expected := range want {
		value, err := lex.next()
		if err != nil {
			t.Fatalf("token %d: %v", index, err)
		}
		if value.kind != expected.kind || value.text != expected.text {
			t.Fatalf(
				"token %d = (%s, %q), want (%s, %q)",
				index,
				value.kind,
				value.text,
				expected.kind,
				expected.text,
			)
		}
	}
}

func TestLexerNumerals(t *testing.T) {
	source := "0 1.25 1. .5 1.e2 1e3 1E-2 0xe 0x1e2 0xff 0XFFFFFFFFFFFFFFFF 0xffffffffffffffffffff 1e400"
	lex := newLexer("numbers.lua", source)
	want := []float64{
		0,
		1.25,
		1,
		0.5,
		100,
		1000,
		0.01,
		14,
		482,
		255,
		float64(^uint64(0)),
		math.Ldexp(1, 80),
		math.Inf(1),
	}
	for index, expected := range want {
		value, err := lex.next()
		if err != nil {
			t.Fatalf("number %d: %v", index, err)
		}
		if value.kind != tokenNumber || value.number != expected {
			t.Fatalf(
				"number %d = (%s, %v), want %v",
				index,
				value.kind,
				value.number,
				expected,
			)
		}
	}
}

func TestLexerKeepsExponentBeforeConcatSeparate(t *testing.T) {
	lex := newLexer("numbers.lua", "1e2..3")
	want := []tokenKind{tokenNumber, tokenConcat, tokenNumber, tokenEOF}
	for index, kind := range want {
		value, err := lex.next()
		if err != nil {
			t.Fatalf("token %d: %v", index, err)
		}
		if value.kind != kind {
			t.Fatalf("token %d = %s, want %s", index, value.kind, kind)
		}
	}
}

func TestLexerNormalizesLongStringNewlines(t *testing.T) {
	for _, newline := range []string{"\n", "\r", "\n\r", "\r\n"} {
		source := "[=[" + newline + "first" + newline + "second]=]"
		lex := newLexer("newlines.lua", source)
		value, err := lex.next()
		if err != nil {
			t.Fatalf("%q: %v", newline, err)
		}
		if value.kind != tokenString || value.text != "first\nsecond" {
			t.Fatalf(
				"%q = (%s, %q), want string %q",
				newline,
				value.kind,
				value.text,
				"first\nsecond",
			)
		}
		if lex.line != 3 {
			t.Fatalf("%q final line = %d, want 3", newline, lex.line)
		}
	}
}

func TestLexerRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"number", "1..2", "malformed number"},
		{"exponent", "1e", "malformed number"},
		{"exponent sign", "1e+", "malformed number"},
		{"hex", "0xno", "malformed number"},
		{"empty hex", "0x", "malformed number"},
		{"hex exponent", "0x1p2", "malformed number"},
		{"uppercase hex exponent", "0XfP2", "malformed number"},
		{"signed hex exponent", "0x1p+2", "malformed number"},
		{"negative hex exponent", "0x1p-2", "malformed number"},
		{"number suffix", "123abc", "malformed number"},
		{"number underscore", "1_0", "malformed number"},
		{"quoted newline", "'first\nsecond'", "unfinished string"},
		{"quoted eof", "\"open", "unfinished string"},
		{"escape", "'\\256'", "escape sequence too large"},
		{"long string", "[=[open", "unfinished long string"},
		{"long comment", "--[=[open", "unfinished long comment"},
		{"delimiter", "[=open", "invalid long string delimiter"},
		{"nested long string", "[[outer [[inner]] outer]]", "nesting of [[...]] is deprecated"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lex := newLexer("@bad.lua", test.source)
			_, err := lex.next()
			if err == nil {
				t.Fatal("malformed source was accepted")
			}
			luaError, ok := err.(*Error)
			if !ok || luaError.Category() != SyntaxError {
				t.Fatalf("error = %T %v, want SyntaxError", err, err)
			}
			if value, ok := luaError.Value().AsString(); !ok ||
				value != luaError.Error() {
				t.Fatalf("error value = %q, %v", value, ok)
			}
			if !strings.Contains(err.Error(), test.want) ||
				!strings.Contains(err.Error(), "bad.lua:1:") {
				t.Fatalf("error = %q, want %q and source line", err, test.want)
			}
		})
	}
}

func TestTokenNames(t *testing.T) {
	if tokenAnd.String() != "and" ||
		tokenConcat.String() != ".." ||
		tokenDoubleColon.String() != "::" ||
		tokenEOF.String() != "<eof>" ||
		tokenKind('+').String() != "'+'" {
		t.Fatal("token diagnostics are incorrect")
	}
	if tokenKind(1).String() != "byte(1)" {
		t.Fatal("control-byte diagnostic is incorrect")
	}
}
