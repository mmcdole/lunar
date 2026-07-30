package compare

import (
	"runtime"
	"strings"
	"testing"

	golua "github.com/Shopify/go-lua"
	lunar "github.com/mmcdole/lunar"
	gopherlua "github.com/yuin/gopher-lua"
)

const (
	scalarCallSource = `
function benchmark(left, right)
	return left + right, left - right
end
`
	callbackCallSource = `
function benchmark()
	local total = 0
	for index = 1, 1000 do
		total = total + host_add(index, 2)
	end
	return total
end
`
	stringEchoSource = `
function benchmark(value)
	return value
end
`
	tableChecksumSource = `
function benchmark(value)
	local total = 0
	for index = 1, 16 do
		total = total + value[index]
	end
	return total + value.alpha + value.beta + value.gamma + value.delta
end
`
)

var boundaryString = strings.Repeat("0123456789abcdef", 8)

func BenchmarkEmbedding(b *testing.B) {
	cases := []struct {
		name      string
		lunar     func(*testing.B)
		gopherLua func(*testing.B)
		goLua     func(*testing.B)
	}{
		{
			name:      "go_to_lua_scalars",
			lunar:     benchmarkGoToLuaScalarsLunar,
			gopherLua: benchmarkGoToLuaScalarsGopherLua,
			goLua:     benchmarkGoToLuaScalarsGoLua,
		},
		{
			name:      "lua_to_go_scalar_1000",
			lunar:     benchmarkLuaToGoScalarLunar,
			gopherLua: benchmarkLuaToGoScalarGopherLua,
			goLua:     benchmarkLuaToGoScalarGoLua,
		},
		{
			name:      "go_string_echo_128B",
			lunar:     benchmarkGoStringEchoLunar,
			gopherLua: benchmarkGoStringEchoGopherLua,
			goLua:     benchmarkGoStringEchoGoLua,
		},
		{
			name:      "prebuilt_go_table_16_4_to_lua",
			lunar:     benchmarkPrebuiltGoTableToLuaLunar,
			gopherLua: benchmarkPrebuiltGoTableToLuaGopherLua,
			goLua:     benchmarkPrebuiltGoTableToLuaGoLua,
		},
		{
			name:      "create_fill_go_table_16_4_to_lua",
			lunar:     benchmarkGoTableToLuaLunar,
			gopherLua: benchmarkGoTableToLuaGopherLua,
			goLua:     benchmarkGoTableToLuaGoLua,
		},
	}
	for _, current := range cases {
		b.Run("case="+current.name, func(b *testing.B) {
			b.Run("runtime=lunar", current.lunar)
			b.Run("runtime=gopherlua", current.gopherLua)
			b.Run("runtime=golua", current.goLua)
		})
	}
}

