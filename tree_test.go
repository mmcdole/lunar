package lua

import (
	"errors"
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestUnsupportedTreeValueMessageIsStable(t *testing.T) {
	const want = "lua: unsupported Go value in table tree"
	if got := ErrUnsupportedTreeValue.Error(); got != want {
		t.Fatalf("ErrUnsupportedTreeValue = %q; want %q", got, want)
	}
}

func TestNewTableFromConvertsNestedMapAndSequence(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	owned, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	config, err := state.NewTableFrom(map[string]any{
		"enabled": true,
		"payload": []byte{'a', 0, 'b'},
		"filters": []any{
			"combat",
			nil,
			map[string]any{"name": "chat"},
		},
		"owned": owned.Value(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if enabled, ok := config.RawGetString("enabled").AsBool(); !ok || !enabled {
		t.Fatalf("enabled = (%v, %v); want true", enabled, ok)
	}
	if payload, ok := config.RawGetString("payload").AsString(); !ok ||
		payload != "a\x00b" {
		t.Fatalf("payload = (%q, %v); want binary string", payload, ok)
	}
	filters, ok := config.RawGetString("filters").AsTable()
	if !ok {
		t.Fatal("filters is not a table")
	}
	if first, ok := filters.RawGetInt(1).AsString(); !ok || first != "combat" {
		t.Fatalf("filters[1] = (%q, %v); want combat", first, ok)
	}
	if !filters.RawGetInt(2).IsNil() {
		t.Fatalf("filters[2] = %v; want nil", filters.RawGetInt(2))
	}
	nested, ok := filters.RawGetInt(3).AsTable()
	if !ok {
		t.Fatal("filters[3] is not a table")
	}
	if name, ok := nested.RawGetString("name").AsString(); !ok || name != "chat" {
		t.Fatalf("filters[3].name = (%q, %v); want chat", name, ok)
	}
	if same, applicable := config.RawGetString("owned").SameObject(
		owned.Value(),
	); !applicable || !same {
		t.Fatalf("owned identity = (%v, %v); want (true, true)", same, applicable)
	}
}

func TestTableTreeConvertsNestedMapAndSequence(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	table, err := state.NewTableFrom(map[string]any{
		"enabled":  true,
		"disabled": false,
		"count":    3,
		"payload":  []byte{'a', 0, 'b'},
		"filters": []any{
			"combat",
			map[string]any{"name": "chat", "weight": 2.5},
		},
		"key\x00bytes": "value\x00bytes",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]any{
		"enabled":  true,
		"disabled": false,
		"count":    float64(3),
		"payload":  "a\x00b",
		"filters": []any{
			"combat",
			map[string]any{"name": "chat", "weight": float64(2.5)},
		},
		"key\x00bytes": "value\x00bytes",
	}
	got := requireTableTree(t, table)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tree = %#v; want %#v", got, want)
	}

	rebuilt, err := state.NewTableFrom(got)
	if err != nil {
		t.Fatalf("NewTableFrom(Tree()) failed: %v", err)
	}
	again := requireTableTree(t, rebuilt)
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("rebuilt Tree = %#v; want %#v", again, want)
	}
}

func TestTableTreeCanonicalizesEmptyTablesAsMaps(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	tests := []struct {
		name string
		new  func() (*Table, error)
	}{
		{
			name: "direct",
			new:  state.NewTable,
		},
		{
			name: "empty slice",
			new: func() (*Table, error) {
				return state.NewTableFrom([]any{})
			},
		},
		{
			name: "nil slice",
			new: func() (*Table, error) {
				var value []any
				return state.NewTableFrom(value)
			},
		},
		{
			name: "nil map",
			new: func() (*Table, error) {
				var value map[string]any
				return state.NewTableFrom(value)
			},
		},
		{
			name: "nil-valued map field",
			new: func() (*Table, error) {
				return state.NewTableFrom(map[string]any{"missing": nil})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table, err := test.new()
			if err != nil {
				t.Fatal(err)
			}
			got := requireTableTree(t, table)
			fields, ok := got.(map[string]any)
			if !ok {
				t.Fatalf("Tree type = %T; want map[string]any", got)
			}
			if fields == nil || len(fields) != 0 {
				t.Fatalf("Tree = %#v; want non-nil empty map", fields)
			}
		})
	}
}

func TestTableTreeNormalizesNestedTypedNilValues(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	var nilMap map[string]any
	var nilSlice []any
	var nilBytes []byte
	table, err := state.NewTableFrom(map[string]any{
		"map":     nilMap,
		"slice":   nilSlice,
		"bytes":   nilBytes,
		"missing": nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"map":   map[string]any{},
		"slice": map[string]any{},
		"bytes": "",
	}
	got := requireTableTree(t, table)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tree = %#v; want %#v", got, want)
	}
}

func TestTableTreeReportsUnsupportedKeyShapes(t *testing.T) {
	tests := []struct {
		name   string
		build  func(*testing.T, *State, *Table)
		detail string
	}{
		{
			name: "sparse sequence",
			build: func(t *testing.T, _ *State, table *Table) {
				setTableInt(t, table, 1, String("one"))
				setTableInt(t, table, 3, String("three"))
			},
		},
		{
			name: "integer index above 32-bit range",
			build: func(t *testing.T, _ *State, table *Table) {
				setTableValue(t, table, Number(1<<31), Bool(true))
			},
			detail: "sparse",
		},
		{
			name: "largest exact float integer index",
			build: func(t *testing.T, _ *State, table *Table) {
				setTableValue(t, table, Number(1<<53), Bool(true))
			},
			detail: "sparse",
		},
		{
			name: "mixed sequence and map",
			build: func(t *testing.T, _ *State, table *Table) {
				setTableInt(t, table, 1, String("one"))
				setTableString(t, table, "name", String("mixed"))
			},
		},
		{
			name: "boolean key",
			build: func(t *testing.T, _ *State, table *Table) {
				setTableValue(t, table, Bool(true), String("value"))
			},
		},
		{
			name: "table key",
			build: func(t *testing.T, state *State, table *Table) {
				key := mustTreeTable(t, state)
				setTableValue(t, table, key.Value(), String("value"))
			},
		},
		{
			name: "fractional key",
			build: func(t *testing.T, _ *State, table *Table) {
				setTableValue(t, table, Number(2.5), String("value"))
			},
		},
		{
			name: "zero key",
			build: func(t *testing.T, _ *State, table *Table) {
				setTableValue(t, table, Number(0), String("value"))
			},
		},
		{
			name: "negative key",
			build: func(t *testing.T, _ *State, table *Table) {
				setTableValue(t, table, Number(-1), String("value"))
			},
		},
		{
			name: "infinite key",
			build: func(t *testing.T, _ *State, table *Table) {
				setTableValue(t, table, Number(math.Inf(1)), String("value"))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			table := mustTreeTable(t, state)
			test.build(t, state, table)
			message := assertUnsupportedTableTree(t, table)
			if test.detail != "" && !strings.Contains(message, test.detail) {
				t.Fatalf("Tree error = %q; want %q detail", message, test.detail)
			}
		})
	}
}

func TestTreeSequenceIndexIsHostIndependent(t *testing.T) {
	for _, index := range []uint64{1, 1 << 31, 1 << 53} {
		got, ok := treeSequenceIndex(numberSlot(float64(index)))
		if !ok || got != index {
			t.Fatalf("treeSequenceIndex(%d) = (%d, %v)", index, got, ok)
		}
	}
	if got, ok := treeSequenceIndex(numberSlot(1<<53 + 2)); ok {
		t.Fatalf("treeSequenceIndex(2^53 + 2) = (%d, true); want rejection", got)
	}
}

func TestTreeFromSlotRejectsNil(t *testing.T) {
	got, err := treeFromSlot(
		nilSlot,
		1,
		make(map[*tableObject]treeVisitState),
	)
	if got != nil || !errors.Is(err, ErrUnsupportedTreeValue) {
		t.Fatalf(
			"treeFromSlot(nil) = (%#v, %v); want (nil, ErrUnsupportedTreeValue)",
			got,
			err,
		)
	}
}

func TestTableTreeReportsUnsupportedReferenceValues(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := state.NewThread(function.Value())
	if err != nil {
		t.Fatal(err)
	}

	for name, value := range map[string]Value{
		"function": function.Value(),
		"userdata": data.Value(),
		"thread":   thread.Value(),
	} {
		t.Run(name+" value", func(t *testing.T) {
			table := mustTreeTable(t, state)
			setTableString(t, table, "value", value)
			assertUnsupportedTableTree(t, table)
		})
		t.Run(name+" key", func(t *testing.T) {
			table := mustTreeTable(t, state)
			setTableValue(t, table, value, String("value"))
			assertUnsupportedTableTree(t, table)
		})
	}
}

func TestTableTreePreservesFloatBits(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	want := []float64{
		math.Copysign(0, -1),
		math.Inf(1),
		math.Inf(-1),
		math.Float64frombits(0x7ff8000000000042),
	}
	table := mustTreeTable(t, state)
	for index, number := range want {
		setTableValue(
			t,
			table,
			Number(float64(index+1)),
			Number(number),
		)
	}

	assertTreeFloatBits(t, requireTableTree(t, table), want)
	rebuilt, err := state.NewTableFrom(requireTableTree(t, table))
	if err != nil {
		t.Fatal(err)
	}
	assertTreeFloatBits(t, requireTableTree(t, rebuilt), want)
}

func TestTableTreeRejectsCyclesAndSharedSubtables(t *testing.T) {
	t.Run("self cycle", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		table := mustTreeTable(t, state)
		setTableString(t, table, "self", table.Value())
		message := assertUnsupportedTableTree(t, table)
		if !strings.Contains(message, "cycle") {
			t.Fatalf("cycle error = %q; want cycle detail", message)
		}
	})

	t.Run("two table cycle", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		left := mustTreeTable(t, state)
		right := mustTreeTable(t, state)
		setTableString(t, left, "right", right.Value())
		setTableString(t, right, "left", left.Value())
		message := assertUnsupportedTableTree(t, left)
		if !strings.Contains(message, "cycle") {
			t.Fatalf("cycle error = %q; want cycle detail", message)
		}
	})

	t.Run("amplifying shared DAG", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		root := mustTreeTable(t, state)
		setTableString(t, root, "answer", Number(42))
		for range 12 {
			parent := mustTreeTable(t, state)
			setTableString(t, parent, "left", root.Value())
			setTableString(t, parent, "right", root.Value())
			root = parent
		}

		message := assertUnsupportedTableTree(t, root)
		if !strings.Contains(message, "shared subtable") {
			t.Fatalf("shared-table error = %q; want sharing detail", message)
		}
	})
}

