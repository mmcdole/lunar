package lua

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"weak"
)

func TestOpenBaseIsExplicitAndUsesTheGlobalEnvironment(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	before, err := state.Global("pcall")
	if err != nil {
		t.Fatal(err)
	}
	if !before.IsNil() {
		t.Fatalf("new state pcall = %v; want nil", before)
	}
	loadedBeforeOpen := mustLoadString(
		t,
		state,
		"@open-base.lua",
		`return pcall(function() return 42 end)`,
	)
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	baseFunctions := []string{
		"assert",
		"collectgarbage",
		"dofile",
		"error",
		"gcinfo",
		"getfenv",
		"getmetatable",
		"ipairs",
		"load",
		"loadfile",
		"loadstring",
		"newproxy",
		"next",
		"pairs",
		"pcall",
		"print",
		"rawequal",
		"rawget",
		"rawset",
		"select",
		"setfenv",
		"setmetatable",
		"tonumber",
		"tostring",
		"type",
		"unpack",
		"xpcall",
	}
	for _, name := range baseFunctions {
		value, globalErr := state.Global(name)
		if globalErr != nil {
			t.Fatal(globalErr)
		}
		if value.Kind() != FunctionKind {
			t.Fatalf("%s = %v; want function", name, value)
		}
	}
	global, err := state.Global("_G")
	if err != nil {
		t.Fatal(err)
	}
	if same, applicable := global.SameObject(
		state.main.globals.owningValue(),
	); !applicable || !same {
		t.Fatal("_G does not identify the canonical global environment")
	}
	version, err := state.Global("_VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := version.AsString(); !ok || text != "Lua 5.1" {
		t.Fatalf("_VERSION = (%q, %v); want Lua 5.1", text, ok)
	}

	results, err := state.Call(loadedBeforeOpen.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true), Number(42))

	oldFunctions := make(map[string]Value, len(baseFunctions))
	for _, name := range baseFunctions {
		oldFunctions[name], _ = state.Global(name)
	}
	if err := state.SetGlobal("pcall", Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("_G", Nil()); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	for _, name := range baseFunctions {
		current, getErr := state.Global(name)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.Kind() != FunctionKind {
			t.Fatalf("reopened %s = %v; want function", name, current)
		}
		if same, applicable := oldFunctions[name].SameObject(current); !applicable || same {
			t.Fatalf("reopened %s did not receive a fresh function", name)
		}
	}
	global, _ = state.Global("_G")
	if same, applicable := global.SameObject(
		state.main.globals.owningValue(),
	); !applicable || !same {
		t.Fatal("reopening did not restore _G")
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); !errors.Is(err, ErrClosed) {
		t.Fatalf("OpenBase after Close = %v; want ErrClosed", err)
	}
}

