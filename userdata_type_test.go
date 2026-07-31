package lua

import (
	"errors"
	"runtime"
	"testing"
)

type typedUserDataRecord struct {
	value int64
}

type otherTypedUserDataRecord struct {
	value int64
}

func TestUserDataTypeRegistrationIsCanonicalAndPrivate(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	first, err := NewUserDataType[*typedUserDataRecord](
		state,
		"test.Record",
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Name() != "test.Record" || first.Metatable() == nil {
		t.Fatalf(
			"descriptor = (%q, %p)",
			first.Name(),
			first.Metatable(),
		)
	}

	again, err := NewUserDataType[*typedUserDataRecord](
		state,
		"test.Record",
	)
	if err != nil {
		t.Fatal(err)
	}
	if again.Metatable() != first.Metatable() {
		t.Fatal("same name and Go type did not reuse the metatable")
	}

	otherName, err := NewUserDataType[*typedUserDataRecord](
		state,
		"test.OtherRecord",
	)
	if err != nil {
		t.Fatal(err)
	}
	if otherName.Metatable() == first.Metatable() {
		t.Fatal("different userdata type names shared a metatable")
	}

	if _, err := NewUserDataType[*otherTypedUserDataRecord](
		state,
		"test.Record",
	); !errors.Is(err, ErrUserDataTypeConflict) {
		t.Fatalf("conflicting registration = %v", err)
	}
	if _, err := NewUserDataType[*typedUserDataRecord](
		state,
		"",
	); !errors.Is(err, ErrInvalidUserDataTypeName) {
		t.Fatalf("empty type name = %v", err)
	}

	registry, err := state.Registry()
	if err != nil {
		t.Fatal(err)
	}
	if value := rawStr(registry, "test.Record"); !value.IsNil() {
		t.Fatalf("Lua registry exposed private type registration as %v", value)
	}
	collision, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.RawSetString("test.Record", collision.Value()); err != nil {
		t.Fatal(err)
	}
	afterCollision, err := NewUserDataType[*typedUserDataRecord](
		state,
		"test.Record",
	)
	if err != nil {
		t.Fatal(err)
	}
	if afterCollision.Metatable() != first.Metatable() {
		t.Fatal("Lua registry collision replaced private type registration")
	}
}

func TestUserDataTypeMetatableSurvivesSemanticCollection(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	var registered *tableObject
	func() {
		descriptor, createErr := NewUserDataType[*typedUserDataRecord](
			state,
			"test.CollectedRecord",
		)
		if createErr != nil {
			t.Fatal(createErr)
		}
		registered = descriptor.Metatable().runtimeObject()
	}()

	runtime.GC()
	if err := state.Collect(); err != nil {
		t.Fatal(err)
	}
	descriptor, err := NewUserDataType[*typedUserDataRecord](
		state,
		"test.CollectedRecord",
	)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Metatable().runtimeObject() != registered {
		t.Fatal("semantic collection replaced the registered metatable")
	}
}

func TestUserDataTypeRequiresMetatableAndPayloadType(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	recordType, err := NewUserDataType[*typedUserDataRecord](
		state,
		"test.Record",
	)
	if err != nil {
		t.Fatal(err)
	}
	otherClass, err := NewUserDataType[*typedUserDataRecord](
		state,
		"test.OtherRecord",
	)
	if err != nil {
		t.Fatal(err)
	}

	payload := &typedUserDataRecord{value: 41}
	data, err := recordType.New(payload)
	if err != nil {
		t.Fatal(err)
	}
	if data.Data() != payload {
		t.Fatal("typed construction did not preserve the public payload")
	}
	metatable, err := state.Metatable(data.Value())
	if err != nil {
		t.Fatal(err)
	}
	if metatable != recordType.Metatable() {
		t.Fatal("typed construction installed the wrong metatable")
	}
	if got, ok := recordType.FromValue(data.Value()); !ok || got != payload {
		t.Fatalf("FromValue = (%p, %v); want (%p, true)", got, ok, payload)
	}
	if _, ok := otherClass.FromValue(data.Value()); ok {
		t.Fatal("wrong userdata class accepted the same Go payload type")
	}
	if _, ok := recordType.FromValue(Number(1)); ok {
		t.Fatal("FromValue accepted a non-userdata value")
	}

	forged, err := state.NewUserData(&otherTypedUserDataRecord{value: 42})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		forged.Value(),
		recordType.Metatable(),
	); err != nil {
		t.Fatal(err)
	}
	if _, ok := recordType.FromValue(forged.Value()); ok {
		t.Fatal("registered metatable accepted the wrong Go payload type")
	}

	hostClassified, err := state.NewUserData(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		hostClassified.Value(),
		recordType.Metatable(),
	); err != nil {
		t.Fatal(err)
	}
	if got, ok := recordType.FromValue(
		hostClassified.Value(),
	); !ok || got != payload {
		t.Fatal("matching host-classified userdata was rejected")
	}

	if err := data.SetData("wrong payload"); err != nil {
		t.Fatal(err)
	}
	if _, ok := recordType.FromValue(data.Value()); ok {
		t.Fatal("payload replacement bypassed typed extraction")
	}
	if err := data.SetData(payload); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(data.Value(), nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := recordType.FromValue(data.Value()); ok {
		t.Fatal("userdata without the registered metatable was accepted")
	}
}

func TestUserDataTypeFromArgumentChecksClassAndPayload(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	otherState, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer otherState.Close()

	recordType, err := NewUserDataType[*typedUserDataRecord](
		state,
		"test.Record",
	)
	if err != nil {
		t.Fatal(err)
	}
	otherClass, err := NewUserDataType[*typedUserDataRecord](
		state,
		"test.OtherRecord",
	)
	if err != nil {
		t.Fatal(err)
	}
	foreignType, err := NewUserDataType[*typedUserDataRecord](
		otherState,
		"test.Record",
	)
	if err != nil {
		t.Fatal(err)
	}

	expected := &typedUserDataRecord{value: 17}
	valid, err := recordType.New(expected)
	if err != nil {
		t.Fatal(err)
	}
	wrongClass, err := otherClass.New(expected)
	if err != nil {
		t.Fatal(err)
	}
	wrongPayload, err := state.NewUserData(&otherTypedUserDataRecord{})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		wrongPayload.Value(),
		recordType.Metatable(),
	); err != nil {
		t.Fatal(err)
	}

	function, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if got, ok := recordType.FromArgument(
			frame,
			0,
		); !ok || got != expected {
			t.Fatalf("valid argument = (%p, %v)", got, ok)
		}
		if _, ok := recordType.FromArgument(frame, 1); ok {
			t.Fatal("wrong class passed FromArgument")
		}
		if _, ok := recordType.FromArgument(frame, 2); ok {
			t.Fatal("wrong payload passed FromArgument")
		}
		if _, ok := recordType.FromArgument(frame, 3); ok {
			t.Fatal("non-userdata passed FromArgument")
		}
		if _, ok := recordType.FromArgument(frame, 4); ok {
			t.Fatal("missing argument passed FromArgument")
		}
		if _, ok := foreignType.FromArgument(frame, 0); ok {
			t.Fatal("foreign descriptor passed FromArgument")
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(
		function.Value(),
		valid.Value(),
		wrongClass.Value(),
		wrongPayload.Value(),
		Bool(true),
	); err != nil {
		t.Fatal(err)
	}
}

func TestUserDataTypeSupportsNilInterfacePayload(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	descriptor, err := NewUserDataType[any](state, "test.Any")
	if err != nil {
		t.Fatal(err)
	}
	data, err := descriptor.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := descriptor.FromValue(data.Value()); !ok || value != nil {
		t.Fatalf("nil interface payload = (%#v, %v)", value, ok)
	}
}

func TestUserDataTypePostCloseReadsAndConstructionErrors(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := NewUserDataType[*typedUserDataRecord](
		state,
		"test.Record",
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := &typedUserDataRecord{value: 9}
	data, err := descriptor.New(payload)
	if err != nil {
		t.Fatal(err)
	}
	metatable := descriptor.Metatable()
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	if descriptor.Name() != "test.Record" ||
		descriptor.Metatable() != metatable {
		t.Fatal("descriptor metadata changed after State closure")
	}
	if got, ok := descriptor.FromValue(
		data.Value(),
	); !ok || got != payload {
		t.Fatal("typed payload became unreadable after State closure")
	}
	if _, err := descriptor.New(payload); !errors.Is(err, ErrClosed) {
		t.Fatalf("typed construction after close = %v", err)
	}
	if _, err := NewUserDataType[*typedUserDataRecord](
		state,
		"test.New",
	); !errors.Is(err, ErrClosed) {
		t.Fatalf("registration after close = %v", err)
	}

	var zero UserDataType[*typedUserDataRecord]
	if zero.Name() != "" || zero.Metatable() != nil {
		t.Fatal("zero descriptor exposed metadata")
	}
	if _, err := zero.New(payload); !errors.Is(
		err,
		ErrInvalidUserDataType,
	) {
		t.Fatalf("zero descriptor construction = %v", err)
	}
	if _, ok := zero.FromValue(data.Value()); ok {
		t.Fatal("zero descriptor extracted a payload")
	}
}
