// Command garm-provider-tart is a GARM external provider for Tart VMs on
// Apple Silicon Mac minis.
//
// GARM execs this binary once per operation and selects the operation through
// the GARM_COMMAND environment variable. Standard output is reserved for the
// JSON payload GARM parses; diagnostics go to standard error.
package main

import (
	"os"

	"github.com/yanksyoon/garm-provider-tart/internal/provider"
)

func main() {
	os.Exit(provider.Run(os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}
