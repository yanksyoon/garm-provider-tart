package provider

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// maxSlotsPerHost caps concurrent guests per Mac. The macOS licence agreement
// (section 2.B.iii) allows at most two virtualised instances per Apple-branded
// host, so a larger value is a licence breach rather than a tuning choice.
const maxSlotsPerHost = 2

// Host is one Apple Silicon Mac mini that runs Tart VMs.
type Host struct {
	// Name is the stable identifier recorded in provider IDs.
	Name string `toml:"name"`
	// Address is the SSH target, host name or IP.
	Address string `toml:"address"`
}

// Config mirrors the provider's TOML config file, whose path GARM passes in
// GARM_PROVIDER_CONFIG_FILE.
type Config struct {
	// SSHUser is the unprivileged account the provider uses on each host.
	SSHUser string `toml:"ssh_user"`
	// SSHKey is the path to the private key for SSHUser.
	SSHKey string `toml:"ssh_key"`
	// CreateTimeoutSeconds bounds one CreateInstance call.
	CreateTimeoutSeconds int `toml:"create_timeout_seconds"`
	// SlotsPerHost is the number of concurrent guests per host.
	SlotsPerHost int `toml:"slots_per_host"`
	// DefaultUser is the guest account that runs the GitHub runner.
	DefaultUser string `toml:"default_user"`
	// Hosts is the fleet this provider manages.
	Hosts []Host `toml:"hosts"`
}

// LoadConfig reads and validates the provider config file.
func LoadConfig(path string) (*Config, error) {
	if path == "" {
		return nil, fmt.Errorf("GARM_PROVIDER_CONFIG_FILE is not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	cfg := &Config{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %q: %w", path, err)
	}
	return cfg, nil
}

// Validate rejects configs the provider cannot honour.
func (c *Config) Validate() error {
	if len(c.Hosts) == 0 {
		return fmt.Errorf("no hosts configured")
	}
	if c.SlotsPerHost == 0 {
		c.SlotsPerHost = 1
	}
	if c.SlotsPerHost > maxSlotsPerHost {
		return fmt.Errorf("slots_per_host %d exceeds the macOS licence cap of %d guests per host", c.SlotsPerHost, maxSlotsPerHost)
	}
	if c.CreateTimeoutSeconds == 0 {
		c.CreateTimeoutSeconds = 900
	}
	seen := make(map[string]bool, len(c.Hosts))
	for _, host := range c.Hosts {
		if host.Name == "" || host.Address == "" {
			return fmt.Errorf("host entries need both name and address")
		}
		if seen[host.Name] {
			return fmt.Errorf("duplicate host name %q", host.Name)
		}
		seen[host.Name] = true
	}
	return nil
}

// HostByName returns the configured host called name.
func (c *Config) HostByName(name string) (Host, bool) {
	for _, host := range c.Hosts {
		if host.Name == name {
			return host, true
		}
	}
	return Host{}, false
}
