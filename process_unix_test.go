//go:build (aix || android || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris) && !ios

package lua

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOSExecutePreservesUnixSystemStatus(t *testing.T) {
	state := newStateWithOS(t)
	defer state.Close()
	chunk := mustLoadString(t, state, "@execute-status.lua", `
return os.execute(""),
  os.execute("exit 0"),
  os.execute("exit 7"),
  os.execute("kill -TERM $$"),
  os.execute("command_that_does_not_exist_lunar 2>/dev/null"),
  os.execute("exit 0\000exit 9", "ignored")
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Number(0),
		Number(0),
		Number(7<<8),
		Number(float64(syscall.SIGTERM)),
		Number(127<<8),
		Number(0),
	)
}

func TestOSExecuteUsesTheRawShellAndProcessEnvironment(t *testing.T) {
	const environmentName = "LUNAR_LUA_EXECUTE_VALUE"
	const environmentValue = "value with spaces"
	t.Setenv(environmentName, environmentValue)

	outputPath := filepathForShell(t.TempDir(), "shell output")
	command := fmt.Sprintf(
		`printf '%%s' "$%s" > %s && printf '%s' >> %s`,
		environmentName,
		quoteShellWord(outputPath),
		"!",
		quoteShellWord(outputPath),
	)
	state := newStateWithOS(t)
	defer state.Close()
	chunk := mustLoadString(
		t,
		state,
		"@execute-shell.lua",
		"return os.execute("+strconv.Quote(command)+")",
	)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(0))
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != environmentValue+"!" {
		t.Fatalf("shell output = %q", content)
	}
}

func TestOSExecuteCoercesNumericCommands(t *testing.T) {
	directory := t.TempDir()
	commandPath := filepathForShell(directory, "0")
	if err := os.WriteFile(
		commandPath,
		[]byte("#!/bin/sh\nexit 6\n"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))

	state := newStateWithOS(t)
	defer state.Close()
	chunk := mustLoadString(
		t,
		state,
		"@execute-number.lua",
		"return os.execute(0)",
	)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(6<<8))
}

func TestOSExecuteDoesNotSpawnAfterContextCancellation(t *testing.T) {
	path := filepathForShell(t.TempDir(), "should-not-exist")
	command := "printf spawned > " + quoteShellWord(path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	status, cancelled := executeHostShell(ctx, command)
	if status != -1 || !cancelled {
		t.Fatalf(
			"pre-cancelled execute = (%d, %v); want (-1, true)",
			status,
			cancelled,
		)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-cancelled command created %q: %v", path, err)
	}
}

func TestChildProcessPublishesOneReusableWaitResult(t *testing.T) {
	command, err := newHostShellCommand("exit 9")
	if err != nil {
		t.Fatalf("host shell is unavailable: %v", err)
	}
	process, err := startChildProcess(command)
	if err != nil {
		t.Fatal(err)
	}
	first, cancelled := process.wait(context.Background())
	if cancelled ||
		hostProcessStatus(first.state) != 9<<8 ||
		first.waitErr == nil ||
		processWaitError(first) != nil {
		t.Fatalf(
			"first wait = (status=%d, err=%v, cancelled=%v)",
			hostProcessStatus(first.state),
			first.waitErr,
			cancelled,
		)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	second, cancelled := process.wait(ctx)
	if cancelled ||
		second.state != first.state ||
		second.waitErr != first.waitErr {
		t.Fatalf(
			"repeated wait = (%+v, %v); want published result",
			second,
			cancelled,
		)
	}
	process.abandon()
}

func TestOSExecuteCancellationTerminatesAndReapsRoot(
	t *testing.T,
) {
	directory := t.TempDir()
	pidPath := filepathForShell(directory, "pids")
	command := fmt.Sprintf(
		`sleep 30 & child=$!; printf '%%s %%s' "$$" "$child" > %s; wait`,
		quoteShellWord(pidPath),
	)
	state := newStateWithOS(t)
	defer state.Close()
	chunk := mustLoadString(
		t,
		state,
		"@execute-cancel.lua",
		"execute_continued = false\n"+
			"pcall(function() os.execute("+strconv.Quote(command)+") end)\n"+
			"execute_continued = true",
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pidResult := make(chan []int, 1)
	go func() {
		pidResult <- waitForProcessIDs(pidPath, 5*time.Second)
		cancel()
	}()
	_, err := state.CallContext(ctx, chunk.Value())
	var failure *Error
	if !errors.As(err, &failure) ||
		failure.Category() != ContextError ||
		!errors.Is(err, context.Canceled) {
		t.Fatalf("execute cancellation = %#v", err)
	}
	pids := <-pidResult
	if len(pids) != 2 {
		t.Fatalf("recorded process IDs = %v; want shell and child", pids)
	}
	defer terminateDetachedTestProcess(t, pids[1])
	waitForProcessGone(t, pids[0])
	continued, err := state.RawGlobal("execute_continued")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, continued, Bool(false))

	recovery := mustLoadString(
		t,
		state,
		"@execute-after-cancel.lua",
		`return 64`,
	)
	results, err := state.Call(recovery.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(64))
}

func filepathForShell(directory, name string) string {
	return directory + string(os.PathSeparator) + name
}

func quoteShellWord(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

func waitForProcessIDs(path string, timeout time.Duration) []int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(path)
		if err == nil {
			fields := strings.Fields(string(content))
			pids := make([]int, 0, len(fields))
			for _, field := range fields {
				pid, conversionErr := strconv.Atoi(field)
				if conversionErr != nil {
					return nil
				}
				pids = append(pids, pid)
			}
			if len(pids) != 0 {
				return pids
			}
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}

func waitForProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("probe process %d: %v", pid, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d survived context cancellation", pid)
		}
		time.Sleep(time.Millisecond)
	}
}

func terminateDetachedTestProcess(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil &&
		!errors.Is(err, syscall.ESRCH) {
		t.Fatalf("terminate detached process %d: %v", pid, err)
	}
	waitForProcessGone(t, pid)
}
