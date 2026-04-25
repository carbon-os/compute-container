//go:build windows

package compute_container

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

func quote(path string) string {
	return `"` + strings.ReplaceAll(path, `"`, `""`) + `"`
}

func (wc *winContainer) scratchFilePath(containerPath string) string {
	p := strings.ReplaceAll(containerPath, "/", `\`)
	if len(p) >= 3 && p[1] == ':' && p[2] == '\\' {
		p = p[3:]
	} else if len(p) >= 2 && p[1] == ':' {
		p = p[2:]
	}
	p = strings.TrimPrefix(p, `\`)
	return filepath.Join(wc.scratch, "Files", p)
}

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

func (wc *winContainer) copyIn(hostPath, containerPath string) error {
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return fmt.Errorf("copy-in: read %q: %w", hostPath, err)
	}
	return wc.writeFile(containerPath, data)
}

func (wc *winContainer) copyOut(containerPath, hostPath string) error {
	var data []byte

	if d, err := os.ReadFile(wc.scratchFilePath(containerPath)); err == nil {
		data = d
	} else {
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

func (wc *winContainer) dirNames(winAbsPath string, dirsOnly bool) ([]string, error) {
	attr := "/A:-D"
	if dirsOnly {
		attr = "/A:D"
	}
	res, err := wc.cmdExec(fmt.Sprintf(`dir /B %s %s`, attr, quote(winAbsPath)))
	if err != nil {
		return nil, err
	}

	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stderr)
		// cmd.exe emits "File Not Found" when the filter matches nothing
		// (e.g. an empty directory, or no files when listing dirs-only).
		// This is not an error — just return an empty list.
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "file not found") || msg == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("%s", msg)
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

func (wc *winContainer) makeDir(path string) error {
	res, err := wc.cmdExec(fmt.Sprintf(`mkdir %s`, quote(winPath(path))))
	if err != nil {
		return fmt.Errorf("make dir %q: %w", path, err)
	}
	if res.ExitCode != 0 {
		msg := strings.ToLower(strings.TrimSpace(res.Stderr))
		if strings.Contains(msg, "already exists") {
			return nil
		}
		return fmt.Errorf("make dir %q: %s", path, strings.TrimSpace(res.Stderr))
	}
	return nil
}

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