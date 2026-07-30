package lua

import (
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOSDateUsesDeterministicCLocale(t *testing.T) {
	state := newStateWithLocation(t, time.FixedZone("TEST", -6*60*60))
	defer state.Close()
	chunk := mustLoadString(t, state, "@date.lua", `
local utc = os.date(
  "!%a|%A|%b|%B|%c|%C|%d|%D|%e|%F|%g|%G|%h|" ..
  "%H|%I|%j|%k|%l|%m|%M|%p|%r|%R|%S|%T|%u|%U|" ..
  "%V|%w|%W|%x|%X|%y|%Y|%Z|%%|%Q",
  0
)
local localTime = os.date("%Y-%m-%d %H:%M:%S %z %Z", 0)
return utc, localTime, os.date("!!%Y", 0),
  os.date("!*tX", 0), os.date("abc%", 0),
  os.date("abc\000%Y", 0)
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String(
			"Thu|Thursday|Jan|January|Thu Jan  1 00:00:00 1970|"+
				"19|01|01/01/70| 1|1970-01-01|70|1970|Jan|"+
				"00|12|001| 0|12|01|00|AM|12:00:00 AM|00:00|"+
				"00|00:00:00|4|00|01|4|00|01/01/70|00:00:00|"+
				"70|1970|UTC|%|Q",
		),
		state.String("1969-12-31 18:00:00 -0600 TEST"),
		state.String("!1970"),
		state.String("*tX"),
		state.String("abc%"),
		state.String("abc"),
	)
}

func TestOSDateTableUsesCompactCalendarFields(t *testing.T) {
	state := newStateWithLocation(t, time.FixedZone("TEST", -6*60*60))
	defer state.Close()
	chunk := mustLoadString(t, state, "@date-table.lua", `
local utc = os.date("!*t", 0)
local localTime = os.date("*t", 0)
return
  utc.sec, utc.min, utc.hour, utc.day, utc.month, utc.year,
  utc.wday, utc.yday, utc.isdst,
  localTime.hour, localTime.day, localTime.month, localTime.year,
  localTime.wday, localTime.yday, localTime.isdst
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Number(0), Number(0), Number(0),
		Number(1), Number(1), Number(1970),
		Number(5), Number(1), Bool(false),
		Number(18), Number(31), Number(12), Number(1969),
		Number(4), Number(365), Bool(false),
	)
}

func TestOSDateUsesStateClockAndLocation(t *testing.T) {
	now := func() time.Time {
		return time.Unix(0, 0)
	}
	first := newStateWithOSOptions(t, Options{
		Location: time.FixedZone("WEST", -6*60*60),
		Now:      now,
	})
	defer first.Close()
	second := newStateWithOSOptions(t, Options{
		Location: time.FixedZone("EAST", 5*60*60+30*60),
		Now:      now,
	})
	defer second.Close()

	source := `return os.time(), os.date("%Y-%m-%d %H:%M %z %Z")`
	for _, test := range []struct {
		name  string
		state *State
		text  string
	}{
		{
			name:  "west",
			state: first,
			text:  "1969-12-31 18:00 -0600 WEST",
		},
		{
			name:  "east",
			state: second,
			text:  "1970-01-01 05:30 +0530 EAST",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(
				t,
				test.state,
				"@state-time.lua",
				source,
			)
			results, err := test.state.Call(chunk.Value())
			if err != nil {
				t.Fatal(err)
			}
			assertTestValues(
				t,
				results,
				Number(0),
				test.state.String(test.text),
			)
		})
	}
}

