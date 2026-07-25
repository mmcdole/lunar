package lua

import (
	"math"
	"strings"
	"testing"
)

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
	if tokens[len(tokens)-1].start != uint32(len(source)) {
		t.Fatal("EOF offset does not identify the end of the source")
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
			if !luaError.Value().IsNil() {
				t.Fatalf("error value = %v, want nil", luaError.Value())
			}
			if !strings.Contains(err.Error(), test.want) ||
				!strings.Contains(err.Error(), "bad.lua:1:") {
				t.Fatalf("error = %q, want %q and source line", err, test.want)
			}
		})
	}
}

func TestSourceIDFormatting(t *testing.T) {
	if got := sourceID("@path/to/chunk.lua"); got != "path/to/chunk.lua" {
		t.Fatalf("file source = %q", got)
	}
	if got := sourceID("=named chunk"); got != "named chunk" {
		t.Fatalf("literal source = %q", got)
	}
	if got := sourceID("return 1\nignored"); got != `[string "return 1..."]` {
		t.Fatalf("string source = %q", got)
	}
	if got := sourceID(""); got != "?" {
		t.Fatalf("empty source = %q", got)
	}
	if got := sourceID("@" + strings.Repeat("x", 100)); len(got) != 55 ||
		!strings.HasPrefix(got, "...") {
		t.Fatalf("long file source = %q", got)
	}
}

func TestTokenNames(t *testing.T) {
	if tokenAnd.String() != "and" ||
		tokenConcat.String() != ".." ||
		tokenEOF.String() != "<eof>" ||
		tokenKind('+').String() != "'+'" {
		t.Fatal("token diagnostics are incorrect")
	}
	if tokenKind(1).String() != "byte(1)" {
		t.Fatal("control-byte diagnostic is incorrect")
	}
}
