//go:build windows

package compute_container

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ── Path helpers ─────────────────────────────────────────────────────────────

// winPath converts a container path to a Windows absolute path.
// Forward slashes are normalised, and a C:\ prefix is added when absent.
//
//	/app/output   →  C:\app\output
//	C:\app        →  C:\app   (unchanged)
func winPath(path string) string {
	p := strings.ReplaceAll(path, "/", `\`)
	if len(p) >= 2 && p[1] == ':' {
		return p
	}
	if strings.HasPrefix(p, `\`) {
		return `C:` + p
	}
	return `C:\` + p
}

// quote wraps a Windows path in double-quotes safe for cmd.exe.
// Any embedded double-quotes are doubled (cmd.exe convention).
func quote(path string) string {
	return `"` + strings.ReplaceAll(path, `"`, `""`) + `"`
}

// scratchFilePath translates a container path to its location on the host
// inside the writable scratch layer. Windows container scratch layers store
// the container filesystem at <scratch>\Files\<relative-path>.
func (wc *winContainer) scratchFilePath(containerPath string) string {
	p := strings.ReplaceAll(containerPath, "/", `\`)
	// Strip leading drive + backslash: C:\foo → foo
	if len(p) >= 3 && p[1] == ':' && p[2] == '\\' {
		p = p[3:]
	} else if len(p) >= 2 && p[1] == ':' {
		p = p[2:]
	}
	p = strings.TrimPrefix(p, `\`)
	return filepath.Join(wc.scratch, "Files", p)
}

// ── Read ─────────────────────────────────────────────────────────────────────

// readFile reads a file from the container using `cmd.exe /C type`.
//
// Output is captured from the stdout pipe. This is safe for text and most
// binary content. The one edge case: if a file contains a raw 0x1A (Ctrl-Z)
// byte, cmd.exe treats it as EOF in text mode and may truncate the output.
// Use CopyOut for strict binary correctness on files that may contain 0x1A.
func (wc *winContainer) readFile(path string) ([]byte, error) {
	res, err := wc.cmdExec(fmt.Sprintf(`type %s`, quote(winPath(path))))
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", path, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("read file %q: %s", path, strings.TrimSpace(res.Stderr))
	}
	return []byte(res.Stdout), nil
}

// ── Write ────────────────────────────────────────────────────────────────────

// writeFile writes data into the container by placing the file directly into
// the scratch (writable) layer on the host filesystem. This is binary-safe and
// does not involve a cmd.exe round-trip.
//
// The scratch layer maps container paths to: <scratch>\Files\<path>
// The running container sees writes here immediately through the overlay.
func (wc *winContainer) writeFile(path string, data []byte) error {
	hostPath := wc.scratchFilePath(path)
	if err := os.MkdirAll(filepath.Dir(hostPath), 0755); err != nil {
		return fmt.Errorf("write file %q: create parent dirs: %w", path, err)
	}
	if err := os.WriteFile(hostPath, data, 0644); err != nil {
		return fmt.Errorf("write file %q: %w", path, err)
	}
	return nil
}

// ── Delete ───────────────────────────────────────────────────────────────────

// deleteFile deletes a file via `cmd.exe /C del /F /Q`.
func (wc *winContainer) deleteFile(path string) error {
	res, err := wc.cmdExec(fmt.Sprintf(`del /F /Q %s`, quote(winPath(path))))
	if err != nil {
		return fmt.Errorf("delete file %q: %w", path, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("delete file %q: %s", path, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// ── Copy ─────────────────────────────────────────────────────────────────────

// copyIn reads a file from the host and writes it into the container.
func (wc *winContainer) copyIn(hostPath, containerPath string) error {
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return fmt.Errorf("copy-in: read %q: %w", hostPath, err)
	}
	return wc.writeFile(containerPath, data)
}

// copyOut copies a file from the container to the host.
//
// Strategy: check the scratch layer on the host first (fast, binary-safe).
// If the file was not written by the container — i.e. it lives in the
// read-only base layer — fall back to reading it via `type` in-container.
func (wc *winContainer) copyOut(containerPath, hostPath string) error {
	var data []byte

	if d, err := os.ReadFile(wc.scratchFilePath(containerPath)); err == nil {
		// File is in the writable scratch layer; read it directly.
		data = d
	} else {
		// File is in the base layer; read via the container's merged view.
		var readErr error
		data, readErr = wc.readFile(containerPath)
		if readErr != nil {
			return fmt.Errorf("copy-out: %w", readErr)
		}
	}

	if err := os.MkdirAll(filepath.Dir(hostPath), 0755); err != nil {
		return fmt.Errorf("copy-out: create parent dirs for %q: %w", hostPath, err)
	}
	if err := os.WriteFile(hostPath, data, 0644); err != nil {
		return fmt.Errorf("copy-out: write %q: %w", hostPath, err)
	}
	return nil
}

// ── Directory ops ────────────────────────────────────────────────────────────

// listDir lists a container directory by running two `dir /B` passes: one for
// subdirectories (/A:D) and one for files (/A:-D). This avoids parsing the
// verbose dir output while still giving us the IsDir flag for each entry.
func (wc *winContainer) listDir(path string) ([]DirEntry, error) {
	p := winPath(path)

	dirs, err := wc.dirNames(p, true)
	if err != nil {
		return nil, fmt.Errorf("list dir %q: %w", path, err)
	}
	files, err := wc.dirNames(p, false)
	if err != nil {
		return nil, fmt.Errorf("list dir %q: %w", path, err)
	}

	entries := make([]DirEntry, 0, len(dirs)+len(files))
	for _, name := range dirs {
		entries = append(entries, DirEntry{Name: name, IsDir: true})
	}
	for _, name := range files {
		entries = append(entries, DirEntry{Name: name, IsDir: false})
	}
	return entries, nil
}

// dirNames runs `dir /B /A:D` or `dir /B /A:-D` and returns the name list,
// stripping the . and .. self-reference entries.
func (wc *winContainer) dirNames(winAbsPath string, dirsOnly bool) ([]string, error) {
	attr := "/A:-D"
	if dirsOnly {
		attr = "/A:D"
	}
	res, err := wc.cmdExec(fmt.Sprintf(`dir /B %s %s`, attr, quote(winAbsPath)))
	if err != nil {
		return nil, err
	}
	// A non-zero exit with empty stderr means the filter matched nothing
	// (e.g. no files, but the dir itself exists) — that is not an error.
	if res.ExitCode != 0 {
		if msg := strings.TrimSpace(res.Stderr); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, nil
	}

	var names []string
	for _, line := range strings.Split(strings.ReplaceAll(res.Stdout, "\r\n", "\n"), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || name == "." || name == ".." {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// makeDir creates a directory (and all parents) via `cmd.exe /C mkdir`.
// Windows mkdir creates intermediate paths by default; it is not an error
// if the directory already exists.
func (wc *winContainer) makeDir(path string) error {
	res, err := wc.cmdExec(fmt.Sprintf(`mkdir %s`, quote(winPath(path))))
	if err != nil {
		return fmt.Errorf("make dir %q: %w", path, err)
	}
	if res.ExitCode != 0 {
		msg := strings.ToLower(strings.TrimSpace(res.Stderr))
		if strings.Contains(msg, "already exists") {
			return nil // idempotent
		}
		return fmt.Errorf("make dir %q: %s", path, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// deleteDir removes a directory and all its contents via `cmd.exe /C rmdir /S /Q`.
func (wc *winContainer) deleteDir(path string) error {
	res, err := wc.cmdExec(fmt.Sprintf(`rmdir /S /Q %s`, quote(winPath(path))))
	if err != nil {
		return fmt.Errorf("delete dir %q: %w", path, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("delete dir %q: %s", path, strings.TrimSpace(res.Stderr))
	}
	return nil
}