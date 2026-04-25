# Carbon Compute Container

Carbon Compute Container is the container runtime for Carbon Compute. It is
responsible for one thing: running containers. It has no concept of image
management, registry access, or layer downloading — it assumes the image is
already prepared on disk and accepts only the paths it needs to do its job.

## Overview

`compute-container` takes a prepared rootfs and scratch path, and provides a
single `Container` handle for running and interacting with it. The API is
designed to be driven by both AI agents and human operators.

## Usage

```go
c, err := compute_container.NewContainer(compute_container.ImageMount{
    BaseLayer: "/path/to/rootfs",
    Scratch:   "/path/to/scratch", // Windows only
    Network:   "nat",              // optional HNS network name; leave empty for no networking
})
if err != nil {
    return err
}
defer c.Close()

// Run a one-shot command and capture output
result, err := c.Exec([]string{"python", "train.py"})

// Inspect the filesystem
entries, err := c.ListDir("/app/output")
data, err    := c.ReadFile("/app/output/metrics.json")

// Drop into an interactive shell
c.Shell()
```

## API

### Lifecycle
| Method | Description |
|---|---|
| `NewContainer(mount)` | Prepare a container from the given image paths |
| `Run(params)` | Start a command with host stdio attached and block until exit |
| `Exec(cmd)` | Run a one-shot command and capture output |
| `Shell()` | Open an interactive PTY session |
| `Kill()` | Forcefully terminate the container |
| `Close()` | Release all resources — always defer this |

### Filesystem
| Method | Description |
|---|---|
| `ReadFile(path)` | Read a file from the container filesystem |
| `WriteFile(path, data)` | Write a file into the container filesystem |
| `DeleteFile(path)` | Delete a file |
| `CopyIn(hostPath, containerPath)` | Copy a file from the host into the container |
| `CopyOut(containerPath, hostPath)` | Copy a file from the container to the host |
| `ListDir(path)` | List directory contents |
| `MakeDir(path)` | Create a directory (recursive, no-op if exists) |
| `DeleteDir(path)` | Delete a directory and all its contents |

## CLI

`container-cli` is a command-line tool for exercising the runtime directly
during development. It exposes the full API surface as subcommands.

```
cd cmd/cli
go build -o container-cli.exe .

container-cli run    --base <path> --scratch <path> [--network <name>] -- <cmd>
container-cli exec   --base <path> --scratch <path> [--network <name>] -- <cmd>
container-cli shell  --base <path> --scratch <path> [--network <name>]
container-cli ls     --base <path> --scratch <path> <container-path>
container-cli cat    --base <path> --scratch <path> <container-path>
container-cli mkdir  --base <path> --scratch <path> <container-path>
container-cli rm     --base <path> --scratch <path> <container-path>
container-cli rmdir  --base <path> --scratch <path> <container-path>
container-cli cp-in  --base <path> --scratch <path> <host-path> <container-path>
container-cli cp-out --base <path> --scratch <path> <container-path> <host-path>
```

The `--network` flag accepts an HNS network name (e.g. `nat`) and attaches the
container to that network on startup. Omit it for an isolated container with no
networking. To list available networks:

```powershell
Get-HnsNetwork | Select Name
```

## Networking (Windows)

HNS network setup is handled by the `tools/net-create` utility. Run it once as
Administrator before starting networked containers.

```
cd tools/net-create
go build -o net-create.exe .

# List existing HNS networks
.\net-create.exe -list

# Create a NAT network (default settings)
.\net-create.exe -name nat -type NAT -subnet 172.20.0.0/16 -gateway 172.20.0.1

# Verify
.\net-create.exe -list

# Delete a network
.\net-create.exe -delete nat
```

Once the network exists, pass its name via `--network`:

```
container-cli.exe run --base <path> --scratch <path> --network nat -- cmd.exe
```

## Platforms

| Platform | Backend                   |
|----------|---------------------------|
| Linux    | namespaces + cgroups      |
| Windows  | Host Compute System (HCS) |

Platform selection is automatic at compile time via Go build tags. The public
API is identical across platforms.

## Architecture

```
compute-container/
  compute.go              // NewContainer, ImageMount
  container.go            // Container, full public API
  types.go                // ExitStatus, ExecResult, DirEntry, RunParams
  platform.go             // internal platform interface
  container_windows.go    // HCS container lifecycle (create, start, keepalive, close)
  exec_windows.go         // process execution (run, exec, shell, runProc)
  fs_windows.go           // filesystem ops via cmd.exe (read, write, list, copy, ...)
  network_windows.go      // HNS network setup and endpoint lifecycle
  cmd/
    cli/
      main.go             // container-cli binary
  tools/
    net-create/
      main.go             // HNS network creation/deletion utility
```

## Scope

This package does not:
- Download or resolve images
- Manage layer caching or digests
- Interact with any container registry

Image preparation is the responsibility of `carbon-os/compute-image`. This
package only accepts the resulting paths.