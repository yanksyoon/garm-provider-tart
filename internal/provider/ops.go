package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	commonParams "github.com/cloudbase/garm-provider-common/params"

	"github.com/yanksyoon/garm-provider-tart/internal/hostctl"
)

// Provider implements the GARM external-provider operations by driving the
// host-owned tart-hostctl registry over SSH. The host is the source of truth;
// this provider holds no state of its own.
type Provider struct {
	cfg          *Config
	controllerID string
}

// New builds a provider from a loaded config.
func New(cfg *Config, controllerID string) *Provider {
	return &Provider{cfg: cfg, controllerID: controllerID}
}

// client builds an SSH client for one host.
func (p *Provider) client(host Host) *Client {
	return NewClient(host, p.cfg.SSHUser, p.cfg.SSHKey, p.cfg.KnownHosts, p.cfg.SSHBin, p.cfg.HostctlBin)
}

// CreateInstance renders the bootstrap JSON on r, clones and boots a VM on the
// first configured host via hostctl, and prints the mapped ProviderInstance.
func (p *Provider) CreateInstance(r io.Reader, w io.Writer) error {
	var bootstrap commonParams.BootstrapInstance
	if err := json.NewDecoder(r).Decode(&bootstrap); err != nil {
		return fmt.Errorf("decode bootstrap params from stdin: %w", err)
	}
	if bootstrap.Name == "" {
		return fmt.Errorf("bootstrap params carry no instance name")
	}
	specs := parseExtraSpecs(bootstrap.ExtraSpecs)
	image := bootstrap.Image
	if image == "" {
		image = p.cfg.Image
	}

	host := p.cfg.Hosts[0]
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.CreateTimeout())
	defer cancel()

	out, err := p.client(host).Run(ctx, "create",
		"--name", bootstrap.Name,
		"--image", image,
		"--cpu", strconv.Itoa(specs.cpu(p.cfg)),
		"--mem", strconv.Itoa(specs.mem(p.cfg)),
		"--disk", strconv.Itoa(specs.disk(p.cfg)),
		"--controller", p.controllerID,
		"--pool", bootstrap.PoolID,
		"--host", host.Name,
	)
	if err != nil {
		var fe *FaultError
		if errors.As(err, &fe) && fe.Fault == hostctl.FaultCapacity {
			return writeJSON(w, erroredInstance(host, bootstrap.Name, fe.Message))
		}
		return err
	}

	var inst hostctl.Instance
	if err := json.Unmarshal(out, &inst); err != nil {
		return fmt.Errorf("parse hostctl create output: %w", err)
	}
	return writeJSON(w, p.providerInstance(inst))
}

// GetInstance reports the state of one instance by asking its host.
func (p *Provider) GetInstance(instanceID string, w io.Writer) error {
	if instanceID == "" {
		return fmt.Errorf("GARM_INSTANCE_ID is not set")
	}
	host, name, err := p.splitInstanceID(instanceID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.CommandTimeout())
	defer cancel()
	out, err := p.client(host).Run(ctx, "get", "--name", name)
	if err != nil {
		var fe *FaultError
		if errors.As(err, &fe) && fe.Fault == hostctl.FaultNotFound {
			return fmt.Errorf("instance %q not found on %s", name, host.Name)
		}
		return err
	}
	var inst hostctl.Instance
	if err := json.Unmarshal(out, &inst); err != nil {
		return fmt.Errorf("parse hostctl get output: %w", err)
	}
	return writeJSON(w, p.providerInstance(inst))
}

// ListInstances is fan-out across the fleet and arrives with placement (T5).
func (p *Provider) ListInstances(w io.Writer) error {
	return writeJSON(w, []commonParams.ProviderInstance{})
}

// DeleteInstance removes one instance via hostctl's delete verb. It is a no-op
// when the VM is gone, and a hard error when the host is unreachable (so GARM
// retries and the host-side tombstone keeps the slot counted).
func (p *Provider) DeleteInstance(instanceID string) error {
	if instanceID == "" {
		return fmt.Errorf("GARM_INSTANCE_ID is not set")
	}
	host, name, err := p.splitInstanceID(instanceID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.CommandTimeout())
	defer cancel()
	_, err = p.client(host).Run(ctx, "delete", "--name", name)
	return err
}

