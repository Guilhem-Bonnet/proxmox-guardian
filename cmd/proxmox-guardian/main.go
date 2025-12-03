package main

import (
	"fmt"
	"os"

	"github.com/Guilhem-Bonnet/proxmox-guardian/internal/cli"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	buildInfo := cli.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    date,
	}

	if err := cli.Execute(buildInfo); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
