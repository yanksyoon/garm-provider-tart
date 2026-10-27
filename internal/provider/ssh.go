package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/yanksyoon/garm-provider-tart/internal/hostctl"
)

// sshExitUnreachable is the OpenSSH exit code for a connection-level failure
// (255): name resolution, connection refused, host key mismatch or
// authentication failure. The provider maps it to a hard error so GARM retries
// rather than reporting success or a stale instance.
const sshExitUnreachable = 255

var (
	// ErrHostUnreachable reports that the host could not be reached over SSH,
	// so any pending delete or create is unresolved and GARM must retry.
	ErrHostUnreachable = errors.New("ssh: host unreachable")
	// ErrCommandTimeout reports that a hostctl verb did not finish in time.
	ErrCommandTimeout = errors.New("ssh: command timed out")
)

// FaultError is a classified hostctl failure. Fault is one of the hostctl fault
// names (capacity, keychain, tart, ...) and lets the provider distinguish a
// capacity fault (returned to GARM as an errored ProviderInstance) from other
// failures (hard errors GARM retries).
type FaultError struct {
	Fault   string
	Message string
}

func (e *FaultError) Error() string { return e.Message }

// Client runs tart-hostctl verbs on one host by exec'ing the system ssh binary.
// Shelling out to ssh (rather than an in-process SSH client) reuses known_hosts
// and the agent for host-key and credential handling, and keeps the break-glass
// `ssh` command wrapper in authorized_keys as the only remote surface.
type Client struct {
	host       Host
	user       string
	keyPath    string
	knownHosts string
	sshBin     string
	hostctlBin string
}

// NewClient builds a Client for one host.
func NewClient(host Host, user, keyPath, knownHosts, sshBin, hostctlBin string) *Client {
	if sshBin == "" {
		sshBin = "ssh"
	}
	if hostctlBin == "" {
		hostctlBin = "/usr/local/bin/tart-hostctl"
	}
	return &Client{
		host:       host,
		user:       user,
		keyPath:    keyPath,
		knownHosts: knownHosts,
		sshBin:     sshBin,
		hostctlBin: hostctlBin,
	}
}

// Run executes one hostctl verb and returns its stdout (JSON). Diagnostics and
// structured faults arrive on stderr and are classified into the returned
// error.
func (c *Client) Run(ctx context.Context, verb string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.sshBin, c.argv(verb, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() == sshExitUnreachable {
				return nil, fmt.Errorf("%w: %s", ErrHostUnreachable, c.host.Address)
			}
			return nil, classifyFault(stderr.String())
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %s", ErrCommandTimeout, verb)
		}
		return nil, fmt.Errorf("ssh %s: %w", c.host.Address, err)
	}
	return stdout.Bytes(), nil
}

// argv builds the full ssh command: options, target, then the remote hostctl
// invocation. The hostctl binary path and every argument are passed verbatim;
// hostctl validates names and image references before touching Tart, so no
// shell interpolation happens here.
func (c *Client) argv(verb string, args ...string) []string {
	argv := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
	}
	if c.keyPath != "" {
		argv = append(argv, "-i", c.keyPath)
	}
	if c.knownHosts != "" {
		argv = append(argv, "-o", "UserKnownHostsFile="+c.knownHosts, "-o", "StrictHostKeyChecking=yes")
	} else {
		argv = append(argv, "-o", "StrictHostKeyChecking=no")
	}
	argv = append(argv, "-p", strconv.Itoa(c.host.port()))
	target := c.host.Address
	if c.user != "" {
		target = c.user + "@" + target
	}
	argv = append(argv, target, c.hostctlBin, verb)
	argv = append(argv, args...)
	return argv
}

// classifyFault parses the structured Fault a failed hostctl verb writes to
// stderr, falling back to a generic failure when stderr is not a Fault object.
func classifyFault(stderr string) error {
	trimmed := bytes.TrimSpace([]byte(stderr))
	var f hostctl.Fault
	if err := json.Unmarshal(trimmed, &f); err == nil && f.Fault != "" {
		return &FaultError{Fault: f.Fault, Message: f.Error}
	}
	return fmt.Errorf("hostctl failed: %s", string(trimmed))
}
