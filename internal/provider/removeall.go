package provider

import (
	"context"
	"encoding/json"

	"github.com/yanksyoon/garm-provider-tart/internal/hostctl"
)

// RemoveAllInstances deletes every VM on every host that carries this
// controller's id, reporting to stderr anything it could not delete (including
// VMs on unreachable hosts). It is best-effort: the host-side reaper and the
// per-host registry remain the source of truth for slot accounting.
func (p *Provider) RemoveAllInstances() error {
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.CreateTimeout())
	defer cancel()

	results := p.fanOut(ctx, p.cfg.Hosts, p.cfg.CommandTimeout(), "list", "--controller", p.controllerID)

	reaped := 0
	for _, r := range results {
		if r.err != nil {
			p.logf("remove-all: skipping %s: %v", r.host.Name, r.err)
			continue
		}
		var instances []hostctl.Instance
		if err := json.Unmarshal(r.out, &instances); err != nil {
			p.logf("remove-all: skipping %s: unreadable list JSON: %v", r.host.Name, err)
			continue
		}
		for _, inst := range instances {
			dctx, dcancel := context.WithTimeout(ctx, p.cfg.CommandTimeout())
			err := p.deleteOnHost(dctx, r.host, inst.Name)
			dcancel()
			if err != nil {
				p.logf("remove-all: could not delete %s on %s: %v", inst.Name, r.host.Name, err)
				continue
			}
			reaped++
		}
	}
	p.metric("orphans_reaped=%d", reaped)
	return nil
}

// deleteOnHost runs the hostctl delete verb on one host.
func (p *Provider) deleteOnHost(ctx context.Context, host Host, name string) error {
	_, err := p.client(host).Run(ctx, "delete", "--name", name)
	return err
}
