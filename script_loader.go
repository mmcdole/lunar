package lua

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"strings"
)

const (
	defaultLogicalPackagePath = "?.lua;?/init.lua"

	defaultLuaPathUnix = "./?.lua;" +
		"/usr/local/share/lua/5.1/?.lua;" +
		"/usr/local/share/lua/5.1/?/init.lua;" +
		"/usr/local/lib/lua/5.1/?.lua;" +
		"/usr/local/lib/lua/5.1/?/init.lua"

	defaultLuaPathWindows = `.\?.lua;!\lua\?.lua;!\lua\?\init.lua;` +
		`!\?.lua;!\?\init.lua`
)

var (
	// ErrScriptLoadingDisabled reports an attempt to open a script through a
	// State whose ScriptLoader grants no script-file access.
	ErrScriptLoadingDisabled = errors.New(
		"lua: script-file loading is disabled",
	)

	// ErrNilScriptFS reports FSLoader called with a nil filesystem.
	ErrNilScriptFS = errors.New("lua: nil script filesystem")

	// ErrNilScriptOpener reports FuncLoader called with a nil opener.
	ErrNilScriptOpener = errors.New("lua: nil script opener")
)

// ScriptOpener opens one logical script name.
//
// Lunar always supplies a non-nil context. The opener should return
// fs.ErrNotExist when a require search may continue with its next
// package.path candidate. Lunar closes every non-nil reader returned by the
// opener, including a reader returned together with an error.
//
// The opener runs under the State's single-executor contract and must not
// reenter that State. An opener shared by multiple States may be called
// concurrently and must provide its own synchronization.
type ScriptOpener func(
	ctx context.Context,
	name string,
) (io.ReadCloser, error)

type scriptLoaderMode uint8

const (
	scriptLoaderDisabled scriptLoaderMode = iota
	scriptLoaderHost
	scriptLoaderFS
	scriptLoaderFunc
)

// ScriptLoader controls how a State opens named Lua scripts.
//
// Its zero value denies script-file access. ScriptLoader values are immutable
// configuration values: modifier methods return a changed copy.
type ScriptLoader struct {
	mode           scriptLoaderMode
	filesystem     fs.FS
	opener         ScriptOpener
	packagePath    string
	packagePathSet bool
}

// HostLoader loads scripts from the host operating system.
//
// New snapshots LUA_PATH for each State unless WithPackagePath supplies an
// explicit initial package.path. This is the only loader that also permits
// filename-less Lua loadfile and dofile to consume Options.Stdin.
func HostLoader() ScriptLoader {
	return ScriptLoader{mode: scriptLoaderHost}
}

// FSLoader loads scripts from filesystem.
//
// Names use fs.FS's slash-separated logical-path contract. The default
// package.path is "?.lua;?/init.lua". New returns ErrNilScriptFS if filesystem
// is nil.
func FSLoader(filesystem fs.FS) ScriptLoader {
	return ScriptLoader{
		mode:       scriptLoaderFS,
		filesystem: filesystem,
	}
}

// FuncLoader loads scripts through opener.
//
// The default package.path is "?.lua;?/init.lua". New returns
// ErrNilScriptOpener if opener is nil.
func FuncLoader(opener ScriptOpener) ScriptLoader {
	return ScriptLoader{
		mode:   scriptLoaderFunc,
		opener: opener,
	}
}

// WithPackagePath returns a loader whose initial Lua package.path is path.
//
// The string uses Lua 5.1's semicolon-separated templates. Lua may later
// replace package.path without changing the State's script backend.
func (loader ScriptLoader) WithPackagePath(path string) ScriptLoader {
	loader.packagePath = path
	loader.packagePathSet = true
	return loader
}

type scriptLoaderConfig struct {
	opener      ScriptOpener
	packagePath string
	separator   string
	stdin       bool
}

func normalizeScriptLoader(
	loader ScriptLoader,
) (scriptLoaderConfig, error) {
	config := scriptLoaderConfig{
		separator: "/",
	}
	switch loader.mode {
	case scriptLoaderDisabled:
		if loader.packagePathSet {
			config.packagePath = loader.packagePath
		} else {
			config.packagePath = defaultLogicalPackagePath
		}
		return config, nil
	case scriptLoaderHost:
		config.opener = func(
			_ context.Context,
			name string,
		) (io.ReadCloser, error) {
			return os.Open(name)
		}
		config.separator = string(os.PathSeparator)
		config.stdin = true
		if loader.packagePathSet {
			config.packagePath = loader.packagePath
			return config, nil
		}
		path, err := initialOSPackagePath()
		if err != nil {
			return scriptLoaderConfig{}, err
		}
		config.packagePath = path
		return config, nil
	case scriptLoaderFS:
		if loader.filesystem == nil {
			return scriptLoaderConfig{}, ErrNilScriptFS
		}
		config.opener = func(
			_ context.Context,
			name string,
		) (io.ReadCloser, error) {
			return loader.filesystem.Open(name)
		}
	case scriptLoaderFunc:
		if loader.opener == nil {
			return scriptLoaderConfig{}, ErrNilScriptOpener
		}
		config.opener = loader.opener
	default:
		panic("lua: invalid ScriptLoader mode")
	}
	if loader.packagePathSet {
		config.packagePath = loader.packagePath
	} else {
		config.packagePath = defaultLogicalPackagePath
	}
	return config, nil
}

func initialOSPackagePath() (string, error) {
	fallback := defaultLuaPathUnix
	if runtime.GOOS == "windows" {
		fallback = defaultLuaPathWindows
	}
	path := packageEnvironmentPath("LUA_PATH", fallback)
	if runtime.GOOS != "windows" || !strings.Contains(path, "!") {
		return path, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf(
			"lua: resolve executable directory: %w",
			err,
		)
	}
	directory := executable
	if separator := strings.LastIndexAny(directory, `\/`); separator >= 0 {
		directory = directory[:separator]
	} else {
		return "", errors.New(
			"lua: executable path has no directory separator",
		)
	}
	return strings.ReplaceAll(path, "!", directory), nil
}

func packageEnvironmentPath(name, fallback string) string {
	path, present := os.LookupEnv(name)
	if !present {
		return fallback
	}
	return strings.ReplaceAll(path, ";;", ";"+fallback+";")
}

func (config *scriptLoaderConfig) open(
	ctx context.Context,
	name string,
	control *loadControl,
) (io.ReadCloser, error) {
	if failure := control.check(); failure != nil {
		return nil, failure
	}
	if config == nil || config.opener == nil {
		return nil, ErrScriptLoadingDisabled
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reader, err := config.opener(ctx, name)
	if err != nil {
		if reader != nil {
			_ = reader.Close()
		}
		if failure := control.check(); failure != nil {
			return nil, failure
		}
		return nil, err
	}
	if reader == nil {
		return nil, ErrNilReader
	}
	if failure := control.check(); failure != nil {
		_ = reader.Close()
		return nil, failure
	}
	return reader, nil
}