func TestNewProxyConstructionAndRegistry(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	newProxy, err := state.Global("newproxy")
	if err != nil {
		t.Fatal(err)
	}
	function := functionObjectFromSlot(slotFromValue(newProxy))
	body := function.nativeBodyUnchecked()
	if len(body.captures) != 1 || !body.captures[0].isTable() {
		t.Fatal("newproxy does not retain one private validity table")
	}
	validMetatables := tableObjectFromSlot(body.captures[0])
	if validMetatables.metatable != validMetatables ||
		tableWeakMode(validMetatables) != weakKeys|weakValues {
		t.Fatal("newproxy validity table is not self-metatabled and weak in both directions")
	}

	plain, err := state.Call(newProxy)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 1 || plain[0].Kind() != UserDataKind {
		t.Fatalf("newproxy() = %v; want one userdata", plain)
	}
	plainObject := userDataObjectFromSlot(slotFromValue(plain[0]))
	if plainObject.payload != nil || plainObject.metatable != nil {
		t.Fatal("plain proxy has a payload or metatable")
	}

	created, err := state.Call(newProxy, Bool(true))
	if err != nil {
		t.Fatal(err)
	}
	createdObject := userDataObjectFromSlot(slotFromValue(created[0]))
	metatable := createdObject.metatable
	if metatable == nil {
		t.Fatal("newproxy(true) has no metatable")
	}
	valid, found := validMetatables.rawSlot(
		slotFromTableObject(metatable),
	)
	if !found || !truthySlot(valid) {
		t.Fatal("newproxy(true) did not register its metatable")
	}

	clone, err := state.Call(newProxy, created[0])
	if err != nil {
		t.Fatal(err)
	}
	if cloneObject := userDataObjectFromSlot(
		slotFromValue(clone[0]),
	); cloneObject.metatable != metatable {
		t.Fatal("newproxy(proxy) did not share the registered metatable")
	}

	unregistered, err := state.NewUserData(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(newProxy, unregistered.Value()); err == nil {
		t.Fatal("newproxy accepted unregistered userdata")
	}

	oldProxy := created[0]
	oldFunction := newProxy
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	reopened, err := state.Global("newproxy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(reopened, oldProxy); err == nil {
		t.Fatal("reopened newproxy accepted an old registry's proxy")
	}
	freshProxy, err := state.Call(reopened, Bool(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(oldFunction, freshProxy[0]); err == nil {
		t.Fatal("retained newproxy accepted a reopened registry's proxy")
	}
	oldClone, err := state.Call(oldFunction, oldProxy)
	if err != nil {
		t.Fatal(err)
	}
	if oldCloneObject := userDataObjectFromSlot(
		slotFromValue(oldClone[0]),
	); oldCloneObject.metatable != metatable {
		t.Fatal("retained newproxy lost its private registry after reopening")
	}
}

func TestNewProxyRegistryDoesNotRootMetatables(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	newProxy, err := state.Global("newproxy")
	if err != nil {
		t.Fatal(err)
	}
	function := functionObjectFromSlot(slotFromValue(newProxy))
	validMetatables := tableObjectFromSlot(
		function.nativeBodyUnchecked().captures[0],
	)

	var (
		proxy          *userDataObject
		metatable      *tableObject
		metatableKey   slot
		publishedToken weak.Pointer[hostToken]
	)
	func() {
		results, callErr := state.Call(newProxy, Bool(true))
		if callErr != nil {
			t.Fatal(callErr)
		}
		proxy = userDataObjectFromSlot(slotFromValue(results[0]))
		metatable = proxy.metatable
		metatableKey = slotFromTableObject(metatable)
		publishedToken = weak.Make((*hostToken)(results[0].ref))
	}()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for publishedToken.Value() != nil {
		runtime.GC()
		select {
		case <-deadline.C:
			t.Fatal("discarded proxy handle remained reachable")
		case <-ticker.C:
		}
	}

	state.collectUnreachable()
	if proxy.owner != nil || metatable.owner != nil {
		t.Fatal("weak validity registry retained a discarded proxy or metatable")
	}
	if value, found := validMetatables.rawSlot(
		metatableKey,
	); found || !value.isNil() {
		t.Fatal("weak validity registry retained a dead metatable entry")
	}
}

func TestNewProxySurvivesAutomaticCollectionAtReturn(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	newProxy, err := state.Global("newproxy")
	if err != nil {
		t.Fatal(err)
	}
	function := functionObjectFromSlot(slotFromValue(newProxy))
	validMetatables := tableObjectFromSlot(
		function.nativeBodyUnchecked().captures[0],
	)
	unreachable := newTable(state, 0, 0)

	control := &state.runtime.collection
	control.budget = 1
	control.debt = 0
	control.requested = false
	control.refreshRunnable()

	results, err := state.Call(newProxy, Bool(true))
	if err != nil {
		t.Fatal(err)
	}
	if unreachable.owner != nil {
		t.Fatal("newproxy allocation did not trigger the due automatic cycle")
	}
	proxy := userDataObjectFromSlot(slotFromValue(results[0]))
	if proxy.owner != state.runtime || proxy.metatable == nil ||
		proxy.metatable.owner != state.runtime {
		t.Fatal("automatic collection swept a returned proxy or its metatable")
	}
	valid, found := validMetatables.rawSlot(
		slotFromTableObject(proxy.metatable),
	)
	if !found || !truthySlot(valid) {
		t.Fatal("automatic collection cleared a returned proxy's registry entry")
	}
}

func TestBaseCollectionControlsShareTheSemanticHeap(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	collector, err := state.Global("collectgarbage")
	if err != nil {
		t.Fatal(err)
	}
	gcinfo, err := state.Global("gcinfo")
	if err != nil {
		t.Fatal(err)
	}

	callCollector := func(option string, amount Value) Value {
		t.Helper()
		arguments := []Value{state.String(option)}
		if amount.Valid() {
			arguments = append(arguments, amount)
		}
		results, callErr := state.Call(collector, arguments...)
		if callErr != nil {
			t.Fatal(callErr)
		}
		if len(results) != 1 {
			t.Fatalf(
				"collectgarbage(%q) returned %d values; want 1",
				option,
				len(results),
			)
		}
		return results[0]
	}
	requireNumber := func(value Value, want float64, operation string) {
		t.Helper()
		number, ok := value.AsNumber()
		if !ok || number != want {
			t.Fatalf("%s result = %v; want %v", operation, value, want)
		}
	}

	requireNumber(
		callCollector("setpause", Number(123)),
		200,
		"initial setpause",
	)
	requireNumber(
		callCollector("setpause", Number(200)),
		123,
		"second setpause",
	)
	requireNumber(
		callCollector("setstepmul", Number(-7)),
		200,
		"initial setstepmul",
	)
	requireNumber(
		callCollector("setstepmul", Number(200)),
		-7,
		"second setstepmul",
	)
	stopped := callCollector("stop", Value{})
	requireNumber(stopped, 0, "stop")
	if !state.runtime.collection.stopped {
		t.Fatal("stop left automatic collection running")
	}
	if result := callCollector("step", Number(1)); !result.Truth() ||
		result.Kind() != BoolKind {
		t.Fatalf("synchronous step result = %v; want true", result)
	}
	if state.runtime.collection.stopped {
		t.Fatal("explicit step did not resume automatic collection")
	}
	requireNumber(callCollector("stop", Value{}), 0, "second stop")
	restarted := callCollector("restart", Value{})
	requireNumber(restarted, 0, "restart")
	if state.runtime.collection.stopped {
		t.Fatal("restart left automatic collection stopped")
	}

	count := callCollector("count", Value{})
	countNumber, ok := count.AsNumber()
	if !ok {
		t.Fatalf("count result = %v; want number", count)
	}
	heapBytes, err := state.HeapBytes()
	if err != nil {
		t.Fatal(err)
	}
	if want := float64(heapBytes) / 1024; countNumber != want {
		t.Fatalf("count = %v KiB; want %v", countNumber, want)
	}

	infoResults, err := state.Call(gcinfo)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, infoResults, Number(float64(heapBytes>>10)))
}

func TestBaseCollectionControlPolicyIsStateLocal(t *testing.T) {
	first := newStateWithBase(t, Options{})
	defer first.Close()
	second := newStateWithBase(t, Options{})
	defer second.Close()

	firstCollector, _ := first.Global("collectgarbage")
	secondCollector, _ := second.Global("collectgarbage")
	firstPause, err := first.Call(
		firstCollector,
		first.String("setpause"),
		Number(73),
	)
	if err != nil {
		t.Fatal(err)
	}
	secondPause, err := second.Call(
		secondCollector,
		second.String("setpause"),
		Number(91),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, firstPause, Number(200))
	assertTestValues(t, secondPause, Number(200))

	if _, err := first.Call(firstCollector, first.String("stop")); err != nil {
		t.Fatal(err)
	}
	if !first.runtime.collection.stopped ||
		second.runtime.collection.stopped {
		t.Fatal("collection stop policy crossed State ownership")
	}
	if first.runtime.collection.pause != 73 ||
		second.runtime.collection.pause != 91 {
		t.Fatal("collection tuning crossed State ownership")
	}
}

func TestBaseAutomaticCollectionHonorsStopAndRestart(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	stop := mustLoadString(
		t,
		state,
		"@automatic-stop.lua",
		`collectgarbage("stop"); return 1`,
	)
	restart := mustLoadString(
		t,
		state,
		"@automatic-restart.lua",
		`collectgarbage("restart"); return 2`,
	)

	if results, err := state.Call(stop.Value()); err != nil {
		t.Fatal(err)
	} else {
		assertTestValues(t, results, Number(1))
	}
	if !state.runtime.collection.stopped {
		t.Fatal("stop did not suspend automatic collection")
	}

	state.resetCollectionDebt()
	state.runtime.collection.budget = 1
	garbage := newTable(state, 0, 0)
	if !state.runtime.collection.requested {
		t.Fatal("allocation while stopped did not record due debt")
	}
	if garbage.owner != state.runtime {
		t.Fatal("host allocation entered the stopped collector")
	}

	if results, err := state.Call(restart.Value()); err != nil {
		t.Fatal(err)
	} else {
		assertTestValues(t, results, Number(2))
	}
	if state.runtime.collection.stopped ||
		state.runtime.collection.requested {
		t.Fatal("restart did not service and reset the due cycle")
	}
	if garbage.owner != nil {
		t.Fatal("restarted automatic collector did not sweep garbage")
	}
}

func TestSuccessfulFinalizerRestoresOuterCollectionSchedule(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	collector, err := state.Global("collectgarbage")
	if err != nil {
		t.Fatal(err)
	}
	collectorSlot := slotFromValue(collector)
	restartArgument := stringSlot(
		state.runtime.strings.make("restart"),
	)
	weakTable, _ := newWeakTableForTest(t, state, "v", 0, 1)
	rootWeakTableForTest(t, state, weakTable)
	target := mustLoadString(
		t,
		state,
		"@successful-finalizer-schedule.lua",
		`return 42`,
	)
	probe := mustLoadString(
		t,
		state,
		"@successful-finalizer-probe.lua",
		`return true`,
	)
	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		if _, failure := frame.callCompactOne(
			collectorSlot,
			[]slot{restartArgument},
		); failure != nil {
			return frame.sealError(failure)
		}
		child := newTable(state, 0, 0)
		if err := weakTable.rawSetStringSlot(
			"held",
			slotFromTableObject(child),
		); err != nil {
			return frame.RaiseString(err.Error())
		}
		return frame.Return()
	})
	metatable := newFinalizerMetatable(
		t,
		state,
		slotFromFunctionObject(handler),
	)

	state.resetCollectionDebt()
	state.runtime.collection.budget = 1
	newFinalizerUserData(state, metatable, nil)
	if _, err := state.Call(target.Value()); err != nil {
		t.Fatal(err)
	}
	if state.runtime.collection.requested {
		t.Fatal("successful finalizer leaked its restart request")
	}
	if value, found := weakTable.rawStringSlot("held"); !found || value.kind() != TableKind {
		t.Fatal("same-cycle finalizer allocation did not survive")
	}

	if _, err := state.Call(probe.Value()); err != nil {
		t.Fatal(err)
	}
	if value, found := weakTable.rawStringSlot("held"); !found || value.kind() != TableKind {
		t.Fatal("redundant post-finalizer cycle cleared the weak value")
	}
}

