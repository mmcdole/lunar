package lua

import (
	"errors"
	"fmt"
	"reflect"
)

// ErrInvalidUserDataTypeName reports an empty userdata type name.
var ErrInvalidUserDataTypeName = errors.New(
	"lua: userdata type name is empty",
)

// ErrUserDataTypeConflict reports reuse of a userdata type name with a
// different Go payload type.
var ErrUserDataTypeConflict = errors.New(
	"lua: userdata type name has a conflicting Go type",
)

// ErrInvalidUserDataType reports use of a zero or otherwise invalid
// UserDataType descriptor.
var ErrInvalidUserDataType = errors.New(
	"lua: invalid userdata type descriptor",
)

type userDataTypeRegistration struct {
	name        string
	payloadType reflect.Type
	metatable   *tableObject
}

// UserDataType is a State-bound descriptor for one class of Go-backed Lua
// userdata.
//
// A value matches the descriptor only when it belongs to the descriptor's
// State, has the descriptor's exact metatable, and its payload is assignable
// to T. The descriptor's metadata and typed reads remain available after the
// State closes; constructing new userdata does not.
type UserDataType[T any] struct {
	state        *State
	owner        *runtimeState
	registration *userDataTypeRegistration
	metatable    *Table
}

// NewUserDataType returns the canonical State-local userdata type named name.
//
// Repeating the same name and T reuses its metatable. Reusing name with a
// different T returns ErrUserDataTypeConflict. Registrations are held in a
// private State registry that is not exposed through debug.getregistry.
func NewUserDataType[T any](
	state *State,
	name string,
) (*UserDataType[T], error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, ErrInvalidUserDataTypeName
	}

	payloadType := reflect.TypeFor[T]()
	if registration := state.userDataTypes[name]; registration != nil {
		if registration.payloadType != payloadType {
			return nil, fmt.Errorf(
				"%w: %q is registered for %v, not %v",
				ErrUserDataTypeConflict,
				name,
				registration.payloadType,
				payloadType,
			)
		}
		return newUserDataTypeDescriptor[T](state, registration), nil
	}

	metatable := newTable(state, 0, 0)
	registration := &userDataTypeRegistration{
		name:        name,
		payloadType: payloadType,
		metatable:   metatable,
	}
	if state.userDataTypes == nil {
		state.userDataTypes =
			make(map[string]*userDataTypeRegistration)
	}
	state.userDataTypes[name] = registration
	return newUserDataTypeDescriptor[T](state, registration), nil
}

func newUserDataTypeDescriptor[T any](
	state *State,
	registration *userDataTypeRegistration,
) *UserDataType[T] {
	return &UserDataType[T]{
		state:        state,
		owner:        state.runtime,
		registration: registration,
		metatable:    registration.metatable.owningHandle(),
	}
}

// Name returns the State-local registration name. A zero descriptor returns
// an empty string.
func (descriptor *UserDataType[T]) Name() string {
	if descriptor == nil || descriptor.registration == nil {
		return ""
	}
	return descriptor.registration.name
}

// Metatable returns the canonical metatable for this userdata type. A zero
// descriptor returns nil.
func (descriptor *UserDataType[T]) Metatable() *Table {
	if descriptor == nil || descriptor.registration == nil {
		return nil
	}
	return descriptor.metatable
}

// New constructs userdata holding payload and installs the descriptor's exact
// metatable.
func (descriptor *UserDataType[T]) New(payload T) (*UserData, error) {
	state, err := descriptor.liveState()
	if err != nil {
		return nil, err
	}
	data := newUserDataObject(
		state,
		payload,
		state.constructionEnvironment(),
		nil,
	)
	data.metatable = descriptor.registration.metatable
	return data.owningHandle(), nil
}

// FromValue returns the typed payload when value belongs to this exact
// userdata type.
//
// It returns false for another State, Lua kind, userdata metatable, or Go
// payload type. Reading an owning value remains safe after the State closes.
func (descriptor *UserDataType[T]) FromValue(value Value) (T, bool) {
	var zero T
	data, ok := value.AsUserData()
	if !ok {
		return zero, false
	}
	return descriptor.fromObject(data.runtimeObject())
}

// FromArgument returns the typed payload when Frame argument index belongs to
// this exact userdata type.
//
// Argument indexes are zero-based. A missing argument or mismatched Lua class
// or Go payload returns false.
func (descriptor *UserDataType[T]) FromArgument(
	frame Frame,
	index int,
) (T, bool) {
	var zero T
	value, present := frame.argument(index)
	if !present || !value.isUserData() {
		return zero, false
	}
	return descriptor.fromObject(userDataObjectFromSlot(value))
}

func (descriptor *UserDataType[T]) liveState() (*State, error) {
	if descriptor == nil ||
		descriptor.state == nil ||
		descriptor.owner == nil ||
		descriptor.registration == nil ||
		descriptor.metatable == nil {
		return nil, ErrInvalidUserDataType
	}
	state := descriptor.state
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	if state.runtime != descriptor.owner ||
		state.userDataTypes[descriptor.registration.name] !=
			descriptor.registration {
		return nil, ErrInvalidUserDataType
	}
	return state, nil
}

func (descriptor *UserDataType[T]) fromObject(
	data *userDataObject,
) (T, bool) {
	var zero T
	if descriptor == nil ||
		descriptor.owner == nil ||
		descriptor.registration == nil ||
		data == nil ||
		data.owner != descriptor.owner ||
		data.metatable != descriptor.registration.metatable {
		return zero, false
	}
	payload, ok := data.payload.(T)
	if ok {
		return payload, true
	}
	if data.payload == nil &&
		descriptor.registration.payloadType.Kind() == reflect.Interface {
		return zero, true
	}
	return zero, false
}
