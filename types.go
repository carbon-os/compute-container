package compute_container

// ExitStatus holds the exit code from a container process.
type ExitStatus struct {
	Code int
}

// ExecResult holds the captured output and exit code from a one-shot command.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// DirEntry represents a single entry in a container directory listing.
type DirEntry struct {
	Name  string
	IsDir bool
}

// RunParams describes a process to run inside the container via Run.
type RunParams struct {
	Cmd        []string
	Env        map[string]string
	WorkingDir string // defaults to C:\
}