func TestBaseCollectionCountIncludesRetainedStringBacking(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()
	collector, err := state.Global("collectgarbage")
	if err != nil {
		t.Fatal(err)
	}
	count := func() float64 {
		t.Helper()
		results, callErr := state.Call(
			collector,
			state.String("count"),
		)
		if callErr != nil {
			t.Fatal(callErr)
		}
		number, ok := results[0].AsNumber()
		if !ok {
			t.Fatalf("collection count = %v; want number", results)
		}
		return number
	}

	before := count()
	text := strings.Repeat("retained string ", 1<<16)
	value := state.String(text)
	if err := state.SetGlobal("largeRetainedString", value); err != nil {
		t.Fatal(err)
	}
	after := count()
	if delta := uint64((after - before) * 1024); delta < uint64(len(text)) {
		t.Fatalf(
			"retained string count delta = %d bytes; want at least %d",
			delta,
			len(text),
		)
	}

	if err := state.SetGlobal("largeRetainedString", Nil()); err != nil {
		t.Fatal(err)
	}
	if err := state.Collect(); err != nil {
		t.Fatal(err)
	}
	released := count()
	if released >= after {
		t.Fatalf(
			"count after releasing string = %v; retained count %v",
			released,
			after,
		)
	}
	runtime.KeepAlive(value)
	runtime.KeepAlive(text)
}

func TestBaseCollectionControlsValidateEveryAmountBeforeAction(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	called := false
	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		called = true
		return frame.Return()
	})
	newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(handler),
		),
		nil,
	)
	collector, err := state.Global("collectgarbage")
	if err != nil {
		t.Fatal(err)
	}

	options := []string{
		"stop",
		"restart",
		"collect",
		"count",
		"step",
		"setpause",
		"setstepmul",
	}
	for _, option := range options {
		_, err := state.Call(
			collector,
			state.String(option),
			Bool(true),
		)
		if err == nil ||
			!strings.Contains(err.Error(), "bad argument #2") ||
			!strings.Contains(err.Error(), "number expected, got boolean") {
			t.Fatalf("%s with invalid amount = %v", option, err)
		}
	}
	if called {
		t.Fatal("invalid collection amount ran a finalizer")
	}
	if state.runtime.collection.stopped ||
		state.runtime.collection.pause != defaultCollectionPause ||
		state.runtime.collection.stepMultiplier !=
			defaultCollectionStepMultiplier {
		t.Fatal("invalid collection amount changed control policy")
	}
}

type failingPrintWriter struct {
	err    error
	writes int
}

func (writer *failingPrintWriter) Write(text []byte) (int, error) {
	writer.writes++
	return 0, writer.err
}

func TestBasePrintUsesLua51ConversionAndOutputOrder(t *testing.T) {
	var output bytes.Buffer
	state := newStateWithBase(t, Options{Stdout: &output})
	defer state.Close()

	chunk := mustLoadString(t, state, "@print.lua", `
local calls=0
tostring=function(value)
	calls=calls+1
	if value=="bad" then return {} end
	if type(value)=="number" then return value end
	return value.."\000hidden"
end
print()
print(12,"text")
local ok,message=pcall(print,"first","bad")
return calls,ok,message
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != "\n12\ttext\nfirst" {
		t.Fatalf("print output = %q", output.String())
	}
	if len(results) != 3 {
		t.Fatalf("print results = %v", results)
	}
	assertTestValue(t, results[0], Number(4))
	assertTestValue(t, results[1], Bool(false))
	message, ok := results[2].AsString()
	if !ok || !strings.Contains(
		message,
		"'tostring' must return a string to 'print'",
	) {
		t.Fatalf("print failure = %v", results[2])
	}

	output.Reset()
	chunk = mustLoadString(t, state, "@print-lookup.lua", `
local calls=0
local function original(value)
	calls=calls+1
	tostring=function() return "replacement" end
	return "original"
end
tostring=original
print(1,2)
return calls
`)
	results, err = state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != "original\toriginal\n" {
		t.Fatalf("one-lookup output = %q", output.String())
	}
	assertTestValues(t, results, Number(2))
}

func TestBasePrintIgnoresWriterFailuresAndIsStateLocal(t *testing.T) {
	sentinel := errors.New("output failed")
	failing := &failingPrintWriter{err: sentinel}
	state := newStateWithBase(t, Options{Stdout: failing})
	chunk := mustLoadString(t, state, "@print-failure.lua", `print(1,2)`)
	if _, err := state.Call(chunk.Value()); err != nil {
		t.Fatal(err)
	}
	if failing.writes != 4 {
		t.Fatalf("writer calls = %d; want 4", failing.writes)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	var firstOutput, secondOutput bytes.Buffer
	first := newStateWithBase(t, Options{Stdout: &firstOutput})
	defer first.Close()
	second := newStateWithBase(t, Options{Stdout: &secondOutput})
	defer second.Close()
	firstChunk := mustLoadString(t, first, "@first-print.lua", `print("first")`)
	secondChunk := mustLoadString(t, second, "@second-print.lua", `print("second")`)
	if _, err := first.Call(firstChunk.Value()); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Call(secondChunk.Value()); err != nil {
		t.Fatal(err)
	}
	if firstOutput.String() != "first\n" ||
		secondOutput.String() != "second\n" {
		t.Fatalf(
			"state outputs = (%q, %q)",
			firstOutput.String(),
			secondOutput.String(),
		)
	}
}

func TestOpenBaseReopensIntoTheCurrentMainEnvironment(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	original, err := state.ThreadEnvironment(state.MainThread())
	if err != nil {
		t.Fatal(err)
	}
	originalPCall := original.RawGetString("pcall")
	originalCoroutine := original.RawGetString("coroutine")

	replacement, err := state.NewTable(0, 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetThreadEnvironment(
		state.MainThread(),
		replacement,
	); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	global, err := state.Global("_G")
	if err != nil {
		t.Fatal(err)
	}
	if same, applicable := global.SameObject(replacement.Value()); !applicable || !same {
		t.Fatal("reopened _G does not identify the replacement environment")
	}
	reopenedPCall, err := state.Global("pcall")
	if err != nil {
		t.Fatal(err)
	}
	if reopenedPCall.Kind() != FunctionKind {
		t.Fatalf("reopened pcall = %v; want function", reopenedPCall)
	}
	if same, applicable := reopenedPCall.SameObject(originalPCall); !applicable || same {
		t.Fatal("reopening reused the old environment's pcall")
	}
	reopenedFunction, _ := reopenedPCall.Function()
	if environment, environmentErr := state.FunctionEnvironment(
		reopenedFunction,
	); environmentErr != nil || environment != replacement {
		t.Fatalf(
			"reopened pcall environment = (%p, %v); want %p",
			environment,
			environmentErr,
			replacement,
		)
	}
	if same, applicable := original.RawGetString("pcall").SameObject(
		originalPCall,
	); !applicable || !same {
		t.Fatal("reopening changed the old environment's pcall")
	}
	if same, applicable := original.RawGetString("coroutine").SameObject(
		originalCoroutine,
	); !applicable || !same {
		t.Fatal("reopening changed the old environment's coroutine library")
	}
	if replacement.RawGetString("coroutine").Kind() != TableKind {
		t.Fatal("reopening did not install coroutine into the replacement environment")
	}
}

func TestBasePCallLua51ResultsAndArguments(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	testCases := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name: "no target results",
			source: `
return pcall(function() end)
`,
			want: []Value{Bool(true)},
		},
		{
			name: "all target results",
			source: `
return pcall(function(...) return ... end, 1, nil, 3, nil)
`,
			want: []Value{
				Bool(true),
				Number(1),
				Nil(),
				Number(3),
				Nil(),
			},
		},
		{
			name: "fixed caller adjustment",
			source: `
local one = pcall(function() return 1, 2 end)
local a, b, c, d = pcall(function() return 9 end)
return one, a, b, c, d
`,
			want: []Value{
				Bool(true),
				Bool(true),
				Number(9),
				Nil(),
				Nil(),
			},
		},
		{
			name: "explicit nil target is protected",
			source: `
return pcall(nil)
`,
			want: []Value{Bool(false)},
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(t, state, "@pcall.lua", test.source)
			results, callErr := state.Call(chunk.Value())
			if callErr != nil {
				t.Fatal(callErr)
			}
			if test.name == "explicit nil target is protected" {
				if len(results) != 2 ||
					results[0].Truth() ||
					!strings.Contains(
						results[1].String(),
						"attempt to call a nil value",
					) {
					t.Fatalf("pcall(nil) = %v", results)
				}
				return
			}
			assertTestValues(t, results, test.want...)
		})
	}

	missing := mustLoadString(t, state, "@missing-pcall.lua", `return pcall()`)
	if _, callErr := state.Call(missing.Value()); callErr == nil ||
		callErr.Error() !=
			"missing-pcall.lua:1: bad argument #1 to 'pcall' (value expected)" {
		t.Fatalf("pcall() error = %v", callErr)
	}
}

func TestBasePCallPreservesCallableAndErrorIdentity(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	callable, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	callableBody, err := state.NewNativeFunction(func(frame Frame) Outcome {
		first, _ := frame.Argument(0)
		second, _ := frame.Argument(1)
		return frame.ReturnValues(first, second)
	})
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__call", callableBody.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(callable.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("callable", callable.Value()); err != nil {
		t.Fatal(err)
	}

	marker, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Raise(marker.Value())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("raise_marker", raiser.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@pcall-identity.lua", `
local callOK, self, argument = pcall(callable, 17)
local raiseOK, value = pcall(raise_marker)
return callOK, self, argument, raiseOK, value
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Fatalf("result count = %d; want 5", len(results))
	}
	assertTestValue(t, results[0], Bool(true))
	if same, applicable := results[1].SameObject(callable.Value()); !applicable || !same {
		t.Fatal("__call did not receive the original callable table")
	}
	assertTestValue(t, results[2], Number(17))
	assertTestValue(t, results[3], Bool(false))
	if same, applicable := results[4].SameObject(marker.Value()); !applicable || !same {
		t.Fatal("pcall did not preserve the arbitrary error object")
	}
}

