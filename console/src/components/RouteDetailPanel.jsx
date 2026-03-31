import React, { useState, useEffect } from 'react'
import StatusBadge from './StatusBadge'
import { useConfig } from '../context/ConfigContext'

/**
 * Shared route detail panel used in both the Routes page (table row expansion)
 * and the Fleets page (instance expansion).
 *
 * Props:
 *   route        – route object (path, hostname, backend_url, id, methods, allowed_roles, gateway_type, audience, team, status)
 *   driftStatus  – 'in sync' | 'drifted' | 'unknown'  (optional)
 *   auditEntries – array of { action, ts } audit log entries for this route (optional)
 *   gatewayUrl   – base URL for test links (e.g. "https://jpmm.jpm.com")
 *   compact      – if true, render a single-column layout (for inline use)
 */
const AUTHN_LABELS = {
  bearer: { label: 'Bearer Token (JWT/DPoP)', color: 'text-emerald-400' },
  mtls: { label: 'Mutual TLS (mTLS)', color: 'text-cyan-400' },
  'api-key': { label: 'API Key', color: 'text-amber-400' },
  none: { label: 'None (Public)', color: 'text-gray-400' },
}

export default function RouteDetailPanel({ route, driftStatus, auditEntries = [], gatewayUrl, compact = false }) {
  const { API_URL } = useConfig()
  const [deployedNodes, setDeployedNodes] = useState([])

  useEffect(() => {
    if (!route?.id || !API_URL) return
    fetch(`${API_URL}/routes/${route.id}/nodes`)
      .then(r => r.json())
      .then(nodes => setDeployedNodes(Array.isArray(nodes) ? nodes : []))
      .catch(() => setDeployedNodes([]))
  }, [route?.id, API_URL])

  const h = route.hostname && route.hostname !== '*' ? route.hostname : null
  const testBase = h ? `https://${h}` : (gatewayUrl || '')
  const testUrl = `${testBase}${route.path}`
  const backend = route.backend_url || route.backend || ''
  const healthPath = route.health_path || '/health'
  const healthUrl = `${backend}${healthPath}`
  const authnMech = route.authn_mechanism || 'bearer'
  const authnInfo = AUTHN_LABELS[authnMech] || AUTHN_LABELS.bearer
  const scopes = route.authz_scopes || []

  const details = (
    <div>
      <div className="text-jpmc-muted mb-2 font-medium uppercase tracking-wider text-[10px]">Route Details</div>
      <div className="space-y-1.5">
        <div><span className="text-jpmc-muted">Test URL:</span>{' '}
          <a href={testUrl} target="_blank" rel="noopener noreferrer"
            className="font-mono text-blue-400 hover:text-blue-300">{testUrl}</a>
        </div>
        {h && <div><span className="text-jpmc-muted">Hostname:</span> <span className="font-mono text-cyan-400">{h}</span></div>}
        <div><span className="text-jpmc-muted">Backend:</span> <span className="font-mono text-jpmc-text/60">{backend}</span></div>
        <div><span className="text-jpmc-muted">Health URL:</span> <span className="font-mono text-emerald-400/70">{healthUrl}</span></div>
        {route.id && <div><span className="text-jpmc-muted">ID:</span> <span className="font-mono text-jpmc-text">{route.id}</span></div>}
        {route.gateway_type && <div><span className="text-jpmc-muted">Gateway:</span> <span className={`font-medium ${route.gateway_type === 'kong' ? 'text-purple-400' : 'text-violet-400'}`}>{route.gateway_type}</span></div>}
        {route.methods && <div><span className="text-jpmc-muted">Methods:</span> <span className="text-jpmc-text">{Array.isArray(route.methods) ? route.methods.join(', ') : route.methods}</span></div>}
        {route.team && <div><span className="text-jpmc-muted">Team:</span> <span className="text-jpmc-text">{route.team}</span></div>}
        {driftStatus && <div><span className="text-jpmc-muted">Drift:</span> <StatusBadge status={driftStatus} /></div>}
      </div>

      {/* Deployed Nodes section */}
      {deployedNodes.length > 0 && (
        <div className="mt-3 pt-3 border-t border-jpmc-border/20">
          <div className="text-jpmc-muted mb-2 font-medium uppercase tracking-wider text-[10px]">Deployed Nodes ({deployedNodes.length})</div>
          <div className="flex flex-wrap gap-1.5">
            {deployedNodes.map((node, i) => (
              <span key={i} className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-jpmc-navy/60 border border-jpmc-border/30 text-[10px]">
                <span className={`w-1.5 h-1.5 rounded-full ${node.status === 'active' ? 'bg-emerald-400' : 'bg-amber-400'}`} />
                <span className="text-jpmc-text font-mono">{node.node_name || node.node_container_id || `node-${i + 1}`}</span>
                {node.datacenter && <span className="text-jpmc-muted">({node.datacenter})</span>}
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Auth & AuthZ section */}
      <div className="mt-3 pt-3 border-t border-jpmc-border/20">
        <div className="text-jpmc-muted mb-2 font-medium uppercase tracking-wider text-[10px]">Authentication & Authorization</div>
        <div className="space-y-1.5">
          <div><span className="text-jpmc-muted">AuthN Mechanism:</span> <span className={`font-medium ${authnInfo.color}`}>{authnInfo.label}</span>
            {route.auth_issuer && route.auth_issuer !== 'N/A' && (
              <span className="ml-2 px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-400 text-[10px] border border-amber-500/20">Issuer: {route.auth_issuer}</span>
            )}
          </div>
          <div><span className="text-jpmc-muted">Audience:</span> <span className={`text-jpmc-text ${route.audience ? '' : 'italic text-jpmc-muted'}`}>{route.audience || 'public (unauthenticated)'}</span></div>
          <div><span className="text-jpmc-muted">Roles:</span> <span className="text-jpmc-text">{(route.allowed_roles || []).join(', ') || 'Any'}</span></div>
          <div>
            <span className="text-jpmc-muted">Required Scopes:</span>{' '}
            {scopes.length > 0 ? (
              <span className="inline-flex flex-wrap gap-1 ml-1">
                {scopes.map((s, i) => (
                  <code key={i} className="px-1.5 py-0.5 rounded bg-blue-500/10 text-blue-400 text-[10px] border border-blue-500/20">{s}</code>
                ))}
              </span>
            ) : <span className="text-jpmc-text">None</span>}
          </div>
          {authnMech !== 'none' && (
            <div className="mt-1 p-2 rounded bg-jpmc-navy/50 border border-jpmc-border/15 text-[10px] text-jpmc-muted">
              Gateway validates the <span className="text-jpmc-text">{authnInfo.label.split(' ')[0]}</span> and
              checks that the token contains {scopes.length > 0 ? <>scopes: <code className="text-blue-400">{scopes.join(', ')}</code></> : 'no specific scopes'} before
              forwarding to the backend.
            </div>
          )}
        </div>
      </div>
    </div>
  )

  const audit = auditEntries.length > 0 ? (
    <div>
      <div className="text-jpmc-muted mb-2 font-medium uppercase tracking-wider text-[10px]">Audit History</div>
      <div className="space-y-1">
        {auditEntries.slice(0, 5).map((a, i) => (
          <div key={i} className="flex flex-col gap-0.5">
            <div className="flex items-center gap-2">
              <span className={`text-[10px] font-medium ${
                a.action === 'CREATE' ? 'text-emerald-400' :
                a.action === 'DELETE' ? 'text-red-400' : 'text-amber-400'
              }`}>{a.action}</span>
              <span className="text-jpmc-muted text-[10px]">{a.ts ? new Date(a.ts * 1000).toLocaleString() : ''}</span>
            </div>
            {a.detail && <div className="text-[10px] text-jpmc-text/70 pl-1">{a.detail}</div>}
          </div>
        ))}
      </div>
    </div>
  ) : null

  if (compact) {
    return (
      <div className="text-xs space-y-3">
        {details}
        {audit}
      </div>
    )
  }

  return (
    <div className="grid grid-cols-2 gap-4 text-xs">
      {details}
      {audit || <div />}
    </div>
  )
}
