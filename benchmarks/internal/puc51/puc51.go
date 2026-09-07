//go:build puc51 && cgo

// Package puc51 provides the small protected-call adapter used by the optional
// PUC Lua 5.1 comparison. Lua itself is supplied by CGO_CFLAGS and CGO_LDFLAGS.
package puc51

/*
#include <stdlib.h>
#include <lua.h>
#include <lauxlib.h>
#include <lualib.h>

#if LUA_VERSION_NUM != 501
#error "the puc51 comparison requires PUC Lua 5.1 headers and library"
#endif

static int lunar_puc_open(lua_State *L, lua_CFunction open, const char *name) {
	lua_pushcfunction(L, open);
	lua_pushstring(L, name);
	return lua_pcall(L, 1, 0, 0);
}

static int lunar_puc_libraries(lua_State *L, unsigned int libraries) {
	int status;
	if ((libraries & 1) && (status = lunar_puc_open(L, luaopen_base, "")))
		return status;
	if ((libraries & 2) && (status = lunar_puc_open(L, luaopen_string, LUA_STRLIBNAME)))
		return status;
	if ((libraries & 4) && (status = lunar_puc_open(L, luaopen_math, LUA_MATHLIBNAME)))
		return status;
	return 0;
}

static int lunar_puc_load(lua_State *L, const char *source, size_t size, const char *name) {
	int status = luaL_loadbuffer(L, source, size, name);
	if (status) return status;
	return lua_pcall(L, 0, 0, 0);
}

static int lunar_puc_reference(lua_State *L, const char *name) {
	lua_getglobal(L, name);
	if (!lua_isfunction(L, -1)) {
		lua_pop(L, 1);
		return LUA_NOREF;
	}
	return luaL_ref(L, LUA_REGISTRYINDEX);
}

// One Go-to-C transition includes both pushing the cached function and the
// protected call. No global-name lookup, loading, or compilation is timed.
static int lunar_puc_call(lua_State *L, int reference) {
	lua_rawgeti(L, LUA_REGISTRYINDEX, reference);
	return lua_pcall(L, 0, 0, 0);
}

static int lunar_puc_number(lua_State *L, const char *name, lua_Number *result) {
	lua_getglobal(L, name);
	int ok = lua_type(L, -1) == LUA_TNUMBER;
	if (ok) *result = lua_tonumber(L, -1);
	lua_pop(L, 1);
	return ok;
}

static const char *lunar_puc_string(lua_State *L, const char *name, size_t *size) {
	lua_getglobal(L, name);
	if (lua_type(L, -1) != LUA_TSTRING) return NULL;
	return lua_tolstring(L, -1, size);
}

static void lunar_puc_pop(lua_State *L) { lua_pop(L, 1); }
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type Libraries uint8

const (
	Base Libraries = 1 << iota
	String
	Math
)

type State struct {
	ptr       *C.lua_State
	reference C.int
}

func New(libraries Libraries) (*State, error) {
	state := &State{ptr: C.luaL_newstate(), reference: C.LUA_NOREF}
	if state.ptr == nil {
		return nil, fmt.Errorf("PUC Lua: could not create state")
	}
	if status := C.lunar_puc_libraries(state.ptr, C.uint(libraries)); status != 0 {
		err := state.error(status)
		_ = state.Close()
		return nil, err
	}
	return state, nil
}

// Prepare loads and executes the setup chunk, then caches the entry function in
// Lua's registry. It is called before warmup and timing.
func (state *State) Prepare(name, source, entry string) error {
	cname, csource, centry := C.CString(name), C.CString(source), C.CString(entry)
	defer C.free(unsafe.Pointer(cname))
	defer C.free(unsafe.Pointer(csource))
	defer C.free(unsafe.Pointer(centry))
	if status := C.lunar_puc_load(state.ptr, csource, C.size_t(len(source)), cname); status != 0 {
		return state.error(status)
	}
	reference := C.lunar_puc_reference(state.ptr, centry)
	if reference == C.LUA_NOREF {
		return fmt.Errorf("PUC Lua: %s is not a function", entry)
	}
	if state.reference != C.LUA_NOREF {
		C.luaL_unref(state.ptr, C.LUA_REGISTRYINDEX, state.reference)
	}
	state.reference = reference
	return nil
}

func (state *State) Run() error {
	if status := C.lunar_puc_call(state.ptr, state.reference); status != 0 {
		return state.error(status)
	}
	return nil
}

func (state *State) GlobalNumber(name string) (float64, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	var result C.lua_Number
	if C.lunar_puc_number(state.ptr, cname, &result) == 0 {
		return 0, fmt.Errorf("PUC Lua: %s is not a number", name)
	}
	return float64(result), nil
}

func (state *State) GlobalString(name string) (string, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	var size C.size_t
	result := C.lunar_puc_string(state.ptr, cname, &size)
	defer C.lunar_puc_pop(state.ptr)
	if result == nil {
		return "", fmt.Errorf("PUC Lua: %s is not a string", name)
	}
	return C.GoStringN(result, C.int(size)), nil
}

func (state *State) Close() error {
	if state.ptr != nil {
		C.lua_close(state.ptr)
		state.ptr = nil
	}
	return nil
}

func (state *State) error(status C.int) error {
	var size C.size_t
	message := C.lua_tolstring(state.ptr, -1, &size)
	defer C.lunar_puc_pop(state.ptr)
	if message == nil {
		return fmt.Errorf("PUC Lua: non-string error (status %d)", status)
	}
	return fmt.Errorf("PUC Lua: %s (status %d)", C.GoStringN(message, C.int(size)), status)
}
