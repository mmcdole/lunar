package lua

// Optional argument helpers.
//
// Each pairs with the required accessor of the same name and collapses the
// IsMissingOrNil-then-read sequence into one call. A missing or nil
// argument yields the supplied default; a present argument is checked
// exactly as the required accessor checks it, so a wrong type is still
// reported rather than replaced by the default.
//
// The ok result therefore means "absent, or present and valid", which is
// what a callback branches on:
//
//	limit, ok := frame.OptInteger(1, 25)
//	if !ok {
//		return frame.ArgTypeError(1, lua.NumberKind)
//	}

// OptBool returns argument index as a Lua boolean, or fallback when the
// argument is missing or nil.
func (frame Frame) OptBool(index int, fallback bool) (bool, bool) {
	if frame.IsMissingOrNil(index) {
		return fallback, true
	}
	return frame.Bool(index)
}

// OptNumber returns argument index as a Lua number, or fallback when the
// argument is missing or nil.
func (frame Frame) OptNumber(index int, fallback float64) (float64, bool) {
	if frame.IsMissingOrNil(index) {
		return fallback, true
	}
	return frame.Number(index)
}

// OptCoerceNumber returns argument index as a Lua number, accepting a
// complete numeric string, or fallback when the argument is missing or nil.
func (frame Frame) OptCoerceNumber(
	index int,
	fallback float64,
) (float64, bool) {
	if frame.IsMissingOrNil(index) {
		return fallback, true
	}
	return frame.CoerceNumber(index)
}

// OptInteger returns argument index as an int64, or fallback when the
// argument is missing or nil. A present argument must satisfy Integer.
func (frame Frame) OptInteger(index int, fallback int64) (int64, bool) {
	if frame.IsMissingOrNil(index) {
		return fallback, true
	}
	return frame.Integer(index)
}

// OptIntegerInRange returns argument index as an int64 within the inclusive
// range, or fallback when the argument is missing or nil.
//
// fallback is returned as supplied and is not itself range-checked, which
// lets a caller default to a documented sentinel outside the range.
func (frame Frame) OptIntegerInRange(
	index int,
	fallback int64,
	minimum int64,
	maximum int64,
) (int64, bool) {
	if frame.IsMissingOrNil(index) {
		return fallback, true
	}
	return frame.IntegerInRange(index, minimum, maximum)
}

// OptString returns argument index as a Lua string, or fallback when the
// argument is missing or nil.
func (frame Frame) OptString(index int, fallback string) (string, bool) {
	if frame.IsMissingOrNil(index) {
		return fallback, true
	}
	return frame.String(index)
}

// OptCoerceString returns argument index as a Lua string, spelling numbers
// as Lua would, or fallback when the argument is missing or nil.
func (frame Frame) OptCoerceString(
	index int,
	fallback string,
) (string, bool) {
	if frame.IsMissingOrNil(index) {
		return fallback, true
	}
	return frame.CoerceString(index)
}

// OptTable returns argument index as a Lua table, or fallback when the
// argument is missing or nil.
func (frame Frame) OptTable(index int, fallback *Table) (*Table, bool) {
	if frame.IsMissingOrNil(index) {
		return fallback, true
	}
	return frame.Table(index)
}

// OptFunction returns argument index as a function, or fallback when the
// argument is missing or nil.
func (frame Frame) OptFunction(
	index int,
	fallback *Function,
) (*Function, bool) {
	if frame.IsMissingOrNil(index) {
		return fallback, true
	}
	return frame.Function(index)
}

// OptUserData returns argument index as userdata, or fallback when the
// argument is missing or nil.
func (frame Frame) OptUserData(
	index int,
	fallback *UserData,
) (*UserData, bool) {
	if frame.IsMissingOrNil(index) {
		return fallback, true
	}
	return frame.UserData(index)
}
