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
	// ErrSourceLoadingDisabled reports an attempt to open source through a
	// State whose SourcePolicy grants no source-file access.
	ErrSourceLoadingDisabled = errors.New(
		"lua: source-file loading is disabled",
	)

	// ErrNilSourceFS reports FSSource called with a nil filesystem.
	ErrNilSourceFS = errors.New("lua: nil source filesystem")

	// ErrNilSourceOpener reports CustomSource called with a nil opener.
	ErrNilSourceOpener = errors.New("lua: nil source opener")
)

// SourceOpener opens one logical source name.
//
// Lunar always supplies a non-nil context. The opener should return
// fs.ErrNotExist when a require search may continue with its next
// package.path candidate. Lunar closes every non-nil reader returned by the
// opener, including a reader returned together with an error.
//
// The opener runs under the State's single-executor contract and must not
// reenter that State. An opener shared by multiple States may be called
// concurrently and must provide its own synchronization.
type SourceOpener func(
	ctx context.Context,
	name string,
) (io.ReadCloser, error)

type sourcePolicyMode uint8

const (
	sourcePolicyDisabled sourcePolicyMode = iota
	sourcePolicyOS
	sourcePolicyFS
	sourcePolicyCustom
)

// SourcePolicy controls where a State may open Lua source files.
//
// Its zero value denies source-file access. SourcePolicy values are immutable
// configuration values: modifier methods return a changed copy.
type SourcePolicy struct {
	mode           sourcePolicyMode
	filesystem     fs.FS
	opener         SourceOpener
	packagePath    string
	packagePathSet bool
}

// OSSource grants access to operating-system files.
//
// New snapshots LUA_PATH for each State unless WithPackagePath supplies an
// explicit initial package.path. This is the only policy that also permits
// filename-less Lua loadfile and dofile to consume Options.Stdin.
func OSSource() SourcePolicy {
	return SourcePolicy{mode: sourcePolicyOS}
}

// FSSource grants access through filesystem.
//
// Names use fs.FS's slash-separated logical-path contract. The default
// package.path is "?.lua;?/init.lua". New returns ErrNilSourceFS if filesystem
// is nil.
func FSSource(filesystem fs.FS) SourcePolicy {
	return SourcePolicy{
		mode:       sourcePolicyFS,
		filesystem: filesystem,
	}
}

// CustomSource grants access through opener.
//
// The default package.path is "?.lua;?/init.lua". New returns
// ErrNilSourceOpener if opener is nil.
func CustomSource(opener SourceOpener) SourcePolicy {
	return SourcePolicy{
		mode:   sourcePolicyCustom,
		opener: opener,
	}
}

// WithPackagePath returns a policy whose initial Lua package.path is path.
//
// The string uses Lua 5.1's semicolon-separated templates. Lua may later
// replace package.path without changing the State's source backend.
func (policy SourcePolicy) WithPackagePath(path string) SourcePolicy {
	policy.packagePath = path
	policy.packagePathSet = true
	return policy
}

type sourceConfig struct {
	opener      SourceOpener
	packagePath string
	separator   string
	stdin       bool
}

func normalizeSourcePolicy(
	policy SourcePolicy,
) (sourceConfig, error) {
	config := sourceConfig{
		separator: "/",
	}
	switch policy.mode {
	case sourcePolicyDisabled:
		if policy.packagePathSet {
			config.packagePath = policy.packagePath
		} else {
			config.packagePath = defaultLogicalPackagePath
		}
		return config, nil
	case sourcePolicyOS:
		config.opener = func(
			_ context.Context,
			name string,
		) (io.ReadCloser, error) {
			return os.Open(name)
		}
		config.separator = string(os.PathSeparator)
		config.stdin = true
		if policy.packagePathSet {
			config.packagePath = policy.packagePath
			return config, nil
		}
		path, err := initialOSPackagePath()
		if err != nil {
			return sourceConfig{}, err
		}
		config.packagePath = path
		return config, nil
	case sourcePolicyFS:
		if policy.filesystem == nil {
			return sourceConfig{}, ErrNilSourceFS
		}
		config.opener = func(
			_ context.Context,
			name string,
		) (io.ReadCloser, error) {
			return policy.filesystem.Open(name)
		}
	case sourcePolicyCustom:
		if policy.opener == nil {
			return sourceConfig{}, ErrNilSourceOpener
		}
		config.opener = policy.opener
	default:
		panic("lua: invalid SourcePolicy mode")
	}
	if policy.packagePathSet {
		config.packagePath = policy.packagePath
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

func (config *sourceConfig) open(
	ctx context.Context,
	name string,
	control *loadControl,
) (io.ReadCloser, error) {
	if failure := control.check(); failure != nil {
		return nil, failure
	}
	if config == nil || config.opener == nil {
		return nil, ErrSourceLoadingDisabled
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
