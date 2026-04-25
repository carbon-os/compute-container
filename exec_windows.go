//go:build windows

package compute_container

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/Microsoft/hcsshim"
)

// runProc is the single chokepoint for all process creation.
//
// It creates a process from cfg, optionally writes stdinData then closes
// stdin, drains stdout and stderr concurrently, waits for exit, and returns
// the captured bytes and exit code.
func (wc *winContainer) runProc(cfg *hcsshim.ProcessConfig, stdinData []byte) (stdout, stderr []byte, exitCode int, err error) {
	wc.mu.Lock()
	proc, err := wc.hc.CreateProcess(cfg)
	wc.mu.Unlock()
	if err != nil {
		return nil, nil, -1, fmt.Errorf("create process: %w", err)
	}
	defer proc.Close()

	pstdin, pstdout, pstderr, err := proc.Stdio()
	if err != nil {
		return nil, nil, -1, fmt.Errorf("stdio: %w", err)
	}

	// Drain stdout and stderr concurrently so we never deadlock on a full pipe
	// buffer while the process is still running.
	var outBuf, errBuf bytes.Buffer
	var drainWg sync.WaitGroup

	if pstdout != nil {
		drainWg.Add(1)
		go func() {
			defer drainWg.Done()
			io.Copy(&outBuf, pstdout)
		}()
	}
	if pstderr != nil {
		drainWg.Add(1)
		go func() {
			defer drainWg.Done()
			io.Copy(&errBuf, pstderr)
		}()
	}

	if pstdin != nil {
		if len(stdinData) > 0 {
			_, _ = pstdin.Write(stdinData)
		}
		_ = pstdin.Close() // signal EOF to the process
	}

	_ = proc.Wait() // non-zero exit comes back as error; we read ExitCode below

	drainWg.Wait() // ensure all output is in the buffers before we return

	code, err := proc.ExitCode()
	if err != nil {
		code = -1
		err = nil
	}
	return outBuf.Bytes(), errBuf.Bytes(), code, nil
}

// cmdExec runs a cmd.exe /C one-liner and returns a structured result.
// This is the primitive used by all filesystem operations.
func (wc *winContainer) cmdExec(cmdLine string) (ExecResult, error) {
	out, errOut, code, err := wc.runProc(&hcsshim.ProcessConfig{
		ApplicationName:  `C:\Windows\System32\cmd.exe`,
		CommandLine:      `cmd.exe /C ` + cmdLine,
		WorkingDirectory: `C:\`,
		EmulateConsole:   false,
		CreateStdInPipe:  false,
		CreateStdOutPipe: true,
		CreateStdErrPipe: true,
	}, nil)
	if err != nil {
		return ExecResult{}, err
	}
	return ExecResult{Stdout: string(out), Stderr: string(errOut), ExitCode: code}, nil
}

// exec runs an arbitrary command inside the container and captures output.
func (wc *winContainer) exec(cmd []string) (ExecResult, error) {
	if len(cmd) == 0 {
		return ExecResult{}, fmt.Errorf("compute-container: empty command")
	}
	out, errOut, code, err := wc.runProc(&hcsshim.ProcessConfig{
		ApplicationName:  cmd[0],
		CommandLine:      buildCmdLine(cmd),
		WorkingDirectory: `C:\`,
		EmulateConsole:   false,
		CreateStdInPipe:  false,
		CreateStdOutPipe: true,
		CreateStdErrPipe: true,
	}, nil)
	if err != nil {
		return ExecResult{}, err
	}
	return ExecResult{Stdout: string(out), Stderr: string(errOut), ExitCode: code}, nil
}

// run starts a process with host stdio attached and blocks until it exits.
func (wc *winContainer) run(params RunParams) (ExitStatus, error) {
	if len(params.Cmd) == 0 {
		return ExitStatus{}, fmt.Errorf("compute-container: empty command")
	}

	env := map[string]string{
		"PATH":    `C:\Windows\System32;C:\Windows`,
		"COMSPEC": `C:\Windows\System32\cmd.exe`,
	}
	for k, v := range params.Env {
		env[k] = v
	}
	workDir := `C:\`
	if params.WorkingDir != "" {
		workDir = params.WorkingDir
	}

	wc.mu.Lock()
	proc, err := wc.hc.CreateProcess(&hcsshim.ProcessConfig{
		ApplicationName:  params.Cmd[0],
		CommandLine:      buildCmdLine(params.Cmd),
		WorkingDirectory: workDir,
		EmulateConsole:   false,
		CreateStdInPipe:  true,
		CreateStdOutPipe: true,
		CreateStdErrPipe: true,
		Environment:      env,
	})
	wc.mu.Unlock()
	if err != nil {
		return ExitStatus{}, fmt.Errorf("compute-container: create process: %w", err)
	}
	defer proc.Close()

	pstdin, pstdout, pstderr, err := proc.Stdio()
	if err != nil {
		return ExitStatus{}, fmt.Errorf("compute-container: stdio: %w", err)
	}

	go io.Copy(os.Stdout, pstdout)
	go io.Copy(os.Stderr, pstderr)
	go func() {
		io.Copy(pstdin, os.Stdin)
		pstdin.Close()
	}()

	_ = proc.Wait()
	code, _ := proc.ExitCode()
	return ExitStatus{Code: code}, nil
}

// shell opens an interactive cmd.exe with a PTY so arrow keys and tab-complete
// work. stderr is merged into stdout in console mode.
func (wc *winContainer) shell() error {
	wc.mu.Lock()
	proc, err := wc.hc.CreateProcess(&hcsshim.ProcessConfig{
		ApplicationName:  `C:\Windows\System32\cmd.exe`,
		CommandLine:      `cmd.exe`,
		WorkingDirectory: `C:\`,
		EmulateConsole:   true,
		CreateStdInPipe:  true,
		CreateStdOutPipe: true,
		CreateStdErrPipe: false, // merged into stdout by the console emulator
		ConsoleSize:      [2]uint{24, 80},
	})
	wc.mu.Unlock()
	if err != nil {
		return fmt.Errorf("compute-container: create shell: %w", err)
	}
	defer proc.Close()

	pstdin, pstdout, _, err := proc.Stdio()
	if err != nil {
		return fmt.Errorf("compute-container: stdio: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(os.Stdout, pstdout)
	}()
	go func() {
		io.Copy(pstdin, os.Stdin)
		pstdin.Close()
	}()

	_ = proc.Wait()
	wg.Wait()
	return nil
}

// buildCmdLine assembles a Windows CommandLine string, quoting any argument
// that contains spaces, tabs, or double-quotes.
func buildCmdLine(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"") {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}