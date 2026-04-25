//go:build windows

package compute_container

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim"
)

type winContainer struct {
	hc         hcsshim.Container
	scratch    string
	endpointID string

	mu        sync.Mutex
	keepProc  hcsshim.Process
	keepStdin io.WriteCloser
}

func newPlatformContainer(mount ImageMount) (containerPlatform, error) {
	scratch, err := filepath.Abs(mount.Scratch)
	if err != nil {
		return nil, fmt.Errorf("compute-container: resolve scratch: %w", err)
	}

	// 1. Read the layerchain.json to get the exact parent layer order (Newest-to-Oldest)
	chainBytes, err := os.ReadFile(filepath.Join(scratch, "layerchain.json"))
	if err != nil {
		return nil, fmt.Errorf("compute-container: read layerchain.json: %w", err)
	}
	var parentChain []string
	if err := json.Unmarshal(chainBytes, &parentChain); err != nil {
		return nil, fmt.Errorf("compute-container: parse layerchain.json: %w", err)
	}

	// 2. Map the parent paths into HCS Layer structs
	// FIX: Revert to standard forward iteration! hcsshim natively expects Newest-to-Oldest.
	var layers []hcsshim.Layer
	for _, p := range parentChain {
		layers = append(layers, hcsshim.Layer{
			ID:   hcsshim.NewGUID(filepath.Base(p)).ToString(),
			Path: p,
		})
	}

	if err := terminateStaleContainers(scratch); err != nil {
		return nil, fmt.Errorf("compute-container: cleanup stale containers: %w", err)
	}

	endpointID, err := setupNetwork(mount.Network)
	if err != nil {
		return nil, err
	}

	name := fmt.Sprintf("carbon-%d", time.Now().UnixNano())
	cfg := hcsshim.ContainerConfig{
		SystemType:      "Container",
		Name:            name,
		Owner:           "carbon-compute",
		LayerFolderPath: scratch,
		Layers:          layers, // Newest-to-Oldest
	}

	// FIX: If Hyper-V isolation is requested, tell HCS to spin up the Utility VM.
	// The UtilityVM kernel lives in the Base OS layer, which is the OLDEST layer
	// (the last element in our Newest-to-Oldest array).
	if mount.HyperV {
		baseLayer := layers[len(layers)-1].Path
		cfg.HvPartition = true
		cfg.HvRuntime = &hcsshim.HvRuntime{
			ImagePath: filepath.Join(baseLayer, "UtilityVM"),
		}
	}

	hc, err := createContainerWithRetry(name, &cfg, scratch)
	if err != nil {
		teardownEndpoint(endpointID)
		return nil, fmt.Errorf("compute-container: create container: %w", err)
	}

	if err := hc.Start(); err != nil {
		_ = hc.Close()
		teardownEndpoint(endpointID)
		return nil, fmt.Errorf("compute-container: start container: %w", err)
	}

	// Hot-attach the network endpoint now that the container is running.
	if err := attachEndpoint(name, endpointID); err != nil {
		_ = hc.Terminate()
		_ = hc.Close()
		teardownEndpoint(endpointID)
		return nil, err
	}

	wc := &winContainer{
		hc:         hc,
		scratch:    scratch,
		endpointID: endpointID,
	}

	if err := wc.startKeepalive(); err != nil {
		_ = hc.Terminate()
		_ = hc.Close()
		teardownEndpoint(endpointID)
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

func createContainerWithRetry(name string, cfg *hcsshim.ContainerConfig, scratch string) (hcsshim.Container, error) {
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
	return nil
}