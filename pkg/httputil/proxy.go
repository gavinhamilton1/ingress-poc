package httputil

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// DefaultClient is a shared HTTP client with reasonable timeouts.
var DefaultClient = &http.Client{Timeout: 30 * time.Second}

// hopByHop headers that should not be forwarded.
var hopByHopHeaders = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true,
	"proxy-authorization": true, "te": true, "trailer": true,
	"transfer-encoding": true, "upgrade": true,
}

// ForwardRequest proxies an incoming request to targetURL, preserving headers
// and injecting trace context. extraHeaders are added on top.
func ForwardRequest(ctx context.Context, client *http.Client, inReq *http.Request, targetURL string, extraHeaders map[string]string) (*http.Response, error) {
	body, _ := io.ReadAll(inReq.Body)

	// Preserve query string from incoming request
	fullURL := targetURL
	if inReq.URL.RawQuery != "" {
		if strings.Contains(fullURL, "?") {
			fullURL += "&" + inReq.URL.RawQuery
		} else {
			fullURL += "?" + inReq.URL.RawQuery
		}
	}

	req, err := http.NewRequestWithContext(ctx, inReq.Method, fullURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	// Copy non-hop-by-hop headers from incoming request
	for k, vals := range inReq.Header {
		if hopByHopHeaders[strings.ToLower(k)] {
			continue
		}
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}

	// Remove host header (Go sets it from the URL)
	req.Header.Del("Host")

	// Add extra headers — special-case "Host" since Go uses req.Host not Header["Host"]
	for k, v := range extraHeaders {
		if strings.EqualFold(k, "Host") {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}

	// Inject trace context
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	return client.Do(req)
}

// CopyResponseHeaders copies response headers to the writer, skipping hop-by-hop.
func CopyResponseHeaders(w http.ResponseWriter, resp *http.Response) {
	for k, vals := range resp.Header {
		if hopByHopHeaders[strings.ToLower(k)] {
			continue
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
}