func TestTableTreeMatchesNewTableFromDepthBoundary(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	if tree, err := tableChainAroundValue(
		t,
		state,
		maxTreeDepth,
		Number(1),
	).Tree(); err != nil || tree == nil {
		t.Fatalf("%d wrappers around scalar = (%T, %v); want success", maxTreeDepth, tree, err)
	}
	assertUnsupportedTableTree(
		t,
		tableChainAroundValue(t, state, maxTreeDepth+1, Number(1)),
	)

	if tree, err := emptyEndingTableChain(
		t,
		state,
		maxTreeDepth+1,
	).Tree(); err != nil || tree == nil {
		t.Fatalf("%d empty-ending levels = (%T, %v); want success", maxTreeDepth+1, tree, err)
	}
	assertUnsupportedTableTree(
		t,
		emptyEndingTableChain(t, state, maxTreeDepth+2),
	)

	if table, err := state.NewTableFrom(
		goTreeAroundValue(maxTreeDepth, float64(1)),
	); err != nil || table == nil {
		t.Fatalf("NewTableFrom with %d wrappers = (%p, %v); want success", maxTreeDepth, table, err)
	}
	if table, err := state.NewTableFrom(
		goTreeAroundValue(maxTreeDepth+1, float64(1)),
	); table != nil || !errors.Is(err, ErrUnsupportedTreeValue) {
		t.Fatalf("NewTableFrom with %d wrappers = (%p, %v); want tree error", maxTreeDepth+1, table, err)
	}
	if table, err := state.NewTableFrom(
		emptyEndingGoTree(maxTreeDepth + 1),
	); err != nil || table == nil {
		t.Fatalf("NewTableFrom with %d empty-ending levels = (%p, %v); want success", maxTreeDepth+1, table, err)
	}
	if table, err := state.NewTableFrom(
		emptyEndingGoTree(maxTreeDepth + 2),
	); table != nil || !errors.Is(err, ErrUnsupportedTreeValue) {
		t.Fatalf("NewTableFrom with %d empty-ending levels = (%p, %v); want tree error", maxTreeDepth+2, table, err)
	}
}

