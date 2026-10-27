package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	commonParams "github.com/cloudbase/garm-provider-common/params"

	"github.com/yanksyoon/garm-provider-tart/internal/hostctl"
)

// ListInstances merges the instance lists from every reachable host, filtered
// by the opaque GARM_POOL_ID (owner) and this controller, deduplicated by
// provider id. Hosts that cannot be read are skipped with a warning, never
// treated as empty; the result is always a JSON array, never null.
func (p *Provider) ListInstances(poolID string, w io.Writer) error {
	if poolID == "" {
		return fmt.Errorf("GARM_POOL_ID is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.CommandTimeout())
	defer cancel()

	results := p.fanOut(ctx, p.cfg.Hosts, p.cfg.CommandTimeout(), "list", "--controller", p.controllerID, "--owner", poolID)

	merged := make([]commonParams.ProviderInstance, 0)
	seen := make(map[string]bool)
	unknown := 0
	for _, r := range results {
		if r.err != nil {
			unknown++
			p.logf("list: skipping %s: %v", r.host.Name, r.err)
			continue
		}
		var instances []hostctl.Instance
		if err := json.Unmarshal(r.out, &instances); err != nil {
			unknown++
			p.logf("list: skipping %s: unreadable list JSON: %v", r.host.Name, err)
			continue
		}
		for _, inst := range instances {
			pi := p.providerInstance(inst)
			if seen[pi.ProviderID] {
				continue
			}
			seen[pi.ProviderID] = true
			merged = append(merged, pi)
		}
	}
	if unknown > 0 {
		p.metric("unknown_hosts=%d", unknown)
	}
	p.metric("list_instances count=%d", len(merged))
	return writeJSON(w, merged)
}
