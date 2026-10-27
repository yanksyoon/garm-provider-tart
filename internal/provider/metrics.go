package provider

import "fmt"

// logf writes one diagnostic line to the provider's stderr writer (which GARM
// forwards verbatim). It is the reporting channel for skipped hosts and failed
// deletes; structured metrics share the same channel with a stable prefix.
func (p *Provider) logf(format string, args ...any) {
	if p.stderr == nil {
		return
	}
	fmt.Fprintf(p.stderr, format+"\n", args...)
}

// metric writes a structured, grep-able metric line to stderr. Fields are
// `key=value` pairs under a stable `provider_metric` prefix so operators and
// logs can key on them without a daemon or metrics endpoint.
func (p *Provider) metric(format string, args ...any) {
	p.logf("provider_metric "+format, args...)
}
