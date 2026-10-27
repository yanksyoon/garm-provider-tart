package provider

import (
	"encoding/json"
	"fmt"
	"io"

	commonParams "github.com/cloudbase/garm-provider-common/params"
)

// Provider implements the GARM external-provider operations. The host driver,
// slot allocator and state file arrive with the next change; every operation
// here returns a well-formed answer so the CLI contract can be exercised.
type Provider struct {
	cfg          *Config
	controllerID string
}

// New builds a provider from a loaded config.
func New(cfg *Config, controllerID string) *Provider {
	return &Provider{cfg: cfg, controllerID: controllerID}
}

// CreateInstance renders the bootstrap JSON on r and prints a ProviderInstance.
func (p *Provider) CreateInstance(r io.Reader, w io.Writer) error {
	var bootstrap commonParams.BootstrapInstance
	if err := json.NewDecoder(r).Decode(&bootstrap); err != nil {
		return fmt.Errorf("decode bootstrap params from stdin: %w", err)
	}
	if bootstrap.Name == "" {
		return fmt.Errorf("bootstrap params carry no instance name")
	}
	host := p.cfg.Hosts[0]
	instance := p.instanceFor(host, bootstrap.Name)
	return writeJSON(w, instance)
}

// GetInstance reports the state of one instance.
func (p *Provider) GetInstance(instanceID string, w io.Writer) error {
	if instanceID == "" {
		return fmt.Errorf("GARM_INSTANCE_ID is not set")
	}
	host, name, err := p.splitInstanceID(instanceID)
	if err != nil {
		return err
	}
	return writeJSON(w, p.instanceFor(host, name))
}

// ListInstances reports every instance belonging to one pool.
func (p *Provider) ListInstances(w io.Writer) error {
	// An empty JSON array, never null: GARM unmarshals this into a slice.
	return writeJSON(w, []commonParams.ProviderInstance{})
}

// DeleteInstance removes one instance. It is a no-op when the instance is gone.
func (p *Provider) DeleteInstance(instanceID string) error {
	if instanceID == "" {
		return fmt.Errorf("GARM_INSTANCE_ID is not set")
	}
	_, _, err := p.splitInstanceID(instanceID)
	return err
}

// Start boots a stopped instance.
func (p *Provider) Start(instanceID string) error {
	if instanceID == "" {
		return fmt.Errorf("GARM_INSTANCE_ID is not set")
	}
	return nil
}

// Stop shuts a running instance down without deleting it.
func (p *Provider) Stop(instanceID string) error {
	if instanceID == "" {
		return fmt.Errorf("GARM_INSTANCE_ID is not set")
	}
	return nil
}

// RemoveAllInstances deletes every VM this controller created. It prints nothing.
func (p *Provider) RemoveAllInstances() error {
	return nil
}

// instanceFor renders the GARM view of one VM.
func (p *Provider) instanceFor(host Host, name string) commonParams.ProviderInstance {
	return commonParams.ProviderInstance{
		ProviderID: host.Name + "/" + name,
		Name:       name,
		OSType:     commonParams.Linux,
		OSName:     "macos",
		OSVersion:  "26",
		OSArch:     commonParams.Arm64,
		Addresses: []commonParams.Address{
			{Address: "192.0.2.10", Type: commonParams.PrivateAddress},
		},
		Status: commonParams.InstanceRunning,
	}
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
	if _, ok := p.cfg.HostByName(p.cfg.Hosts[0].Name); ok {
		return p.cfg.Hosts[0], instanceID, nil
	}
	return Host{}, "", fmt.Errorf("unknown instance %q", instanceID)
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
