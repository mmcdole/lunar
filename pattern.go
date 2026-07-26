package lua

import "strings"

// Lua 5.1 pattern matching.
//
// This is a direct translation of PUC Lua 5.1's matcher, not a regular
// expression engine: Lua patterns have no alternation, their quantifiers apply
// only to single items, and backtracking order is observable through captures
// and through the number of times a gsub replacement runs. The translation
// therefore preserves PUC's control flow, including which constructs recurse
// and which continue iteratively at the same level.
//
// Two representation differences from C are handled explicitly rather than
// incidentally.
//
// PUC reads both the pattern and the subject as NUL-terminated C strings while
// Lua strings may contain embedded NUL. Reading one byte past the end here
// yields 0, so an embedded NUL behaves exactly as PUC's terminator does: it
// ends the pattern, it is what '%z' matches in the subject, and it is the
// character '%b' and '%f' see just past the subject. No separate special case
// is needed for either.
//
// PUC's matcher recurses on the C stack with no depth limit, so a deeply
// nested pattern crashes the process. A Go stack overflow is fatal and
// unrecoverable too, and this runtime is embedded in host programs, so genuine
// recursion is bounded and reports the error Lua 5.2 introduced for it,
// "pattern too complex". Recursion depth follows the number of pattern items
// rather than the subject length, so the bound is about pathological patterns
// and never about large inputs.
//
// See THIRD_PARTY_NOTICES.md for the Lua reference implementation's license.
const (
	// maxPatternCaptures is LUA_MAXCAPTURES.
	maxPatternCaptures = 32
	// maxPatternDepth bounds the recursion this matcher genuinely performs.
	//
	// The number is a stack budget, not a semantic limit. Each level costs
	// roughly 250 bytes of goroutine stack here, so this caps one match at
	// about 2 MB. For comparison, Lua 5.2 through 5.4 reject the same
	// recursion at 200 levels, while PUC 5.1.5 on an 8 MB C stack survives
	// about 50,000 and crashes before 100,000. Real patterns nest fewer than
	// a hundred levels; raising this only trades host memory for tolerance of
	// machine-generated patterns.
	maxPatternDepth = 8192
	// patternEscape is L_ESC.
	patternEscape = '%'
	// patternSpecials is SPECIALS, the set that disqualifies a plain search.
	patternSpecials = "^$*+?.([%-"
	// patternContextFailed is distinct from both the disabled zero budget and
	// every live countdown value.
	patternContextFailed = ^uint16(0)
)

// Capture lengths reuse PUC's two negative markers.
const (
	capturePosition   = -2
	captureUnfinished = -1
)

// match results are a subject offset, or one of these markers. A failure is
// permanent and abandons the search immediately, as PUC's error longjmp does.
const (
	matchFailed  = -2
	matchNoMatch = -1
)

type patternCapture struct {
	init   int
	length int
}

// matchState is one pattern match over one subject. It holds no Lua state, so
// the caller decides how a failure becomes a Lua error.
type matchState struct {
	source         string
	pattern        string
	level          int
	depth          int
	failure        string
	thread         *Thread
	contextBudget  uint16
	contextFailure *Error
	captures       [maxPatternCaptures]patternCapture
}

// reset prepares state for a fresh search. The capture array is bounded by
// level and needs no clearing.
func (state *matchState) reset(source, pattern string) {
	state.source = source
	state.pattern = pattern
	state.level = 0
	state.depth = 0
	state.failure = ""
	state.thread = nil
	state.contextBudget = 0
	state.contextFailure = nil
}

// bindContext makes matcher work cooperative with a context-aware call. Raw
// calls leave thread nil, so their hot path pays only one predictable branch.
func (state *matchState) bindContext(thread *Thread) {
	if thread == nil || thread.state.execution.done == nil {
		return
	}
	state.thread = thread
	state.contextBudget = contextPollInterval
}

// restart prepares the next start position within the same search.
func (state *matchState) restart() {
	state.level = 0
	state.depth = 0
}

// consumeContextWork samples cancellation while the matcher owns control
// instead of the bytecode executor.
func (state *matchState) consumeContextWork() bool {
	budget := state.contextBudget
	if budget == patternContextFailed {
		return false
	}
	budget--
	state.contextBudget = budget
	if budget != 0 {
		return true
	}
	return state.pollContext()
}

func (state *matchState) pollContext() bool {
	state.contextBudget = contextPollInterval
	if failure := pollExecutionContext(state.thread); failure != nil {
		state.contextFailure = failure
		state.contextBudget = patternContextFailed
		return false
	}
	return true
}

func (state *matchState) fail(message string) int {
	if state.failure == "" {
		state.failure = message
	}
	return matchFailed
}

