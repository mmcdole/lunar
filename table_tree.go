package lua

import (
	"errors"
	"fmt"
	"math"
	"runtime"
)

// ErrUnsupportedTreeValue reports a value or table shape that cannot be
// represented by the conversions between Lua tables and Go value trees.
var ErrUnsupportedTreeValue = errors.New(
	"lua: unsupported Go value in table tree",
)

// maxTreeDepth bounds conversion in both directions. A cyclic Go graph reports
// an error instead of exhausting the Go stack; Table.Tree detects Lua table
// cycles directly. Data shaped like a decoded document is far shallower than
// this.
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

// Tree converts table and its nested tables to a Go value tree.
//
// A table whose keys are exactly the positive integers 1 through n becomes a
// []any. A table with only string keys becomes a map[string]any; an empty table
// uses the map form. Boolean, number, and string values become bool, float64,
// and string. Strings retain their exact bytes. Nested tables are converted
// recursively.
//
// Mixed or sparse tables, unsupported key or value kinds, cycles, repeated
// table references, and values exceeding the same nesting bound as
// NewTableFrom report ErrUnsupportedTreeValue. Tree performs raw reads only:
// it does not invoke metamethods, inspect metatables, or execute Lua. Like the
// other Table readers, it remains available after the owning State closes and
// observes the frozen snapshot left by Close.
//
// Tree may be called synchronously on a table obtained during a native
// callback. Otherwise, on a live State, it must be serialized with every other
// operation on that State.
//
// The returned tree owns its maps and slices. Repeated references to one Lua
// table are rejected because representing them would require either aliasing
// those maps and slices or expanding a small Lua graph into an exponentially
// larger tree.
func (table *Table) Tree() (any, error) {
	object := table.runtimeObject()
	if object == nil || object.owner == nil {
		runtime.KeepAlive(table)
		return nil, ErrClosed
	}

	result, err := treeFromTableObject(
		object,
		0,
		make(map[*tableObject]treeVisitState),
	)
	runtime.KeepAlive(table)
	if err != nil {
		return nil, err
	}
	return result, nil
}

type treeVisitState uint8

const (
	treeVisitActive treeVisitState = iota + 1
	treeVisitComplete
)

func treeFromTableObject(
	table *tableObject,
	depth int,
	visits map[*tableObject]treeVisitState,
) (any, error) {
	if table == nil || table.owner == nil {
		return nil, ErrClosed
	}
	switch visits[table] {
	case treeVisitActive:
		return nil, fmt.Errorf(
			"%w: table contains a cycle",
			ErrUnsupportedTreeValue,
		)
	case treeVisitComplete:
		return nil, fmt.Errorf(
			"%w: table contains a shared subtable",
			ErrUnsupportedTreeValue,
		)
	}
	visits[table] = treeVisitActive
	defer func() {
		visits[table] = treeVisitComplete
	}()

	integerCount := 0
	stringCount := 0
	var maximumIndex uint64
	previous := nilSlot
	for {
		key, _, found, err := table.next(previous)
		if err != nil {
			return nil, err
		}
		if !found {
			break
		}

		switch key.kind() {
		case NumberKind:
			index, sequenceKey := treeSequenceIndex(key)
			if !sequenceKey {
				return nil, fmt.Errorf(
					"%w: number key %s is not a supported positive integer index",
					ErrUnsupportedTreeValue,
					key.diagnosticString(),
				)
			}
			integerCount++
			if index > maximumIndex {
				maximumIndex = index
			}
		case StringKind:
			stringCount++
		default:
			return nil, fmt.Errorf(
				"%w: table key has kind %s",
				ErrUnsupportedTreeValue,
				key.kind(),
			)
		}
		previous = key
	}

	if integerCount != 0 && stringCount != 0 {
		return nil, fmt.Errorf(
			"%w: table mixes sequence and string keys",
			ErrUnsupportedTreeValue,
		)
	}
	if integerCount != 0 {
		if maximumIndex != uint64(integerCount) {
			return nil, fmt.Errorf(
				"%w: table sequence is sparse (%d keys through index %d)",
				ErrUnsupportedTreeValue,
				integerCount,
				maximumIndex,
			)
		}
		if uint64(integerCount) > uint64(math.MaxInt32) {
			return nil, fmt.Errorf(
				"%w: sequence of %d elements",
				ErrUnsupportedTreeValue,
				integerCount,
			)
		}
		sequence := make([]any, integerCount)
		for index := range sequence {
			value, _ := table.rawIntSlot(index + 1)
			converted, err := treeFromSlot(value, depth+1, visits)
			if err != nil {
				return nil, err
			}
			sequence[index] = converted
		}
		return sequence, nil
	}

	fields := make(map[string]any, stringCount)
	previous = nilSlot
	for {
		key, value, found, err := table.next(previous)
		if err != nil {
			return nil, err
		}
		if !found {
			break
		}
		converted, err := treeFromSlot(value, depth+1, visits)
		if err != nil {
			return nil, err
		}
		fields[stringSlotText(key)] = converted
		previous = key
	}
	return fields, nil
}

func treeFromSlot(
	value slot,
	depth int,
	visits map[*tableObject]treeVisitState,
) (any, error) {
	if depth > maxTreeDepth {
		return nil, fmt.Errorf(
			"%w: nesting exceeds %d levels",
			ErrUnsupportedTreeValue,
			maxTreeDepth,
		)
	}

	switch value.kind() {
	case BoolKind:
		return value.ref == trueMarkerPointer, nil
	case NumberKind:
		return math.Float64frombits(value.bits), nil
	case StringKind:
		return stringSlotText(value), nil
	case TableKind:
		return treeFromTableObject(
			tableObjectFromSlot(value),
			depth,
			visits,
		)
	default:
		return nil, fmt.Errorf(
			"%w: table value has kind %s",
			ErrUnsupportedTreeValue,
			value.kind(),
		)
	}
}

func treeSequenceIndex(key slot) (uint64, bool) {
	if !key.isNumber() {
		return 0, false
	}
	number := math.Float64frombits(key.bits)
	const largestExactFloatInteger float64 = 1 << 53
	if number < 1 ||
		number > largestExactFloatInteger ||
		math.Trunc(number) != number {
		return 0, false
	}
	return uint64(number), true
}
