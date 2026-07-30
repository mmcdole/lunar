package lua_test

import (
	"fmt"

	"github.com/mmcdole/lunik"
)

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

	chunk, err := state.LoadString("@answer.lua", `return 6 * 7`)
	if err != nil {
		panic(err)
	}
	results, err := state.Call(chunk.Value())
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