func benchmarkGoToLuaScalarsLunar(b *testing.B) {
	state, function := loadLunarFunction(b, scalarCallSource)
	arguments := [...]lunar.Value{lunar.Number(40), lunar.Number(2)}
	var results [2]lunar.Value
	var count int
	var sum, difference float64
	var sumOK, differenceOK bool
	run := func() error {
		var err error
		count, err = state.CallInto(function, arguments[:], results[:])
		if err != nil {
			return err
		}
		sum, sumOK = results[0].AsNumber()
		difference, differenceOK = results[1].AsNumber()
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateLunarScalarResults(
		b,
		sum,
		sumOK,
		difference,
		differenceOK,
		count,
	)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateLunarScalarResults(
		b,
		sum,
		sumOK,
		difference,
		differenceOK,
		count,
	)
}

func validateLunarScalarResults(
	b *testing.B,
	sum float64,
	sumOK bool,
	difference float64,
	differenceOK bool,
	count int,
) {
	b.Helper()
	if count != 2 || !sumOK || !differenceOK || sum != 42 || difference != 38 {
		b.Fatalf(
			"results = (%d, %v/%v, %v/%v), want (2, 42/true, 38/true)",
			count,
			sum,
			sumOK,
			difference,
			differenceOK,
		)
	}
}

func benchmarkGoToLuaScalarsGopherLua(b *testing.B) {
	state, function := loadGopherLuaFunction(b, scalarCallSource)
	call := gopherlua.P{Fn: function, NRet: 2, Protect: true}
	var sum, difference gopherlua.LNumber
	var sumOK, differenceOK bool
	run := func() error {
		if err := state.CallByParam(
			call,
			gopherlua.LNumber(40),
			gopherlua.LNumber(2),
		); err != nil {
			return err
		}
		sum, sumOK = state.Get(-2).(gopherlua.LNumber)
		difference, differenceOK = state.Get(-1).(gopherlua.LNumber)
		state.Pop(2)
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateGopherLuaScalarResults(b, sum, sumOK, difference, differenceOK)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateGopherLuaScalarResults(b, sum, sumOK, difference, differenceOK)
}

func validateGopherLuaScalarResults(
	b *testing.B,
	sum gopherlua.LNumber,
	sumOK bool,
	difference gopherlua.LNumber,
	differenceOK bool,
) {
	b.Helper()
	if !sumOK || !differenceOK || sum != 42 || difference != 38 {
		b.Fatalf(
			"results = (%v/%v, %v/%v), want (42/true, 38/true)",
			sum,
			sumOK,
			difference,
			differenceOK,
		)
	}
}

func benchmarkGoToLuaScalarsGoLua(b *testing.B) {
	state, functionIndex := loadGoLuaFunction(b, scalarCallSource)
	var sum, difference float64
	var sumOK, differenceOK bool
	run := func() error {
		state.PushValue(functionIndex)
		state.PushNumber(40)
		state.PushNumber(2)
		if err := state.ProtectedCall(2, 2, 0); err != nil {
			return err
		}
		sum, sumOK = state.ToNumber(-2)
		difference, differenceOK = state.ToNumber(-1)
		state.Pop(2)
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateGoLuaScalarResults(b, sum, sumOK, difference, differenceOK)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateGoLuaScalarResults(b, sum, sumOK, difference, differenceOK)
}

func validateGoLuaScalarResults(
	b *testing.B,
	sum float64,
	sumOK bool,
	difference float64,
	differenceOK bool,
) {
	b.Helper()
	if !sumOK || !differenceOK || sum != 42 || difference != 38 {
		b.Fatalf(
			"results = (%v/%v, %v/%v), want (42/true, 38/true)",
			sum,
			sumOK,
			difference,
			differenceOK,
		)
	}
}

func benchmarkLuaToGoScalarLunar(b *testing.B) {
	state, err := lunar.New(lunar.Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := state.Close(); err != nil {
			b.Error(err)
		}
	})
	hostAdd, err := state.NewNativeFunction(func(frame lunar.Frame) lunar.Outcome {
		left, leftOK := frame.Number(0)
		right, rightOK := frame.Number(1)
		if !leftOK || !rightOK {
			return frame.RaiseString("host_add expects two numbers")
		}
		return frame.ReturnNumber(left + right)
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := state.SetRawGlobal("host_add", hostAdd.Value()); err != nil {
		b.Fatal(err)
	}
	function := loadLunarFunctionInState(b, state, callbackCallSource)
	var result [1]lunar.Value
	var count int
	var number float64
	var numberOK bool
	run := func() error {
		var err error
		count, err = state.CallInto(function, nil, result[:])
		if err != nil {
			return err
		}
		number, numberOK = result[0].AsNumber()
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateLunarNumberResult(b, number, numberOK, count, 502_500)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateLunarNumberResult(b, number, numberOK, count, 502_500)
}

func benchmarkLuaToGoScalarGopherLua(b *testing.B) {
	state := gopherlua.NewState(gopherlua.Options{SkipOpenLibs: true})
	b.Cleanup(state.Close)
	state.SetGlobal("host_add", state.NewFunction(func(current *gopherlua.LState) int {
		left, leftOK := current.Get(1).(gopherlua.LNumber)
		right, rightOK := current.Get(2).(gopherlua.LNumber)
		if !leftOK || !rightOK {
			current.RaiseError("host_add expects two numbers")
			return 0
		}
		current.Push(left + right)
		return 1
	}))
	function := loadGopherLuaFunctionInState(b, state, callbackCallSource)
	call := gopherlua.P{Fn: function, NRet: 1, Protect: true}
	var result gopherlua.LNumber
	var resultOK bool
	run := func() error {
		if err := state.CallByParam(call); err != nil {
			return err
		}
		result, resultOK = state.Get(-1).(gopherlua.LNumber)
		state.Pop(1)
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateGopherLuaNumberResult(b, result, resultOK, 502_500)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateGopherLuaNumberResult(b, result, resultOK, 502_500)
}

func benchmarkLuaToGoScalarGoLua(b *testing.B) {
	state := golua.NewState()
	state.PushGoFunction(func(current *golua.State) int {
		left, leftOK := current.ToNumber(1)
		right, rightOK := current.ToNumber(2)
		if !leftOK || !rightOK {
			golua.Errorf(current, "host_add expects two numbers")
			return 0
		}
		current.PushNumber(left + right)
		return 1
	})
	state.SetGlobal("host_add")
	functionIndex := loadGoLuaFunctionInState(b, state, callbackCallSource)
	var result float64
	var resultOK bool
	run := func() error {
		state.PushValue(functionIndex)
		if err := state.ProtectedCall(0, 1, 0); err != nil {
			return err
		}
		result, resultOK = state.ToNumber(-1)
		state.Pop(1)
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateGoLuaNumberResult(b, result, resultOK, 502_500)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateGoLuaNumberResult(b, result, resultOK, 502_500)
}

func benchmarkGoStringEchoLunar(b *testing.B) {
	state, function := loadLunarFunction(b, stringEchoSource)
	var arguments [1]lunar.Value
	var results [1]lunar.Value
	var count int
	var err error
	var result string
	var resultOK bool
	run := func() error {
		arguments[0] = state.String(boundaryString)
		count, err = state.CallInto(function, arguments[:], results[:])
		if err != nil {
			return err
		}
		result, resultOK = results[0].AsString()
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateStringResult(b, result, resultOK, count)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateStringResult(b, result, resultOK, count)
}

func benchmarkGoStringEchoGopherLua(b *testing.B) {
	state, function := loadGopherLuaFunction(b, stringEchoSource)
	call := gopherlua.P{Fn: function, NRet: 1, Protect: true}
	var result gopherlua.LString
	var resultOK bool
	run := func() error {
		if err := state.CallByParam(call, gopherlua.LString(boundaryString)); err != nil {
			return err
		}
		result, resultOK = state.Get(-1).(gopherlua.LString)
		state.Pop(1)
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateStringResult(b, string(result), resultOK, 1)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateStringResult(b, string(result), resultOK, 1)
}

func benchmarkGoStringEchoGoLua(b *testing.B) {
	state, functionIndex := loadGoLuaFunction(b, stringEchoSource)
	var result string
	var resultOK bool
	run := func() error {
		state.PushValue(functionIndex)
		state.PushString(boundaryString)
		if err := state.ProtectedCall(1, 1, 0); err != nil {
			return err
		}
		result, resultOK = state.ToString(-1)
		state.Pop(1)
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateStringResult(b, result, resultOK, 1)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateStringResult(b, result, resultOK, 1)
}

func validateStringResult(
	b *testing.B,
	result string,
	ok bool,
	count int,
) {
	b.Helper()
	if count != 1 || !ok || result != boundaryString {
		b.Fatalf(
			"result = (%d, %q/%v), want (1, %q/true)",
			count,
			result,
			ok,
			boundaryString,
		)
	}
}

var tableRecordFields = [...]struct {
	name  string
	value float64
}{
	{name: "alpha", value: 17},
	{name: "beta", value: 18},
	{name: "gamma", value: 19},
	{name: "delta", value: 20},
}

func benchmarkPrebuiltGoTableToLuaLunar(b *testing.B) {
	state, function := loadLunarFunction(b, tableChecksumSource)
	table, err := state.NewTableWithCapacity(16, 4)
	if err != nil {
		b.Fatal(err)
	}
	if err := fillLunarBenchmarkTable(table); err != nil {
		b.Fatal(err)
	}
	arguments := [...]lunar.Value{table.Value()}
	var results [1]lunar.Value
	var count int
	var result float64
	var resultOK bool
	run := func() error {
		var callErr error
		count, callErr = state.CallInto(function, arguments[:], results[:])
		if callErr != nil {
			return callErr
		}
		result, resultOK = results[0].AsNumber()
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateLunarNumberResult(b, result, resultOK, count, 210)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateLunarNumberResult(b, result, resultOK, count, 210)
}

func benchmarkPrebuiltGoTableToLuaGopherLua(b *testing.B) {
	state, function := loadGopherLuaFunction(b, tableChecksumSource)
	call := gopherlua.P{Fn: function, NRet: 1, Protect: true}
	table := state.CreateTable(16, 4)
	fillGopherLuaBenchmarkTable(table)
	var result gopherlua.LNumber
	var resultOK bool
	run := func() error {
		if err := state.CallByParam(call, table); err != nil {
			return err
		}
		result, resultOK = state.Get(-1).(gopherlua.LNumber)
		state.Pop(1)
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateGopherLuaNumberResult(b, result, resultOK, 210)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateGopherLuaNumberResult(b, result, resultOK, 210)
}

func benchmarkPrebuiltGoTableToLuaGoLua(b *testing.B) {
	state, functionIndex := loadGoLuaFunction(b, tableChecksumSource)
	tableIndex := pushGoLuaBenchmarkTable(state)
	var result float64
	var resultOK bool
	run := func() error {
		state.PushValue(functionIndex)
		state.PushValue(tableIndex)
		if err := state.ProtectedCall(1, 1, 0); err != nil {
			return err
		}
		result, resultOK = state.ToNumber(-1)
		state.Pop(1)
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateGoLuaNumberResult(b, result, resultOK, 210)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateGoLuaNumberResult(b, result, resultOK, 210)
}

func benchmarkGoTableToLuaLunar(b *testing.B) {
	state, function := loadLunarFunction(b, tableChecksumSource)
	var arguments [1]lunar.Value
	var results [1]lunar.Value
	var count int
	var err error
	var result float64
	var resultOK bool
	run := func() error {
		table, tableErr := state.NewTableWithCapacity(16, 4)
		if tableErr != nil {
			return tableErr
		}
		if tableErr := fillLunarBenchmarkTable(table); tableErr != nil {
			return tableErr
		}
		arguments[0] = table.Value()
		count, err = state.CallInto(function, arguments[:], results[:])
		if err != nil {
			return err
		}
		result, resultOK = results[0].AsNumber()
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateLunarNumberResult(b, result, resultOK, count, 210)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateLunarNumberResult(b, result, resultOK, count, 210)
}

func benchmarkGoTableToLuaGopherLua(b *testing.B) {
	state, function := loadGopherLuaFunction(b, tableChecksumSource)
	call := gopherlua.P{Fn: function, NRet: 1, Protect: true}
	var result gopherlua.LNumber
	var resultOK bool
	run := func() error {
		table := state.CreateTable(16, 4)
		fillGopherLuaBenchmarkTable(table)
		if err := state.CallByParam(call, table); err != nil {
			return err
		}
		result, resultOK = state.Get(-1).(gopherlua.LNumber)
		state.Pop(1)
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateGopherLuaNumberResult(b, result, resultOK, 210)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateGopherLuaNumberResult(b, result, resultOK, 210)
}

func benchmarkGoTableToLuaGoLua(b *testing.B) {
	state, functionIndex := loadGoLuaFunction(b, tableChecksumSource)
	var result float64
	var resultOK bool
	run := func() error {
		state.PushValue(functionIndex)
		pushGoLuaBenchmarkTable(state)
		if err := state.ProtectedCall(1, 1, 0); err != nil {
			return err
		}
		result, resultOK = state.ToNumber(-1)
		state.Pop(1)
		return nil
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	validateGoLuaNumberResult(b, result, resultOK, 210)

	runtime.GC()
	b.ReportAllocs()
	for b.Loop() {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
	validateGoLuaNumberResult(b, result, resultOK, 210)
}

func fillLunarBenchmarkTable(table *lunar.Table) error {
	for index := 1; index <= 16; index++ {
		if err := table.RawSetInt(
			index,
			lunar.Number(float64(index)),
		); err != nil {
			return err
		}
	}
	for _, field := range tableRecordFields {
		if err := table.RawSetString(
			field.name,
			lunar.Number(field.value),
		); err != nil {
			return err
		}
	}
	return nil
}

func fillGopherLuaBenchmarkTable(table *gopherlua.LTable) {
	for index := 1; index <= 16; index++ {
		table.RawSetInt(index, gopherlua.LNumber(index))
	}
	for _, field := range tableRecordFields {
		table.RawSetString(field.name, gopherlua.LNumber(field.value))
	}
}

func pushGoLuaBenchmarkTable(state *golua.State) int {
	state.CreateTable(16, 4)
	tableIndex := state.Top()
	for index := 1; index <= 16; index++ {
		state.PushNumber(float64(index))
		state.RawSetInt(tableIndex, index)
	}
	for _, field := range tableRecordFields {
		state.PushString(field.name)
		state.PushNumber(field.value)
		state.RawSet(tableIndex)
	}
	return tableIndex
}

func validateLunarNumberResult(
	b *testing.B,
	number float64,
	ok bool,
	count int,
	want float64,
) {
	b.Helper()
	if count != 1 || !ok || number != want {
		b.Fatalf(
			"result = (%d, %v/%v), want (1, %v/true)",
			count,
			number,
			ok,
			want,
		)
	}
}

func validateGopherLuaNumberResult(
	b *testing.B,
	result gopherlua.LNumber,
	ok bool,
	want float64,
) {
	b.Helper()
	if !ok || float64(result) != want {
		b.Fatalf("result = (%v/%v), want (%v/true)", result, ok, want)
	}
}

func validateGoLuaNumberResult(
	b *testing.B,
	result float64,
	ok bool,
	want float64,
) {
	b.Helper()
	if !ok || result != want {
		b.Fatalf("result = (%v/%v), want (%v/true)", result, ok, want)
	}
}

func loadLunarFunction(
	b *testing.B,
	source string,
) (*lunar.State, lunar.Value) {
	b.Helper()
	state, err := lunar.New(lunar.Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := state.Close(); err != nil {
			b.Error(err)
		}
	})
	return state, loadLunarFunctionInState(b, state, source)
}

func loadLunarFunctionInState(
	b *testing.B,
	state *lunar.State,
	source string,
) lunar.Value {
	b.Helper()
	chunk, err := state.LoadString("@embedding.lua", source)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := state.CallInto(chunk.Value(), nil, nil); err != nil {
		b.Fatal(err)
	}
	function, err := state.RawGlobal("benchmark")
	if err != nil {
		b.Fatal(err)
	}
	if function.Kind() != lunar.FunctionKind {
		b.Fatalf("benchmark has kind %s, want function", function.Kind())
	}
	return function
}

func loadGopherLuaFunction(
	b *testing.B,
	source string,
) (*gopherlua.LState, *gopherlua.LFunction) {
	b.Helper()
	state := gopherlua.NewState(gopherlua.Options{SkipOpenLibs: true})
	b.Cleanup(state.Close)
	return state, loadGopherLuaFunctionInState(b, state, source)
}

func loadGopherLuaFunctionInState(
	b *testing.B,
	state *gopherlua.LState,
	source string,
) *gopherlua.LFunction {
	b.Helper()
	if err := state.DoString(source); err != nil {
		b.Fatal(err)
	}
	function, ok := state.GetGlobal("benchmark").(*gopherlua.LFunction)
	if !ok {
		b.Fatal("benchmark is not a function")
	}
	return function
}

func loadGoLuaFunction(
	b *testing.B,
	source string,
) (*golua.State, int) {
	b.Helper()
	state := golua.NewState()
	return state, loadGoLuaFunctionInState(b, state, source)
}

func loadGoLuaFunctionInState(
	b *testing.B,
	state *golua.State,
	source string,
) int {
	b.Helper()
	if err := golua.DoString(state, source); err != nil {
		b.Fatal(err)
	}
	state.Global("benchmark")
	if !state.IsFunction(-1) {
		b.Fatal("benchmark is not a function")
	}
	return state.Top()
}