func TestOSTimeReadsThroughIndexInLua51Order(t *testing.T) {
	state := newStateWithLocation(t, time.UTC)
	defer state.Close()
	chunk := mustLoadString(t, state, "@time-index.lua", `
local fields = {
  day = 1,
  month = 1,
  year = 1970,
}
local order = {}
local source = setmetatable({}, {
  __index = function(_, key)
    order[#order + 1] = key
    return fields[key]
  end,
})
local stamp = os.time(source, "ignored")
return stamp, table.concat(order, ","),
  source.sec, source.min, source.hour
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Number(12*60*60),
		state.String("sec,min,hour,day,month,year,isdst"),
		Nil(), Nil(), Nil(),
	)
}

func TestOSTimeResolvesDaylightTransitionsLikeMktime(t *testing.T) {
	testCases := []struct {
		name     string
		location string
		source   string
		want     []Value
	}{
		{
			name:     "New York one-hour transitions",
			location: "America/New_York",
			source: `
local function stamp(month, day, hour, min, isdst)
  return os.time {
    year = 2024, month = month, day = day,
    hour = hour, min = min, isdst = isdst,
  }
end
return
  stamp(3, 10, 2, 30, nil),
  stamp(3, 10, 2, 30, false),
  stamp(3, 10, 2, 30, true),
  stamp(11, 3, 1, 30, nil),
  stamp(11, 3, 1, 30, false),
  stamp(11, 3, 1, 30, true)
`,
			want: []Value{
				Number(1710055800),
				Number(1710055800),
				Number(1710052200),
				Number(1730611800),
				Number(1730615400),
				Number(1730611800),
			},
		},
		{
			name:     "Lord Howe half-hour transitions",
			location: "Australia/Lord_Howe",
			source: `
local function stamp(month, day, hour, min, isdst)
  return os.time {
    year = 2024, month = month, day = day,
    hour = hour, min = min, isdst = isdst,
  }
end
return
  stamp(4, 7, 1, 45, nil),
  stamp(4, 7, 1, 45, false),
  stamp(4, 7, 1, 45, true),
  stamp(10, 6, 2, 15, nil),
  stamp(10, 6, 2, 15, false),
  stamp(10, 6, 2, 15, true)
`,
			want: []Value{
				Number(1712414700),
				Number(1712416500),
				Number(1712414700),
				Number(1728143100),
				Number(1728143100),
				Number(1728141300),
			},
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			location, err := time.LoadLocation(test.location)
			if err != nil {
				t.Skipf("timezone database unavailable: %v", err)
			}
			state := newStateWithLocation(t, location)
			defer state.Close()
			chunk := mustLoadString(
				t,
				state,
				"@time-transition.lua",
				test.source,
			)
			results, err := state.Call(chunk.Value())
			if err != nil {
				t.Fatal(err)
			}
			assertTestValues(t, results, test.want...)
		})
	}

	zone := strings.Repeat("Z", 256)
	date := time.Date(
		2020,
		1,
		2,
		3,
		4,
		5,
		0,
		time.FixedZone(zone, 0),
	)
	formatted, tooLarge := formatDate("%+", date)
	if tooLarge {
		t.Fatal("long timezone name exceeded construction limit")
	}
	if len(formatted) != datePlusLength(date) ||
		!strings.Contains(string(formatted), zone) {
		t.Fatalf("%%+ length/content = %d, %q", len(formatted), formatted)
	}
}

func TestOSTimeNormalizesFieldsAndHonorsDaylightHint(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}
	state := newStateWithLocation(t, newYork)
	defer state.Close()
	chunk := mustLoadString(t, state, "@time-normalize.lua", `
local normalized = os.time {
  year = 2023, month = 13, day = 32,
  hour = 25, min = 61, sec = 61,
}
local winterAuto = os.time {
  year = 2024, month = 1, day = 15, hour = 12,
}
local winterDST = os.time {
  year = 2024, month = 1, day = 15, hour = 12, isdst = true,
}
local summerAuto = os.time {
  year = 2024, month = 7, day = 15, hour = 12,
}
local summerStandard = os.time {
  year = 2024, month = 7, day = 15, hour = 12, isdst = false,
}
return os.date("!%Y-%m-%d %H:%M:%S", normalized),
  winterAuto - winterDST,
  summerStandard - summerAuto
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String("2024-02-02 07:02:01"),
		Number(60*60),
		Number(60*60),
	)
}

func TestOSTimeUsesMinusOneAsTheLua51FailureSentinel(t *testing.T) {
	state := newStateWithLocation(t, time.UTC)
	defer state.Close()
	chunk := mustLoadString(t, state, "@time-minus-one.lua", `
return os.time {
  year = 1969, month = 12, day = 31,
  hour = 23, min = 59, sec = 59,
}
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Nil())
}

func TestOSSetLocaleKeepsStateSemanticsDeterministic(t *testing.T) {
	state := newStateWithOS(t)
	defer state.Close()
	chunk := mustLoadString(t, state, "@locale.lua", `
return os.setlocale(), os.setlocale(nil, "time"),
  os.setlocale("C", "numeric"), os.setlocale("POSIX"),
  os.setlocale("", "all"), os.setlocale("not-a-locale"),
  os.setlocale("C\000ignored", "time\000ignored")
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String("C"),
		state.String("C"),
		state.String("C"),
		state.String("C"),
		state.String("C"),
		Nil(),
		state.String("C"),
	)
}

func TestOSClockMeasuresProcessCPU(t *testing.T) {
	state := newStateWithOS(t)
	defer state.Close()
	chunk := mustLoadString(t, state, "@clock.lua", `return os.clock()`)
	readClock := func() float64 {
		results, err := state.Call(chunk.Value())
		if err != nil {
			t.Fatal(err)
		}
		value, ok := results[0].AsNumber()
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			t.Fatalf("os.clock = %v", results[0])
		}
		return value
	}

	beforeSleep := readClock()
	time.Sleep(40 * time.Millisecond)
	afterSleep := readClock()
	if afterSleep < beforeSleep {
		t.Fatalf("clock moved backward: %v then %v", beforeSleep, afterSleep)
	}

	beforeWork := afterSleep
	deadline := time.Now().Add(25 * time.Millisecond)
	var value uint64
	for time.Now().Before(deadline) {
		value = value*1664525 + 1013904223
	}
	if value == 0 {
		t.Log("CPU loop wrapped to zero")
	}
	afterWork := readClock()
	if afterWork < beforeWork {
		t.Fatalf(
			"clock moved backward after CPU work: %v then %v",
			beforeWork,
			afterWork,
		)
	}
}

