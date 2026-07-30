//go:build (aix || android || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris) && !ios

package lua

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestIOPopenReadUsesTheCanonicalFileSurface(t *testing.T) {
	state := newStateWithIO(t, Options{})
	defer state.Close()

	command := `printf 'a\000b\nsecond'`
	results := runIOChunk(t, state, `
local file=assert(io.popen(`+luaTestQuote(command)+`,nil,"ignored"))
local before=io.type(file)
local sameMetatable=getmetatable(file)==getmetatable(io.stdin)
local text=file:read("*a")
local closed=file:close()
return before,sameMetatable,text,closed,io.type(file),tostring(file)
`)
	assertTestValues(
		t,
		results,
		state.String("file"),
		Bool(true),
		state.String("a\x00b\nsecond"),
		Bool(true),
		state.String("closed file"),
		state.String("file (closed)"),
	)
}

func TestIOPopenWriteFlushesClosesAndWaits(t *testing.T) {
	path := filepathForShell(t.TempDir(), "popen output")
	state := newStateWithIO(t, Options{})
	defer state.Close()

	command := "cat > " + quoteShellWord(path)
	results := runIOChunk(t, state, `
local file=assert(io.popen(`+luaTestQuote(command)+`,"w"))
local written=file:write("a\000b",17)
local closed=file:close()
return written,closed,io.type(file)
`)
	assertTestValues(
		t,
		results,
		Bool(true),
		Bool(true),
		state.String("closed file"),
	)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "a\x00b17" {
		t.Fatalf("popen output = %q", content)
	}
}

func TestIOPopenCloseIgnoresChildStatusAndDirectionsStayRaw(
	t *testing.T,
) {
	state := newStateWithIO(t, Options{})
	defer state.Close()

	results := runIOChunk(t, state, `
local exited=assert(io.popen("exit 7","r"))
local exitClose=exited:close()
local signalled=assert(io.popen("kill -TERM $$","r"))
local signalClose=signalled:close()

local reader=assert(io.popen("printf x","r"))
local writeResult,writeMessage,writeCode=reader:write("bad")
local seekResult,seekMessage,seekCode=reader:seek()
local readClose=reader:close()

local writer=assert(io.popen("cat >/dev/null","w"))
local readResult,readMessage,readCode=writer:read()
local writeClose=writer:close()

return exitClose,signalClose,
  writeResult,writeMessage,writeCode,
  seekResult,seekMessage,seekCode,readClose,
  readResult,readMessage,readCode,writeClose
`)
	assertTestValues(
		t,
		[]Value{
			results[0],
			results[1],
			results[2],
			results[4],
			results[5],
			results[7],
			results[8],
			results[9],
			results[11],
			results[12],
		},
		Bool(true),
		Bool(true),
		Nil(),
		Number(float64(syscall.EBADF)),
		Nil(),
		Number(float64(syscall.ESPIPE)),
		Bool(true),
		Nil(),
		Number(float64(syscall.EBADF)),
		Bool(true),
	)
	for _, index := range []int{3, 6, 10} {
		message, ok := results[index].AsString()
		if !ok || message == "" {
			t.Fatalf("popen failure message %d = %v", index, results[index])
		}
	}
}

