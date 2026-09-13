package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/open-policy-agent/opa/rego"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// preparedPolicy caches a compiled Rego query alongside a hash of the
// source it was compiled from, so a route_cache refresh that re-fetches the
// same unchanged Rego doesn't force a recompile on every poll.
type preparedPolicy struct {
	sourceHash [32]byte
	query      rego.PreparedEvalQuery
}

var (
	policyCacheMu sync.RWMutex
	policyCache   = map[string]*preparedPolicy{} // policyID -> prepared
)

// getOrCompilePolicy returns a prepared query for policyID, compiling (or
// recompiling, if the source changed since last time) as needed. This is
// what makes payload validation an in-process function call rather than a
// network hop: OPA's Go SDK compiles Rego to an internal representation
// once, here, and every subsequent request just evaluates that already-
// compiled query directly in this process.
func getOrCompilePolicy(ctx context.Context, policyID, source string) (rego.PreparedEvalQuery, error) {
	hash := sha256.Sum256([]byte(source))

	policyCacheMu.RLock()
	cached, ok := policyCache[policyID]
	policyCacheMu.RUnlock()
	if ok && cached.sourceHash == hash {
		return cached.query, nil
	}

	query, err := rego.New(
		rego.Query("data.ingress.policy.payload."+policyID),
		rego.Module(policyID+".rego", source),
	).PrepareForEval(ctx)
	if err != nil {
		return rego.PreparedEvalQuery{}, err
	}

	policyCacheMu.Lock()
	policyCache[policyID] = &preparedPolicy{sourceHash: hash, query: query}
	policyCacheMu.Unlock()

	return query, nil
}

// payloadCheckResult is the outcome of evaluating a generated per-route
// payload-validation policy.
type payloadCheckResult struct {
	Allow      bool
	DenyReason []string
}

// checkPayloadOPA evaluates the generated Rego policy (package
// ingress.policy.payload.<policyID>, produced by the repo-onboarding agent)
// against one request's method/path/body — in-process, via the OPA Go SDK,
// with no network hop to a separate OPA service. This is why the route
// cache carries the Rego source itself rather than just a policy ID: there
// is nothing else to ask for it.
//
// Fails open — same philosophy as the existing fine-grained OPA check in
// svc-api and the ext_authz filter's own failure_mode_allow — on any
// compile error or eval error. A misconfigured or buggy generated policy
// must never block traffic; only an explicit `allow: false` from a policy
// that DID evaluate should ever produce a 403.
func checkPayloadOPA(ctx context.Context, tracer trace.Tracer, policyID, source, method, path string, body []byte) payloadCheckResult {
	ctx, span := tracer.Start(ctx, "opa.payload")
	defer span.End()
	span.SetAttributes(attribute.String("opa.policy_id", policyID), attribute.String("http.route", path))

	if source == "" {
		// Route cache hasn't synced this policy's source yet (or it was
		// removed) — fail open rather than blocking on stale/missing state.
		span.SetAttributes(attribute.Bool("opa.allow", true), attribute.String("opa.note", "no_source_cached"))
		return payloadCheckResult{Allow: true}
	}

	query, err := getOrCompilePolicy(ctx, policyID, source)
	if err != nil {
		log.Printf("payload-policy: failed to compile policy %q, failing open: %v", policyID, err)
		span.SetAttributes(attribute.Bool("opa.allow", true), attribute.String("opa.error", err.Error()))
		return payloadCheckResult{Allow: true}
	}

	var bodyVal interface{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &bodyVal); err != nil {
			// Not valid JSON — pass the raw string through so the policy can
			// still reject it (e.g. "expected a JSON object") rather than
			// silently treating an unparsable payload as absent.
			bodyVal = string(body)
		}
	}

	results, err := query.Eval(ctx, rego.EvalInput(map[string]interface{}{
		"method": method,
		"path":   path,
		"body":   bodyVal,
	}))
	if err != nil {
		log.Printf("payload-policy: eval error for policy %q, failing open: %v", policyID, err)
		span.SetAttributes(attribute.Bool("opa.allow", true), attribute.String("opa.error", err.Error()))
		return payloadCheckResult{Allow: true}
	}

	if len(results) == 0 || len(results[0].Expressions) == 0 {
		span.SetAttributes(attribute.Bool("opa.allow", true), attribute.String("opa.note", "no_result"))
		return payloadCheckResult{Allow: true}
	}

	resultMap, ok := results[0].Expressions[0].Value.(map[string]interface{})
	if !ok {
		span.SetAttributes(attribute.Bool("opa.allow", true), attribute.String("opa.note", fmt.Sprintf("unexpected_result_shape:%T", results[0].Expressions[0].Value)))
		return payloadCheckResult{Allow: true}
	}

	allow, _ := resultMap["allow"].(bool)
	var denyReasons []string
	if raw, ok := resultMap["deny_reason"].([]interface{}); ok {
		for _, r := range raw {
			if s, ok := r.(string); ok {
				denyReasons = append(denyReasons, s)
			}
		}
	}

	span.SetAttributes(attribute.Bool("opa.allow", allow))
	return payloadCheckResult{Allow: allow, DenyReason: denyReasons}
}