func TestTableTreeReadsClosedStateSnapshot(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	root := mustTreeTable(t, state)
	nested := newTable(state, 0, 2)
	binaryKey := string([]byte{'k', 'e', 'y', 0, 'b', 'y', 't', 'e', 's'})
	wantKey := strings.Clone(binaryKey)
	long := strings.Repeat("binary\x00value", 12)
	wantLong := strings.Clone(long)
	if err := nested.rawSetStringSlot(
		binaryKey,
		slotFromValue(state.String(long)),
	); err != nil {
		t.Fatal(err)
	}
	if err := root.runtimeObject().rawSetStringSlot(
		"nested",
		slotFromTableObject(nested),
	); err != nil {
		t.Fatal(err)
	}
	nested = nil
	binaryKey = ""
	long = ""
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	runtime.GC()

	want := map[string]any{
		"nested": map[string]any{wantKey: wantLong},
	}
	got := requireTableTree(t, root)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("closed Tree = %#v; want %#v", got, want)
	}
}

func TestTableTreeStringsOutliveSourceState(t *testing.T) {
	tree, want := treeFromClosedDynamicStrings(t)
	for range 5 {
		runtime.GC()
	}
	if !reflect.DeepEqual(tree, want) {
		t.Fatalf("collected Tree = %#v; want %#v", tree, want)
	}
	runtime.KeepAlive(tree)
}

