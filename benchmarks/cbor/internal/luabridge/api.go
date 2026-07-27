package luabridge

// Kind is the engine-neutral Lua value kind used by the benchmark oracle.
type Kind uint8

const (
	InvalidKind Kind = iota
	NilKind
	BoolKind
	NumberKind
	StringKind
	FunctionKind
	UserDataKind
	ThreadKind
	TableKind
)

// Options contains only workload policy shared by both runtime adapters.
type Options struct {
	FixedUnixTime int64
}
