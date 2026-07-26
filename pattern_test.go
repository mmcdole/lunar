package lua

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// patternEngineCase is one recorded engine case. offset is a zero-based
// subject offset, already normalized the way string.find normalizes its init
// argument, so the engine is exercised without a library in between.
//
// want is what PUC Lua 5.1.5 produces, in the shared spelling
// runPatternEngineCase builds. Regenerate or re-verify with:
//
//	BADGER_LUA51=/path/to/lua-5.1.5/src/lua go test -run PatternEngineCasesMatch
//
// The recording driver forces PUC through its matcher even for patterns whose
// bytes would otherwise qualify for string.find's plain scan, so these cases
// describe the engine and not the library shortcut.
type patternEngineCase struct {
	name    string
	subject string
	pattern string
	offset  int
	want    string
}

// runPatternEngineCase drives the engine the way string.find does and prints
// the outcome as the recorded Lua driver prints it: a failure message, "nil",
// or the one-based span followed by each capture.
func runPatternEngineCase(subject, pattern string, offset int) string {
	var state matchState
	start, end, found := state.find(subject, pattern, offset)
	if state.failed() {
		return "error '" + state.failure + "'"
	}
	if !found {
		return "nil"
	}
	parts := make([]string, 0, 4)
	parts = append(parts, strconv.Itoa(start+1), strconv.Itoa(end))
	// string.find reports only the explicit captures; the whole match is
	// already described by the span.
	for index := 0; index < state.captureCount(false); index++ {
		value, ok := state.captureValue(index, start, end)
		if !ok {
			return "error '" + state.failure + "'"
		}
		if value.isPosition {
			parts = append(parts, strconv.Itoa(value.position))
			continue
		}
		parts = append(parts, "'"+value.text+"'")
	}
	return strings.Join(parts, " ")
}

func TestPatternEngineMatchesLua51(t *testing.T) {
	for _, test := range patternEngineLua51Cases {
		t.Run(test.name, func(t *testing.T) {
			got := runPatternEngineCase(
				test.subject,
				test.pattern,
				test.offset,
			)
			if got != test.want {
				t.Fatalf(
					"subject %q pattern %q offset %d\n got: %s\nwant: %s",
					test.subject,
					test.pattern,
					test.offset,
					got,
					test.want,
				)
			}
		})
	}
}

