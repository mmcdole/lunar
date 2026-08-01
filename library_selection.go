package lua

import (
	"errors"
	"fmt"
)

// ErrInvalidLibrary reports an unknown standard-library value in a
// construction-time LibrarySet.
var ErrInvalidLibrary = errors.New("lua: invalid standard library")

// Library identifies one Lua standard library.
type Library uint8

const (
	BaseLibrary Library = iota
	CoroutineLibrary
	PackageLibrary
	TableLibrary
	IOLibrary
	OSLibrary
	StringLibrary
	MathLibrary
	DebugLibrary

	libraryCount
)

// LibrarySet selects the standard libraries installed by New.
//
// Order does not affect installation and duplicate entries are ignored.
// BaseLibrary includes Lua 5.1's coroutine library; CoroutineLibrary exists
// so a State can expose coroutines without the base globals.
type LibrarySet []Library

// CoreLibraries returns the standard libraries that do not grant ambient
// file, process, environment, or debug access.
//
// The set contains the base (including coroutine), package, table, string,
// and math libraries. The package library can use preloaded modules without a
// ScriptLoader and gains no file access on its own.
func CoreLibraries() LibrarySet {
	return LibrarySet{
		BaseLibrary,
		PackageLibrary,
		TableLibrary,
		StringLibrary,
		MathLibrary,
	}
}

// FullLibraries returns every implemented Lua 5.1 standard library.
//
// In addition to CoreLibraries, it includes IO, OS, and debug. Those
// libraries expose ambient host capabilities and mutable runtime internals.
func FullLibraries() LibrarySet {
	return LibrarySet{
		BaseLibrary,
		PackageLibrary,
		TableLibrary,
		IOLibrary,
		OSLibrary,
		StringLibrary,
		MathLibrary,
		DebugLibrary,
	}
}

type librarySelection [libraryCount]bool

func normalizeLibrarySet(libraries LibrarySet) (librarySelection, error) {
	var selection librarySelection
	for _, library := range libraries {
		if library >= libraryCount {
			return librarySelection{}, fmt.Errorf(
				"%w: %d",
				ErrInvalidLibrary,
				library,
			)
		}
		selection[library] = true
	}
	return selection, nil
}

func (state *State) openLibraries(selection librarySelection) error {
	if selection[BaseLibrary] {
		if err := state.OpenBase(); err != nil {
			return err
		}
	} else if selection[CoroutineLibrary] {
		if err := state.OpenCoroutine(); err != nil {
			return err
		}
	}
	if selection[PackageLibrary] {
		if err := state.OpenPackage(); err != nil {
			return err
		}
	}
	if selection[TableLibrary] {
		if err := state.OpenTable(); err != nil {
			return err
		}
	}
	if selection[IOLibrary] {
		if err := state.OpenIO(); err != nil {
			return err
		}
	}
	if selection[OSLibrary] {
		if err := state.OpenOS(); err != nil {
			return err
		}
	}
	if selection[StringLibrary] {
		if err := state.OpenString(); err != nil {
			return err
		}
	}
	if selection[MathLibrary] {
		if err := state.OpenMath(); err != nil {
			return err
		}
	}
	if selection[DebugLibrary] {
		if err := state.OpenDebug(); err != nil {
			return err
		}
	}
	return nil
}