// patternAt and sourceAt return 0 past either end, reproducing the C string
// terminator PUC relies on.
func (state *matchState) patternAt(index int) byte {
	if index < 0 || index >= len(state.pattern) {
		return 0
	}
	return state.pattern[index]
}

func (state *matchState) sourceAt(index int) byte {
	if index < 0 || index >= len(state.source) {
		return 0
	}
	return state.source[index]
}

// match runs the matcher from subject offset s and pattern offset p. It
// returns the subject offset just past the match, matchNoMatch, or
// matchFailed with state.failure set.
func (state *matchState) match(s, p int) int {
	if state.contextBudget != 0 && !state.consumeContextWork() {
		return matchFailed
	}
	state.depth++
	if state.depth > maxPatternDepth {
		state.depth--
		return state.fail("pattern too complex")
	}
	result := state.matchFrom(s, p)
	state.depth--
	return result
}

// matchFrom is PUC's match with its `goto init` tail calls written as loop
// iterations. Only the constructs PUC genuinely recurses through call match.
func (state *matchState) matchFrom(s, p int) int {
	for {
		switch state.patternAt(p) {
		case '(':
			if state.patternAt(p+1) == ')' {
				return state.startCapture(s, p+2, capturePosition)
			}
			return state.startCapture(s, p+1, captureUnfinished)

		case ')':
			return state.endCapture(s, p+1)

		case 0:
			return s

		case '$':
			if state.patternAt(p+1) == 0 {
				if s == len(state.source) {
					return s
				}
				return matchNoMatch
			}

		case patternEscape:
			switch next := state.patternAt(p + 1); {
			case next == 'b':
				end := state.matchBalance(s, p+2)
				if end < 0 {
					return end
				}
				s, p = end, p+4
				continue
			case next == 'f':
				p += 2
				if state.patternAt(p) != '[' {
					return state.fail(
						"missing '[' after '%f' in pattern",
					)
				}
				classEnd := state.classEnd(p)
				if classEnd < 0 {
					return classEnd
				}
				if state.matchBracketClass(
					state.sourceAt(s-1),
					p,
					classEnd-1,
				) || !state.matchBracketClass(
					state.sourceAt(s),
					p,
					classEnd-1,
				) {
					return matchNoMatch
				}
				p = classEnd
				continue
			case isPatternDigit(next):
				end := state.matchCapture(s, next)
				if end < 0 {
					return end
				}
				s, p = end, p+2
				continue
			}
		}

		// Any remaining pattern byte is a single item, optionally followed by
		// a quantifier.
		classEnd := state.classEnd(p)
		if classEnd < 0 {
			return classEnd
		}
		matched := s < len(state.source) &&
			state.singleMatch(state.source[s], p, classEnd)

		switch state.patternAt(classEnd) {
		case '?':
			if matched {
				if result := state.match(s+1, classEnd+1); result != matchNoMatch {
					return result
				}
			}
			p = classEnd + 1
		case '*':
			return state.maxExpand(s, p, classEnd)
		case '+':
			if !matched {
				return matchNoMatch
			}
			return state.maxExpand(s+1, p, classEnd)
		case '-':
			return state.minExpand(s, p, classEnd)
		default:
			if !matched {
				return matchNoMatch
			}
			s++
			p = classEnd
		}
	}
}

// maxExpand consumes the longest run the item allows and gives repetitions
// back one at a time. Lua's greedy quantifiers are defined by this order.
func (state *matchState) maxExpand(s, p, classEnd int) int {
	count := 0
	if state.contextBudget == 0 {
		for s+count < len(state.source) {
			if !state.singleMatch(state.source[s+count], p, classEnd) {
				break
			}
			count++
		}
	} else {
		for s+count < len(state.source) {
			if !state.singleMatch(state.source[s+count], p, classEnd) {
				break
			}
			count++
			if !state.consumeContextWork() {
				return matchFailed
			}
		}
	}
	for count >= 0 {
		result := state.match(s+count, classEnd+1)
		if result != matchNoMatch {
			return result
		}
		count--
	}
	return matchNoMatch
}

// minExpand takes repetitions one at a time, which is Lua's lazy quantifier.
func (state *matchState) minExpand(s, p, classEnd int) int {
	for {
		result := state.match(s, classEnd+1)
		if result != matchNoMatch {
			return result
		}
		if s < len(state.source) &&
			state.singleMatch(state.source[s], p, classEnd) {
			s++
			continue
		}
		return matchNoMatch
	}
}

func (state *matchState) startCapture(s, p, what int) int {
	level := state.level
	if level >= maxPatternCaptures {
		return state.fail("too many captures")
	}
	state.captures[level].init = s
	state.captures[level].length = what
	state.level = level + 1
	result := state.match(s, p)
	if result == matchNoMatch {
		state.level--
	}
	return result
}

