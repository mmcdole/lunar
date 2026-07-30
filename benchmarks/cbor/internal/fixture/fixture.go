// Package fixture builds deterministic, publishable nested-graph corpora.
package fixture

import (
	"fmt"
	"os"
	"path/filepath"

	lua "github.com/mmcdole/lunik/benchmarks/cbor/internal/luabridge"
)

const FixedTimestamp = 1_700_000_000

// Summary is the independently calculated structural expectation for a preset.
type Summary struct {
	Areas   int `json:"areas"`
	Rooms   int `json:"rooms"`
	Exits   int `json:"exits"`
	Tables  int `json:"tables"`
	Entries int `json:"entries"`
}

// Preset defines one deterministic public workload.
type Preset struct {
	Name        string
	Areas       int
	Rooms       int
	ExitLists   int
	Exits       int
	EmptyNotes  int
	FourFields  int
	TenFields   int
	SixFields   int
	SevenFields int
	EightFields int
}

var presets = map[string]Preset{
	"small": {
		Name: "small", Areas: 3, Rooms: 32, ExitLists: 31, Exits: 96,
		EmptyNotes: 4, FourFields: 90, TenFields: 20, SixFields: 12,
		SevenFields: 5, EightFields: 4,
	},
	"large": {
		Name: "large", Areas: 341, Rooms: 36_705, ExitLists: 36_280, Exits: 109_742,
		EmptyNotes: 439, FourFields: 108_834, TenFields: 22_144, SixFields: 13_500,
		SevenFields: 1_317, EightFields: 993,
	},
}

// Lookup returns a public preset by name.
func Lookup(name string) (Preset, bool) {
	preset, ok := presets[name]
	return preset, ok
}

// Expected calculates the graph size without constructing or encoding it.
func (p Preset) Expected() Summary {
	recordEntries := p.FourFields*4 + p.TenFields*10 + p.SixFields*6 +
		p.SevenFields*7 + p.EightFields*8
	return Summary{
		Areas:  p.Areas,
		Rooms:  p.Rooms,
		Exits:  p.Exits,
		Tables: 6 + p.Areas + p.Rooms + p.ExitLists + p.Exits + p.EmptyNotes,
		Entries: 6 + p.Areas + p.Rooms + p.ExitLists + p.EmptyNotes +
			p.Exits + recordEntries,
	}
}