// Start boots a stopped instance. Not implemented until stop/start semantics
// land with the bootstrap template (T3).
func (p *Provider) Start(instanceID string) error {
	if instanceID == "" {
		return fmt.Errorf("GARM_INSTANCE_ID is not set")
	}
	return fmt.Errorf("start is not implemented")
}

// Stop shuts a running instance down without deleting it.
func (p *Provider) Stop(instanceID string) error {
	if instanceID == "" {
		return fmt.Errorf("GARM_INSTANCE_ID is not set")
	}
	return fmt.Errorf("stop is not implemented")
}

// RemoveAllInstances is fan-out across the fleet and arrives with placement (T5).
func (p *Provider) RemoveAllInstances() error {
	return nil
}

// providerInstance maps a hostctl instance to the GARM view. Only `ready` (and
// its post-bootstrap states) maps to GARM `running`; a `running` record whose
// readiness probe failed maps to `error` with the last_error as provider_fault.
func (p *Provider) providerInstance(inst hostctl.Instance) commonParams.ProviderInstance {
	pi := commonParams.ProviderInstance{
		ProviderID: inst.ProviderID,
		Name:       inst.Name,
		OSType:     commonParams.Linux,
		OSName:     "macos",
		OSVersion:  "26",
		OSArch:     commonParams.Arm64,
		Status:     mapStatus(inst.State),
	}
	if inst.IP != "" {
		pi.Addresses = []commonParams.Address{{Address: inst.IP, Type: commonParams.PrivateAddress}}
	}
	if inst.State == hostctl.StateRunning && inst.LastError != "" {
		pi.ProviderFault = []byte(inst.LastError)
	}
	return pi
}

// erroredInstance is the capacity-fault answer: GARM treats it as a failed
// create and retries rather than a provider crash.
func erroredInstance(host Host, name, msg string) commonParams.ProviderInstance {
	return commonParams.ProviderInstance{
		ProviderID:    host.Name + "/" + name,
		Name:          name,
		OSType:        commonParams.Linux,
		OSName:        "macos",
		OSArch:        commonParams.Arm64,
		Status:        commonParams.InstanceError,
		ProviderFault: []byte(msg),
	}
}

// mapStatus translates a hostctl state into a GARM instance status.
func mapStatus(s hostctl.State) commonParams.InstanceStatus {
	switch s {
	case hostctl.StateReady, hostctl.StateBootstrapping, hostctl.StateIdle, hostctl.StateAdopted:
		return commonParams.InstanceRunning
	case hostctl.StateCloning:
		return commonParams.InstanceCreating
	case hostctl.StateRunning, hostctl.StateFailed:
		return commonParams.InstanceError
	case hostctl.StateDeleting:
		return commonParams.InstanceDeleting
	default:
		return commonParams.InstanceStatusUnknown
	}
}

// extraSpecs is the subset of the pool extra_specs the provider understands:
// per-VM resource overrides. Everything else GARM injects (the runner install
// template wrapper) is ignored.
type extraSpecs struct {
	CPU      int `json:"cpu"`
	MemoryMB int `json:"mem"`
	DiskGB   int `json:"disk"`
}

func parseExtraSpecs(raw json.RawMessage) extraSpecs {
	var specs extraSpecs
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &specs)
	}
	return specs
}

func (s extraSpecs) cpu(cfg *Config) int {
	if s.CPU > 0 {
		return s.CPU
	}
	return cfg.DefaultCPU
}

func (s extraSpecs) mem(cfg *Config) int {
	if s.MemoryMB > 0 {
		return s.MemoryMB
	}
	return cfg.DefaultMemoryMB
}

func (s extraSpecs) disk(cfg *Config) int {
	if s.DiskGB > 0 {
		return s.DiskGB
	}
	return cfg.DefaultDiskGB
}

// splitInstanceID parses "<host>/<vm-name>", or accepts a bare GARM instance
// name, which GARM sends when a create failed before a provider ID existed.
func (p *Provider) splitInstanceID(instanceID string) (Host, string, error) {
	for _, host := range p.cfg.Hosts {
		prefix := host.Name + "/"
		if len(instanceID) > len(prefix) && instanceID[:len(prefix)] == prefix {
			return host, instanceID[len(prefix):], nil
		}
	}
	return p.cfg.Hosts[0], instanceID, nil
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
