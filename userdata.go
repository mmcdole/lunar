package lua

import (
	"runtime"
	"unsafe"
)

// UserData is an opaque owning handle for a Lua userdata object holding a Go
// value.
//
// The payload is opaque to Lua unless native functions expose operations on
// it. Metatable and environment changes are controlled by State operations.
// Repeated publication of the same live Lua object returns the same handle
// pointer. Execution slots retain the compact object directly and do not pass
// through this handle.
//
// UserData must not be copied after first use. Retain and pass its pointer.
type UserData hostToken

type userDataObject struct {
	objectHeader
	payload     any
	metatable   *tableObject
	environment *tableObject
	resource    *nativeResourceToken
	gcMark      objectMark
	flags       userDataFlags
}

func newUserDataObject(
	state *State,
	payload any,
	environment *tableObject,
	resource *nativeResourceToken,
) *userDataObject {
	if state == nil ||
		state.runtime == nil ||
		environment != nil && environment.owner != state.runtime {
		panic("lua: invalid userdata construction")
	}
	data := &userDataObject{
		payload:     payload,
		environment: environment,
		resource:    resource,
	}
	state.registerUserData(data)
	return data
}

// Value returns the owning Lua value for userdata.
func (data *UserData) Value() Value {
	token := data.token()
	if token == nil ||
		token.owner == nil ||
		token.object == nil ||
		token.kind != UserDataKind {
		return Value{}
	}
	value := Value{ref: unsafe.Pointer(token), bits: uint64(UserDataKind)}
	runtime.KeepAlive(data)
	return value
}

// Data returns the Go payload. Reading the payload remains safe after the
// owning State closes. Userdata reserved for a runtime library has no public
// payload and returns nil.
func (data *UserData) Data() any {
	object := data.runtimeObject()
	if object == nil {
		return nil
	}
	payload := object.payload
	runtime.KeepAlive(data)
	return payload
}

// SetData replaces the Go payload. Runtime-owned userdata returns
// ErrReadOnlyUserData.
func (data *UserData) SetData(payload any) error {
	token := data.token()
	if token == nil ||
		token.owner == nil ||
		token.owner.closed.Load() ||
		token.object == nil ||
		token.kind != UserDataKind {
		return ErrClosed
	}
	object := (*userDataObject)(token.object)
	if object.resource != nil {
		return ErrReadOnlyUserData
	}
	object.payload = payload
	runtime.KeepAlive(data)
	return nil
}

func (data *UserData) token() *hostToken {
	return (*hostToken)(data)
}

func (data *UserData) runtimeObject() *userDataObject {
	token := data.token()
	if token == nil ||
		token.kind != UserDataKind ||
		token.object == nil {
		return nil
	}
	return (*userDataObject)(token.object)
}

func (data *userDataObject) owningHandle() *UserData {
	if data == nil {
		return nil
	}
	token := data.objectHeader.owningToken(
		UserDataKind,
		unsafe.Pointer(data),
	)
	return (*UserData)(token)
}

func (data *userDataObject) owningValue() Value {
	handle := data.owningHandle()
	return handle.Value()
}

func userDataObjectFromSlot(value slot) *userDataObject {
	if !value.isUserData() {
		panic("lua: slot is not userdata")
	}
	return (*userDataObject)(value.ref)
}

func userDataHandleFromSlot(value slot) *UserData {
	return userDataObjectFromSlot(value).owningHandle()
}
