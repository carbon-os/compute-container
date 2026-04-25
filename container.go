package compute_container

// Container is a handle to a running container instance.
// Create one with NewContainer; the zero value is not valid.
type Container struct {
	p containerPlatform
}

// ── Lifecycle ────────────────────────────────────────────────────────────────

// Run starts the given command with host stdin/stdout/stderr attached and
// blocks until the process exits.
func (c *Container) Run(params RunParams) (ExitStatus, error) {
	return c.p.run(params)
}

// Exec runs a one-shot command inside a running container and captures its
// output.
func (c *Container) Exec(cmd []string) (ExecResult, error) {
	return c.p.exec(cmd)
}

// Shell opens an interactive shell session inside the container.
func (c *Container) Shell() error {
	return c.p.shell()
}

// Kill forcefully terminates the container.
func (c *Container) Kill() error {
	return c.p.kill()
}

// Close releases all container resources. Always defer this.
func (c *Container) Close() error {
	return c.p.close()
}

// ── Filesystem ───────────────────────────────────────────────────────────────

// ReadFile reads a file from the container filesystem.
func (c *Container) ReadFile(path string) ([]byte, error) {
	return c.p.readFile(path)
}

// WriteFile writes data to a file inside the container filesystem.
// Parent directories must already exist; see MakeDir.
func (c *Container) WriteFile(path string, data []byte) error {
	return c.p.writeFile(path, data)
}

// DeleteFile deletes a file from the container filesystem.
func (c *Container) DeleteFile(path string) error {
	return c.p.deleteFile(path)
}

// CopyIn copies a file from the host into the container.
func (c *Container) CopyIn(hostPath, containerPath string) error {
	return c.p.copyIn(hostPath, containerPath)
}

// CopyOut copies a file from the container to the host.
func (c *Container) CopyOut(containerPath, hostPath string) error {
	return c.p.copyOut(containerPath, hostPath)
}

// ListDir lists the contents of a directory inside the container.
func (c *Container) ListDir(path string) ([]DirEntry, error) {
	return c.p.listDir(path)
}

// MakeDir creates a directory (and all intermediate parents) inside the
// container. It is not an error if the directory already exists.
func (c *Container) MakeDir(path string) error {
	return c.p.makeDir(path)
}

// DeleteDir removes a directory and all its contents from the container.
func (c *Container) DeleteDir(path string) error {
	return c.p.deleteDir(path)
}