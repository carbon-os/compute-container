//go:build windows

package compute_container

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim"
)

type winContainer struct {
	hc          hcsshim.Container
	di          hcsshim.DriverInfo
	baseLayerID string
	scratch     string

	mu        sync.Mutex
	keepProc  hcsshim.Process
	keepStdin io.WriteCloser
}

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
		Flavour: 1,
		HomeDir: filepath.Dir(baseLayer),
	}
	baseLayerID := filepath.Base(baseLayer)

	if err := terminateStaleContainers(scratch); err != nil {
		return nil, fmt.Errorf("compute-container: cleanup stale containers: %w", err)
	}

	if err := hcsshim.PrepareLayer(di, baseLayerID, nil); err != nil {
		_ = err // may already be prepared; CreateContainer will surface real failures
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

	hc, err := createContainerWithRetry(name, &cfg, scratch, di, baseLayerID)
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

// terminateStaleContainers finds any running HCS containers whose scratch
// (LayerFolderPath) matches ours and forcefully terminates them. The Owners
// filter is intentionally omitted so we catch containers started under any
// owner name from a previous build or unclean exit.
func terminateStaleContainers(scratchPath string) error {
	q := hcsshim.ComputeSystemQuery{
		Types: []string{"Container"},
	}
	infos, err := hcsshim.GetContainers(q)
	if err != nil {
		// Non-fatal; CreateContainer will surface the real error if the
		// scratch is still locked.
		return nil
	}

	for _, info := range infos {
		if !strings.EqualFold(info.RuntimeImagePath, scratchPath) {
			continue
		}
		c, err := hcsshim.OpenContainer(info.ID)
		if err != nil {
			continue
		}
		_ = c.Terminate()
		_ = c.Close()
	}

	return nil
}

// createContainerWithRetry calls hcsshim.CreateContainer and, if the scratch
// layer is still locked from a prior run, performs a broader stale-container
// sweep, waits briefly for HCS to release the layer, then retries once.
func createContainerWithRetry(name string, cfg *hcsshim.ContainerConfig, scratch string, di hcsshim.DriverInfo, baseLayerID string) (hcsshim.Container, error) {
	hc, err := hcsshim.CreateContainer(name, cfg)
	if err == nil {
		return hc, nil
	}

	if !strings.Contains(err.Error(), "being used by another process") {
		return nil, err
	}

	_ = terminateStaleContainers(scratch)
	time.Sleep(500 * time.Millisecond)

	hc, err = hcsshim.CreateContainer(name, cfg)
	return hc, err
}

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