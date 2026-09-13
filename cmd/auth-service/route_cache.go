package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// routePolicy is the minimal projection of a Route CRD that the ext_authz
// handler needs to decide whether an incoming request requires payload
// validation, and the Rego source needed to actually run it in-process.
type routePolicy struct {
	Hostname   string
	Path       string
	PolicyRef  string
	RegoSource string
}

// routeCache holds the most recently fetched set of active routes that carry
// a payload_policy_ref. It's refreshed on a timer from the management API and
// read on every request, so a full-slice atomic swap (rather than a mutex) is
// the simplest safe way to publish updates — POC route counts are small
// enough that a linear longest-prefix scan on read is fine.
var routeCache atomic.Value // []routePolicy

func init() {
	routeCache.Store([]routePolicy{})
}

// startRouteCacheRefresher polls the management API for active routes with a
// payload policy attached and rebuilds the in-memory cache on the given
// interval. Failures are logged and the previous cache is left in place —
// this must never block or fail closed, since it sits in front of every
// request's auth check.
func startRouteCacheRefresher(managementAPIURL string, interval time.Duration) {
	refresh := func() {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(managementAPIURL + "/routes?status=active")
		if err != nil {
			log.Printf("route-cache: failed to fetch routes: %v", err)
			return
		}
		defer resp.Body.Close()

		var routes []struct {
			Hostname          string `json:"hostname"`
			Path              string `json:"path"`
			PayloadPolicyRef  string `json:"payload_policy_ref"`
			PayloadPolicyRego string `json:"payload_policy_rego"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&routes); err != nil {
			log.Printf("route-cache: failed to decode routes response: %v", err)
			return
		}

		policies := make([]routePolicy, 0, len(routes))
		for _, rt := range routes {
			if rt.PayloadPolicyRef == "" {
				continue
			}
			policies = append(policies, routePolicy{
				Hostname:   rt.Hostname,
				Path:       rt.Path,
				PolicyRef:  rt.PayloadPolicyRef,
				RegoSource: rt.PayloadPolicyRego,
			})
		}
		routeCache.Store(policies)
		if len(policies) > 0 {
			log.Printf("route-cache: refreshed, %d route(s) with a payload policy attached", len(policies))
		}
	}

	refresh() // populate synchronously at startup so early traffic isn't missed
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			refresh()
		}
	}()
}

// lookupPayloadPolicy returns the payload_policy_ref and Rego source of the
// best-matching cached route for hostname+path, or ("", "") if none matches.
// Matching mirrors Envoy's own prefix-match routing: longest path prefix
// wins, and a route with hostname "*" (or unset) matches any hostname.
func lookupPayloadPolicy(hostname, path string) (policyID, regoSource string) {
	policies, _ := routeCache.Load().([]routePolicy)
	bestLen := -1
	for _, p := range policies {
		if p.Hostname != "" && p.Hostname != "*" && !strings.EqualFold(p.Hostname, hostname) {
			continue
		}
		if !strings.HasPrefix(path, p.Path) {
			continue
		}
		if len(p.Path) > bestLen {
			bestLen = len(p.Path)
			policyID = p.PolicyRef
			regoSource = p.RegoSource
		}
	}
	return policyID, regoSource
}
