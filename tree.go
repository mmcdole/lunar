package lua

import (
	"errors"
	"fmt"
	"math"
)

// ErrUnsupportedTreeValue reports a Go value that NewTableFrom cannot convert.
var ErrUnsupportedTreeValue = errors.New(
	"lua: unsupported Go value in table tree",
)

// maxTreeDepth bounds conversion so a cyclic Go graph reports an error instead
// of exhausting the Go stack. Data shaped like a decoded document is far
// shallower than this.
const maxTreeDepth = 256

// NewTableFrom builds a Lua table from a Go value tree in one pass.
//
// It converts nil, bool, the signed and unsigned integer kinds, float32,
// float64, string, []byte, []any, map[string]any, and an owning Value that
// already belongs to this State. Nested maps and slices become nested tables;
// a slice becomes a one-based sequence. Any other Go type reports
// ErrUnsupportedTreeValue and leaves no partially built table reachable.
//
// Integers outside float64's exact range can lose precision, as they would
// through any Lua number. Conversion performs raw assignments only: it never
// invokes __newindex and never executes Lua, so it is also usable from a native
// callback through Frame.State.
func (state *State) NewTableFrom(tree any) (*Table, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	value, err := state.treeSlot(tree, 0)
	if err != nil {
		return nil, err
	}
	if !value.isTable() {
		return nil, fmt.Errorf(
			"%w: top level is %T, not a map or slice",
			ErrUnsupportedTreeValue,
			tree,
		)
	}
	return tableObjectFromSlot(value).owningHandle(), nil
}

func (state *State) treeSlot(tree any, depth int) (slot, error) {
	if depth > maxTreeDepth {
		return nilSlot, fmt.Errorf(
			"%w: nesting exceeds %d levels",
			ErrUnsupportedTreeValue,
			maxTreeDepth,
		)
	}
	switch typed := tree.(type) {
	case nil:
		return nilSlot, nil
	case bool:
		if typed {
			return trueSlot, nil
		}
		return falseSlot, nil
	case string:
		return stringSlot(state.runtime.strings.make(typed)), nil
	case []byte:
		return stringSlot(state.runtime.strings.makeBytes(typed)), nil
	case float64:
		return numberSlot(typed), nil
	case float32:
		return numberSlot(float64(typed)), nil
	case int:
		return numberSlot(float64(typed)), nil
	case int8:
		return numberSlot(float64(typed)), nil
	case int16:
		return numberSlot(float64(typed)), nil
	case int32:
		return numberSlot(float64(typed)), nil
	case int64:
		return numberSlot(float64(typed)), nil
	case uint:
		return numberSlot(float64(typed)), nil
	case uint8:
		return numberSlot(float64(typed)), nil
	case uint16:
		return numberSlot(float64(typed)), nil
	case uint32:
		return numberSlot(float64(typed)), nil
	case uint64:
		return numberSlot(float64(typed)), nil
	case Value:
		if err := state.runtime.accept(typed); err != nil {
			return nilSlot, err
		}
		return state.runtime.importAcceptedSlot(slotFromValue(typed)), nil
	case map[string]any:
		return state.treeMap(typed, depth)
	case []any:
		return state.treeSequence(typed, depth)
	default:
		return nilSlot, fmt.Errorf(
			"%w: %T",
			ErrUnsupportedTreeValue,
			tree,
		)
	}
}

func (state *State) treeMap(
	fields map[string]any,
	depth int,
) (slot, error) {
	table := newTable(state, 0, len(fields))
	for name, field := range fields {
		value, err := state.treeSlot(field, depth+1)
		if err != nil {
			return nilSlot, err
		}
		if value.isNil() {
			continue
		}
		if err := table.rawSetStringSlot(name, value); err != nil {
			return nilSlot, err
		}
	}
	return slotFromTableObject(table), nil
}

func (state *State) treeSequence(
	elements []any,
	depth int,
) (slot, error) {
	if uint64(len(elements)) > uint64(math.MaxInt32) {
		return nilSlot, fmt.Errorf(
			"%w: sequence of %d elements",
			ErrUnsupportedTreeValue,
			len(elements),
		)
	}
	table := newTable(state, len(elements), 0)
	for index, element := range elements {
		value, err := state.treeSlot(element, depth+1)
		if err != nil {
			return nilSlot, err
		}
		if value.isNil() {
			continue
		}
		table.setInteger(index+1, value)
	}
	return slotFromTableObject(table), nil
}