func TestTableTreeIgnoresMetatable(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	table := mustTreeTable(t, state)
	setTableString(t, table, "actual", Number(7))
	called := false
	trap, err := state.NewNativeFunction(func(frame Frame) Outcome {
		called = true
		frame.ThrowString("metatable executed")
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	metatable := mustTreeTable(t, state)
	setTableString(t, metatable, "__index", trap.Value())
	setTableString(t, metatable, "self", metatable.Value())
	if err := state.SetMetatable(table.Value(), metatable); err != nil {
		t.Fatal(err)
	}

	got := requireTableTree(t, table)
	want := map[string]any{"actual": float64(7)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tree = %#v; want %#v", got, want)
	}
	if called {
		t.Fatal("Tree invoked a metatable function")
	}
}

func TestTableTreeRoundTripsInsideNativeCallback(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	transform, err := state.NewNativeFunction(func(frame Frame) Outcome {
		argument, ok := frame.Table(0)
		if !ok {
			frame.ThrowArgTypeError(0, TableKind)
		}
		tree, treeErr := argument.Tree()
		if treeErr != nil {
			frame.ThrowError(treeErr)
		}
		fields, ok := tree.(map[string]any)
		if !ok {
			frame.ThrowString("expected a map tree")
		}
		count, ok := fields["count"].(float64)
		if !ok {
			frame.ThrowString("expected a numeric count")
		}
		nested, ok := fields["nested"].(map[string]any)
		if !ok {
			frame.ThrowString("expected a nested map")
		}
		items, ok := fields["items"].([]any)
		if !ok || len(items) != 1 {
			frame.ThrowString("expected a one-element sequence")
		}
		fields["count"] = count + 1
		fields["native"] = true
		nested["name"] = "changed"
		items[0] = "changed"
		fields["items"] = append(items, "added")
		result, buildErr := frame.State().NewTableFrom(fields)
		if buildErr != nil {
			frame.ThrowError(buildErr)
		}
		return frame.ReturnValue(result.Value())
	})
	if err != nil {
		t.Fatal(err)
	}
	input, err := state.NewTableFrom(map[string]any{
		"count":  4,
		"nested": map[string]any{"name": "original"},
		"items":  []any{"original"},
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := state.CallOne(transform.Value(), input.Value())
	if err != nil {
		t.Fatal(err)
	}
	result, ok := value.AsTable()
	if !ok {
		t.Fatalf("native result is %s; want table", value.Kind())
	}
	want := map[string]any{
		"count":  float64(5),
		"native": true,
		"nested": map[string]any{"name": "changed"},
		"items":  []any{"changed", "added"},
	}
	got := requireTableTree(t, result)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("native Tree round trip = %#v; want %#v", got, want)
	}
	wantOriginal := map[string]any{
		"count":  float64(4),
		"nested": map[string]any{"name": "original"},
		"items":  []any{"original"},
	}
	original := requireTableTree(t, input)
	if !reflect.DeepEqual(original, wantOriginal) {
		t.Fatalf("Tree mutation changed the Lua argument: %#v; want %#v", original, wantOriginal)
	}
}

func TestTableTreeNormalizesNilSequenceElements(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	tests := []struct {
		name        string
		input       []any
		want        any
		unsupported bool
	}{
		{name: "trailing nil", input: []any{"a", nil}, want: []any{"a"}},
		{name: "leading nil", input: []any{nil, "b"}, unsupported: true},
		{name: "middle nil", input: []any{"a", nil, "c"}, unsupported: true},
		{name: "all nil", input: []any{nil, nil}, want: map[string]any{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table, err := state.NewTableFrom(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if test.unsupported {
				assertUnsupportedTableTree(t, table)
				return
			}
			got := requireTableTree(t, table)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Tree = %#v; want %#v", got, test.want)
			}
		})
	}
}

func TestTableTreeRejectsInvalidReceivers(t *testing.T) {
	var nilTable *Table
	if tree, err := nilTable.Tree(); tree != nil || !errors.Is(err, ErrClosed) {
		t.Fatalf("nil Table.Tree = (%#v, %v); want (nil, ErrClosed)", tree, err)
	}
	var zero Table
	if tree, err := zero.Tree(); tree != nil || !errors.Is(err, ErrClosed) {
		t.Fatalf("zero Table.Tree = (%#v, %v); want (nil, ErrClosed)", tree, err)
	}
}

func requireTableTree(t testing.TB, table *Table) any {
	t.Helper()
	tree, err := table.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if tree == nil {
		t.Fatal("Tree returned nil without an error")
	}
	return tree
}

func assertUnsupportedTableTree(t testing.TB, table *Table) string {
	t.Helper()
	tree, err := table.Tree()
	if tree != nil {
		t.Fatalf("failed Tree returned a partial result of type %T", tree)
	}
	if !errors.Is(err, ErrUnsupportedTreeValue) {
		t.Fatalf("Tree error = %v; want ErrUnsupportedTreeValue", err)
	}
	return err.Error()
}

func mustTreeTable(t testing.TB, state *State) *Table {
	t.Helper()
	table, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func setTableValue(t testing.TB, table *Table, key, value Value) {
	t.Helper()
	if err := table.RawSet(key, value); err != nil {
		t.Fatal(err)
	}
}

func setTableInt(t testing.TB, table *Table, key int, value Value) {
	t.Helper()
	if err := table.RawSetInt(key, value); err != nil {
		t.Fatal(err)
	}
}

func setTableString(t testing.TB, table *Table, key string, value Value) {
	t.Helper()
	if err := table.RawSetString(key, value); err != nil {
		t.Fatal(err)
	}
}

func assertTreeFloatBits(t testing.TB, tree any, want []float64) {
	t.Helper()
	sequence, ok := tree.([]any)
	if !ok {
		t.Fatalf("Tree type = %T; want []any", tree)
	}
	if len(sequence) != len(want) {
		t.Fatalf("Tree length = %d; want %d", len(sequence), len(want))
	}
	for index, expected := range want {
		got, ok := sequence[index].(float64)
		if !ok {
			t.Fatalf("Tree[%d] type = %T; want float64", index, sequence[index])
		}
		if math.Float64bits(got) != math.Float64bits(expected) {
			t.Fatalf(
				"Tree[%d] bits = %#x; want %#x",
				index,
				math.Float64bits(got),
				math.Float64bits(expected),
			)
		}
	}
}

func tableChainAroundValue(
	t testing.TB,
	state *State,
	levels int,
	value Value,
) *Table {
	t.Helper()
	var outer *Table
	for range levels {
		outer = mustTreeTable(t, state)
		setTableString(t, outer, "next", value)
		value = outer.Value()
	}
	return outer
}

func emptyEndingTableChain(
	t testing.TB,
	state *State,
	levels int,
) *Table {
	t.Helper()
	if levels <= 0 {
		t.Fatal("empty-ending chain requires at least one table")
	}
	outer := mustTreeTable(t, state)
	for range levels - 1 {
		parent := mustTreeTable(t, state)
		setTableString(t, parent, "next", outer.Value())
		outer = parent
	}
	return outer
}

func goTreeAroundValue(levels int, value any) any {
	for range levels {
		value = map[string]any{"next": value}
	}
	return value
}

func emptyEndingGoTree(levels int) any {
	if levels <= 0 {
		panic("empty-ending Go tree requires at least one map")
	}
	var tree any = map[string]any{}
	for range levels - 1 {
		tree = map[string]any{"next": tree}
	}
	return tree
}

func treeFromClosedDynamicStrings(
	t testing.TB,
) (any, map[string]any) {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	shortKey := string([]byte{0xff, 'k', 0})
	shortValue := string([]byte{0xfe, 'v', 0})
	longKey := strings.Repeat("long-key-\x00", 12)
	longValue := strings.Repeat("long-value-\x00", 12)
	want := map[string]any{
		strings.Clone(shortKey): strings.Clone(longValue),
		strings.Clone(longKey):  strings.Clone(shortValue),
	}
	table, err := state.NewTableFrom(map[string]any{
		shortKey: longValue,
		longKey:  shortValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	tree, err := table.Tree()
	if err != nil {
		t.Fatal(err)
	}
	return tree, want
}
