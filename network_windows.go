//go:build windows

package compute_container

import (
	"fmt"
	"strings"

	"github.com/Microsoft/hcsshim"
)

func setupNetwork(networkName string) (endpointID string, err error) {
	if networkName == "" {
		return "", nil
	}

	networks, err := hcsshim.HNSListNetworkRequest("GET", "", "")
	if err != nil {
		return "", fmt.Errorf("compute-container: list HNS networks: %w", err)
	}

	var network *hcsshim.HNSNetwork
	for i := range networks {
		if strings.EqualFold(networks[i].Name, networkName) {
			network = &networks[i]
			break
		}
	}
	if network == nil {
		return "", fmt.Errorf("compute-container: HNS network %q not found (run: Get-HnsNetwork | Select Name)", networkName)
	}

	ep, err := (&hcsshim.HNSEndpoint{
		VirtualNetwork: network.Id,
	}).Create()
	if err != nil {
		return "", fmt.Errorf("compute-container: create HNS endpoint on %q: %w", networkName, err)
	}

	return ep.Id, nil
}

// attachEndpoint hot-attaches the HNS endpoint to a running container.
func attachEndpoint(containerID, endpointID string) error {
	if endpointID == "" {
		return nil
	}
	if err := hcsshim.HotAttachEndpoint(containerID, endpointID); err != nil {
		return fmt.Errorf("compute-container: hot-attach endpoint: %w", err)
	}
	return nil
}

func teardownEndpoint(id string) {
	if id == "" {
		return
	}
	ep, err := hcsshim.GetHNSEndpointByID(id)
	if err != nil {
		return
	}
	_, _ = ep.Delete()
}