//go:build windows

package compute_container

import (
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim"
)

// winContainer is the Windows/HCS implementation of containerPlatform.
type winContainer struct {
	hc          hcsshim.Container
	di          hcsshim.DriverInfo
	baseLayerID string // directory name of the base layer, used for Prepare/Unprepare
	scratch     string // absolute path to the scratch (writable) layer directory

	mu sync.Mutex // serialises CreateProcess calls

	// keepProc is a long-running cmd.exe whose stdin we hold open so that
	// the container does not exit when there are no other active processes.
	keepProc  hcsshim.Process
	keepStdin io.WriteCloser
}

// newPlatformContainer is the Windows factory wired into NewContainer.
func newPlatformContainer(mount ImageMount) (containerPlatform, error) {
	baseLayer, err := filepath.Abs(mount.BaseLayer)
	if err != nil {
		return nil, fmt.Errorf("compute-container: resolve base layer: %w", err)
	}
	scratch, err := filepath.Abs(mount.Scratch)
	if err != nil {
		return nil, fmt.Errorf("compute-container: resolve scratch: %w", err)
	}

	di := hcsshim.DriverInfo{
		Flavour: 1, // windowsfilter
		HomeDir: filepath.Dir(baseLayer),
	}
	baseLayerID := filepath.Base(baseLayer)

	// PrepareLayer activates the layer in the HCS filter driver.
	// It may already be prepared from a prior run; treat that as non-fatal.
	if err := hcsshim.PrepareLayer(di, baseLayerID, nil); err != nil {
		_ = err // best-effort; CreateContainer will fail if truly broken
	}

	name := fmt.Sprintf("carbon-%d", time.Now().UnixNano())
	cfg := hcsshim.ContainerConfig{
		SystemType:      "Container",
		Name:            name,
		Owner:           "carbon-compute",
		LayerFolderPath: scratch,
		Layers: []hcsshim.Layer{
			{
				ID:   hcsshim.NewGUID(baseLayerID).ToString(),
				Path: baseLayer,
			},
		},
	}

	hc, err := hcsshim.CreateContainer(name, &cfg)
	if err != nil {
		_ = hcsshim.UnprepareLayer(di, baseLayerID)
		return nil, fmt.Errorf("compute-container: create container: %w", err)
	}

	if err := hc.Start(); err != nil {
		_ = hc.Close()
		_ = hcsshim.UnprepareLayer(di, baseLayerID)
		return nil, fmt.Errorf("compute-container: start container: %w", err)
	}

	wc := &winContainer{
		hc:          hc,
		di:          di,
		baseLayerID: baseLayerID,
		scratch:     scratch,
	}

	if err := wc.startKeepalive(); err != nil {
		_ = hc.Terminate()
		_ = hc.Close()
		_ = hcsshim.UnprepareLayer(di, baseLayerID)
		return nil, fmt.Errorf("compute-container: keepalive: %w", err)
	}

	return wc, nil
}

// startKeepalive spawns a cmd.exe whose stdin pipe we keep open indefinitely.
// As long as this process is alive the container will not auto-exit.
func (wc *winContainer) startKeepalive() error {
	proc, err := wc.hc.CreateProcess(&hcsshim.ProcessConfig{
		ApplicationName:  `C:\Windows\System32\cmd.exe`,
		CommandLine:      `cmd.exe`,
		WorkingDirectory: `C:\`,
		EmulateConsole:   false,
		CreateStdInPipe:  true,
		CreateStdOutPipe: false,
		CreateStdErrPipe: false,
	})
	if err != nil {
		return fmt.Errorf("create process: %w", err)
	}
	stdin, _, _, err := proc.Stdio()
	if err != nil {
		_ = proc.Close()
		return fmt.Errorf("stdio: %w", err)
	}
	wc.keepProc = proc
	wc.keepStdin = stdin
	return nil
}

func (wc *winContainer) kill() error {
	return wc.hc.Terminate()
}

func (wc *winContainer) close() error {
	wc.mu.Lock()
	defer wc.mu.Unlock()

	if wc.keepStdin != nil {
		_ = wc.keepStdin.Close()
	}
	if wc.keepProc != nil {
		_ = wc.keepProc.Close()
	}

	if err := wc.hc.Shutdown(); err != nil {
		_ = wc.hc.Terminate()
	}
	_ = wc.hc.Close()
	_ = hcsshim.UnprepareLayer(wc.di, wc.baseLayerID)
	return nil
}