// TestPatternEngineCasesMatchTheLua51Oracle re-derives every recorded engine
// expectation from a real Lua 5.1 interpreter. It is skipped unless
// BADGER_LUA51 names one, because the reference binary is deliberately not
// carried in this repository.
func TestPatternEngineCasesMatchTheLua51Oracle(t *testing.T) {
	binary := os.Getenv("BADGER_LUA51")
	if binary == "" {
		t.Skip("set BADGER_LUA51 to a Lua 5.1 interpreter to verify")
	}
	driver := &strings.Builder{}
	driver.WriteString(patternOracleDriver)
	driver.WriteString("local cases = {\n")
	for _, test := range patternEngineLua51Cases {
		fmt.Fprintf(
			driver,
			"{%s, %s, %d},\n",
			quoteLuaString(test.subject),
			quoteLuaString(test.pattern),
			test.offset+1,
		)
	}
	driver.WriteString("}\nrun(cases)\n")

	path := filepath.Join(t.TempDir(), "pattern-oracle.lua")
	if err := os.WriteFile(path, []byte(driver.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(binary, path).Output()
	if err != nil {
		t.Fatalf("%s: %v", binary, err)
	}
	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(lines) != len(patternEngineLua51Cases) {
		t.Fatalf(
			"oracle produced %d lines; want %d",
			len(lines),
			len(patternEngineLua51Cases),
		)
	}
	record := os.Getenv("BADGER_LUA51_RECORD") != ""
	for index, test := range patternEngineLua51Cases {
		if record {
			t.Logf(
				"{\n\tname:    %q,\n\tsubject: %q,\n\tpattern: %q,\n\toffset:  %d,\n\twant:    %q,\n},",
				test.name,
				test.subject,
				test.pattern,
				test.offset,
				lines[index],
			)
			continue
		}
		if lines[index] != test.want {
			t.Errorf(
				"%s: oracle disagrees with the recorded expectation\nsubject %q pattern %q\n got: %s\nwant: %s",
				test.name,
				test.subject,
				test.pattern,
				lines[index],
				test.want,
			)
		}
	}
}

// patternOracleDriver prints each case the way runPatternEngineCase does.
//
// It forces PUC through its matcher even for a pattern whose bytes would
// qualify for string.find's plain scan. That shortcut can disagree with the
// matcher when the pattern contains an embedded NUL, because strpbrk stops
// there while the plain search that follows uses the full byte length. Adding
// a position capture puts a magic byte first and reports the same span.
const patternOracleDriver = `
local SPECIALS = "^$*+?.([%-"

local function fmt(v)
  if v == nil then return "nil" end
  if type(v) == "number" then return string.format("%.14g", v) end
  return "'" .. v .. "'"
end

local function collect(...)
  local out = {}
  for i = 1, select("#", ...) do out[#out + 1] = fmt((select(i, ...))) end
  return table.concat(out, " ")
end

local function usesMatcher(p)
  for i = 1, #p do
    local c = p:sub(i, i)
    if c == "\0" then return false end
    if SPECIALS:find(c, 1, true) then return true end
  end
  return false
end

function run(cases)
  for i = 1, #cases do
    local subject, pattern, init = cases[i][1], cases[i][2], cases[i][3]
    local ok, err = pcall(string.match, subject, pattern, init)
    if not ok then
      io.write("error '", tostring(err), "'\n")
    elseif usesMatcher(pattern) then
      io.write(collect(string.find(subject, pattern, init)), "\n")
    else
      local a, b = string.find(subject, "()" .. pattern, init)
      if a == nil then io.write("nil\n") else io.write(a, " ", b, "\n") end
    end
  end
end
`

// TestPatternEngineAbandonsAFailedPatternImmediately confirms that a rejected
// pattern stops the whole search, the way PUC's error longjmp does, instead of
// being recorded and discovered after the scan finishes.
//
// The pattern below backtracks exponentially before reaching its malformed
// tail, so a search that swallowed the failure and continued would not finish
// in any reasonable time.
func TestPatternEngineAbandonsAFailedPatternImmediately(t *testing.T) {
	var state matchState
	_, _, found := state.find(
		strings.Repeat("a", 30),
		strings.Repeat("a*", 24)+"%",
		0,
	)
	if found {
		t.Fatal("malformed pattern reported a match")
	}
	if state.failure != "malformed pattern (ends with '%')" {
		t.Fatalf("failure = %q", state.failure)
	}

	// An unfinished capture is different: PUC lets the match itself succeed
	// and only fails when a capture is read, which gsub relies on.
	var unfinished matchState
	start, end, found := unfinished.find("abc", "(a", 0)
	if !found || unfinished.failed() || start != 0 || end != 1 {
		t.Fatalf(
			"span = %d..%d found=%v failure=%q",
			start,
			end,
			found,
			unfinished.failure,
		)
	}
	if _, ok := unfinished.captureValue(0, start, end); ok {
		t.Fatal("reading an unfinished capture succeeded")
	}
	if unfinished.failure != "unfinished capture" {
		t.Fatalf("failure = %q; want unfinished capture", unfinished.failure)
	}
}

// TestPatternEngineLimitsCaptures holds LUA_MAXCAPTURES.
func TestPatternEngineLimitsCaptures(t *testing.T) {
	subject := strings.Repeat("a", maxPatternCaptures+8)

	var accepted matchState
	_, end, found := accepted.find(
		subject,
		strings.Repeat("(a)", maxPatternCaptures),
		0,
	)
	if !found || accepted.failed() {
		t.Fatalf(
			"%d captures rejected: %q",
			maxPatternCaptures,
			accepted.failure,
		)
	}
	if end != maxPatternCaptures {
		t.Fatalf("span end = %d; want %d", end, maxPatternCaptures)
	}
	if accepted.captureCount(false) != maxPatternCaptures {
		t.Fatalf(
			"capture count = %d; want %d",
			accepted.captureCount(false),
			maxPatternCaptures,
		)
	}

	var rejected matchState
	if _, _, found := rejected.find(
		subject,
		strings.Repeat("(a)", maxPatternCaptures+1),
		0,
	); found {
		t.Fatal("more than LUA_MAXCAPTURES captures were accepted")
	}
	if rejected.failure != "too many captures" {
		t.Fatalf("failure = %q; want too many captures", rejected.failure)
	}
}

// TestPatternEngineBoundsRecursion records the engine's one deliberate
// divergence from PUC Lua 5.1.
//
// PUC recurses on the C stack with no limit, so a deeply nested pattern
// crashes the interpreter. A Go stack overflow is fatal and unrecoverable, and
// this runtime is embedded in host programs, so the same recursion is bounded
// and reports the error Lua 5.2 introduced for it. Depth follows the number of
// pattern items rather than the subject length, so ordinary patterns over long
// subjects are unaffected, which the final case pins.
func TestPatternEngineBoundsRecursion(t *testing.T) {
	within := maxPatternDepth - 64
	var accepted matchState
	if _, _, found := accepted.find(
		strings.Repeat("a", within),
		strings.Repeat("a?", within),
		0,
	); !found || accepted.failed() {
		t.Fatalf(
			"depth %d rejected: %q",
			within,
			accepted.failure,
		)
	}

	var rejected matchState
	beyond := maxPatternDepth + 64
	if _, _, found := rejected.find(
		strings.Repeat("a", beyond),
		strings.Repeat("a?", beyond),
		0,
	); found {
		t.Fatal("an over-deep pattern reported a match")
	}
	if rejected.failure != "pattern too complex" {
		t.Fatalf("failure = %q; want pattern too complex", rejected.failure)
	}

	// A long subject alone must never approach the limit: greedy repetition
	// scans iteratively and gives repetitions back without nesting.
	var long matchState
	if _, end, found := long.find(
		strings.Repeat("a", 200000)+"b",
		"a*b",
		0,
	); !found || long.failed() || end != 200001 {
		t.Fatalf(
			"long subject: end=%d found=%v failure=%q",
			end,
			found,
			long.failure,
		)
	}
}

// TestPatternEngineRestartsCleanly confirms that the scan resets capture state
// per start position, so a capture recorded by a failed attempt cannot leak
// into the match that succeeds later.
func TestPatternEngineRestartsCleanly(t *testing.T) {
	var state matchState
	start, end, found := state.find("xxabab", "(a)(b)", 0)
	if !found || start != 2 || end != 4 {
		t.Fatalf("span = %d..%d found=%v", start, end, found)
	}
	if state.captureCount(false) != 2 {
		t.Fatalf("capture count = %d; want 2", state.captureCount(false))
	}
	for index, want := range []string{"a", "b"} {
		value, ok := state.captureValue(index, start, end)
		if !ok || value.isPosition || value.text != want {
			t.Fatalf("capture %d = %#v ok=%v", index, value, ok)
		}
	}
}

func TestPatternAnchorFollowsLua51(t *testing.T) {
	for _, test := range []struct {
		pattern  string
		stripped string
		anchored bool
	}{
		{pattern: "^abc", stripped: "abc", anchored: true},
		{pattern: "abc", stripped: "abc", anchored: false},
		{pattern: "^", stripped: "", anchored: true},
		{pattern: "", stripped: "", anchored: false},
		{pattern: "a^b", stripped: "a^b", anchored: false},
	} {
		stripped, anchored := patternAnchor(test.pattern)
		if stripped != test.stripped || anchored != test.anchored {
			t.Fatalf(
				"patternAnchor(%q) = (%q, %v); want (%q, %v)",
				test.pattern,
				stripped,
				anchored,
				test.stripped,
				test.anchored,
			)
		}
	}
}

// TestPatternHasSpecialsFollowsStrpbrk pins the plain-search decision,
// including PUC's inconsistency: strpbrk stops at an embedded NUL while the
// plain search that follows uses the pattern's full byte length.
func TestPatternHasSpecialsFollowsStrpbrk(t *testing.T) {
	for _, test := range []struct {
		pattern string
		want    bool
	}{
		{pattern: "", want: false},
		{pattern: "abc", want: false},
		{pattern: ")", want: false},
		{pattern: "]", want: false},
		{pattern: "}", want: false},
		{pattern: "a.b", want: true},
		{pattern: "^a", want: true},
		{pattern: "a$", want: true},
		{pattern: "a*", want: true},
		{pattern: "a+", want: true},
		{pattern: "a?", want: true},
		{pattern: "(a)", want: true},
		{pattern: "[a]", want: true},
		{pattern: "%a", want: true},
		{pattern: "a-b", want: true},
		{pattern: "a\x00*", want: false},
		{pattern: "a*\x00", want: true},
	} {
		if got := patternHasSpecials(test.pattern); got != test.want {
			t.Fatalf(
				"patternHasSpecials(%q) = %v; want %v",
				test.pattern,
				got,
				test.want,
			)
		}
	}
}

// TestPatternEngineSearchDoesNotAllocate holds the engine to the compact
// contract: a match produces subject slices and offsets, never a copy.
func TestPatternEngineSearchDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)

	subject := "the quick brown fox jumps over the lazy dog"
	for _, test := range []struct {
		name    string
		pattern string
	}{
		{name: "literal", pattern: "brown"},
		{name: "class", pattern: "%s(%a+)%s"},
		{name: "greedy", pattern: "t.*g"},
		{name: "lazy", pattern: "t.-g"},
		{name: "set", pattern: "[bcd][a-z]+"},
		{name: "captures", pattern: "(%a+) (%a+) (%a+)"},
		{name: "no match", pattern: "(%d+)%.(%d+)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var state matchState
			allocations := testing.AllocsPerRun(256, func() {
				start, end, found := state.find(subject, test.pattern, 0)
				if found {
					for index := 0; index < state.captureCount(true); index++ {
						state.captureValue(index, start, end)
					}
				}
			})
			if allocations != 0 {
				t.Fatalf("search allocated %v times per run", allocations)
			}
		})
	}
}

