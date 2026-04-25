//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"
	"github.com/sirupsen/logrus"
	compute_container "github.com/carbon-os/compute-container"
)

const usage = `container-cli — Carbon Compute Container CLI

Usage:
  container-cli <command> --base <path> --scratch <path> [options]

Commands:
  run    -- <cmd...>               Run a command with host stdio attached
  exec   -- <cmd...>               Run a command and capture output
  shell                            Open an interactive shell
  ls     <container-path>          List directory contents
  cat    <container-path>          Print file contents to stdout
  mkdir  <container-path>          Create a directory (parents included)
  rm     <container-path>          Delete a file
  rmdir  <container-path>          Delete a directory and its contents
  cp-in  <host-path> <ctr-path>    Copy a file from host into container
  cp-out <ctr-path>  <host-path>   Copy a file from container to host
`

func init() {
	logrus.SetOutput(io.Discard)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	subcmd := os.Args[1]
	rest := os.Args[2:]

	fs := flag.NewFlagSet(subcmd, flag.ExitOnError)
	base := fs.String("base", "", "Base layer path (required)")
	scratch := fs.String("scratch", "", "Scratch layer path (required)")

	if err := fs.Parse(rest); err != nil {
		fatalf("%v", err)
	}
	positional := fs.Args()

	if *base == "" || *scratch == "" {
		fmt.Fprintln(os.Stderr, "error: --base and --scratch are required")
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	c, err := compute_container.NewContainer(compute_container.ImageMount{
		BaseLayer: *base,
		Scratch:   *scratch,
	})
	if err != nil {
		fatalf("open container: %v", err)
	}
	defer c.Close()

	switch subcmd {
	case "run":
		requireArgs(subcmd, positional, 1)
		status, err := c.Run(compute_container.RunParams{Cmd: positional})
		if err != nil {
			fatalf("run: %v", err)
		}
		os.Exit(status.Code)

	case "exec":
		requireArgs(subcmd, positional, 1)
		result, err := c.Exec(positional)
		if err != nil {
			fatalf("exec: %v", err)
		}
		fmt.Print(result.Stdout)
		fmt.Fprint(os.Stderr, result.Stderr)
		os.Exit(result.ExitCode)

	case "shell":
		if err := c.Shell(); err != nil {
			fatalf("shell: %v", err)
		}

	case "ls":
		requireArgs(subcmd, positional, 1)
		entries, err := c.ListDir(positional[0])
		if err != nil {
			fatalf("ls: %v", err)
		}
		for _, e := range entries {
			kind := "file"
			if e.IsDir {
				kind = "dir "
			}
			fmt.Printf("[%s] %s\n", kind, e.Name)
		}

	case "cat":
		requireArgs(subcmd, positional, 1)
		data, err := c.ReadFile(positional[0])
		if err != nil {
			fatalf("cat: %v", err)
		}
		os.Stdout.Write(data)

	case "mkdir":
		requireArgs(subcmd, positional, 1)
		if err := c.MakeDir(positional[0]); err != nil {
			fatalf("mkdir: %v", err)
		}

	case "rm":
		requireArgs(subcmd, positional, 1)
		if err := c.DeleteFile(positional[0]); err != nil {
			fatalf("rm: %v", err)
		}

	case "rmdir":
		requireArgs(subcmd, positional, 1)
		if err := c.DeleteDir(positional[0]); err != nil {
			fatalf("rmdir: %v", err)
		}

	case "cp-in":
		requireArgs(subcmd, positional, 2)
		if err := c.CopyIn(positional[0], positional[1]); err != nil {
			fatalf("cp-in: %v", err)
		}

	case "cp-out":
		requireArgs(subcmd, positional, 2)
		if err := c.CopyOut(positional[0], positional[1]); err != nil {
			fatalf("cp-out: %v", err)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", subcmd, usage)
		os.Exit(1)
	}
}

func requireArgs(cmd string, args []string, n int) {
	if len(args) < n {
		fatalf("%s: expected %d argument(s), got %d\n\n%s", cmd, n, len(args), usage)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}