func (state *matchState) endCapture(s, p int) int {
	level := state.captureToClose()
	if level < 0 {
		return matchFailed
	}
	state.captures[level].length = s - state.captures[level].init
	result := state.match(s, p)
	if result == matchNoMatch {
		state.captures[level].length = captureUnfinished
	}
	return result
}

func (state *matchState) captureToClose() int {
	for level := state.level - 1; level >= 0; level-- {
		if state.captures[level].length == captureUnfinished {
			return level
		}
	}
	return state.fail("invalid pattern capture")
}

// matchCapture matches a %1..%9 back reference against the captured text.
func (state *matchState) matchCapture(s int, digit byte) int {
	level := state.checkCapture(digit)
	if level < 0 {
		return matchFailed
	}
	length := state.captures[level].length
	if length < 0 {
		// A position capture holds no text. PUC compares its marker as an
		// unsigned length, which no remaining subject can satisfy, so the
		// back reference simply fails to match.
		return matchNoMatch
	}
	init := state.captures[level].init
	if len(state.source)-s >= length &&
		state.source[init:init+length] == state.source[s:s+length] {
		return s + length
	}
	return matchNoMatch
}

func (state *matchState) checkCapture(digit byte) int {
	level := int(digit) - '1'
	if level < 0 ||
		level >= state.level ||
		state.captures[level].length == captureUnfinished {
		return state.fail("invalid capture index")
	}
	return level
}

// matchBalance implements %bxy: match from an opening x to its matching y.
func (state *matchState) matchBalance(s, p int) int {
	open := state.patternAt(p)
	closing := state.patternAt(p + 1)
	if open == 0 || closing == 0 {
		return state.fail("unbalanced pattern")
	}
	if state.sourceAt(s) != open {
		return matchNoMatch
	}
	depth := 1
	if state.contextBudget != 0 {
		for s++; s < len(state.source); s++ {
			if state.source[s] == closing {
				depth--
				if depth == 0 {
					return s + 1
				}
			} else if state.source[s] == open {
				depth++
			}
			if !state.consumeContextWork() {
				return matchFailed
			}
		}
		return matchNoMatch
	}
	for s++; s < len(state.source); s++ {
		if state.source[s] == closing {
			depth--
			if depth == 0 {
				return s + 1
			}
		} else if state.source[s] == open {
			depth++
		}
	}
	return matchNoMatch
}

// classEnd returns the pattern offset just past the single item starting at p.
func (state *matchState) classEnd(p int) int {
	character := state.patternAt(p)
	p++
	switch character {
	case patternEscape:
		if state.patternAt(p) == 0 {
			return state.fail("malformed pattern (ends with '%')")
		}
		return p + 1
	case '[':
		if state.patternAt(p) == '^' {
			p++
		}
		// A ']' immediately after '[' or '[^' is a literal, so the set is
		// scanned at least once before the closing bracket is accepted.
		for {
			if state.patternAt(p) == 0 {
				return state.fail("malformed pattern (missing ']')")
			}
			current := state.patternAt(p)
			p++
			if current == patternEscape && state.patternAt(p) != 0 {
				p++
			}
			if state.patternAt(p) == ']' {
				return p + 1
			}
		}
	default:
		return p
	}
}

func (state *matchState) singleMatch(c byte, p, classEnd int) bool {
	switch state.patternAt(p) {
	case '.':
		return true
	case patternEscape:
		return matchPatternClass(c, state.patternAt(p+1))
	case '[':
		return state.matchBracketClass(c, p, classEnd-1)
	default:
		return state.patternAt(p) == c
	}
}

func (state *matchState) matchBracketClass(c byte, p, classEnd int) bool {
	sign := true
	if state.patternAt(p+1) == '^' {
		sign = false
		p++
	}
	for p++; p < classEnd; p++ {
		switch {
		case state.patternAt(p) == patternEscape:
			p++
			if matchPatternClass(c, state.patternAt(p)) {
				return sign
			}
		case state.patternAt(p+1) == '-' && p+2 < classEnd:
			p += 2
			if state.patternAt(p-2) <= c && c <= state.patternAt(p) {
				return sign
			}
		case state.patternAt(p) == c:
			return sign
		}
	}
	return !sign
}

// matchPatternClass applies a %a-style class. The classifications are C's in
// the "C" locale, which is byte-oriented and ASCII-only; Go's Unicode-aware
// helpers would classify bytes above 0x7f differently and are deliberately not
// used. An uppercase class letter complements its lowercase form, and any
// other escaped byte matches itself.
func matchPatternClass(c byte, class byte) bool {
	var result bool
	switch class | 0x20 {
	case 'a':
		result = isPatternAlpha(c)
	case 'c':
		result = isPatternControl(c)
	case 'd':
		result = isPatternDigit(c)
	case 'l':
		result = isPatternLower(c)
	case 'p':
		result = isPatternPunct(c)
	case 's':
		result = isPatternSpace(c)
	case 'u':
		result = isPatternUpper(c)
	case 'w':
		result = isPatternAlpha(c) || isPatternDigit(c)
	case 'x':
		result = isPatternHexDigit(c)
	case 'z':
		result = c == 0
	default:
		return class == c
	}
	if isPatternLower(class) {
		return result
	}
	return !result
}

