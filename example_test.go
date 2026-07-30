package lua_test

import (
	"fmt"
	"testing/fstest"

	"github.com/mmcdole/lunar"
)

func ExampleFSSource() {
	scripts := fstest.MapFS{
		"main.lua": {
			Data: []byte(`return require("modules.answer")`),
		},
		"modules/answer.lua": {
			Data: []byte(`return 6 * 7`),
		},
	}
	state, err := lua.New(lua.Options{
		Source: lua.FSSource(scripts),
	})
	if err != nil {
		panic(err)
	}
	defer state.Close()
	if err := state.OpenPackage(); err != nil {
		panic(err)
	}

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
			return frame.ArgTypeError(0, lua.NumberKind)
		}
		right, ok := frame.Number(1)
		if !ok {
			return frame.ArgTypeError(1, lua.NumberKind)
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
			return frame.ArgTypeError(
				0,
				lua.StringKind,
				lua.NumberKind,
			)
		}

		limit := int64(25)
		if !frame.IsMissingOrNil(1) {
			limit, ok = frame.IntegerInRange(1, 1, 100)
			if !ok {
				return frame.ArgError(
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