func TestBaseXPCallMatchesLua51HandlerSemantics(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	testCases := []struct {
		name   string
		source string
		want   []Value
	}{
		{
			name: "target gets no arguments and bad handler is ignored",
			source: `
return xpcall(function(...) return 3, ... end, 7, 8, 9)
`,
			want: []Value{Bool(true), Number(3)},
		},
		{
			name: "handler receives the error",
			source: `
return xpcall(
	function() return nil + 1 end,
	function(value) return value end
)
`,
			want: []Value{Bool(false)},
		},
		{
			name: "no handler result becomes nil",
			source: `
return xpcall(
	function() return nil + 1 end,
	function() end
)
`,
			want: []Value{Bool(false), Nil()},
		},
		{
			name: "only first handler result survives",
			source: `
return xpcall(
	function() return nil + 1 end,
	function() return 4, 5 end
)
`,
			want: []Value{Bool(false), Number(4)},
		},
		{
			name: "bad handler fails only when needed",
			source: `
return xpcall(function() return nil + 1 end, 7)
`,
			want: []Value{
				Bool(false),
				state.String("error in error handling"),
			},
		},
		{
			name: "failing handler becomes fixed error",
			source: `
return xpcall(
	function() return nil + 1 end,
	function() return nil + 2 end
)
`,
			want: []Value{
				Bool(false),
				state.String("error in error handling"),
			},
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(t, state, "@xpcall.lua", test.source)
			results, callErr := state.Call(chunk.Value())
			if callErr != nil {
				t.Fatal(callErr)
			}
			if test.name == "handler receives the error" {
				if len(results) != 2 ||
					results[0].Truth() ||
					!strings.Contains(
						results[1].String(),
						"attempt to perform arithmetic",
					) {
					t.Fatalf("xpcall transformed error = %v", results)
				}
				return
			}
			assertTestValues(t, results, test.want...)
		})
	}

	missing := mustLoadString(t, state, "@missing-xpcall.lua", `return xpcall(function() end)`)
	if _, callErr := state.Call(missing.Value()); callErr == nil ||
		callErr.Error() !=
			"missing-xpcall.lua:1: bad argument #2 to 'xpcall' (value expected)" {
		t.Fatalf("xpcall without handler error = %v", callErr)
	}
}

