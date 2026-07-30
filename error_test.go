package lua

import (
	"errors"
	"strings"
	"testing"
)

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
	if got := sourceID(""); got != `[string ""]` {
		t.Fatalf("empty source = %q", got)
	}
	fortyThree := strings.Repeat("x", 43)
	if got := sourceID(fortyThree); got != `[string "`+fortyThree+`"]` {
		t.Fatalf("43-byte string source = %q", got)
	}
	if got := sourceID(fortyThree + "x"); got !=
		`[string "`+fortyThree+`..."]` {
		t.Fatalf("44-byte string source = %q", got)
	}
	if got := sourceID(fortyThree + "\nignored"); got !=
		`[string "`+fortyThree+`..."]` {
		t.Fatalf("newline string source = %q", got)
	}
	if got := sourceID("@" + strings.Repeat("x", 100)); len(got) != 55 ||
		!strings.HasPrefix(got, "...") {
		t.Fatalf("long file source = %q", got)
	}
}

func TestRuntimeTypeErrorsNameOperandsLikeLua51(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "global index",
			source: `return missing.x`,
			want:   `attempt to index global 'missing' (a nil value)`,
		},
		{
			name:   "local index",
			source: `local item; return item.x`,
			want:   `attempt to index local 'item' (a nil value)`,
		},
		{
			name:   "parameter index",
			source: `local function f(item) return item.x end; return f(nil)`,
			want:   `attempt to index local 'item' (a nil value)`,
		},
		{
			name:   "upvalue index",
			source: `local item; local function f() return item.x end; return f()`,
			want:   `attempt to index upvalue 'item' (a nil value)`,
		},
		{
			name:   "field index",
			source: `local root={}; return root.item.x`,
			want:   `attempt to index field 'item' (a nil value)`,
		},
		{
			name:   "temporary index",
			source: `return (function() return nil end)().x`,
			want:   `attempt to index a nil value`,
		},
		{
			name:   "local index write",
			source: `local item; item.x=1`,
			want:   `attempt to index local 'item' (a nil value)`,
		},
		{
			name:   "global index write",
			source: `missing.x=1`,
			want:   `attempt to index global 'missing' (a nil value)`,
		},
		{
			name:   "upvalue index write",
			source: `local item; local function f() item.x=1 end; f()`,
			want:   `attempt to index upvalue 'item' (a nil value)`,
		},
		{
			name:   "field index write",
			source: `local root={}; root.item.x=1`,
			want:   `attempt to index field 'item' (a nil value)`,
		},
		{
			name:   "global call",
			source: `return missing()`,
			want:   `attempt to call global 'missing' (a nil value)`,
		},
		{
			name:   "local initializer sees global",
			source: `local callback=callback()`,
			want:   `attempt to call global 'callback' (a nil value)`,
		},
		{
			name:   "local call",
			source: `local callback; return callback()`,
			want:   `attempt to call local 'callback' (a nil value)`,
		},
		{
			name:   "shadowed local call",
			source: `local outer=1; do local inner; return inner() end`,
			want:   `attempt to call local 'inner' (a nil value)`,
		},
		{
			name:   "reused local register",
			source: `do local expired=1 end; local active; return active()`,
			want:   `attempt to call local 'active' (a nil value)`,
		},
		{
			name:   "upvalue call",
			source: `local callback; local function f() return callback() end; return f()`,
			want:   `attempt to call upvalue 'callback' (a nil value)`,
		},
		{
			name:   "field call",
			source: `local root={}; return root.callback()`,
			want:   `attempt to call field 'callback' (a nil value)`,
		},
		{
			name:   "method call",
			source: `local root={}; return root:callback()`,
			want:   `attempt to call method 'callback' (a nil value)`,
		},
		{
			name:   "invalid method receiver",
			source: `local root; return root:callback()`,
			want:   `attempt to index local 'root' (a nil value)`,
		},
		{
			name:   "dynamic field call",
			source: `local root={x=3}; local key="x"; return root[key]()`,
			want:   `attempt to call field '?' (a number value)`,
		},
		{
			name:   "temporary call",
			source: `return (function() return nil end)()()`,
			want:   `attempt to call a nil value`,
		},
		{
			name:   "global arithmetic",
			source: `return missing+1`,
			want:   `attempt to perform arithmetic on global 'missing' (a nil value)`,
		},
		{
			name:   "right arithmetic operand",
			source: `local item={}; return 1+item`,
			want:   `attempt to perform arithmetic on local 'item' (a table value)`,
		},
		{
			name:   "upvalue arithmetic",
			source: `local count; local function f() return count+1 end; return f()`,
			want:   `attempt to perform arithmetic on upvalue 'count' (a nil value)`,
		},
		{
			name:   "field arithmetic",
			source: `local root={}; return root.count+1`,
			want:   `attempt to perform arithmetic on field 'count' (a nil value)`,
		},
		{
			name:   "constant arithmetic",
			source: `return "bad"+1`,
			want:   `attempt to perform arithmetic on a string value`,
		},
		{
			name:   "unary arithmetic",
			source: `local item; return -item`,
			want:   `attempt to perform arithmetic on local 'item' (a nil value)`,
		},
		{
			name:   "field concatenation",
			source: `local root={text={}}; return root.text..""`,
			want:   `attempt to concatenate field 'text' (a table value)`,
		},
		{
			name:   "right concatenation operand",
			source: `local text; return ""..text`,
			want:   `attempt to concatenate local 'text' (a nil value)`,
		},
		{
			name:   "temporary concatenation",
			source: `return (function() return nil end)()..""`,
			want:   `attempt to concatenate a nil value`,
		},
		{
			name:   "global length",
			source: `return #missing`,
			want:   `attempt to get length of global 'missing' (a nil value)`,
		},
		{
			name:   "local length",
			source: `local text; return #text`,
			want:   `attempt to get length of local 'text' (a nil value)`,
		},
		{
			name:   "upvalue length",
			source: `local text; local function f() return #text end; return f()`,
			want:   `attempt to get length of upvalue 'text' (a nil value)`,
		},
		{
			name:   "field length",
			source: `local root={}; return #root.text`,
			want:   `attempt to get length of field 'text' (a nil value)`,
		},
		{
			name:   "temporary length",
			source: `return #(function() return nil end)()`,
			want:   `attempt to get length of a nil value`,
		},
		{
			name:   "control-flow call",
			source: `local callback; return (callback or callback)()`,
			want:   `attempt to call a nil value`,
		},
		{
			name:   "global control-flow call",
			source: `return (missing or missing)()`,
			want:   `attempt to call a nil value`,
		},
		{
			name:   "global control-flow arithmetic",
			source: `return (missing or missing)+1`,
			want:   `attempt to perform arithmetic on a nil value`,
		},
		{
			name:   "upvalue control-flow length",
			source: `local missing; local function f() return #(missing or missing) end; return f()`,
			want:   `attempt to get length of a nil value`,
		},
		{
			name:   "generic iterator",
			source: `for value in 1 do end`,
			want:   `attempt to call a number value`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			function, err := state.LoadString("@operand.lua", test.source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = state.Call(function.Value())
			var luaErr *Error
			if !errors.As(err, &luaErr) {
				t.Fatalf("error = %T %v; want *Error", err, err)
			}
			want := "operand.lua:1: " + test.want
			if got := luaErr.Error(); got != want {
				t.Fatalf("error = %q; want %q", got, want)
			}
			if value, ok := luaErr.Value().AsString(); !ok || value != want {
				t.Fatalf("error value = %q, %v; want %q", value, ok, want)
			}
		})
	}
}

