package lua

import (
	"math"
	"math/bits"
)

// Lua 5.1 defines its own PI rather than taking a platform M_PI, and derives
// degree conversion from it. Both conversions keep PUC's operation: deg
// divides by the constant and rad multiplies by it, so their rounding matches.
const (
	mathPi               = 3.14159265358979323846
	mathRadiansPerDegree = mathPi / 180.0
)

// defaultRandomSeed mirrors C's implicit srand(1): a program that never calls
// math.randomseed still observes one fixed, reproducible sequence.
const defaultRandomSeed = 1

var mathLibraryFunctions = [...]struct {
	name  string
	entry NativeFunc
}{
	{name: "abs", entry: mathAbs},
	{name: "acos", entry: mathAcos},
	{name: "asin", entry: mathAsin},
	{name: "atan", entry: mathAtan},
	{name: "atan2", entry: mathAtan2},
	{name: "ceil", entry: mathCeil},
	{name: "cos", entry: mathCos},
	{name: "cosh", entry: mathCosh},
	{name: "deg", entry: mathDeg},
	{name: "exp", entry: mathExp},
	{name: "floor", entry: mathFloor},
	{name: "fmod", entry: mathFmod},
	{name: "frexp", entry: mathFrexp},
	{name: "ldexp", entry: mathLdexp},
	{name: "log", entry: mathLog},
	{name: "log10", entry: mathLog10},
	{name: "max", entry: mathMax},
	{name: "min", entry: mathMin},
	{name: "modf", entry: mathModf},
	{name: "pow", entry: mathPow},
	{name: "rad", entry: mathRad},
	{name: "sin", entry: mathSin},
	{name: "sinh", entry: mathSinh},
	{name: "sqrt", entry: mathSqrt},
	{name: "tan", entry: mathTan},
	{name: "tanh", entry: mathTanh},
}

// mathLibraryRandomFunctions are the two entries bound to the library's own
// generator. Lua 5.1 shares one process-global C generator between them; here
// each opened math library owns exactly one, so the pair is constructed
// together rather than registered as free functions.
var mathLibraryRandomFunctions = [...]struct {
	name string
	bind func(*randomSource) NativeFunc
}{
	{name: "random", bind: mathRandom},
	{name: "randomseed", bind: mathRandomSeed},
}

// OpenMath installs the Lua 5.1 math library.
//
// Each call replaces the global math table, its functions, and its private
// random generator with fresh canonical objects.
func (state *State) OpenMath() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	loaded, err := state.ensureLoadedModules()
	if err != nil {
		return err
	}
	const constantCount = 3 // pi, huge, and the mod alias.
	library := newTable(
		state,
		0,
		len(mathLibraryFunctions)+
			len(mathLibraryRandomFunctions)+
			constantCount,
	)
	for _, definition := range mathLibraryFunctions {
		function, functionErr := state.newNativeFunctionObject(
			definition.entry,
			nil,
		)
		if functionErr != nil {
			return functionErr
		}
		if setErr := library.rawSetStringSlot(
			definition.name,
			slotFromFunctionObject(function),
		); setErr != nil {
			return setErr
		}
	}

	source := &randomSource{}
	source.seed(defaultRandomSeed)
	for _, definition := range mathLibraryRandomFunctions {
		function, functionErr := state.newNativeFunctionObject(
			definition.bind(source),
			nil,
		)
		if functionErr != nil {
			return functionErr
		}
		if setErr := library.rawSetStringSlot(
			definition.name,
			slotFromFunctionObject(function),
		); setErr != nil {
			return setErr
		}
	}

	// The standard Lua 5.1 distribution defines LUA_COMPAT_MOD, which
	// publishes math.fmod a second time as math.mod. It aliases the same
	// canonical Function rather than registering a second one.
	fmod, found := library.rawStringSlot("fmod")
	if !found {
		panic("lua: math.fmod was not installed")
	}
	if err := library.rawSetStringSlot("mod", fmod); err != nil {
		return err
	}
	if err := library.rawSetStringValue("pi", Number(mathPi)); err != nil {
		return err
	}
	if err := library.rawSetStringValue(
		"huge",
		Number(math.Inf(1)),
	); err != nil {
		return err
	}
	if err := state.globalEnvironment().rawSetStringSlot(
		"math",
		slotFromTableObject(library),
	); err != nil {
		return err
	}
	state.setLoadedModule(loaded, "math", slotFromTableObject(library))
	return nil
}

func mathAbs(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Abs(number))
}

func mathAcos(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Acos(number))
}

func mathAsin(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Asin(number))
}

func mathAtan(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Atan(number))
}

func mathAtan2(frame Frame) Outcome {
	y, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	x, ok := frame.numberArgument(1)
	if !ok {
		return numberArgumentError(frame, 1)
	}
	return frame.ReturnNumber(math.Atan2(y, x))
}

func mathCeil(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Ceil(number))
}

func mathCos(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Cos(number))
}

func mathCosh(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Cosh(number))
}

func mathDeg(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(number / mathRadiansPerDegree)
}

func mathExp(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Exp(number))
}

func mathFloor(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Floor(number))
}

func mathFmod(frame Frame) Outcome {
	left, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	right, ok := frame.numberArgument(1)
	if !ok {
		return numberArgumentError(frame, 1)
	}
	return frame.ReturnNumber(math.Mod(left, right))
}

func mathFrexp(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	fraction, exponent := math.Frexp(number)
	return frame.returnCompactValues(
		[2]slot{numberSlot(fraction), numberSlot(float64(exponent))},
		2,
		nil,
	)
}

