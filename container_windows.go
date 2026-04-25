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
	endpointID  string

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
		_ = err
	}

	endpointID, err := setupNetwork(mount.Network)
	if err != nil {
		_ = hcsshim.UnprepareLayer(di, baseLayerID)
		return nil, err
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
		// No EndpointList here — we hot-attach after Start()
	}

	hc, err := createContainerWithRetry(name, &cfg, scratch, di, baseLayerID)
	if err != nil {
		teardownEndpoint(endpointID)
		_ = hcsshim.UnprepareLayer(di, baseLayerID)
		return nil, fmt.Errorf("compute-container: create container: %w", err)
	}

	if err := hc.Start(); err != nil {
		_ = hc.Close()
		teardownEndpoint(endpointID)
		_ = hcsshim.UnprepareLayer(di, baseLayerID)
		return nil, fmt.Errorf("compute-container: start container: %w", err)
	}

	// Hot-attach the network endpoint now that the container is running.
	if err := attachEndpoint(name, endpointID); err != nil {
		_ = hc.Terminate()
		_ = hc.Close()
		teardownEndpoint(endpointID)
		_ = hcsshim.UnprepareLayer(di, baseLayerID)
		return nil, err
	}

	wc := &winContainer{
		hc:          hc,
		di:          di,
		baseLayerID: baseLayerID,
		scratch:     scratch,
		endpointID:  endpointID,
	}

	if err := wc.startKeepalive(); err != nil {
		_ = hc.Terminate()
		_ = hc.Close()
		teardownEndpoint(endpointID)
		_ = hcsshim.UnprepareLayer(di, baseLayerID)
		return nil, fmt.Errorf("compute-container: keepalive: %w", err)
	}

	return wc, nil
}

func terminateStaleContainers(scratchPath string) error {
	q := hcsshim.ComputeSystemQuery{
		Types: []string{"Container"},
	}
	infos, err := hcsshim.GetContainers(q)
	if err != nil {
		return nil
	}

	for _, info := range infos {
		if !strings.HasPrefix(info.ID, "carbon-") &&
			!strings.HasPrefix(info.Name, "carbon-") {
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

func createContainerWithRetry(name string, cfg *hcsshim.ContainerConfig, scratch string, di hcsshim.DriverInfo, baseLayerID string) (hcsshim.Container, error) {
	hc, err := hcsshim.CreateContainer(name, cfg)
	if err == nil {
		return hc, nil
	}

	if !strings.Contains(err.Error(), "being used by another process") {
		return nil, err
	}

	_ = terminateStaleContainers(scratch)
	time.Sleep(2 * time.Second)

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
	teardownEndpoint(wc.endpointID)
	_ = hcsshim.UnprepareLayer(wc.di, wc.baseLayerID)
	return nil
}