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
})
if err != nil {
    return err
}
defer c.Close()

// Run a one-shot command and wait for exit
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
| `Run(params)` | Start the container and block until exit |
| `Exec(cmd)` | Run a one-shot command in a running container |
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
| `MakeDir(path)` | Create a directory (recursive) |
| `DeleteDir(path)` | Delete a directory and its contents |

## CLI

`container-cli` is a command line tool for exercising the runtime directly
during development. It exposes the full API surface as subcommands.

```
container-cli run    --base <path> --scratch <path> -- <cmd>
container-cli exec   --base <path> --scratch <path> -- <cmd>
container-cli shell  --base <path> --scratch <path>
container-cli ls     --base <path> --scratch <path> <container-path>
container-cli cat    --base <path> --scratch <path> <container-path>
container-cli mkdir  --base <path> --scratch <path> <container-path>
container-cli rm     --base <path> --scratch <path> <container-path>
container-cli cp-in  --base <path> --scratch <path> <host-path> <container-path>
container-cli cp-out --base <path> --scratch <path> <container-path> <host-path>
```

## Platforms

| Platform | Backend                   |
|----------|---------------------------|
| Linux    | namespaces + cgroups      |
| Windows  | Host Compute System (HCS) |

Platform selection is automatic at compile time. The public API is identical
across platforms.

## Architecture

```
compute-container/
  compute.go            // NewContainer, ImageMount
  container.go          // Container, full public API
  types.go              // ExitStatus, ExecResult, DirEntry
  platform.go           // internal platform interface
  windows/
    container.go        // HCS implementation
    layer.go            // PrepareLayer / UnprepareLayer
    exec.go             // CreateProcess, Stdio
    fs.go               // Filesystem ops via structured exec
  linux/
    container.go        // clone(2), namespace setup
    exec.go             // process execution
    fs.go               // Filesystem ops via structured exec
  cmd/
    cli/
      main.go           // container-cli binary
```

## Scope

This package does not:
- Download or resolve images
- Manage layer caching or digests
- Interact with any container registry

Image preparation is the responsibility of `carbon-os/compute-image`. This
package only accepts the resulting paths.