func isPatternAlpha(c byte) bool {
	lowered := c | 0x20
	return lowered >= 'a' && lowered <= 'z'
}

func isPatternDigit(c byte) bool { return c >= '0' && c <= '9' }

func isPatternLower(c byte) bool { return c >= 'a' && c <= 'z' }

func isPatternUpper(c byte) bool { return c >= 'A' && c <= 'Z' }

func isPatternControl(c byte) bool { return c < 0x20 || c == 0x7f }

func isPatternPunct(c byte) bool {
	return c > 0x20 && c < 0x7f &&
		!isPatternAlpha(c) &&
		!isPatternDigit(c)
}

func isPatternSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

func isPatternHexDigit(c byte) bool {
	if isPatternDigit(c) {
		return true
	}
	lowered := c | 0x20
	return lowered >= 'a' && lowered <= 'f'
}

// failed reports whether the pattern itself was rejected, which the caller
// must distinguish from an ordinary failure to match.
func (state *matchState) failed() bool {
	return state.failure != "" || state.contextFailure != nil
}

// searchFrom finds the first match at or after subject offset init, returning
// the span source[start:end]. An anchored search only tries init. The subject
// end is a legal start position, so an empty pattern matches there.
//
// This is the scan string.find, string.match, and string.gmatch share; gsub
// advances differently and drives match directly.
func (state *matchState) searchFrom(
	init int,
	anchored bool,
) (start, end int, found bool) {
	for s := init; ; s++ {
		state.restart()
		result := state.match(s, 0)
		if result >= 0 {
			return s, result, true
		}
		if result == matchFailed || anchored || s >= len(state.source) {
			return 0, 0, false
		}
	}
}

// find is the search string.find and string.match perform: strip the anchor,
// then scan from init. Captures are read from the state afterwards.
func (state *matchState) find(
	source string,
	pattern string,
	init int,
) (start, end int, found bool) {
	stripped, anchored := patternAnchor(pattern)
	state.reset(source, stripped)
	return state.searchFrom(init, anchored)
}

// patternAnchor strips a leading '^' and reports whether the pattern is
// anchored.
//
// string.find, string.match, and string.gsub apply it. string.gmatch
// deliberately does not, so '^' is an ordinary character there, exactly as in
// PUC Lua 5.1.
func patternAnchor(pattern string) (string, bool) {
	if len(pattern) != 0 && pattern[0] == '^' {
		return pattern[1:], true
	}
	return pattern, false
}

// patternCaptureValue is one capture result. A position capture carries a
// one-based subject index instead of text.
type patternCaptureValue struct {
	text       string
	position   int
	isPosition bool
}

// captureCount returns how many values a successful match yields. A pattern
// with no explicit capture yields the whole match, except where PUC passes a
// null subject to suppress it.
func (state *matchState) captureCount(wholeMatch bool) int {
	if state.level == 0 && wholeMatch {
		return 1
	}
	return state.level
}

// captureValue returns capture index for a match spanning source[s:e].
func (state *matchState) captureValue(
	index int,
	s int,
	e int,
) (patternCaptureValue, bool) {
	if index >= state.level {
		if index != 0 {
			state.fail("invalid capture index")
			return patternCaptureValue{}, false
		}
		return patternCaptureValue{text: state.source[s:e]}, true
	}
	capture := &state.captures[index]
	switch capture.length {
	case captureUnfinished:
		state.fail("unfinished capture")
		return patternCaptureValue{}, false
	case capturePosition:
		return patternCaptureValue{
			position:   capture.init + 1,
			isPosition: true,
		}, true
	}
	return patternCaptureValue{
		text: state.source[capture.init : capture.init+capture.length],
	}, true
}

// patternHasSpecials reports whether pattern contains a magic byte, which is
// how string.find decides that an unanchored search may use a plain scan.
//
// It reproduces strpbrk over PUC's NUL-terminated pattern, so an embedded NUL
// hides everything after it. The plain search that follows still uses the
// pattern's full byte length, matching PUC exactly.
func patternHasSpecials(pattern string) bool {
	for index := 0; index < len(pattern); index++ {
		character := pattern[index]
		if character == 0 {
			return false
		}
		if strings.IndexByte(patternSpecials, character) >= 0 {
			return true
		}
	}
	return false
}
