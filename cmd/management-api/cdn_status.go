package main

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// cdnServiceURLs holds the internal docker/K8s addresses of the multi-CDN
// simulation components. Probed live on every /cdn-status request rather
// than cached, since this endpoint exists specifically to show the true
// current state (e.g. for an operator watching a simulated Akamai outage
// play out), not a periodically-refreshed snapshot.
var cdnServiceURLs = struct {
	trafficManager string
	akamaiGTM      string
	akamaiEdge     string
	cfLB           string
	cfEdge         string
}{
	trafficManager: getEnvOr("TRAFFIC_MANAGER_URL", "http://mock-multi-cdn-traffic-manager:8010"),
	akamaiGTM:      getEnvOr("AKAMAI_GTM_URL", "http://mock-akamai-gtm:8010"),
	akamaiEdge:     getEnvOr("AKAMAI_EDGE_URL", "http://mock-akamai-edge:8011"),
	cfLB:           getEnvOr("CF_LB_URL", "http://mock-cf-lb:8030"),
	cfEdge:         getEnvOr("CF_EDGE_URL", "http://mock-cf-edge:8031"),
}

// probeHealth hits a service's /health endpoint with a short timeout and
// reports whether it responded 200. A failure here (timeout, connection
// refused, non-200) means "down" — this is deliberately binary since that's
// exactly what the traffic manager's own routing decision is based on.
func probeHealth(ctx context.Context, baseURL string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type cdnStatusResponse struct {
	TrafficManager struct {
		Up      bool           `json:"up"`
		Weights map[string]int `json:"weights,omitempty"`
	} `json:"traffic_manager"`
	Akamai struct {
		GTM  bool `json:"gtm"`
		Edge bool `json:"edge"`
	} `json:"akamai"`
	Cloudflare struct {
		LB   bool `json:"lb"`
		Edge bool `json:"edge"`
	} `json:"cloudflare"`
	ActiveActive bool `json:"active_active"`
}

// getCDNStatus reports the live, directly-probed health of every component
// in the multi-CDN simulation (traffic manager, both CDNs' load-balancer and
// edge layers) — the console dashboard's CDN widget polls this to show the
// true current state rather than assuming both paths are always up.
func getCDNStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var resp cdnStatusResponse
	var wg sync.WaitGroup
	wg.Add(5)

	go func() { defer wg.Done(); resp.TrafficManager.Up = probeHealth(ctx, cdnServiceURLs.trafficManager) }()
	go func() { defer wg.Done(); resp.Akamai.GTM = probeHealth(ctx, cdnServiceURLs.akamaiGTM) }()
	go func() { defer wg.Done(); resp.Akamai.Edge = probeHealth(ctx, cdnServiceURLs.akamaiEdge) }()
	go func() { defer wg.Done(); resp.Cloudflare.LB = probeHealth(ctx, cdnServiceURLs.cfLB) }()
	go func() { defer wg.Done(); resp.Cloudflare.Edge = probeHealth(ctx, cdnServiceURLs.cfEdge) }()
	wg.Wait()

	akamaiUp := resp.Akamai.GTM && resp.Akamai.Edge
	cfUp := resp.Cloudflare.LB && resp.Cloudflare.Edge
	resp.ActiveActive = akamaiUp && cfUp

	// Weights are static config today (AKAMAI_WEIGHT/CLOUDFLARE_WEIGHT on the
	// traffic manager) — surfaced here so the dashboard can show the
	// configured split alongside live health, without the console needing
	// its own copy of these defaults.
	resp.TrafficManager.Weights = map[string]int{
		"akamai":     intEnvOr("AKAMAI_WEIGHT", 50),
		"cloudflare": intEnvOr("CLOUDFLARE_WEIGHT", 50),
	}

	writeJSON(w, http.StatusOK, resp)
}

func intEnvOr(key string, def int) int {
	v := getEnvOr(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