func TestDateFormatterCalendarBoundaries(t *testing.T) {
	testCases := []struct {
		name   string
		date   time.Time
		format string
		want   string
	}{
		{
			name:   "ISO year before calendar year",
			date:   time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC),
			format: "%g|%G|%V|%U|%W",
			want:   "15|2015|53|00|00",
		},
		{
			name:   "Sunday starts week U",
			date:   time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC),
			format: "%g|%G|%V|%U|%W",
			want:   "16|2016|52|01|00",
		},
		{
			name:   "Monday reaches week W 53",
			date:   time.Date(2018, 12, 31, 0, 0, 0, 0, time.UTC),
			format: "%g|%G|%V|%U|%W",
			want:   "19|2019|01|52|53",
		},
		{
			name: "leap day and non-hour offset",
			date: time.Date(
				2000,
				2,
				29,
				0,
				0,
				0,
				0,
				time.FixedZone("NEPAL", 5*60*60+45*60),
			),
			format: "%F|%j|%z|%Z",
			want:   "2000-02-29|060|+0545|NEPAL",
		},
		{
			name:   "negative year padding",
			date:   time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC),
			format: "%Y|%C|%y",
			want:   "-001|-1|99",
		},
	}
	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			got, tooLarge := formatDate(test.format, test.date)
			if tooLarge {
				t.Fatal("small date format exceeded construction limit")
			}
			if string(got) != test.want {
				t.Fatalf(
					"formatDate(%q) = %q; want %q",
					test.format,
					got,
					test.want,
				)
			}
		})
	}
}

func TestDateTimeHelpersCoverSignedLimits(t *testing.T) {
	testCases := []struct {
		name       string
		seconds    int64
		offset     int
		ok         bool
		wantSecond int64
	}{
		{
			name:    "minimum underflow",
			seconds: mathMinInt64,
			offset:  1,
		},
		{
			name:       "minimum plus one",
			seconds:    mathMinInt64,
			offset:     -1,
			ok:         true,
			wantSecond: mathMinInt64 + 1,
		},
		{
			name:    "maximum overflow",
			seconds: mathMaxInt64,
			offset:  -1,
		},
		{
			name:       "maximum minus one",
			seconds:    mathMaxInt64,
			offset:     1,
			ok:         true,
			wantSecond: mathMaxInt64 - 1,
		},
	}
	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			got, ok := dateWithZoneOffset(
				time.Unix(test.seconds, 0).UTC(),
				test.offset,
				time.UTC,
			)
			if ok != test.ok {
				t.Fatalf("dateWithZoneOffset ok = %v; want %v", ok, test.ok)
			}
			if ok && got.Unix() != test.wantSecond {
				t.Fatalf(
					"dateWithZoneOffset = %d; want %d",
					got.Unix(),
					test.wantSecond,
				)
			}
		})
	}

	minimumInt := -int(^uint(0)>>1) - 1
	got := string(appendDateInteger(nil, minimumInt, 2, '0'))
	want := strconv.FormatInt(int64(minimumInt), 10)
	if got != want {
		t.Fatalf("minimum integer format = %q; want %q", got, want)
	}
}

// Go's Windows environment lookup allocates while converting the name and
// value between UTF-8 and UTF-16. Keep its zero-allocation regression on other
// platforms while measuring every other scalar operation on Windows too.
func TestWarmOSScalarOperationsDoNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	t.Setenv("LUNAR_LUA_OS_ALLOC", "value")
	state := newStateWithOS(t)
	defer state.Close()
	loader := mustLoadString(t, state, "@os-alloc.lua", `
local clock = os.clock
local difftime = os.difftime
local getenv = os.getenv
local setlocale = os.setlocale
local ostime = os.time
local date = { year = 1970, month = 1, day = 2 }
local checkEnvironment = `+strconv.FormatBool(runtime.GOOS != "windows")+`
return function()
  local total = 0
  for index = 1, 20 do
    total = total + clock() + difftime(index, 1) + ostime(date)
    if checkEnvironment and getenv("LUNAR_LUA_OS_ALLOC") ~= "value" then
      error("environment")
    end
    if setlocale() ~= "C" then error("locale") end
  end
  return total
end
`)
	results, err := state.Call(loader.Value())
	if err != nil {
		t.Fatal(err)
	}
	body := results[0]
	var destination [1]Value
	for range 32 {
		if _, err := state.CallInto(
			body,
			nil,
			destination[:],
		); err != nil {
			t.Fatal(err)
		}
	}
	allocations := testing.AllocsPerRun(64, func() {
		if _, err := state.CallInto(
			body,
			nil,
			destination[:],
		); err != nil {
			t.Fatal(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("warm OS scalar calls allocated %v times per run", allocations)
	}
}

func newStateWithLocation(
	t *testing.T,
	location *time.Location,
) *State {
	t.Helper()
	state, err := New(Options{Location: location})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.OpenTable(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.OpenOS(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state
}

func newStateWithOSOptions(t *testing.T, options Options) *State {
	t.Helper()
	state, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.OpenOS(); err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state
}
