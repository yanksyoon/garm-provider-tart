package hostctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"
)

// Exit codes for the hostctl CLI, matching the provider's contract: 0 success,
// 1 operation or configuration failure, 2 usage error.
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
)

// CLIEnv carries the process environment the CLI reads.
type CLIEnv struct {
	Root             string
	SlotsPerHost     int
	QuarantineTTL    string // duration string, e.g. "24h"; empty means default
	StaleCreateTTL   string // duration string, e.g. "5m"; empty means default
	ProbeTimeoutSecs int    // readiness-probe timeout in seconds; 0 means default
	HostName         string
	ControllerID     string
	TartBin          string
	SecurityBin      string
	DfBin            string
}

// Run dispatches one hostctl verb and returns the process exit code. JSON
// results go to stdout; diagnostics (including structured Fault objects) go to
// stderr.
func Run(args []string, env CLIEnv, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeFault(stderr, FaultUsage, "no verb given")
		return ExitUsage
	}
	verb, rest := args[0], args[1:]

	svc := NewService(Options{
		Root:           env.Root,
		HostName:       env.HostName,
		ControllerID:   env.ControllerID,
		SlotsPerHost:   env.SlotsPerHost,
		QuarantineTTL:  parseTTL(env.QuarantineTTL),
		StaleCreateTTL: parseTTL(env.StaleCreateTTL),
		ProbeTimeout:   time.Duration(env.ProbeTimeoutSecs) * time.Second,
		Tart:           NewRealTart(env.TartBin),
		Health:         NewRealHealth(env.SecurityBin, env.DfBin, env.Root),
	})

	switch verb {
	case "status":
		status, err := svc.Status()
		if err != nil {
			return writeErr(stderr, err)
		}
		return writeJSON(stdout, status, stderr)
	case "create":
		req, err := parseCreate(rest)
		if err != nil {
			return writeErr(stderr, err)
		}
		inst, err := svc.Create(req)
		if err != nil {
			return writeErr(stderr, err)
		}
		return writeJSON(stdout, inst, stderr)
	case "get":
		name, err := oneStringFlag(rest, "name")
		if err != nil {
			return writeErr(stderr, err)
		}
		inst, err := svc.Get(name)
		if err != nil {
			return writeErr(stderr, err)
		}
		return writeJSON(stdout, inst, stderr)
	case "list":
		controller, owner, err := parseList(rest)
		if err != nil {
			return writeErr(stderr, err)
		}
		instances, err := svc.List(controller, owner)
		if err != nil {
			return writeErr(stderr, err)
		}
		return writeJSON(stdout, instances, stderr)
	case "delete":
		name, err := oneStringFlag(rest, "name")
		if err != nil {
			return writeErr(stderr, err)
		}
		if err := svc.Delete(name); err != nil {
			return writeErr(stderr, err)
		}
		return ExitOK
	case "bootstrap":
		name, err := oneStringFlag(rest, "name")
		if err != nil {
			return writeErr(stderr, err)
		}
		inst, err := svc.Bootstrap(name, stdin)
		if err != nil {
			return writeErr(stderr, err)
		}
		return writeJSON(stdout, inst, stderr)
	case "drain":
		if err := svc.Drain(); err != nil {
			return writeErr(stderr, err)
		}
		return ExitOK
	case "undrain":
		if err := svc.Undrain(); err != nil {
			return writeErr(stderr, err)
		}
		return ExitOK
	default:
		writeFault(stderr, FaultUsage, fmt.Sprintf("unknown verb %q", verb))
		return ExitUsage
	}
}

func parseTTL(s string) (d time.Duration) {
	if s == "" {
		return 0
	}
	d, _ = time.ParseDuration(s)
	return d
}

func writeErr(stderr io.Writer, err error) int {
	var fe *FaultError
	if errors.As(err, &fe) {
		data, _ := json.Marshal(fe)
		fmt.Fprintln(stderr, string(data))
		if fe.Fault == FaultUsage {
			return ExitUsage
		}
		return ExitFailure
	}
	writeFault(stderr, FaultTart, err.Error())
	return ExitFailure
}

func writeFault(stderr io.Writer, fault, msg string) {
	data, _ := json.Marshal(Fault{Fault: fault, Error: msg})
	fmt.Fprintln(stderr, string(data))
}

func writeJSON(stdout io.Writer, value any, stderr io.Writer) int {
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		writeFault(stderr, FaultTart, err.Error())
		return ExitFailure
	}
	return ExitOK
}

// oneStringFlag parses a single --<name> <value> pair.
func oneStringFlag(args []string, name string) (string, error) {
	flag := "--" + name
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			if i+1 >= len(args) {
				return "", &FaultError{Fault: FaultUsage, Message: fmt.Sprintf("--%s needs a value", name)}
			}
			return args[i+1], nil
		}
	}
	return "", &FaultError{Fault: FaultUsage, Message: fmt.Sprintf("--%s is required", name)}
}

func parseCreate(args []string) (CreateRequest, error) {
	var req CreateRequest
	var err error
	if req.Name, err = oneStringFlag(args, "name"); err != nil {
		return req, err
	}
	if req.Image, err = oneStringFlag(args, "image"); err != nil {
		return req, err
	}
	if req.Controller, err = oneStringFlag(args, "controller"); err != nil {
		return req, err
	}
	req.CPU, _ = intFlag(args, "cpu", 4)
	req.MemoryMB, _ = intFlag(args, "mem", 8192)
	req.DiskGB, _ = intFlag(args, "disk", 50)
	req.Owner, _ = optionalStringFlag(args, "owner")
	req.Host, _ = optionalStringFlag(args, "host")
	return req, nil
}

func parseList(args []string) (controller, owner string, err error) {
	controller, _ = optionalStringFlag(args, "controller")
	owner, _ = optionalStringFlag(args, "owner")
	return controller, owner, nil
}

func optionalStringFlag(args []string, name string) (string, bool) {
	flag := "--" + name
	for i := 0; i < len(args); i++ {
		if args[i] == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func intFlag(args []string, name string, def int) (int, bool) {
	if v, ok := optionalStringFlag(args, name); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n, true
		}
	}
	return def, false
}