// WriteCBOR builds p and encodes it through the benchmark's pinned vendored
// Lua codec.
func WriteCBOR(codecRoot, output string, p Preset) (Summary, error) {
	L, err := lua.NewState(lua.Options{FixedUnixTime: FixedTimestamp})
	if err != nil {
		return Summary{}, err
	}
	defer func() {
		_ = L.Close()
	}()

	if err := L.PrependPackagePath(codecRoot); err != nil {
		return Summary{}, fmt.Errorf("set package path: %w", err)
	}
	if err := L.DoString(
		"@load-cbor.lua",
		`__lunik_cbor_codec = require "cbor"`,
	); err != nil {
		return Summary{}, fmt.Errorf("load CBOR codec: %w", err)
	}
	moduleValue, err := L.Global("__lunik_cbor_codec")
	if err != nil {
		return Summary{}, fmt.Errorf("read CBOR module: %w", err)
	}
	module, ok := lua.ValueTable(moduleValue)
	if !ok {
		return Summary{}, fmt.Errorf(
			"CBOR module returned %s",
			lua.ValueTypeName(moduleValue),
		)
	}

	graph, err := build(L, p)
	if err != nil {
		return Summary{}, err
	}
	encode := lua.TableRawGetString(module, "encode")
	if lua.ValueKind(encode) != lua.FunctionKind {
		return Summary{}, fmt.Errorf(
			"CBOR encode is %s, not a function",
			lua.ValueTypeName(encode),
		)
	}
	encodedValue, err := L.CallOne(encode, lua.TableValue(graph))
	if err != nil {
		return Summary{}, fmt.Errorf("encode generated %s preset: %w", p.Name, err)
	}
	encoded, ok := lua.ValueString(encodedValue)
	if !ok {
		return Summary{}, fmt.Errorf("CBOR encode returned a non-string")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return Summary{}, fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(output, []byte(encoded), 0o644); err != nil {
		return Summary{}, fmt.Errorf("write generated CBOR: %w", err)
	}
	return p.Expected(), nil
}

func build(L *lua.State, p Preset) (*lua.Table, error) {
	recordTotal := p.Areas + p.Rooms + p.Exits
	shapeTotal := p.FourFields + p.TenFields + p.SixFields + p.SevenFields + p.EightFields
	if recordTotal != shapeTotal {
		return nil, fmt.Errorf("preset %s has %d records but %d record shapes", p.Name, recordTotal, shapeTotal)
	}
	if p.ExitLists <= 0 || p.Exits < p.ExitLists {
		return nil, fmt.Errorf("preset %s has invalid exit distribution", p.Name)
	}

	recordOrdinal := 0
	nextRecord := func() (*lua.Table, error) {
		recordOrdinal++
		return makeRecord(L, recordOrdinal, fieldCount(p, recordOrdinal))
	}

	areas, err := L.NewTable(0, 0)
	if err != nil {
		return nil, err
	}
	for i := 1; i <= p.Areas; i++ {
		record, err := nextRecord()
		if err != nil {
			return nil, err
		}
		if err := lua.TableRawSetString(
			areas,
			fmt.Sprintf("area:%06d", i),
			lua.TableValue(record),
		); err != nil {
			return nil, err
		}
	}

	if p.Rooms < 9 || p.ExitLists < 9 {
		return nil, fmt.Errorf("preset %s needs at least nine rooms and exit lists", p.Name)
	}
	rooms, err := L.NewTable(0, 0)
	if err != nil {
		return nil, err
	}
	for i := 1; i <= p.Rooms; i++ {
		record, err := nextRecord()
		if err != nil {
			return nil, err
		}
		if i <= p.Rooms-9 {
			err = lua.TableRawSetString(
				rooms,
				fmt.Sprintf("room:%06d", i),
				lua.TableValue(record),
			)
		} else {
			err = lua.TableRawSetInt(
				rooms,
				i-(p.Rooms-9),
				lua.TableValue(record),
			)
		}
		if err != nil {
			return nil, err
		}
	}

	exits, err := L.NewTable(0, 0)
	if err != nil {
		return nil, err
	}
	base, extra := p.Exits/p.ExitLists, p.Exits%p.ExitLists
	for i := 1; i <= p.ExitLists; i++ {
		count := base
		if i <= extra {
			count++
		}
		list, err := L.NewTable(count, 0)
		if err != nil {
			return nil, err
		}
		for j := 1; j <= count; j++ {
			record, err := nextRecord()
			if err != nil {
				return nil, err
			}
			if err := lua.TableRawSetInt(
				list,
				j,
				lua.TableValue(record),
			); err != nil {
				return nil, err
			}
		}
		if i <= p.ExitLists-9 {
			err = lua.TableRawSetString(
				exits,
				fmt.Sprintf("room:%06d", i),
				lua.TableValue(list),
			)
		} else {
			err = lua.TableRawSetInt(
				exits,
				i-(p.ExitLists-9),
				lua.TableValue(list),
			)
		}
		if err != nil {
			return nil, err
		}
	}

	areanotes, err := L.NewTable(0, 0)
	if err != nil {
		return nil, err
	}
	roomnotes, err := L.NewTable(0, 0)
	if err != nil {
		return nil, err
	}
	for i := 1; i <= p.EmptyNotes; i++ {
		target := roomnotes
		if i == 1 {
			target = areanotes
		}
		note, err := L.NewTable(0, 0)
		if err != nil {
			return nil, err
		}
		if err := lua.TableRawSetString(
			target,
			fmt.Sprintf("note:%06d", i),
			lua.TableValue(note),
		); err != nil {
			return nil, err
		}
	}

	root, err := L.NewTable(0, 0)
	if err != nil {
		return nil, err
	}
	for _, field := range []struct {
		key   string
		value lua.Value
	}{
		{key: "areas", value: lua.TableValue(areas)},
		{key: "rooms", value: lua.TableValue(rooms)},
		{key: "exits", value: lua.TableValue(exits)},
		{key: "areanotes", value: lua.TableValue(areanotes)},
		{key: "roomnotes", value: lua.TableValue(roomnotes)},
		{key: "timestamp", value: lua.Number(FixedTimestamp)},
	} {
		if err := lua.TableRawSetString(root, field.key, field.value); err != nil {
			return nil, err
		}
	}
	return root, nil
}

func fieldCount(p Preset, ordinal int) int {
	for _, group := range []struct {
		count  int
		fields int
	}{
		{p.FourFields, 4},
		{p.TenFields, 10},
		{p.SixFields, 6},
		{p.SevenFields, 7},
		{p.EightFields, 8},
	} {
		if ordinal <= group.count {
			return group.fields
		}
		ordinal -= group.count
	}
	panic("record ordinal outside preset")
}

var recordKeys = [...]string{
	"id", "name", "zone", "flags", "level",
	"terrain", "x", "y", "description", "updated",
}

func makeRecord(
	L *lua.State,
	ordinal, fields int,
) (*lua.Table, error) {
	record, err := L.NewTable(0, fields)
	if err != nil {
		return nil, err
	}
	values := [...]lua.Value{
		lua.Number(float64(ordinal)),
		L.String(fmt.Sprintf("record-%06d", ordinal%55_156)),
		lua.Number(float64(ordinal % 341)),
		lua.Bool(ordinal%2 == 0),
		lua.Number(float64(ordinal % 201)),
		L.String([]string{"city", "field", "forest", "water"}[ordinal%4]),
		lua.Number(float64((ordinal % 2_001) - 1_000)),
		lua.Number(float64(((ordinal * 7) % 2_001) - 1_000)),
		L.String(fmt.Sprintf("generated graph record %d", ordinal%997)),
		lua.Number(float64(FixedTimestamp + ordinal%86_400)),
	}
	for i := 0; i < fields; i++ {
		if err := lua.TableRawSetString(
			record,
			recordKeys[i],
			values[i],
		); err != nil {
			return nil, err
		}
	}
	return record, nil
}
