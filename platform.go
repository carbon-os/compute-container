package compute_container

// containerPlatform is the internal interface satisfied by each OS backend.
// The public Container type delegates everything here.
type containerPlatform interface {
	run(params RunParams) (ExitStatus, error)
	exec(cmd []string) (ExecResult, error)
	shell() error
	kill() error
	close() error

	readFile(path string) ([]byte, error)
	writeFile(path string, data []byte) error
	deleteFile(path string) error
	copyIn(hostPath, containerPath string) error
	copyOut(containerPath, hostPath string) error
	listDir(path string) ([]DirEntry, error)
	makeDir(path string) error
	deleteDir(path string) error
}