func TestBaseXPCallPreservesErrorIdentityAndRejectsCallableHandler(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	marker, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Raise(marker.Value())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("raise_marker", raiser.Value()); err != nil {
		t.Fatal(err)
	}

	callableHandler, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	handlerBody, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnString("must not run")
	})
	if err != nil {
		t.Fatal(err)
	}
	handlerMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := handlerMetatable.RawSetString("__call", handlerBody.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(callableHandler.Value(), handlerMetatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("callable_handler", callableHandler.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@xpcall-identity.lua", `
local firstOK, first = xpcall(raise_marker, function(value) return value end)
local secondOK, second = xpcall(raise_marker, callable_handler)
return firstOK, first, secondOK, second
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("result count = %d; want 4", len(results))
	}
	assertTestValue(t, results[0], Bool(false))
	if same, applicable := results[1].SameObject(marker.Value()); !applicable || !same {
		t.Fatal("xpcall handler did not receive the original error object")
	}
	assertTestValue(t, results[2], Bool(false))
	if message, ok := results[3].AsString(); !ok ||
		message != "error in error handling" {
		t.Fatalf("callable handler failure = %v", results[3])
	}
}

func TestBaseXPCallDiscardsExtraArgumentsBeforeCallingTarget(t *testing.T) {
	const valueLimit = 8
	state := newStateWithBase(t, Options{MaxValues: valueLimit})
	defer state.Close()

	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		call := frame.activation()
		if frame.depth != 2 {
			return frame.ReturnBool(false)
		}
		xpcall := frame.thread.frames[frame.depth-2]
		expectedBase := int(xpcall.base) + 2
		if int(call.resultBase) != expectedBase {
			return frame.ReturnBool(false)
		}
		for index := int(call.base); index < len(frame.thread.values); index++ {
			if frame.thread.values[index] != (slot{}) {
				return frame.ReturnBool(false)
			}
		}
		return frame.ReturnBool(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	xpcall, err := state.Global("xpcall")
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(
		xpcall,
		target.Value(),
		Nil(),
		Number(1),
		Number(2),
		Number(3),
		Number(4),
		Number(5),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true), Bool(true))
}

func TestProtectedCallsPreserveAnExplicitNilError(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Raise(Nil())
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnBool(
			frame.ArgumentCount() == 1 &&
				frame.Kind(0) == NilKind,
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("raise_nil", raiser.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("handle_nil", handler.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@nil-error.lua", `
local firstOK, first = pcall(raise_nil)
local secondOK, second = xpcall(raise_nil, handle_nil)
return firstOK, first, secondOK, second
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Bool(false),
		Nil(),
		Bool(false),
		Bool(true),
	)
}

func TestBaseErrorPreservesObjectIdentityAndHonorsLuaLevels(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	marker, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("marker", marker.Value()); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(t, state, "@base-error-level.lua", `local objectOK,object=pcall(function() error(marker) end)
local function inner(level)
	error("boom",level)
end
local function outer(level)
	return pcall(inner,level)
end
local a,b=outer(1); local c,d=outer(2); local e,f=outer(3)
return objectOK,object,a,b,c,d,e,f`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 8 {
		t.Fatalf("result count = %d; want 8", len(results))
	}
	assertTestValue(t, results[0], Bool(false))
	if same, applicable := results[1].SameObject(marker.Value()); !applicable || !same {
		t.Fatal("error did not preserve the arbitrary error object")
	}
	assertTestValues(
		t,
		results[2:],
		Bool(false),
		state.String("base-error-level.lua:3: boom"),
		Bool(false),
		state.String("boom"),
		Bool(false),
		state.String("base-error-level.lua:6: boom"),
	)
}

func TestBaseToStringCallsMetamethodWithCanonicalObjects(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	target, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	receivedTarget := false
	method, err := state.NewNativeFunction(func(frame Frame) Outcome {
		value, present := frame.Argument(0)
		if present {
			receivedTarget, _ = value.SameObject(target.Value())
		}
		return frame.ReturnValue(marker.Value())
	})
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__tostring", method.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(target.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	tostring, err := state.Global("tostring")
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(tostring, target.Value())
	if err != nil {
		t.Fatal(err)
	}
	if !receivedTarget {
		t.Fatal("__tostring did not receive the canonical target")
	}
	if len(results) != 1 {
		t.Fatalf("result count = %d; want 1", len(results))
	}
	if same, applicable := results[0].SameObject(marker.Value()); !applicable || !same {
		t.Fatal("tostring did not preserve the metamethod's arbitrary result")
	}
}

func TestBaseMetatableProtectionPreservesSentinelIdentity(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	target, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	sentinel, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := actual.RawSetString("__metatable", sentinel.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(target.Value(), actual); err != nil {
		t.Fatal(err)
	}

	getmetatable, err := state.Global("getmetatable")
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(getmetatable, target.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("getmetatable returned %d values; want 1", len(results))
	}
	if same, applicable := results[0].SameObject(sentinel.Value()); !applicable || !same {
		t.Fatal("getmetatable did not preserve the protected sentinel")
	}

	setmetatable, err := state.Global("setmetatable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(setmetatable, target.Value(), Nil()); err == nil ||
		err.Error() != "cannot change a protected metatable" {
		t.Fatalf("protected setmetatable error = %v", err)
	}
	current, err := state.Metatable(target.Value())
	if err != nil {
		t.Fatal(err)
	}
	if current != actual {
		t.Fatal("failed setmetatable changed the protected metatable")
	}
}

// Lua 5.1 delegates decimal conversion to the platform strtod, whose handling
// of embedded NUL and named non-finite values varies. Badger's decimal grammar
// is deliberately byte-complete and deterministic. Explicit nondecimal bases
// retain strtoul's C-string boundary for Lua 5.1 compatibility.
func TestBaseToNumberUsesTheDeterministicDecimalGrammar(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	chunk := mustLoadString(t, state, "@tonumber-grammar.lua",
		`return tonumber("10\000junk",10),tonumber("10\000junk",2)`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Nil(), Number(2))
}

func TestWarmBaseLibraryCallsDoNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newStateWithBase(t, Options{Stdout: io.Discard})
	defer state.Close()

	testCases := []struct {
		name   string
		source string
	}{
		{
			name: "scalar and raw operations",
			source: `local a,eq,get,set,kind,choose,number,text=assert,rawequal,rawget,rawset,type,select,tonumber,tostring
local t={1,2,3,x=4}
return function()
	local total=0
	for i=1,100 do
		a(true)
		if eq(t,t) and kind(t)=="table" then total=total+1 end
		total=total+get(t,1)+choose(2,0,2)+number("12")
		set(t,"x",get(t,"x")+1)
		if text(false)=="false" then total=total+1 end
		if text(12.5)=="12.5" then total=total+1 end
	end
	return total
end`,
		},
		{
			name: "iteration and unpack",
			source: `local t={1,2,3}
local pairs,ipairs,unpack=pairs,ipairs,unpack
return function()
	local total=0
	for repeatIndex=1,20 do
		for _,value in pairs(t) do total=total+value end
		for _,value in ipairs(t) do total=total+value end
		local a,b,c=unpack(t)
		total=total+a+b+c
	end
	return total
end`,
		},
		{
			name: "metatable access",
			source: `local get,set,eq=getmetatable,setmetatable,rawequal
local mt={}
local t=set({},mt)
return function()
	local total=0
	for i=1,100 do
		if eq(get(t),mt) and eq(set(t,mt),t) then total=total+1 end
	end
	return total
end`,
		},
		{
			name: "print",
			source: `local print=print
return function()
	for i=1,100 do print(12.5,false) end
	return true
end`,
		},
		{
			name: "function and thread environments",
			source: `local get,set=getfenv,setfenv
local functionEnvironment=get(1)
local threadEnvironment=get(0)
local target=function() end
set(target,functionEnvironment)
return function()
	local total=0
	for i=1,100 do
		set(target,functionEnvironment)
		set(0,threadEnvironment)
		if get(target)==functionEnvironment and
			get(1)==functionEnvironment and
			get(0)==threadEnvironment then
			total=total+1
		end
	end
	return total
end`,
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(t, state, "@base-allocation.lua", test.source)
			results, err := state.Call(chunk.Value())
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 {
				t.Fatalf("loader produced %d results; want 1", len(results))
			}
			body := results[0]
			var destination [1]Value
			for index := 0; index < 64; index++ {
				if _, err := state.CallInto(body, nil, destination[:]); err != nil {
					t.Fatal(err)
				}
			}
			allocations := testing.AllocsPerRun(64, func() {
				if _, err := state.CallInto(body, nil, destination[:]); err != nil {
					t.Fatal(err)
				}
			})
			if allocations != 0 {
				t.Fatalf("warm calls allocated %v times per run", allocations)
			}
		})
	}
}

func TestBaseLibraryCasesMatchLua51(t *testing.T) {
	runLua51Cases(t, baseLibraryLua51Cases)
}

var baseLibraryLua51Cases = []lua51Case{
	{
		name: "collection_control_values_and_types",
		source: `local a=collectgarbage("stop")
local b=collectgarbage("restart")
local c=collectgarbage()
local d=collectgarbage("setpause",123.9)
local e=collectgarbage("setpause",200)
local f=collectgarbage("setstepmul","145.9")
local g=collectgarbage("setstepmul",200)
local h=collectgarbage("step",1000000)
return type(a),a,type(b),b,type(c),c,d,e,f,g,type(h),h`,
		want: "ok 'number' 0 'number' 0 'number' 0 200 123 200 145 'boolean' true",
	},
	{
		name:   "collection_option_and_unconditional_amount_validation",
		source: `local a,b=pcall(collectgarbage,"bogus"); local c,d=pcall(collectgarbage,1); local e,f=pcall(collectgarbage,"count",{}); local g,h=pcall(collectgarbage,"collect\000junk"); return a,b,c,d,e,f,g,h`,
		want:   "ok false 'bad argument #1 to '?' (invalid option 'bogus')' false 'bad argument #1 to '?' (invalid option '1')' false 'bad argument #2 to '?' (number expected, got table)' true 0",
	},
	{
		name:   "collection_count_and_gcinfo_types",
		source: `return type(collectgarbage("count")),type(gcinfo()),collectgarbage("count")>=gcinfo(),collectgarbage("count")<gcinfo()+1`,
		want:   "ok 'number' 'number' true true",
	},
	{
		name:   "newproxy_plain_fresh_and_shared_metatables",
		source: `local a=newproxy(); local b=newproxy(false); local p=newproxy(true); local mt=getmetatable(p); local q=newproxy(p); local r=newproxy(true); return type(a),getmetatable(a)==nil,type(b),getmetatable(b)==nil,rawequal(getmetatable(q),mt),rawequal(getmetatable(r),mt)`,
		want:   "ok 'userdata' true 'userdata' true true false",
	},
	{
		name:   "newproxy_accepts_registered_metatables_and_bypasses_protection",
		source: `local p=newproxy(true); local mt=getmetatable(p); local t=setmetatable({},mt); local q=newproxy(t); mt.__metatable="sealed"; local r=newproxy(p); local protected=getmetatable(r); mt.__metatable=nil; return type(q),rawequal(getmetatable(q),mt),protected,rawequal(getmetatable(r),mt)`,
		want:   "ok 'userdata' true 'sealed' true",
	},
	{
		name:   "newproxy_rejects_unregistered_truthy_values",
		source: `local a,b=pcall(newproxy,1); local c,d=pcall(newproxy,{}); local e,f=pcall(newproxy,newproxy(false)); return a,b,c,d,e,f`,
		want:   "ok false 'bad argument #1 to '?' (boolean or proxy expected)' false 'bad argument #1 to '?' (boolean or proxy expected)' false 'bad argument #1 to '?' (boolean or proxy expected)'",
	},
	{
		name:   "newproxy_validates_the_arguments_actual_metatable",
		source: `local proxy=newproxy(true); local mt=getmetatable(proxy); local a,b=pcall(newproxy,mt); mt.__metatable="sealed"; local fake=setmetatable({},{__metatable="sealed"}); local c,d=pcall(newproxy,fake); return a,b,c,d`,
		want:   "ok false 'bad argument #1 to '?' (boolean or proxy expected)' false 'bad argument #1 to '?' (boolean or proxy expected)'",
	},
	{
		name:   "newproxy_direct_and_method_argument_diagnostics",
		source: `local a,b=pcall(function() return newproxy(1) end); local holder={make=newproxy}; local c,d=pcall(function() return holder:make() end); return a,b,c,d`,
		want:   "ok false 'case:1: bad argument #1 to 'newproxy' (boolean or proxy expected)' false 'case:1: calling 'make' on bad self (boolean or proxy expected)'",
	},
	{
		name:   "newproxy_registry_survives_finalizer_separation",
		source: `local created,shared; do local proxy=newproxy(true); local mt=getmetatable(proxy); mt.__gc=function(self) mt.__gc=nil; created=newproxy(self); shared=rawequal(getmetatable(created),mt) end end; collectgarbage(); return type(created),shared`,
		want:   "ok 'userdata' true",
	},
	{
		name:   "assert_returns_every_argument",
		source: `local a,b,c=assert("yes",nil,3); return a,b,c,select("#",assert(true,nil,3))`,
		want:   "ok 'yes' nil 3 3",
	},
	{
		name:   "assert_default_and_numeric_messages",
		source: `local a,b=pcall(assert,false); local c,d=pcall(assert,false,23); return a,b,c,d`,
		want:   "ok false 'assertion failed!' false '23'",
	},
	{
		name:   "assert_formats_message_through_c_string_boundary",
		source: `local ok,message=pcall(assert,false,"a\000b"); return ok,message`,
		want:   "ok false 'a'",
	},
	{
		name:   "error_preserves_values_and_level_zero_text",
		source: `local marker={}; local a,b=pcall(error,marker); local c,d=pcall(error,"plain",0); return a,rawequal(b,marker),c,d`,
		want:   "ok false true false 'plain'",
	},
	{
		name:   "error_without_a_value_raises_nil",
		source: `local count=select("#",pcall(error)); local ok,value=pcall(error); return count,ok,value`,
		want:   "ok 2 false nil",
	},
	{
		name: "error_levels",
		source: `local function inner(level) error("boom",level) end
local function outer(level) return pcall(inner,level) end
local a,b=outer(1); local c,d=outer(2); local e,f=outer(3)
return a,b,c,d,e,f`,
		want: "ok false 'case:1: boom' false 'boom' false 'case:2: boom'",
	},
	{
		name:   "function_environments_and_running_function_replacement",
		source: `local gf,sf=getfenv,setfenv; local first={x=1}; local second={x=2}; local f=function() return x end; local same=sf(f,first); sf(1,second); local current=x; return gf()==second,gf(1)==second,gf(f)==first,same==f,f(),current`,
		want:   "ok true true true true 1 2",
	},
	{
		name:   "thread_environment_is_distinct_and_setfenv_zero_returns_nothing",
		source: `local gf,sf=getfenv,setfenv; local original=gf(0); local functionenv=gf(1); local e={}; local n=select("#",sf(0,e)); local threadSame=gf(0)==e; local functionSame=gf(1)==functionenv; sf(0,original); return n,threadSame,functionSame`,
		want:   "ok 0 true true",
	},
	{
		name:   "environment_numeric_targets_follow_lua51_conversion",
		source: `local gf,sf,pc=getfenv,setfenv,pcall; local original=gf(0); local functionenv=gf(1); local environment={}; local sameDefault=gf(nil)==functionenv; local count=select("#",sf("0",environment)); local sameThread=gf(0)==environment; local ok,message=pc(sf,0.5,{}); local unchanged=gf(0)==environment; sf(0,original); return sameDefault,count,sameThread,ok,message,unchanged`,
		want:   "ok true 0 true false ''setfenv' cannot change environment of given object' true",
	},
	{
		name:   "coroutine_environment_inheritance_isolation_and_suspension",
		source: `local gf,sf=getfenv,setfenv; local create,resume,yield=coroutine.create,coroutine.resume,coroutine.yield; local original=gf(0); local a,b,c={},{},{}; sf(0,a); local first=create(function() local inherited=gf(0)==a; sf(0,c); local nested=create(function() return gf(0)==c end); local ok,nestedInherited=resume(nested); local resumed=yield(inherited,gf(0)==c,ok,nestedInherited); return gf(0)==c,resumed end); sf(0,b); local ok1,i,r,okn,n=resume(first); local main1=gf(0)==b; local ok2,persist,arg=resume(first,77); local second=create(function() return gf(0)==b end); local ok3,sibling=resume(second); local main2=gf(0)==b; sf(0,original); return ok1,i,r,okn,n,main1,ok2,persist,arg,ok3,sibling,main2`,
		want:   "ok true true true true true true true true 77 true true true",
	},
	{
		name:   "native_getfenv_uses_thread_environment_and_setfenv_rejects_it",
		source: `local gf,sf,pc=getfenv,setfenv,pcall; local original=gf(0); local native=pc; local environment={}; sf(0,environment); local ok,message=pc(sf,native,{}); local same=gf(native)==environment; sf(0,original); return same,ok,message`,
		want:   "ok true false ''setfenv' cannot change environment of given object'",
	},
	{
		name:   "environment_levels_distinguish_native_lua_and_tail_frames",
		source: `local gf,sf,pc=getfenv,setfenv,pcall; local thread=gf(0); local probeEnvironment={}; local function probe(level) local ok,value=pc(gf,level); if not ok then return false,value end; if value==thread then return true,"thread" elseif value==probeEnvironment then return true,"probe" else return true,"other" end end; sf(probe,probeEnvironment); local function target(level) return probe(level) end; local function tail(level) return target(level) end; local a,b=tail(1); local c,d=tail(2); local e,f=tail(3); local g,h=tail(5); return a,b,c,d,e,f,g,h`,
		want:   "ok true 'thread' true 'probe' false 'no function environment for tail call at level 3' true 'thread'",
	},
	{
		name:   "setfenv_tail_level_and_argument_validation",
		source: `local gf,sf,pc=getfenv,setfenv,pcall; local environment={}; local function probe(level) return pc(sf,level,environment) end; local function target(level) return probe(level) end; local function tail(level) return target(level) end; local a,b=tail(3); local c,d=pc(gf,-1); local e,f=pc(gf,999); local g,h=pc(sf,-1,nil); return a,b,c,d,e,f,g,h`,
		want:   "ok false 'no function environment for tail call at level 3' false 'bad argument #1 to '?' (level must be non-negative)' false 'bad argument #1 to '?' (invalid level)' false 'bad argument #2 to '?' (table expected, got nil)'",
	},
	{
		name:   "metatable_install_remove_and_identity",
		source: `local mt={tag=1}; local t=setmetatable({},mt); return rawequal(getmetatable(t),mt),rawequal(setmetatable(t,nil),t),getmetatable(t)`,
		want:   "ok true true nil",
	},
	{
		name:   "protected_metatable",
		source: `local mt={__metatable="sealed"}; local t=setmetatable({},mt); local ok,e=pcall(setmetatable,t,{}); return getmetatable(t),ok,e`,
		want:   "ok 'sealed' false 'cannot change a protected metatable'",
	},
	{
		name:   "rawequal_ignores_equal_metamethod",
		source: `local mt={__eq=function() return true end}; local a=setmetatable({},mt); local b=setmetatable({},mt); return a==b,rawequal(a,b),rawequal(a,a),rawequal(1,"1"),rawequal(0,-0)`,
		want:   "ok true false true false true",
	},
	{
		name:   "rawget_rawset_ignore_metamethods",
		source: `local mt={__index=function() return 99 end,__newindex=function() error("used") end}; local t=setmetatable({},mt); local same=rawset(t,"x",7); return rawequal(same,t),rawget(t,"x"),rawget(t,"missing"),t.missing`,
		want:   "ok true 7 nil 99",
	},
	{
		name:   "rawset_rejects_nil_and_nan_keys",
		source: `local a,b=pcall(rawset,{},nil,1); local c,d=pcall(rawset,{},0/0,1); return a,b,c,d`,
		want:   "ok false 'table index is nil' false 'table index is NaN'",
	},
	{
		name:   "type_names_every_available_lua_kind",
		source: `return type(nil),type(false),type(1),type("x"),type({}),type(function() end),type(coroutine.create(function() end))`,
		want:   "ok 'nil' 'boolean' 'number' 'string' 'table' 'function' 'thread'",
	},
	{
		name:   "next_walks_array_and_reports_end",
		source: `local t={10,20}; local k1,v1=next(t); local k2,v2=next(t,k1); local k3=next(t,k2); return k1,v1,k2,v2,k3`,
		want:   "ok 1 10 2 20 nil",
	},
	{
		name:   "next_rejects_unknown_continuation",
		source: `local ok,e=pcall(next,{[1]=1},2); return ok,e`,
		want:   "ok false 'invalid key to 'next''",
	},
	{
		name:   "pairs_captures_next_and_ignores_pairs_metamethod",
		source: `local hit=false; local t=setmetatable({a=1,b=2},{__pairs=function() hit=true end}); local f,s,k=pairs(t); local sum,count=0,0; for _,v in f,s,k do sum=sum+v; count=count+1 end; return rawequal(f,next),rawequal(s,t),k,sum,count,hit`,
		want:   "ok false true nil 3 2 false",
	},
	{
		name:   "pairs_iterator_is_private_and_ignores_reassigned_next",
		source: `next=function() error("replacement used") end; local t={4,5}; local f,s,k=pairs(t); local sum=0; for _,v in f,s,k do sum=sum+v end; return rawequal(f,next),sum`,
		want:   "ok false 9",
	},
	{
		name:   "ipairs_is_raw_and_stops_at_first_hole",
		source: `local t=setmetatable({10,20,nil,40},{__index=function() return 99 end}); local f,s,k=ipairs(t); local sum,count=0,0; for _,v in f,s,k do sum=sum+v; count=count+1 end; return rawequal(s,t),k,sum,count`,
		want:   "ok true 0 30 2",
	},
	{
		name:   "select_counts_and_slices_arguments",
		source: `local n=select("#",1,nil,3); local weird=select("#anything",1,2); local a,b=select(2,"a","b","c"); local c,d=select(-2,"a","b","c"); return n,weird,a,b,c,d,select(99,1,2)`,
		want:   "ok 3 2 'b' 'c' 'b' 'c'",
	},
	{
		name:   "select_rejects_zero_and_too_negative_indexes",
		source: `local a,b=pcall(select,0,1,2); local c,d=pcall(select,-4,1,2); return a,b,c,d`,
		want:   "ok false 'bad argument #1 to '?' (index out of range)' false 'bad argument #1 to '?' (index out of range)'",
	},
	{
		name:   "unpack_preserves_holes_and_empty_ranges",
		source: `local t={[1]="a",[3]="c"}; local a,b,c=unpack(t,1,3); return select("#",unpack(t,1,3)),a,b,c,select("#",unpack(t,4,2))`,
		want:   "ok 3 'a' nil 'c' 0",
	},
	{
		name:   "unpack_honors_explicit_bounds",
		source: `return unpack({10,20,30},2,2),select("#",unpack({},1,0))`,
		want:   "ok 20 0",
	},
	{
		name:   "tonumber_standard_conversion",
		source: `return tonumber(12),tonumber("  -12.5 "),tonumber("0x10"),tonumber("1x"),tonumber(true),tonumber("bad")`,
		want:   "ok 12 -12.5 16 nil nil nil",
	},
	{
		name:   "tonumber_explicit_bases",
		source: `return tonumber("101",2),tonumber("  +ff ",16),tonumber("0xff",16),tonumber("z",36),tonumber("12",8),tonumber("2",2)`,
		want:   "ok 5 255 255 35 10 nil",
	},
	{
		name:   "tonumber_base_validation_order",
		source: `local a,b=pcall(tonumber,{},1); local c,d=pcall(tonumber,"10",1); return a,b,c,d`,
		want:   "ok false 'bad argument #1 to '?' (string expected, got table)' false 'bad argument #2 to '?' (base out of range)'",
	},
	{
		name:   "tostring_primitives_and_reference_kinds",
		source: `local t={}; local f=function() end; return tostring(nil),tostring(false),tostring(12.5),tostring("x"),type(tostring(t)),type(tostring(f)),tostring(12.5)=="12.5"`,
		want:   "ok 'nil' 'false' '12.5' 'x' 'string' 'string' true",
	},
	{
		name:   "tostring_returns_arbitrary_metamethod_result",
		source: `local marker={}; local target=setmetatable({},{__tostring=function(self) return marker end}); return rawequal(tostring(target),marker)`,
		want:   "ok true",
	},
}

func newStateWithBase(t testing.TB, options Options) *State {
	t.Helper()
	state, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state
}

// lua51Case is one recorded differential case. Each source is a complete Lua
// 5.1 chunk and want is the outcome PUC Lua 5.1.5 produces for it, in the
// shared spelling formatLua51Value defines.
//
// Recorded expectations are verified against a real interpreter by
// TestLua51OracleMatchesLibraryCases. Regenerate or re-verify with:
//
//	BADGER_LUA51=/path/to/lua-5.1.5/src/lua go test -run OracleMatches -v
//
// and record new cases with BADGER_LUA51_RECORD=1 set as well.
type lua51Case struct {
	name   string
	source string
	want   string
}

// runLua51Case executes source on a State with every implemented library open
// and reports the outcome the recorded Lua 5.1 driver would print.
func runLua51Case(t *testing.T, source string) string {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenPackage(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenMath(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenTable(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenIO(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenOS(); err != nil {
		t.Fatal(err)
	}
	chunk, err := state.LoadString("=case", source)
	if err != nil {
		return "syntax " + formatLua51Text(err.Error())
	}
	results, err := state.Call(chunk.Value())
	if err != nil {
		var failure *Error
		if errors.As(err, &failure) {
			return "error " + formatLua51Value(failure.Value())
		}
		return "error " + formatLua51Text(err.Error())
	}
	parts := make([]string, 0, len(results)+1)
	parts = append(parts, "ok")
	for _, value := range results {
		parts = append(parts, formatLua51Value(value))
	}
	return strings.Join(parts, " ")
}

// formatLua51Value spells one result the way the Lua driver does. Numbers use
// Lua 5.1's own %.14g primitive, with non-finite values normalized because
// their C-library spelling is platform-dependent.
func formatLua51Value(value Value) string {
	switch value.Kind() {
	case NumberKind:
		number, _ := value.AsNumber()
		switch {
		case math.IsNaN(number):
			return "nan"
		case math.IsInf(number, 1):
			return "inf"
		case math.IsInf(number, -1):
			return "-inf"
		}
		return value.String()
	case StringKind:
		text, _ := value.AsString()
		return formatLua51Text(text)
	case NilKind, BoolKind:
		return value.String()
	default:
		return value.Kind().String()
	}
}

func formatLua51Text(text string) string {
	return "'" + text + "'"
}

func runLua51Cases(t *testing.T, cases []lua51Case) {
	t.Helper()
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := runLua51Case(t, test.source); got != test.want {
				t.Fatalf(
					"%s\n got: %s\nwant: %s",
					test.source,
					got,
					test.want,
				)
			}
		})
	}
}

// TestLua51OracleMatchesLibraryCases re-derives every recorded expectation
// from a real Lua 5.1 interpreter. It is skipped unless BADGER_LUA51 names one,
// because the reference binary is deliberately not carried in this repository.
func TestLua51OracleMatchesLibraryCases(t *testing.T) {
	binary := os.Getenv("BADGER_LUA51")
	if binary == "" {
		t.Skip("set BADGER_LUA51 to a Lua 5.1 interpreter to verify")
	}
	cases := make(
		[]lua51Case,
		0,
		len(baseLibraryLua51Cases)+
			len(loadLibraryLua51Cases)+
			len(packageLibraryLua51Cases)+
			len(mathLibraryLua51Cases)+
			len(tableLibraryLua51Cases)+
			len(stringLibraryLua51Cases)+
			len(ioLibraryLua51Cases)+
			len(osLibraryLua51Cases),
	)
	cases = append(cases, baseLibraryLua51Cases...)
	cases = append(cases, loadLibraryLua51Cases...)
	cases = append(cases, packageLibraryLua51Cases...)
	cases = append(cases, mathLibraryLua51Cases...)
	cases = append(cases, tableLibraryLua51Cases...)
	cases = append(cases, stringLibraryLua51Cases...)
	cases = append(cases, ioLibraryLua51Cases...)
	cases = append(cases, osLibraryLua51Cases...)

	driver := &strings.Builder{}
	driver.WriteString(lua51OracleDriver)
	driver.WriteString("local sources = {\n")
	for _, test := range cases {
		driver.WriteString(quoteLuaString(test.source))
		driver.WriteString(",\n")
	}
	driver.WriteString("}\nrun(sources)\n")

	path := filepath.Join(t.TempDir(), "oracle.lua")
	if err := os.WriteFile(path, []byte(driver.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(binary, path).Output()
	if err != nil {
		t.Fatalf("%s: %v", binary, err)
	}
	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("oracle produced %d lines; want %d", len(lines), len(cases))
	}
	record := os.Getenv("BADGER_LUA51_RECORD") != ""
	for index, test := range cases {
		if record {
			t.Logf("{\n\tname:   %q,\n\tsource: %q,\n\twant:   %q,\n},", test.name, test.source, lines[index])
			continue
		}
		if lines[index] != test.want {
			t.Errorf(
				"%s: oracle disagrees with the recorded expectation\n%s\n got: %s\nwant: %s",
				test.name,
				test.source,
				lines[index],
				test.want,
			)
		}
	}
}

// lua51OracleDriver formats results exactly as formatLua51Value does.
const lua51OracleDriver = `
local positiveInfinity = math.huge
local negativeInfinity = -positiveInfinity

local function fmt(v)
  local t = type(v)
  if t == "number" then
    if v ~= v then return "nan" end
    if v == positiveInfinity then return "inf" end
    if v == negativeInfinity then return "-inf" end
    return string.format("%.14g", v)
  elseif t == "string" then
    return "'" .. v .. "'"
  elseif t == "nil" or t == "boolean" then
    return tostring(v)
  end
  return t
end

local function collect(...)
  local n = select("#", ...)
  local out = { (select(1, ...)) and "ok" or "error" }
  for i = 2, n do out[#out + 1] = fmt((select(i, ...))) end
  return table.concat(out, " ")
end

function run(sources)
  for i = 1, #sources do
    local chunk, err = loadstring(sources[i], "=case")
    if not chunk then
      io.write("syntax '", tostring(err), "'\n")
    else
      io.write(collect(pcall(chunk)), "\n")
    end
  end
end
`

// quoteLuaString spells text as a Lua 5.1 double-quoted literal.
func quoteLuaString(text string) string {
	quoted := &strings.Builder{}
	quoted.WriteByte('"')
	for index := 0; index < len(text); index++ {
		switch character := text[index]; character {
		case '"', '\\':
			quoted.WriteByte('\\')
			quoted.WriteByte(character)
		case '\n':
			quoted.WriteString("\\n")
		case '\r':
			quoted.WriteString("\\r")
		case '\t':
			quoted.WriteString("\\t")
		default:
			if character < 0x20 || character == 0x7f {
				fmt.Fprintf(quoted, "\\%03d", character)
				continue
			}
			quoted.WriteByte(character)
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}