func TestIOPopenArgumentsModesAndCStringBoundary(t *testing.T) {
	marker := filepathForShell(t.TempDir(), "invalid-mode-ran")
	directory := t.TempDir()
	numberCommand := filepathForShell(directory, "0")
	if err := os.WriteFile(
		numberCommand,
		[]byte("#!/bin/sh\nprintf numeric\n"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv(
		"PATH",
		directory+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	state := newStateWithIO(t, Options{})
	defer state.Close()
	source := `
local absentOK,absentMessage=pcall(io.popen)
local commandOK,commandMessage=pcall(io.popen,{})
local modeOK,modeMessage=pcall(io.popen,"exit 0",{})
local invalid,invalidMessage,invalidCode=io.popen(
  ` + luaTestQuote("touch "+quoteShellWord(marker)) + `,"bad")
local truncated=assert(io.popen(
  "printf ok\000printf wrong","r\000wrong","ignored"))
local truncatedText=truncated:read("*a")
local truncatedClose=truncated:close()
local numeric=assert(io.popen(0))
local numericText=numeric:read("*a")
local numericClose=numeric:close()
return absentOK,absentMessage,commandOK,commandMessage,
  modeOK,modeMessage,
  invalid,invalidMessage,invalidCode,
  truncatedText,truncatedClose,numericText,numericClose
`
	results := runIOChunk(t, state, source)
	assertTestValues(
		t,
		[]Value{
			results[0],
			results[2],
			results[4],
			results[6],
			results[8],
			results[9],
			results[10],
			results[11],
			results[12],
		},
		Bool(false),
		Bool(false),
		Bool(false),
		Nil(),
		Number(float64(syscall.EINVAL)),
		state.String("ok"),
		Bool(true),
		state.String("numeric"),
		Bool(true),
	)
	for index, fragment := range map[int]string{
		1: "bad argument #1 to '?' (string expected, got no value)",
		3: "bad argument #1 to '?' (string expected, got table)",
		5: "bad argument #2 to '?' (string expected, got table)",
		7: "invalid",
	} {
		message, ok := results[index].AsString()
		if !ok || !strings.Contains(
			strings.ToLower(message),
			strings.ToLower(fragment),
		) {
			t.Fatalf("popen argument result %d = %v", index, results[index])
		}
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid popen mode executed its command: %v", err)
	}
}

func TestIOPopenCancellationInterruptsBlockedReadAndReapsRoot(
	t *testing.T,
) {
	directory := t.TempDir()
	pidPath := filepathForShell(directory, "pids")
	command := fmt.Sprintf(
		`sleep 30 & child=$!; printf '%%s %%s' "$$" "$child" > %s; wait`,
		quoteShellWord(pidPath),
	)
	state := newStateWithIO(t, Options{})
	defer state.Close()
	chunk := mustLoadString(
		t,
		state,
		"@popen-read-cancel.lua",
		"popen_continued=false\n"+
			"local file=assert(io.popen("+luaTestQuote(command)+",\"r\"))\n"+
			"popen_file=file\n"+
			"pcall(function() return file:read(\"*a\") end)\n"+
			"popen_continued=true",
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pidResult := make(chan []int, 1)
	go func() {
		pidResult <- waitForProcessIDs(pidPath, 5*time.Second)
		cancel()
	}()
	_, err := state.CallContext(ctx, chunk.Value())
	assertPopenContextFailure(t, err)
	pids := <-pidResult
	if len(pids) != 2 {
		t.Fatalf("recorded process IDs = %v; want shell and child", pids)
	}
	defer terminateDetachedTestProcess(t, pids[1])
	waitForProcessGone(t, pids[0])
	continued, err := state.RawGlobal("popen_continued")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, continued, Bool(false))
	results := runIOChunk(t, state, "return io.type(popen_file)")
	assertTestValues(t, results, state.String("closed file"))
}

func TestIOPopenCancellationUnblocksAfterShellExit(
	t *testing.T,
) {
	pidPath := filepathForShell(t.TempDir(), "pids")
	command := fmt.Sprintf(
		`exec 3<&0; sleep 30 <&3 & child=$!; printf '%%s %%s' "$$" "$child" > %s; exit 0`,
		quoteShellWord(pidPath),
	)
	state := newStateWithIO(t, Options{})
	defer state.Close()
	chunk := mustLoadString(
		t,
		state,
		"@popen-background-cancel.lua",
		"local file=assert(io.popen("+luaTestQuote(command)+",\"r\"))\n"+
			"popen_background_file=file\n"+
			"return file:read(\"*a\")",
	)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := state.CallContext(ctx, chunk.Value())
		result <- err
	}()

	pids := waitForProcessIDs(pidPath, 5*time.Second)
	if len(pids) != 2 {
		cancel()
		<-result
		t.Fatalf("recorded process IDs = %v; want shell and child", pids)
	}
	defer terminateDetachedTestProcess(t, pids[1])
	waitForProcessGone(t, pids[0])
	cancel()
	select {
	case err := <-result:
		assertPopenContextFailure(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("popen read did not stop after context cancellation")
	}
	results := runIOChunk(
		t,
		state,
		"return io.type(popen_background_file)",
	)
	assertTestValues(t, results, state.String("closed file"))
}

func TestIOPopenCancellationInterruptsBlockedWriteAndReapsRoot(
	t *testing.T,
) {
	directory := t.TempDir()
	pidPath := filepathForShell(directory, "pids")
	// Publishing the PIDs only after one byte reaches the child proves
	// file:write has entered the process-file operation before cancellation.
	command := fmt.Sprintf(
		`dd bs=1 count=1 of=/dev/null 2>/dev/null; sleep 30 & child=$!; printf '%%s %%s' "$$" "$child" > %s; wait`,
		quoteShellWord(pidPath),
	)
	state := newStateWithIO(t, Options{})
	defer state.Close()
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(
		t,
		state,
		"@popen-write-cancel.lua",
		"popen_write_continued=false\n"+
			"local payload=string.rep(\"x\",1048576)\n"+
			"local file=assert(io.popen("+luaTestQuote(command)+",\"w\"))\n"+
			"file:write(payload)\n"+
			"popen_write_continued=true",
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pidResult := make(chan []int, 1)
	go func() {
		pidResult <- waitForProcessIDs(pidPath, 5*time.Second)
		cancel()
	}()
	_, err := state.CallContext(ctx, chunk.Value())
	assertPopenContextFailure(t, err)
	pids := <-pidResult
	if len(pids) != 2 {
		t.Fatalf("recorded process IDs = %v; want shell and child", pids)
	}
	defer terminateDetachedTestProcess(t, pids[1])
	waitForProcessGone(t, pids[0])
	continued, err := state.RawGlobal("popen_write_continued")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, continued, Bool(false))
}

func TestIOPopenCancellationInterruptsBlockedFlushAndReapsRoot(
	t *testing.T,
) {
	directory := t.TempDir()
	pidPath := filepathForShell(directory, "pids")
	// Publishing the PIDs only after one byte reaches the child proves
	// file:flush has entered the process-file operation before cancellation.
	command := fmt.Sprintf(
		`dd bs=1 count=1 of=/dev/null 2>/dev/null; sleep 30 & child=$!; printf '%%s %%s' "$$" "$child" > %s; wait`,
		quoteShellWord(pidPath),
	)
	state := newStateWithIO(t, Options{})
	defer state.Close()
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(
		t,
		state,
		"@popen-flush-cancel.lua",
		"popen_flush_continued=false\n"+
			"local payload=string.rep(\"x\",1048576)\n"+
			"local file=assert(io.popen("+luaTestQuote(command)+",\"w\"))\n"+
			"assert(file:setvbuf(\"full\",1048576))\n"+
			"assert(file:write(payload))\n"+
			"file:flush()\n"+
			"popen_flush_continued=true",
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pidResult := make(chan []int, 1)
	go func() {
		pidResult <- waitForProcessIDs(pidPath, 5*time.Second)
		cancel()
	}()
	_, err := state.CallContext(ctx, chunk.Value())
	assertPopenContextFailure(t, err)
	pids := <-pidResult
	if len(pids) != 2 {
		t.Fatalf("recorded process IDs = %v; want shell and child", pids)
	}
	defer terminateDetachedTestProcess(t, pids[1])
	waitForProcessGone(t, pids[0])
	continued, err := state.RawGlobal("popen_flush_continued")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, continued, Bool(false))
}

func TestIOPopenAlternateEntryPointsHonorCancellation(t *testing.T) {
	cases := []struct {
		name string
		mode string
		body string
	}{
		{
			name: "default input",
			mode: "r",
			body: "assert(io.input(file)==file)\n" +
				"return io.read(\"*a\")",
		},
		{
			name: "default output",
			mode: "w",
			body: "assert(io.output(file)==file)\n" +
				"return io.write(payload)",
		},
		{
			name: "line iterator",
			mode: "r",
			body: "local iterator=file:lines()\n" +
				"return iterator()",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			pidPath := filepathForShell(t.TempDir(), "pids")
			command := fmt.Sprintf(
				`sleep 30 >&1 & child=$!; printf '%%s %%s' "$$" "$child" > %s; exit 0`,
				quoteShellWord(pidPath),
			)
			setup := ""
			if test.mode == "w" {
				// Do not publish the PIDs until io.write reaches the
				// pipe. The detached child then keeps that pipe open
				// after the command-processor root exits.
				command = fmt.Sprintf(
					`dd bs=1 count=1 of=/dev/null 2>/dev/null; exec 3<&0; sleep 30 <&3 & child=$!; printf '%%s %%s' "$$" "$child" > %s; exit 0`,
					quoteShellWord(pidPath),
				)
				setup = "local payload=string.rep(\"x\",1048576)\n"
			}
			state := newStateWithIO(t, Options{})
			defer state.Close()
			if err := state.OpenString(); err != nil {
				t.Fatal(err)
			}
			source := setup + "local file=assert(io.popen(" +
				luaTestQuote(command) + "," +
				luaTestQuote(test.mode) + "))\n" +
				"popen_entry_file=file\n" +
				test.body
			chunk := mustLoadString(
				t,
				state,
				"@popen-entry-cancel.lua",
				source,
			)

			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				_, err := state.CallContext(ctx, chunk.Value())
				result <- err
			}()

			pids := waitForProcessIDs(pidPath, 5*time.Second)
			if len(pids) != 2 {
				cancel()
				<-result
				t.Fatalf(
					"recorded process IDs = %v; want shell and child",
					pids,
				)
			}
			defer terminateDetachedTestProcess(t, pids[1])
			waitForProcessGone(t, pids[0])
			cancel()
			select {
			case err := <-result:
				assertPopenContextFailure(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("process-file operation ignored cancellation")
			}
			results := runIOChunk(
				t,
				state,
				"return io.type(popen_entry_file)",
			)
			assertTestValues(
				t,
				results,
				state.String("closed file"),
			)
		})
	}
}

func TestIOPopenCancellationInterruptsCloseAndReapsRoot(
	t *testing.T,
) {
	directory := t.TempDir()
	pidPath := filepathForShell(directory, "pids")
	command := fmt.Sprintf(
		`sleep 30 & child=$!; printf '%%s %%s' "$$" "$child" > %s; wait`,
		quoteShellWord(pidPath),
	)
	state := newStateWithIO(t, Options{})
	defer state.Close()
	chunk := mustLoadString(
		t,
		state,
		"@popen-close-cancel.lua",
		"popen_close_continued=false\n"+
			"local file=assert(io.popen("+luaTestQuote(command)+",\"r\"))\n"+
			"file:close()\n"+
			"popen_close_continued=true",
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pidResult := make(chan []int, 1)
	go func() {
		pidResult <- waitForProcessIDs(pidPath, 5*time.Second)
		cancel()
	}()
	_, err := state.CallContext(ctx, chunk.Value())
	assertPopenContextFailure(t, err)
	pids := <-pidResult
	if len(pids) != 2 {
		t.Fatalf("recorded process IDs = %v; want shell and child", pids)
	}
	defer terminateDetachedTestProcess(t, pids[1])
	waitForProcessGone(t, pids[0])
	continued, err := state.RawGlobal("popen_close_continued")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, continued, Bool(false))
}

func TestIOPopenStateCloseTerminatesAndReapsRoot(
	t *testing.T,
) {
	pidPath := filepathForShell(t.TempDir(), "pids")
	command := fmt.Sprintf(
		`sleep 30 & child=$!; printf '%%s %%s' "$$" "$child" > %s; wait`,
		quoteShellWord(pidPath),
	)
	state := newStateWithIO(t, Options{})
	chunk := mustLoadString(
		t,
		state,
		"@popen-state-close.lua",
		"return assert(io.popen("+luaTestQuote(command)+",\"r\"))",
	)
	results, err := state.Call(chunk.Value())
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	pids := waitForProcessIDs(pidPath, 5*time.Second)
	if len(pids) != 2 {
		state.Close()
		t.Fatalf("recorded process IDs = %v; want shell and child", pids)
	}
	defer terminateDetachedTestProcess(t, pids[1])

	start := time.Now()
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("State.Close waited %v for a terminated popen", elapsed)
	}
	waitForProcessGone(t, pids[0])
	runtime.KeepAlive(results)
}

func TestIOPopenStateCloseReturnsAfterShellExit(
	t *testing.T,
) {
	pidPath := filepathForShell(t.TempDir(), "pids")
	command := fmt.Sprintf(
		`sleep 30 & child=$!; printf '%%s %%s' "$$" "$child" > %s; exit 0`,
		quoteShellWord(pidPath),
	)
	state := newStateWithIO(t, Options{})
	chunk := mustLoadString(
		t,
		state,
		"@popen-background-state-close.lua",
		"return assert(io.popen("+luaTestQuote(command)+",\"r\"))",
	)
	results, err := state.Call(chunk.Value())
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	pids := waitForProcessIDs(pidPath, 5*time.Second)
	if len(pids) != 2 {
		state.Close()
		t.Fatalf("recorded process IDs = %v; want shell and child", pids)
	}
	defer terminateDetachedTestProcess(t, pids[1])
	waitForProcessGone(t, pids[0])

	start := time.Now()
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("State.Close waited %v for a detached background job", elapsed)
	}
	runtime.KeepAlive(results)
}

