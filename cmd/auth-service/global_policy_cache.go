package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

// globalPolicySource holds the most recently fetched global payload policy's
// Rego source, refreshed on the same cadence as routeCache. Empty string
// means "not yet fetched" or "fetch failed" — checkPayloadOPA already fails
// open on an empty source, so this needs no special-casing at the call site.
var globalPolicySource atomic.Value // string

func init() {
	globalPolicySource.Store("")
}

// startGlobalPolicyRefresher polls the management API for the platform-wide
// payload policy (package ingress.policy.payload.global) and keeps the
// in-memory copy current. Like startRouteCacheRefresher, failures are
// logged and the previous value is left in place — never block or fail
// closed from here, since this sits in front of every request with a body.
func startGlobalPolicyRefresher(managementAPIURL string, interval time.Duration) {
	refresh := func() {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(managementAPIURL + "/policies/global")
		if err != nil {
			log.Printf("global-policy: failed to fetch: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Printf("global-policy: fetch returned HTTP %d", resp.StatusCode)
			return
		}

		var payload struct {
			RegoSource string `json:"rego_source"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			log.Printf("global-policy: failed to decode response: %v", err)
			return
		}
		globalPolicySource.Store(payload.RegoSource)
	}

	refresh()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			refresh()
		}
	}()
}

func currentGlobalPolicySource() string {
	s, _ := globalPolicySource.Load().(string)
	return s
}
