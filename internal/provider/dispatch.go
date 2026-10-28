package provider

import (
	"fmt"
	"io"
	"os"
)

// GARM operations, as named in GARM_COMMAND.
const (
	CommandCreateInstance     = "CreateInstance"
	CommandDeleteInstance     = "DeleteInstance"
	CommandGetInstance        = "GetInstance"
	CommandListInstances      = "ListInstances"
	CommandRemoveAllInstances = "RemoveAllInstances"
	CommandStart              = "StartInstance"
	CommandStop               = "StopInstance"
)

// Exit codes: 0 success, 1 operation or configuration failure, 2 usage error.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const usage = `garm-provider-tart - GARM external provider for Tart VMs

GARM invokes this binary once per operation and selects it with GARM_COMMAND:
  CreateInstance     read BootstrapInstance JSON on stdin, print ProviderInstance
  GetInstance        print ProviderInstance for GARM_INSTANCE_ID
  ListInstances      print a JSON array for GARM_POOL_ID
  DeleteInstance     delete GARM_INSTANCE_ID (no-op when already gone)
  RemoveAllInstances delete every VM created by GARM_CONTROLLER_ID
  StartInstance, StopInstance  boot or shut down GARM_INSTANCE_ID

Configuration comes from the TOML file in GARM_PROVIDER_CONFIG_FILE.
`

// Run executes one GARM operation and returns the process exit code.
func Run(getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	command := getenv("GARM_COMMAND")
	if command == "" {
		fmt.Fprintln(stderr, "GARM_COMMAND is not set")
		return exitUsage
	}
	if command == "--help" || command == "-h" || command == "help" {
		fmt.Fprint(stdout, usage)
		return exitOK
	}
	if !knownCommand(command) {
		fmt.Fprintf(stderr, "unknown GARM_COMMAND %q\n", command)
		return exitUsage
	}

	cfg, err := LoadConfig(getenv("GARM_PROVIDER_CONFIG_FILE"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	provider := New(cfg, getenv("GARM_CONTROLLER_ID"))

	if err := dispatch(provider, command, getenv, stdin, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	return exitOK
}

// knownCommand reports whether GARM commands us to do something we implement.
func knownCommand(command string) bool {
	switch command {
	case CommandCreateInstance, CommandDeleteInstance, CommandGetInstance,
		CommandListInstances, CommandRemoveAllInstances, CommandStart, CommandStop:
		return true
	default:
		return false
	}
}

// dispatch routes a known command to its operation.
func dispatch(p *Provider, command string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) error {
	p.stderr = stderr
	switch command {
	case CommandCreateInstance:
		return p.CreateInstance(stdin, stdout)
	case CommandGetInstance:
		return p.GetInstance(getenv("GARM_INSTANCE_ID"), stdout)
	case CommandListInstances:
		return p.ListInstances(getenv("GARM_POOL_ID"), stdout)
	case CommandDeleteInstance:
		return p.DeleteInstance(getenv("GARM_INSTANCE_ID"))
	case CommandRemoveAllInstances:
		return p.RemoveAllInstances()
	case CommandStart:
		return p.Start(getenv("GARM_INSTANCE_ID"))
	case CommandStop:
		return p.Stop(getenv("GARM_INSTANCE_ID"))
	default:
		return fmt.Errorf("unhandled GARM_COMMAND %q", command)
	}
}

var _ = os.Getenv