func BenchmarkPatternEngine(b *testing.B) {
	for _, benchmark := range []struct {
		name    string
		subject string
		pattern string
	}{
		{
			name:    "plain items",
			subject: "the quick brown fox jumps over the lazy dog",
			pattern: "brown fox",
		},
		{
			name:    "greedy",
			subject: "the quick brown fox jumps over the lazy dog",
			pattern: "t.*g",
		},
		{
			name:    "captures",
			subject: "key=value",
			pattern: "(%w+)=(%w+)",
		},
		{
			name:    "miss",
			subject: "the quick brown fox jumps over the lazy dog",
			pattern: "(%d+)%.(%d+)",
		},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			var state matchState
			b.ReportAllocs()
			for range b.N {
				state.find(
					benchmark.subject,
					benchmark.pattern,
					0,
				)
			}
		})
	}
}

var patternEngineLua51Cases = []patternEngineCase{
	{
		name:    "literal",
		subject: "hello world",
		pattern: "world",
		offset:  0,
		want:    "7 11",
	},
	{
		name:    "literal_absent",
		subject: "hello",
		pattern: "xyz",
		offset:  0,
		want:    "nil",
	},
	{
		name:    "anchor_hit",
		subject: "hello",
		pattern: "^he",
		offset:  0,
		want:    "1 2",
	},
	{
		name:    "anchor_miss",
		subject: "hello",
		pattern: "^el",
		offset:  0,
		want:    "nil",
	},
	{
		name:    "anchor_at_init",
		subject: "baa",
		pattern: "^a",
		offset:  1,
		want:    "2 2",
	},
	{
		name:    "dollar",
		subject: "hello",
		pattern: "lo$",
		offset:  0,
		want:    "4 5",
	},
	{
		name:    "dollar_miss",
		subject: "hello",
		pattern: "ll$",
		offset:  0,
		want:    "nil",
	},
	{
		name:    "dollar_mid_is_literal",
		subject: "a$b",
		pattern: "a$b",
		offset:  0,
		want:    "1 3",
	},
	{
		name:    "anchor_and_dollar",
		subject: "abc",
		pattern: "^abc$",
		offset:  0,
		want:    "1 3",
	},
	{
		name:    "empty_pattern",
		subject: "abc",
		pattern: "",
		offset:  0,
		want:    "1 0",
	},
	{
		name:    "empty_pattern_at_end",
		subject: "abc",
		pattern: "",
		offset:  3,
		want:    "4 3",
	},
	{
		name:    "empty_pattern_past_end",
		subject: "abc",
		pattern: "",
		offset:  3,
		want:    "4 3",
	},
	{
		name:    "empty_subject",
		subject: "",
		pattern: "",
		offset:  0,
		want:    "1 0",
	},
	{
		name:    "empty_subject_pattern",
		subject: "",
		pattern: "a",
		offset:  0,
		want:    "nil",
	},
	{
		name:    "dot",
		subject: "abc",
		pattern: ".",
		offset:  0,
		want:    "1 1",
	},
	{
		name:    "dot_star",
		subject: "abc",
		pattern: ".*",
		offset:  0,
		want:    "1 3",
	},
	{
		name:    "class_alpha",
		subject: "12ab",
		pattern: "%a+",
		offset:  0,
		want:    "3 4",
	},
	{
		name:    "class_digit",
		subject: "ab12",
		pattern: "%d+",
		offset:  0,
		want:    "3 4",
	},
	{
		name:    "class_upper",
		subject: "abAB",
		pattern: "%u+",
		offset:  0,
		want:    "3 4",
	},
	{
		name:    "class_lower",
		subject: "ABab",
		pattern: "%l+",
		offset:  0,
		want:    "3 4",
	},
	{
		name:    "class_space",
		subject: "a \x09 b",
		pattern: "%s+",
		offset:  0,
		want:    "2 4",
	},
	{
		name:    "class_punct",
		subject: "ab,.!x",
		pattern: "%p+",
		offset:  0,
		want:    "3 5",
	},
	{
		name:    "class_word",
		subject: "  ab1_",
		pattern: "%w+",
		offset:  0,
		want:    "3 5",
	},
	{
		name:    "class_hex",
		subject: "zz0aFg",
		pattern: "%x+",
		offset:  0,
		want:    "3 5",
	},
	{
		name:    "class_control",
		subject: "ab\x01\x02c",
		pattern: "%c+",
		offset:  0,
		want:    "3 4",
	},
	{
		name:    "class_nul",
		subject: "a\x00b",
		pattern: "%z",
		offset:  0,
		want:    "2 2",
	},
	{
		name:    "class_negated",
		subject: "abc1",
		pattern: "%A+",
		offset:  0,
		want:    "4 4",
	},
	{
		name:    "class_negated_digit",
		subject: "12ab",
		pattern: "%D+",
		offset:  0,
		want:    "3 4",
	},
	{
		name:    "class_high_bytes_are_not_alpha",
		subject: "\x80\x81a",
		pattern: "%a+",
		offset:  0,
		want:    "3 3",
	},
	{
		name:    "class_high_bytes_negated",
		subject: "\x80\x81a",
		pattern: "%A+",
		offset:  0,
		want:    "1 2",
	},
	{
		name:    "escaped_literal",
		subject: "a.b",
		pattern: "a%.b",
		offset:  0,
		want:    "1 3",
	},
	{
		name:    "escaped_percent",
		subject: "50%",
		pattern: "%%",
		offset:  0,
		want:    "3 3",
	},
	{
		name:    "set",
		subject: "xyzabc",
		pattern: "[abc]+",
		offset:  0,
		want:    "4 6",
	},
	{
		name:    "set_negated",
		subject: "abcxyz",
		pattern: "[^abc]+",
		offset:  0,
		want:    "4 6",
	},
	{
		name:    "set_range",
		subject: "!!abc!!",
		pattern: "[a-c]+",
		offset:  0,
		want:    "3 5",
	},
	{
		name:    "set_range_negated",
		subject: "abcx",
		pattern: "[^a-c]+",
		offset:  0,
		want:    "4 4",
	},
	{
		name:    "set_leading_bracket",
		subject: "a]b",
		pattern: "[]]",
		offset:  0,
		want:    "2 2",
	},
	{
		name:    "set_negated_leading_bracket",
		subject: "]]a",
		pattern: "[^]]",
		offset:  0,
		want:    "3 3",
	},
	{
		name:    "set_trailing_dash",
		subject: "a-b",
		pattern: "[a-]+",
		offset:  0,
		want:    "1 2",
	},
	{
		name:    "set_escaped_class",
		subject: "ab12",
		pattern: "[%d]+",
		offset:  0,
		want:    "3 4",
	},
	{
		name:    "set_escaped_bracket",
		subject: "a]b",
		pattern: "[%]]",
		offset:  0,
		want:    "2 2",
	},
	{
		name:    "set_mixed",
		subject: "__abc12__",
		pattern: "[%a%d]+",
		offset:  0,
		want:    "3 7",
	},
	{
		name:    "set_negated_class",
		subject: "ab12",
		pattern: "[^%d]+",
		offset:  0,
		want:    "1 2",
	},
	{
		name:    "set_caret_literal",
		subject: "a^b",
		pattern: "[%^]",
		offset:  0,
		want:    "2 2",
	},
	{
		name:    "set_dash_range_edge",
		subject: "abc",
		pattern: "[b-b]",
		offset:  0,
		want:    "2 2",
	},
	{
		name:    "set_reversed_range",
		subject: "abc",
		pattern: "[c-a]",
		offset:  0,
		want:    "nil",
	},
	{
		name:    "star_greedy",
		subject: "<<a>>",
		pattern: "<.*>",
		offset:  0,
		want:    "1 5",
	},
	{
		name:    "minus_lazy",
		subject: "<<a>>",
		pattern: "<.->",
		offset:  0,
		want:    "1 4",
	},
	{
		name:    "plus",
		subject: "aaab",
		pattern: "a+",
		offset:  0,
		want:    "1 3",
	},
	{
		name:    "plus_requires_one",
		subject: "b",
		pattern: "a+",
		offset:  0,
		want:    "nil",
	},
	{
		name:    "question",
		subject: "color colour",
		pattern: "colou?r",
		offset:  0,
		want:    "1 5",
	},
	{
		name:    "question_absent",
		subject: "abc",
		pattern: "x?a",
		offset:  0,
		want:    "1 1",
	},
	{
		name:    "star_zero",
		subject: "bbb",
		pattern: "a*b",
		offset:  0,
		want:    "1 1",
	},
	{
		name:    "nested_greedy",
		subject: "aaa",
		pattern: "a*a*a*",
		offset:  0,
		want:    "1 3",
	},
	{
		name:    "lazy_empty",
		subject: "abc",
		pattern: "a-",
		offset:  0,
		want:    "1 0",
	},
	{
		name:    "capture_one",
		subject: "key=value",
		pattern: "(%w+)=",
		offset:  0,
		want:    "1 4 'key'",
	},
	{
		name:    "capture_two",
		subject: "key=value",
		pattern: "(%w+)=(%w+)",
		offset:  0,
		want:    "1 9 'key' 'value'",
	},
	{
		name:    "capture_nested",
		subject: "abc",
		pattern: "((a)(b))",
		offset:  0,
		want:    "1 2 'ab' 'a' 'b'",
	},
	{
		name:    "capture_empty",
		subject: "abc",
		pattern: "()",
		offset:  0,
		want:    "1 0 1",
	},
	{
		name:    "capture_position",
		subject: "hello",
		pattern: "()ll()",
		offset:  0,
		want:    "3 4 3 5",
	},
	{
		name:    "capture_position_and_text",
		subject: "hello",
		pattern: "()(ll)()",
		offset:  0,
		want:    "3 4 3 'll' 5",
	},
	{
		name:    "capture_optional",
		subject: "abc",
		pattern: "(x)?a",
		offset:  0,
		want:    "nil",
	},
	{
		name:    "backreference",
		subject: "abcabc",
		pattern: "(abc)%1",
		offset:  0,
		want:    "1 6 'abc'",
	},
	{
		name:    "backreference_miss",
		subject: "abcabd",
		pattern: "(abc)%1",
		offset:  0,
		want:    "nil",
	},
	{
		name:    "backreference_class",
		subject: "aa bb",
		pattern: "(%a)%1",
		offset:  0,
		want:    "1 2 'a'",
	},
	{
		name:    "backreference_to_position_never_matches",
		subject: "abc",
		pattern: "()a%1",
		offset:  0,
		want:    "nil",
	},
	{
		name:    "balance",
		subject: "(x(y)z)tail",
		pattern: "%b()",
		offset:  0,
		want:    "1 7",
	},
	{
		name:    "balance_unmatched",
		subject: "(x(y)z",
		pattern: "%b()",
		offset:  0,
		want:    "3 5",
	},
	{
		name:    "balance_same_delimiter",
		subject: "'quoted' rest",
		pattern: "%b''",
		offset:  0,
		want:    "1 8",
	},
	{
		name:    "balance_immediate",
		subject: "ab",
		pattern: "%b()",
		offset:  0,
		want:    "nil",
	},
	{
		name:    "frontier_word_start",
		subject: "THE quick",
		pattern: "%f[%a]%a+",
		offset:  0,
		want:    "1 3",
	},
	{
		name:    "frontier_word_start_later",
		subject: "THE quick",
		pattern: "%f[%a]%a+",
		offset:  3,
		want:    "5 9",
	},
	{
		name:    "frontier_at_end",
		subject: "abc",
		pattern: "%f[%z]",
		offset:  0,
		want:    "4 3",
	},
	{
		name:    "frontier_at_start",
		subject: "abc",
		pattern: "%f[%a]",
		offset:  0,
		want:    "1 0",
	},
	{
		name:    "frontier_negated_set",
		subject: "  ab",
		pattern: "%f[^%s]",
		offset:  0,
		want:    "3 2",
	},
	{
		name:    "init_middle",
		subject: "abcabc",
		pattern: "abc",
		offset:  3,
		want:    "4 6",
	},
	{
		name:    "init_negative",
		subject: "abcabc",
		pattern: "abc",
		offset:  3,
		want:    "4 6",
	},
	{
		name:    "init_negative_clamped",
		subject: "abc",
		pattern: "a",
		offset:  0,
		want:    "1 1",
	},
	{
		name:    "init_past_end",
		subject: "abc",
		pattern: "b",
		offset:  3,
		want:    "nil",
	},
	{
		name:    "init_exactly_end",
		subject: "abc",
		pattern: "c",
		offset:  2,
		want:    "3 3",
	},
	{
		name:    "malformed_trailing_escape",
		subject: "abc",
		pattern: "%",
		offset:  0,
		want:    "error 'malformed pattern (ends with '%')'",
	},
	{
		name:    "malformed_trailing_escape_after_item",
		subject: "abc",
		pattern: "a%",
		offset:  0,
		want:    "error 'malformed pattern (ends with '%')'",
	},
	{
		name:    "malformed_missing_bracket",
		subject: "abc",
		pattern: "[",
		offset:  0,
		want:    "error 'malformed pattern (missing ']')'",
	},
	{
		name:    "malformed_missing_bracket_content",
		subject: "abc",
		pattern: "[abc",
		offset:  0,
		want:    "error 'malformed pattern (missing ']')'",
	},
	{
		name:    "malformed_missing_bracket_negated",
		subject: "abc",
		pattern: "[^abc",
		offset:  0,
		want:    "error 'malformed pattern (missing ']')'",
	},
	{
		name:    "malformed_missing_bracket_escape",
		subject: "abc",
		pattern: "[a%]",
		offset:  0,
		want:    "error 'malformed pattern (missing ']')'",
	},
	{
		name:    "malformed_balance_no_args",
		subject: "abc",
		pattern: "%b",
		offset:  0,
		want:    "error 'unbalanced pattern'",
	},
	{
		name:    "malformed_balance_one_arg",
		subject: "abc",
		pattern: "%ba",
		offset:  0,
		want:    "error 'unbalanced pattern'",
	},
	{
		name:    "malformed_frontier_no_set",
		subject: "abc",
		pattern: "%f",
		offset:  0,
		want:    "error 'missing '[' after '%f' in pattern'",
	},
	{
		name:    "malformed_frontier_wrong",
		subject: "abc",
		pattern: "%fa",
		offset:  0,
		want:    "error 'missing '[' after '%f' in pattern'",
	},
	{
		name:    "malformed_unfinished_capture",
		subject: "abc",
		pattern: "(",
		offset:  0,
		want:    "error 'unfinished capture'",
	},
	{
		name:    "malformed_unfinished_capture_nested",
		subject: "abc",
		pattern: "((a)",
		offset:  0,
		want:    "error 'unfinished capture'",
	},
	{
		name:    "malformed_capture_index",
		subject: "abc",
		pattern: "%1",
		offset:  0,
		want:    "error 'invalid capture index'",
	},
	{
		name:    "malformed_capture_index_high",
		subject: "abc",
		pattern: "(a)%2",
		offset:  0,
		want:    "error 'invalid capture index'",
	},
	{
		name:    "malformed_capture_index_zero",
		subject: "abc",
		pattern: "%0",
		offset:  0,
		want:    "error 'invalid capture index'",
	},
	{
		name:    "malformed_capture_index_inside",
		subject: "abc",
		pattern: "(%1)",
		offset:  0,
		want:    "error 'invalid capture index'",
	},
	{
		name:    "nul_in_subject",
		subject: "a\x00b",
		pattern: "a%zb",
		offset:  0,
		want:    "1 3",
	},
	{
		name:    "nul_in_pattern_ends_it",
		subject: "abc",
		pattern: "a\x00z",
		offset:  0,
		want:    "1 1",
	},
	{
		name:    "nul_only_pattern",
		subject: "abc",
		pattern: "\x00",
		offset:  0,
		want:    "1 0",
	},
	{
		name:    "high_byte_literal",
		subject: "a\x80b",
		pattern: "\x80",
		offset:  0,
		want:    "2 2",
	},
	{
		name:    "high_byte_set",
		subject: "a\x80b",
		pattern: "[\x80\x81]",
		offset:  0,
		want:    "2 2",
	},
	{
		name:    "high_byte_dot",
		subject: "\x80\x81",
		pattern: ".+",
		offset:  0,
		want:    "1 2",
	},
}
