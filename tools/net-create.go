//go:build windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Microsoft/hcsshim"
)

func main() {
	name    := flag.String("name", "nat", "HNS network name")
	netType := flag.String("type", "NAT", "HNS network type (NAT, L2Bridge, Transparent, etc.)")
	subnet  := flag.String("subnet", "172.20.0.0/16", "Subnet address prefix")
	gateway := flag.String("gateway", "172.20.0.1", "Gateway address")
	list    := flag.Bool("list", false, "List existing HNS networks and exit")
	delete  := flag.String("delete", "", "Delete HNS network by name and exit")
	flag.Parse()

	// ── List ────────────────────────────────────────────────────────────────
	if *list {
		networks, err := hcsshim.HNSListNetworkRequest("GET", "", "")
		if err != nil {
			fatalf("list networks: %v", err)
		}
		if len(networks) == 0 {
			fmt.Println("(no HNS networks found)")
			return
		}
		for _, n := range networks {
			subnets := make([]string, len(n.Subnets))
			for i, s := range n.Subnets {
				subnets[i] = s.AddressPrefix
			}
			fmt.Printf("%-30s  type=%-12s  id=%s  subnets=%s\n",
				n.Name, n.Type, n.Id, strings.Join(subnets, ","))
		}
		return
	}

	// ── Delete ───────────────────────────────────────────────────────────────
	if *delete != "" {
		networks, err := hcsshim.HNSListNetworkRequest("GET", "", "")
		if err != nil {
			fatalf("list networks: %v", err)
		}
		var found *hcsshim.HNSNetwork
		for i := range networks {
			if strings.EqualFold(networks[i].Name, *delete) {
				found = &networks[i]
				break
			}
		}
		if found == nil {
			fatalf("network %q not found", *delete)
		}
		if _, err := found.Delete(); err != nil {
			fatalf("delete network %q: %v", *delete, err)
		}
		fmt.Printf("deleted network %q (%s)\n", found.Name, found.Id)
		return
	}

	// ── Check for existing ───────────────────────────────────────────────────
	networks, err := hcsshim.HNSListNetworkRequest("GET", "", "")
	if err != nil {
		fatalf("list networks: %v", err)
	}
	for _, n := range networks {
		if strings.EqualFold(n.Name, *name) {
			fmt.Printf("network %q already exists (id=%s type=%s)\n", n.Name, n.Id, n.Type)
			os.Exit(0)
		}
	}

	// ── Create ───────────────────────────────────────────────────────────────
	network := &hcsshim.HNSNetwork{
		Name: *name,
		Type: *netType,
		Subnets: []hcsshim.Subnet{
			{
				AddressPrefix:  *subnet,
				GatewayAddress: *gateway,
			},
		},
	}

	created, err := network.Create()
	if err != nil {
		fatalf("create network: %v", err)
	}

	out, _ := json.MarshalIndent(created, "", "  ")
	fmt.Printf("created network:\n%s\n", out)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}