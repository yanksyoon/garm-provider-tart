// Command tart-hostctl is the host-side control plane for a Tart host. It owns
// the per-instance registry under /var/db/tart-fleet and exposes an idempotent
// verb set the provider drives over SSH. It is installed at
// /usr/local/bin/tart-hostctl on every Mac.
package main

import (
	"os"
	"strconv"

	"github.com/yanksyoon/garm-provider-tart/internal/hostctl"
)

func main() {
	env := hostctl.CLIEnv{
		Root:             getenv("TART_FLEET_ROOT", "/var/db/tart-fleet"),
		SlotsPerHost:     getenvInt("TART_FLEET_SLOTS_PER_HOST", hostctl.MaxSlotsPerHost),
		StaleCreateTTL:   os.Getenv("TART_FLEET_STALE_CREATE_TIMEOUT"),
		ProbeTimeoutSecs: getenvInt("TART_FLEET_PROBE_TIMEOUT_SECONDS", 0),
		HostName:         os.Getenv("TART_FLEET_HOST"),
		ControllerID:     os.Getenv("TART_FLEET_CONTROLLER_ID"),
		TartBin:          getenv("TART_FLEET_TART_BIN", "tart"),
		SecurityBin:      getenv("TART_FLEET_SECURITY_BIN", "security"),
		DfBin:            getenv("TART_FLEET_DF_BIN", "df"),
	}
	// Quarantine TTL is configured in hours so an operator can set a whole
	// number; the hostctl package accepts a duration string. The stale-create
	// timeout is already a duration string ("5m"); empty means the default.
	if h := os.Getenv("TART_FLEET_QUARANTINE_TTL_HOURS"); h != "" {
		env.QuarantineTTL = h + "h"
	}
	os.Exit(hostctl.Run(os.Args[1:], env, os.Stdin, os.Stdout, os.Stderr))
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