func TestRuntimeTypeErrorsDoNotNameDerivedMetamethodValues(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	operand, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__add", Number(2)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(operand.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetRawGlobal("operand", operand.Value()); err != nil {
		t.Fatal(err)
	}

	function, err := state.LoadString(
		"@operand.lua",
		`return operand+1`,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Call(function.Value())
	var luaErr *Error
	if !errors.As(err, &luaErr) {
		t.Fatalf("error = %T %v; want *Error", err, err)
	}
	const want = "operand.lua:1: attempt to call a number value"
	if got := luaErr.Error(); got != want {
		t.Fatalf("error = %q; want %q", got, want)
	}

	chained, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	chainMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := chainMetatable.RawSetString("__index", Number(2)); err != nil {
		t.Fatal(err)
	}
	if err := chainMetatable.RawSetString("__newindex", Number(2)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(chained.Value(), chainMetatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetRawGlobal("chained", chained.Value()); err != nil {
		t.Fatal(err)
	}
	function, err = state.LoadString(
		"@operand.lua",
		`return chained.missing`,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Call(function.Value())
	if !errors.As(err, &luaErr) {
		t.Fatalf("chain error = %T %v; want *Error", err, err)
	}
	const chainWant = "operand.lua:1: attempt to index a number value"
	if got := luaErr.Error(); got != chainWant {
		t.Fatalf("chain error = %q; want %q", got, chainWant)
	}

	function, err = state.LoadString(
		"@operand.lua",
		`chained.missing=1`,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Call(function.Value())
	if !errors.As(err, &luaErr) {
		t.Fatalf("newindex chain error = %T %v; want *Error", err, err)
	}
	if got := luaErr.Error(); got != chainWant {
		t.Fatalf("newindex chain error = %q; want %q", got, chainWant)
	}
}

func TestOperandDescriptionFollowsBytecodeStructure(t *testing.T) {
	kept := prototypeStringSlot(newInternedText("kept"))
	skipped := prototypeStringSlot(newInternedText("skipped"))
	child := &Prototype{upvalues: 1}

	tests := []struct {
		name      string
		prototype *Prototype
		pc        int
		register  int
		category  string
		value     string
		found     bool
	}{
		{
			name: "descending move",
			prototype: &Prototype{
				code: []instruction{
					makeABx(opGetGlobal, 0, 0),
					makeABC(opMove, 1, 0, 0),
					makeABC(opCall, 1, 1, 1),
				},
				constants: []slot{kept},
				registers: 2,
			},
			pc:       2,
			register: 1,
			category: "global",
			value:    "kept",
			found:    true,
		},
		{
			name: "ascending move remains unnamed",
			prototype: &Prototype{
				code: []instruction{
					makeABx(opGetGlobal, 1, 0),
					makeABC(opMove, 0, 1, 0),
					makeABC(opCall, 0, 1, 1),
				},
				constants: []slot{kept},
				registers: 2,
			},
			pc:       2,
			register: 0,
		},
		{
			name: "forward jump skips producer",
			prototype: &Prototype{
				code: []instruction{
					makeABx(opGetGlobal, 0, 0),
					makeAsBx(opJump, 0, 1),
					makeABx(opGetGlobal, 0, 1),
					makeABC(opCall, 0, 1, 1),
				},
				constants: []slot{kept, skipped},
				registers: 1,
			},
			pc:       3,
			register: 0,
			category: "global",
			value:    "kept",
			found:    true,
		},
		{
			name: "closure binding is not executable",
			prototype: &Prototype{
				code: []instruction{
					makeABx(opGetGlobal, 0, 0),
					makeABx(opClosure, 1, 0),
					makeABC(opMove, 0, 0, 0),
					makeABC(opCall, 0, 1, 1),
				},
				constants: []slot{kept},
				children:  []*Prototype{child},
				registers: 2,
			},
			pc:       3,
			register: 0,
			category: "global",
			value:    "kept",
			found:    true,
		},
		{
			name: "for preparation skips loop body",
			prototype: &Prototype{
				code: []instruction{
					makeABx(opGetGlobal, 0, 0),
					makeAsBx(opForPrep, 1, 1),
					makeABx(opGetGlobal, 0, 1),
					makeABC(opCall, 0, 1, 1),
				},
				constants: []slot{kept, skipped},
				registers: 5,
			},
			pc:       3,
			register: 0,
			category: "global",
			value:    "kept",
			found:    true,
		},
		{
			name: "setlist extension is not executable",
			prototype: &Prototype{
				code: []instruction{
					makeABx(opGetGlobal, 0, 0),
					makeABC(opSetList, 1, 0, 0),
					makeABx(opGetGlobal, 0, 1),
					makeABC(opCall, 0, 1, 1),
				},
				constants: []slot{kept, skipped},
				registers: 2,
			},
			pc:       3,
			register: 0,
			category: "global",
			value:    "kept",
			found:    true,
		},
		{
			name: "partial upvalue debug",
			prototype: &Prototype{
				code: []instruction{
					makeABC(opGetUpvalue, 0, 0, 0),
					makeABC(opCall, 0, 1, 1),
				},
				registers: 1,
				upvalues:  1,
			},
			pc:       1,
			register: 0,
			category: "upvalue",
			value:    "?",
			found:    true,
		},
		{
			name: "active local takes precedence",
			prototype: &Prototype{
				code: []instruction{
					makeABx(opGetGlobal, 0, 0),
					makeABC(opCall, 0, 1, 1),
				},
				constants: []slot{kept},
				debug: &prototypeDebug{
					locals: []localInfo{{
						name:    newInternedText("localValue"),
						startPC: 0,
						endPC:   2,
					}},
				},
				registers: 1,
			},
			pc:       1,
			register: 0,
			category: "local",
			value:    "localValue",
			found:    true,
		},
		{
			name: "dynamic field",
			prototype: &Prototype{
				code: []instruction{
					makeABC(opGetTable, 0, 1, 0),
					makeABC(opCall, 0, 1, 1),
				},
				registers: 2,
			},
			pc:       1,
			register: 0,
			category: "field",
			value:    "?",
			found:    true,
		},
		{
			name: "field name follows C string diagnostics",
			prototype: &Prototype{
				code: []instruction{
					makeABC(
						opGetTable,
						0,
						1,
						registerOrConstant(0, true),
					),
					makeABC(opCall, 0, 1, 1),
				},
				constants: []slot{
					prototypeStringSlot(newInternedText("field\x00suffix")),
				},
				registers: 2,
			},
			pc:       1,
			register: 0,
			category: "field",
			value:    "field",
			found:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			category, value, found := test.prototype.describeOperand(
				test.pc,
				test.register,
			)
			if found != test.found ||
				category != test.category ||
				value != test.value {
				t.Fatalf(
					"description = (%q, %q, %v); want (%q, %q, %v)",
					category,
					value,
					found,
					test.category,
					test.value,
					test.found,
				)
			}
		})
	}
}

func TestArbitraryUserDataErrorPublishesAtGoBoundary(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()
	if entries, keys, _ := hostDirectoryKindCounts(
		&state.runtime.hosts,
		UserDataKind,
	); entries != 0 || keys != 0 {
		t.Fatalf(
			"opening libraries published userdata: entries=%d keys=%d",
			entries,
			keys,
		)
	}

	chunk := mustLoadString(t, state, "=userdata-error", `
local file=assert(io.tmpfile())
error(file,0)
`)
	_, err := state.Call(chunk.Value())
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("userdata error = %T %v; want *Error", err, err)
	}
	first, ok := failure.Value().AsUserData()
	if !ok {
		t.Fatalf("userdata error Value = %v", failure.Value())
	}
	second, ok := failure.Value().AsUserData()
	if !ok || second != first {
		t.Fatalf(
			"repeated error Value = (%p, %v); want (%p, true)",
			second,
			ok,
			first,
		)
	}
	if entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		UserDataKind,
	); entries != 1 || keys != 1 || stale != 0 {
		t.Fatalf(
			"escaped error directory = entries:%d keys:%d stale:%d; want 1/1/0",
			entries,
			keys,
			stale,
		)
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	afterClose, ok := failure.Value().AsUserData()
	if !ok || afterClose != first {
		t.Fatalf(
			"post-close error Value = (%p, %v); want (%p, true)",
			afterClose,
			ok,
			first,
		)
	}
}

func TestCoroutineUserDataErrorPublishesAtGoBoundary(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()
	chunk := mustLoadString(t, state, "=coroutine-userdata-error", `
error(io.tmpfile(),0)
`)
	thread, err := state.NewThread(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}

	_, status, err := thread.Resume()
	var failure *Error
	if !errors.As(err, &failure) || status != ThreadDead {
		t.Fatalf(
			"coroutine userdata error = (status=%v, %T %v); want dead *Error",
			status,
			err,
			err,
		)
	}
	if _, ok := failure.Value().AsUserData(); !ok {
		t.Fatalf("coroutine error Value = %v", failure.Value())
	}
	if entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		UserDataKind,
	); entries != 1 || keys != 1 || stale != 0 {
		t.Fatalf(
			"coroutine error directory = entries:%d keys:%d stale:%d; want 1/1/0",
			entries,
			keys,
			stale,
		)
	}
}

func TestNativeFrameUserDataErrorsPublishBeforeReturningToGo(
	t *testing.T,
) {
	for _, boundary := range []string{"call", "index"} {
		t.Run(boundary, func(t *testing.T) {
			state := newStateWithIO(t, Options{})
			defer state.Close()

			var invoke func(Frame) error
			switch boundary {
			case "call":
				target := mustLoadString(
					t,
					state,
					"=frame-call-userdata-error",
					`error(io.tmpfile(),0)`,
				)
				invoke = func(frame Frame) error {
					_, err := frame.Call(target.Value())
					return err
				}
			case "index":
				constructor := mustLoadString(
					t,
					state,
					"=frame-index-userdata-error",
					`
return setmetatable({},{
	__index=function()
		error(io.tmpfile(),0)
	end,
})
`,
				)
				results, err := state.Call(constructor.Value())
				if err != nil {
					t.Fatal(err)
				}
				target, ok := results[0].AsTable()
				if !ok {
					t.Fatalf("index target = %v", results[0])
				}
				invoke = func(frame Frame) error {
					_, err := frame.Index(
						target.Value(),
						state.String("missing"),
					)
					return err
				}
			default:
				t.Fatalf("unknown boundary %q", boundary)
			}

			var nested *Error
			var exposedBeforeReturn bool
			bridge, err := state.NewNativeFunction(
				func(frame Frame) Outcome {
					err := invoke(frame)
					if !errors.As(err, &nested) {
						return frame.RaiseString("nested call did not return *Error")
					}
					exposedBeforeReturn =
						nested.value.Valid() &&
							!nested.hasCompactValue
					return frame.RaiseError(nested)
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			_, err = state.Call(bridge.Value())
			var outer *Error
			if !errors.As(err, &outer) {
				t.Fatalf("outer error = %T %v; want *Error", err, err)
			}
			if !exposedBeforeReturn {
				t.Fatal("Frame boundary returned a compact error to Go")
			}
			nestedValue := nested.Value()
			outerValue := outer.Value()
			if same, applicable := nestedValue.SameObject(
				outerValue,
			); !applicable || !same {
				t.Fatalf(
					"nested/outer error identity = (%v, %v)",
					same,
					applicable,
				)
			}
			if entries, keys, stale := hostDirectoryKindCounts(
				&state.runtime.hosts,
				UserDataKind,
			); entries != 1 || keys != 1 || stale != 0 {
				t.Fatalf(
					"Frame %s directory = entries:%d keys:%d stale:%d; want 1/1/0",
					boundary,
					entries,
					keys,
					stale,
				)
			}
		})
	}
}
