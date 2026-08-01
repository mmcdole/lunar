package lua_test

import (
	"fmt"
	"testing/fstest"

	"github.com/mmcdole/lunar"
)

type exampleCounter struct {
	value int64
}

func ExampleFSLoader() {
	scripts := fstest.MapFS{
		"main.lua": {
			Data: []byte(`return require("modules.answer")`),
		},
		"modules/answer.lua": {
			Data: []byte(`return 6 * 7`),
		},
	}
	state, err := lua.New(lua.Options{
		Libraries:    lua.LibrarySet{lua.PackageLibrary},
		ScriptLoader: lua.FSLoader(scripts),
	})
	if err != nil {
		panic(err)
	}
	defer state.Close()
	results, err := state.DoFile("main.lua")
	if err != nil {
		panic(err)
	}
	answer, _ := results[0].AsNumber()
	fmt.Println(answer)

	// Output:
	// 42
}

func Example() {
	state, err := lua.New(lua.Options{})
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := state.Close(); err != nil {
			panic(err)
		}
	}()

	results, err := state.DoString("@answer.lua", `return 6 * 7`)
	if err != nil {
		panic(err)
	}
	answer, _ := results[0].AsNumber()
	fmt.Println(answer)

	// Output:
	// 42
}

func ExampleState_NewNativeFunction() {
	state, err := lua.New(lua.Options{})
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := state.Close(); err != nil {
			panic(err)
		}
	}()

	add, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
		left, ok := frame.Number(0)
		if !ok {
			frame.ThrowArgTypeError(0, lua.NumberKind)
		}
		right, ok := frame.Number(1)
		if !ok {
			frame.ThrowArgTypeError(1, lua.NumberKind)
		}
		return frame.ReturnNumber(left + right)
	})
	if err != nil {
		panic(err)
	}
	if err := state.SetGlobal("host_add", add.Value()); err != nil {
		panic(err)
	}

	chunk, err := state.LoadString(
		"@host.lua",
		`return host_add(20, 22)`,
	)
	if err != nil {
		panic(err)
	}
	results, err := state.Call(chunk.Value())
	if err != nil {
		panic(err)
	}
	sum, _ := results[0].AsNumber()
	fmt.Println(sum)

	// Output:
	// 42
}

func ExampleFrame_IntegerInRange() {
	state, err := lua.New(lua.Options{})
	if err != nil {
		panic(err)
	}
	defer state.Close()

	describe, err := state.NewNativeFunction(func(frame lua.Frame) lua.Outcome {
		label, ok := frame.CoerceString(0)
		if !ok {
			frame.ThrowArgTypeError(
				0,
				lua.StringKind,
				lua.NumberKind,
			)
		}

		limit := int64(25)
		if !frame.IsMissingOrNil(1) {
			limit, ok = frame.IntegerInRange(1, 1, 100)
			if !ok {
				frame.ThrowArgError(
					1,
					"integer from 1 through 100 expected",
				)
			}
		}
		return frame.ReturnString(fmt.Sprintf("%s:%d", label, limit))
	})
	if err != nil {
		panic(err)
	}

	results, err := state.Call(describe.Value(), lua.Number(42))
	if err != nil {
		panic(err)
	}
	text, _ := results[0].AsString()
	fmt.Println(text)

	// Output:
	// 42:25
}

func ExampleNewUserDataType() {
	state, err := lua.New(lua.Options{})
	if err != nil {
		panic(err)
	}
	defer state.Close()

	counterType, err := lua.NewUserDataType[*exampleCounter](
		state,
		"example.Counter",
	)
	if err != nil {
		panic(err)
	}
	methods, err := state.NewTable()
	if err != nil {
		panic(err)
	}
	if err := state.SetFunctions(
		methods,
		map[string]lua.NativeFunc{
			"add": func(frame lua.Frame) lua.Outcome {
				counter, ok := counterType.FromArgument(frame, 0)
				if !ok {
					frame.ThrowArgError(
						0,
						counterType.Name()+" expected",
					)
				}
				amount := int64(1)
				if !frame.IsMissingOrNil(1) {
					amount, ok = frame.IntegerInRange(
						1,
						-1_000,
						1_000,
					)
					if !ok {
						frame.ThrowArgError(
							1,
							"bounded integer expected",
						)
					}
				}
				counter.value += amount
				return frame.ReturnNumber(float64(counter.value))
			},
		},
	); err != nil {
		panic(err)
	}
	if err := counterType.Metatable().RawSetString(
		"__index",
		methods.Value(),
	); err != nil {
		panic(err)
	}

	newCounter, err := state.NewNativeFunction(
		func(frame lua.Frame) lua.Outcome {
			initial, ok := frame.Integer(0)
			if !ok {
				frame.ThrowArgTypeError(0, lua.NumberKind)
			}
			counter, createErr := counterType.New(
				&exampleCounter{value: initial},
			)
			if createErr != nil {
				frame.ThrowError(createErr)
			}
			return frame.ReturnValue(counter.Value())
		},
	)
	if err != nil {
		panic(err)
	}
	if err := state.SetGlobal("new_counter", newCounter.Value()); err != nil {
		panic(err)
	}

	results, err := state.DoString("@counter.lua", `
local counter = new_counter(10)
return counter:add(5), counter:add()
`)
	if err != nil {
		panic(err)
	}
	first, _ := results[0].AsNumber()
	second, _ := results[1].AsNumber()
	fmt.Println(first, second)

	// Output:
	// 15 16
}
