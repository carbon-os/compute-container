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

	// Terminate any previously leaked containers that are still holding the
	// scratch layer open. Without this, a crashed or unclean prior run leaves
	// the scratch locked and CreateContainer fails with "file in use".
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

// terminateStaleContainers finds any running HCS containers whose scratch
// (LayerFolderPath) matches ours and forcefully terminates them. This recovers
// from a prior unclean exit that left the scratch layer locked.
func terminateStaleContainers(scratchPath string) error {
	q := hcsshim.ComputeSystemQuery{
		Types:  []string{"Container"},
		Owners: []string{"carbon-compute"},
	}
	infos, err := hcsshim.GetContainers(q)
	if err != nil {
		// GetContainers failing is non-fatal; we'll let CreateContainer
		// surface the real error if the scratch is still locked.
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