func TestIOPopenCollectionAbandonsAndReapsRoot(t *testing.T) {
	pidPath := filepathForShell(t.TempDir(), "pids")
	command := fmt.Sprintf(
		`sleep 30 & child=$!; printf '%%s %%s' "$$" "$child" > %s; wait`,
		quoteShellWord(pidPath),
	)
	state := newStateWithIO(t, Options{})
	defer state.Close()
	baseline := nativeResourceCount(state.resources)
	runIOChunk(t, state, `
popen_collection_file=assert(io.popen(
  `+luaTestQuote(command)+`,"r"))
return true
`)
	pids := waitForProcessIDs(pidPath, 5*time.Second)
	if len(pids) != 2 {
		t.Fatalf("recorded process IDs = %v; want shell and child", pids)
	}
	defer terminateDetachedTestProcess(t, pids[1])
	runIOChunk(t, state, `
popen_collection_file=nil
return true
`)

	deadline := time.Now().Add(5 * time.Second)
	for nativeResourceCount(state.resources) != baseline {
		if time.Now().After(deadline) {
			t.Fatalf(
				"collected popen resource count = %d; want %d",
				nativeResourceCount(state.resources),
				baseline,
			)
		}
		if _, failure := state.collectAndFinalize(); failure != nil {
			t.Fatal(failure)
		}
		runtime.GC()
		runtime.Gosched()
	}
	waitForProcessGone(t, pids[0])
}

func TestOpenHostShellPipeDoesNotSpawnAfterCancellation(t *testing.T) {
	path := filepathForShell(t.TempDir(), "should-not-exist")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pipe, process, err, cancelled := openHostShellPipe(
		ctx,
		"printf spawned > "+quoteShellWord(path),
		processPipeRead,
	)
	if pipe != nil || process != nil || err != nil || !cancelled {
		t.Fatalf(
			"pre-cancelled popen = (%v, %v, %v, %v)",
			pipe,
			process,
			err,
			cancelled,
		)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-cancelled popen created %q: %v", path, err)
	}
}

func assertPopenContextFailure(t *testing.T, err error) {
	t.Helper()
	var failure *Error
	if !errors.As(err, &failure) ||
		failure.Category() != ContextError ||
		!errors.Is(err, context.Canceled) {
		t.Fatalf("popen cancellation = %#v", err)
	}
}