func mathLdexp(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	exponent, ok := frame.integerArgument(1)
	if !ok {
		return numberArgumentError(frame, 1)
	}
	return frame.ReturnNumber(math.Ldexp(number, exponent))
}

func mathLog(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Log(number))
}

func mathLog10(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Log10(number))
}

// mathMax and mathMin keep Lua 5.1's explicit scan rather than delegating to
// Go's math.Max and math.Min. PUC seeds the result with argument one and
// replaces it only on a strict comparison, so a NaN argument is propagated
// exactly when it appears first; Go's helpers instead propagate any NaN.
func mathMax(frame Frame) Outcome {
	count := frame.ArgumentCount()
	largest, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	for index := 1; index < count; index++ {
		number, valid := frame.numberArgument(index)
		if !valid {
			return numberArgumentError(frame, index)
		}
		if number > largest {
			largest = number
		}
	}
	return frame.ReturnNumber(largest)
}

func mathMin(frame Frame) Outcome {
	count := frame.ArgumentCount()
	smallest, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	for index := 1; index < count; index++ {
		number, valid := frame.numberArgument(index)
		if !valid {
			return numberArgumentError(frame, index)
		}
		if number < smallest {
			smallest = number
		}
	}
	return frame.ReturnNumber(smallest)
}

func mathModf(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	integral, fractional := math.Modf(number)
	// C's modf splits an infinity into that infinity and a zero of the same
	// sign. Go reports a NaN fractional part instead, so restore the C result.
	if math.IsInf(number, 0) {
		fractional = math.Copysign(0, number)
	}
	return frame.returnCompactValues(
		[2]slot{numberSlot(integral), numberSlot(fractional)},
		2,
		nil,
	)
}

func mathPow(frame Frame) Outcome {
	base, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	exponent, ok := frame.numberArgument(1)
	if !ok {
		return numberArgumentError(frame, 1)
	}
	return frame.ReturnNumber(math.Pow(base, exponent))
}

func mathRad(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(number * mathRadiansPerDegree)
}

func mathSin(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Sin(number))
}

func mathSinh(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Sinh(number))
}

func mathSqrt(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Sqrt(number))
}

func mathTan(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Tan(number))
}

func mathTanh(frame Frame) Outcome {
	number, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	return frame.ReturnNumber(math.Tanh(number))
}

// mathRandom reproduces Lua 5.1's arity rules and interval arithmetic. The
// generator advances before the arguments are inspected, exactly as PUC calls
// rand() first, so a rejected call still consumes one value.
func mathRandom(source *randomSource) NativeFunc {
	return func(frame Frame) Outcome {
		fraction := source.next()
		switch frame.ArgumentCount() {
		case 0:
			return frame.ReturnNumber(fraction)
		case 1:
			upper, ok := frame.integerArgument(0)
			if !ok {
				return numberArgumentError(frame, 0)
			}
			if upper < 1 {
				return baseArgumentError(frame, 0, "interval is empty")
			}
			return frame.ReturnNumber(
				math.Floor(fraction*float64(upper)) + 1,
			)
		case 2:
			lower, ok := frame.integerArgument(0)
			if !ok {
				return numberArgumentError(frame, 0)
			}
			upper, ok := frame.integerArgument(1)
			if !ok {
				return numberArgumentError(frame, 1)
			}
			if lower > upper {
				return baseArgumentError(frame, 1, "interval is empty")
			}
			// The span is computed in floating point. PUC evaluates
			// u-l+1 in C int arithmetic, which overflows for wide
			// intervals; the interval bounds are exactly representable
			// here, so this is defined for every accepted pair.
			span := float64(upper) - float64(lower) + 1
			return frame.ReturnNumber(
				math.Floor(fraction*span) + float64(lower),
			)
		default:
			return libraryError(frame, "wrong number of arguments")
		}
	}
}

func mathRandomSeed(source *randomSource) NativeFunc {
	return func(frame Frame) Outcome {
		seed, ok := frame.integerArgument(0)
		if !ok {
			return numberArgumentError(frame, 0)
		}
		source.seed(seed)
		return frame.Return()
	}
}

// randomSource is one math library's private pseudo-random generator.
//
// Lua 5.1 delegates math.random to C rand(), whose sequence, period, and
// resolution are implementation-defined and whose seed is process-global.
// Reproducing that is neither possible nor desirable, so Lunar uses
// xoshiro256** seeded through SplitMix64: the sequence is identical on every
// platform, a seeded run is reproducible, the whole 53-bit mantissa is
// significant, and two States cannot disturb each other's stream.
type randomSource struct {
	state [4]uint64
}

func (source *randomSource) seed(seed int) {
	value := uint64(int64(seed))
	for index := range source.state {
		value += 0x9e3779b97f4a7c15
		mixed := value
		mixed = (mixed ^ (mixed >> 30)) * 0xbf58476d1ce4e5b9
		mixed = (mixed ^ (mixed >> 27)) * 0x94d049bb133111eb
		source.state[index] = mixed ^ (mixed >> 31)
	}
}

// next returns a number in [0, 1), the range Lua 5.1 documents for a
// no-argument math.random.
func (source *randomSource) next() float64 {
	return float64(source.nextBits()>>11) / (1 << 53)
}

func (source *randomSource) nextBits() uint64 {
	result := bits.RotateLeft64(source.state[1]*5, 7) * 9
	shifted := source.state[1] << 17
	source.state[2] ^= source.state[0]
	source.state[3] ^= source.state[1]
	source.state[1] ^= source.state[2]
	source.state[0] ^= source.state[3]
	source.state[2] ^= shifted
	source.state[3] = bits.RotateLeft64(source.state[3], 45)
	return result
}
