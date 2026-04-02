import React, { useState, useRef, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Send, Loader2, ChevronDown, ChevronRight, Plus, Trash2,
  Clock, CheckCircle2, XCircle, AlertCircle, Activity,
} from 'lucide-react'
import GlassCard from '../components/GlassCard'
import TraceFlow from '../components/TraceFlow'
import { useAuth } from '../context/AuthContext'
import { useConfig } from '../context/ConfigContext'

function base64urlEncode(buffer) {
  const bytes = new Uint8Array(buffer)
  let str = ''
  bytes.forEach(b => str += String.fromCharCode(b))
  return btoa(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
}

async function generateDpopProof(method, url, privateKey, publicJwk) {
  const header = {
    typ: 'dpop+jwt', alg: 'ES256',
    jwk: { kty: publicJwk.kty, crv: publicJwk.crv, x: publicJwk.x, y: publicJwk.y },
  }
  const payload = {
    jti: crypto.randomUUID(), htm: method.toUpperCase(),
    htu: url.split('?')[0], iat: Math.floor(Date.now() / 1000),
  }
  const headerB64 = base64urlEncode(new TextEncoder().encode(JSON.stringify(header)))
  const payloadB64 = base64urlEncode(new TextEncoder().encode(JSON.stringify(payload)))
  const sigInput = `${headerB64}.${payloadB64}`

  const key = await crypto.subtle.importKey('jwk', privateKey, { name: 'ECDSA', namedCurve: 'P-256' }, false, ['sign'])
  const sig = await crypto.subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, key, new TextEncoder().encode(sigInput))

  return `${headerB64}.${payloadB64}.${base64urlEncode(sig)}`
}

const METHOD_COLORS = {
  GET: 'text-emerald-400 bg-emerald-500/15',
  POST: 'text-blue-400 bg-blue-500/15',
  PUT: 'text-amber-400 bg-amber-500/15',
  DELETE: 'text-red-400 bg-red-500/15',
}

export default function RequestTester() {
  const { session, dpopKeys } = useAuth()
  const { API_URL, GATEWAY_URL, JAEGER_URL } = useConfig()

  const [requests, setRequests] = useState([])
  const [testPath, setTestPath] = useState('')
  const [selectedRoute, setSelectedRoute] = useState(null)
  const [testMethod, setTestMethod] = useState('GET')
  const [useAuth_, setUseAuth_] = useState(true)
  const [sending, setSending] = useState(false)
  const [expandedRequest, setExpandedRequest] = useState(null)
  const [showTraceFor, setShowTraceFor] = useState(null)
  const [customHeaders, setCustomHeaders] = useState([])
  const [showSuggestions, setShowSuggestions] = useState(false)
  const inputRef = useRef(null)

  // Fetch all routes for the dropdown
  const { data: allRoutes = [] } = useQuery({
    queryKey: ['routes-for-tester'],
    queryFn: () => fetch(`${API_URL}/routes`).then(r => r.json()).catch(() => []),
  })

  // Fetch fleets for fleet name lookup
  const { data: fleets = [] } = useQuery({
    queryKey: ['fleets-for-tester'],
    queryFn: () => fetch(`${API_URL}/fleets`).then(r => r.json()).catch(() => []),
  })

  const routeSuggestions = useMemo(() => {
    const fleetMap = {}
    for (const f of fleets) {
      fleetMap[f.subdomain] = f
    }
    return allRoutes
      .filter(r => r.status === 'active')
      .map(r => {
        const h = r.hostname && r.hostname !== '*' ? r.hostname : null
        const fleet = h ? fleetMap[h] : null
        const label = h ? `${h}${r.path}` : r.path
        return {
          routeData: r, fleet, hostname: h, label,
          searchText: `${label} ${fleet?.name || ''} ${fleet?.lob || ''} ${r.gateway_type} ${r.team}`.toLowerCase(),
        }
      })
      .sort((a, b) => a.label.localeCompare(b.label))
  }, [allRoutes, fleets, GATEWAY_URL])

  const addHeader = () => {
    setCustomHeaders([...customHeaders, { key: '', value: '' }])
  }

  const removeHeader = (idx) => {
    setCustomHeaders(customHeaders.filter((_, i) => i !== idx))
  }

  const updateHeader = (idx, field, val) => {
    const updated = [...customHeaders]
    updated[idx][field] = val
    setCustomHeaders(updated)
  }

  const filteredSuggestions = routeSuggestions.filter(s =>
    testPath === '' || s.searchText.includes(testPath.toLowerCase())
  )

  const selectSuggestion = (suggestion) => {
    setSelectedRoute(suggestion)
    setTestPath(suggestion.label)
    setShowSuggestions(false)
  }

  const sendRequest = async () => {
    setSending(true)
    // Route through the real hostname URL so the browser sends the correct Host header
    // (browsers forbid setting Host via fetch headers)
    const sel = selectedRoute
    const hostname = sel?.hostname
    const path = sel?.routeData?.path || testPath
    // Route through the nginx proxy — same-origin, no CORS issues
    // For hostname routes, pass the hostname via X-Route-Host header (nginx forwards it as Host)
    const url = `${GATEWAY_URL}${path || testPath}`
    const headers = { 'User-Agent': 'IngressConsole/1.0' }
    if (hostname) {
      headers['X-Route-Host'] = hostname
    }
    let dpopProof = null

    if (useAuth_ && session?.session_jwt && dpopKeys) {
      headers['Authorization'] = `Bearer ${session.session_jwt}`
      try {
        dpopProof = await generateDpopProof(testMethod, url, dpopKeys.privateKey, dpopKeys.publicKey)
        headers['DPoP'] = dpopProof
      } catch (e) {
        console.error('DPoP generation failed:', e)
      }
    }

    // Add custom headers
    customHeaders.forEach(h => {
      if (h.key.trim()) headers[h.key.trim()] = h.value
    })

    const start = Date.now()
    try {
      const resp = await fetch(url, { method: testMethod, headers })
      const latency = Date.now() - start
      const respHeaders = {}
      resp.headers.forEach((v, k) => { respHeaders[k] = v })
      const body = await resp.clone().json().catch(async () => ({ raw: await resp.text() }))

      const traceparent = resp.headers.get('traceparent') || ''
      const traceId = traceparent ? traceparent.split('-')[1] : ''

      const entry = {
        id: crypto.randomUUID(), timestamp: new Date().toISOString(),
        method: testMethod, path: testPath, status: resp.status,
        latency, body, traceId, responseHeaders: respHeaders,
        subject: session?.email || 'anonymous',
        roles: session?.roles || [],
        result: resp.ok ? 'OK' : 'ERROR',
        dpop: !!dpopProof,
      }
      setRequests(prev => [entry, ...prev].slice(0, 50))
      setExpandedRequest(entry.id)
    } catch (e) {
      const entry = {
        id: crypto.randomUUID(), timestamp: new Date().toISOString(),
        method: testMethod, path: testPath, status: 0,
        latency: Date.now() - start, body: { error: e.message }, traceId: '',
        responseHeaders: {},
        subject: session?.email || 'anonymous', roles: [],
        result: 'NETWORK_ERROR', dpop: !!dpopProof,
      }
      setRequests(prev => [entry, ...prev].slice(0, 50))
      setExpandedRequest(entry.id)
    }
    setSending(false)
  }

  const getStatusColor = (status) => {
    if (!status) return 'text-red-400'
    if (status >= 200 && status < 300) return 'text-emerald-400'
    if (status >= 400 && status < 500) return 'text-amber-400'
    return 'text-red-400'
  }

  const getStatusBg = (status) => {
    if (!status) return 'bg-red-500/10 border-red-500/30'
    if (status >= 200 && status < 300) return 'bg-emerald-500/10 border-emerald-500/30'
    if (status >= 400 && status < 500) return 'bg-amber-500/10 border-amber-500/30'
    return 'bg-red-500/10 border-red-500/30'
  }

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-bold text-white">Request Tester</h1>
        <p className="text-sm text-jpmc-muted">Send requests through the ingress gateway and inspect responses</p>
      </div>

      {/* Request Builder */}
      <GlassCard delay={0.05} className="relative z-10">
        <div className="p-5 space-y-4">
          {/* Method + Path */}
          <div className="flex gap-3">
            <select
              className="select-field w-[110px] shrink-0 font-semibold"
              value={testMethod}
              onChange={e => setTestMethod(e.target.value)}
            >
              {['GET', 'POST', 'PUT', 'DELETE'].map(m => (
                <option key={m} value={m}>{m}</option>
              ))}
            </select>
            <div className="flex-1 relative">
              <input
                ref={inputRef}
                className="input-field font-mono"
                value={testPath}
                onChange={e => { setTestPath(e.target.value); setSelectedRoute(null); setShowSuggestions(true) }}
                onFocus={() => setShowSuggestions(true)}
                onBlur={() => setTimeout(() => setShowSuggestions(false), 200)}
                placeholder="Type to search routes... (e.g. jpmm, research, markets)"
              />
              {selectedRoute && (
                <div className="absolute right-2 top-1/2 -translate-y-1/2 flex items-center gap-1.5">
                  {selectedRoute.fleet && (
                    <span className="text-[9px] px-1.5 py-0.5 rounded bg-blue-500/10 text-blue-400 border border-blue-500/20">{selectedRoute.fleet.name}</span>
                  )}
                  <span className={`text-[9px] px-1.5 py-0.5 rounded ${selectedRoute.routeData.gateway_type === 'kong' ? 'bg-purple-500/10 text-purple-400 border border-purple-500/20' : 'bg-violet-500/10 text-violet-400 border border-violet-500/20'}`}>
                    {selectedRoute.routeData.gateway_type}
                  </span>
                </div>
              )}
              <AnimatePresence>
                {showSuggestions && filteredSuggestions.length > 0 && (
                  <motion.div
                    initial={{ opacity: 0, y: -4 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, y: -4 }}
                    className="absolute left-0 right-0 top-full mt-1 glass-card border border-jpmc-border z-[200] max-h-64 overflow-y-auto"
                  >
                    {filteredSuggestions.map(s => (
                      <button
                        key={s.label}
                        className="w-full text-left px-3 py-2 hover:bg-jpmc-hover transition-colors flex items-center gap-3"
                        onMouseDown={() => selectSuggestion(s)}
                      >
                        <code className="text-xs text-blue-400 font-mono flex-1">{s.label}</code>
                        {s.fleet && (
                          <span className="text-[9px] text-jpmc-muted">{s.fleet.name}</span>
                        )}
                        <span className={`text-[9px] px-1.5 py-0.5 rounded ${s.routeData.gateway_type === 'kong' ? 'bg-purple-500/10 text-purple-400' : 'bg-violet-500/10 text-violet-400'}`}>
                          {s.routeData.gateway_type}
                        </span>
                        <span className="text-[9px] text-jpmc-muted">{s.routeData.audience || 'public'}</span>
                      </button>
                    ))}
                  </motion.div>
                )}
              </AnimatePresence>
            </div>
            <motion.button
              whileHover={{ scale: 1.02 }}
              whileTap={{ scale: 0.98 }}
              onClick={sendRequest}
              disabled={sending}
              className="btn-primary flex items-center gap-2 px-6 shrink-0"
            >
              {sending ? <Loader2 size={14} className="animate-spin" /> : <Send size={14} />}
              Send
            </motion.button>
          </div>

          {/* Options row */}
          <div className="flex items-center gap-4 flex-wrap">
            <label className="flex items-center gap-2 text-sm text-jpmc-muted cursor-pointer">
              <input
                type="checkbox"
                checked={useAuth_}
                onChange={e => setUseAuth_(e.target.checked)}
                className="rounded border-jpmc-border bg-jpmc-navy"
              />
              <span>Send as authenticated user</span>
              {session && useAuth_ && (
                <span className="badge-blue text-[10px]">{session.email}</span>
              )}
            </label>
          </div>

          {/* Custom Headers */}
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs font-medium text-jpmc-muted uppercase tracking-wider">Custom Headers</span>
              <button onClick={addHeader} className="text-xs text-blue-400 hover:text-blue-300 flex items-center gap-1 transition-colors">
                <Plus size={12} /> Add Header
              </button>
            </div>
            {customHeaders.length > 0 && (
              <div className="space-y-2">
                {customHeaders.map((h, idx) => (
                  <div key={idx} className="flex gap-2 items-center">
                    <input
                      className="input-field flex-1"
                      placeholder="Header name"
                      value={h.key}
                      onChange={e => updateHeader(idx, 'key', e.target.value)}
                    />
                    <input
                      className="input-field flex-1"
                      placeholder="Value"
                      value={h.value}
                      onChange={e => updateHeader(idx, 'value', e.target.value)}
                    />
                    <button onClick={() => removeHeader(idx)} className="p-2 text-jpmc-muted hover:text-red-400 transition-colors">
                      <Trash2 size={14} />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </GlassCard>

      {/* Request History */}
      <div className="space-y-3">
        <h2 className="text-sm font-semibold text-jpmc-text">Request History</h2>
        {requests.length === 0 ? (
          <GlassCard delay={0.1}>
            <div className="text-center py-12 text-jpmc-muted text-sm">
              No requests yet. Use the builder above to send requests through the gateway.
            </div>
          </GlassCard>
        ) : (
          requests.map((r, idx) => {
            const isExpanded = expandedRequest === r.id
            return (
              <motion.div
                key={r.id}
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: idx === 0 ? 0 : 0 }}
              >
                <div className={`glass-card overflow-hidden ${isExpanded ? 'border-jpmc-border' : ''}`}>
                  {/* Summary row */}
                  <div
                    className="flex items-center gap-4 p-4 cursor-pointer hover:bg-jpmc-hover/50 transition-colors"
                    onClick={() => setExpandedRequest(isExpanded ? null : r.id)}
                  >
                    <span className={`text-xs font-bold px-2 py-0.5 rounded ${METHOD_COLORS[r.method] || 'text-jpmc-muted bg-jpmc-navy'}`}>
                      {r.method}
                    </span>
                    <code className="text-sm text-jpmc-text flex-1 truncate">{r.path}</code>
                    <span className={`text-sm font-bold ${getStatusColor(r.status)}`}>
                      {r.status || 'ERR'}
                    </span>
                    <span className="text-xs text-jpmc-muted flex items-center gap-1">
                      <Clock size={11} />
                      {r.latency}ms
                    </span>
                    <span className="text-xs text-jpmc-muted">{r.subject}</span>
                    {r.dpop && <span className="badge-blue text-[9px]">DPoP</span>}
                    {isExpanded ? <ChevronDown size={14} className="text-jpmc-muted" /> : <ChevronRight size={14} className="text-jpmc-muted" />}
                  </div>

                  {/* Expanded details */}
                  <AnimatePresence>
                    {isExpanded && (
                      <motion.div
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: 'auto', opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        className="overflow-hidden"
                      >
                        <div className="border-t border-jpmc-border/30 p-4 space-y-4">
                          {/* Status banner */}
                          <div className={`flex items-center gap-3 p-3 rounded-lg border ${getStatusBg(r.status)}`}>
                            {r.result === 'OK'
                              ? <CheckCircle2 size={16} className="text-emerald-400" />
                              : r.result === 'NETWORK_ERROR'
                                ? <AlertCircle size={16} className="text-red-400" />
                                : <XCircle size={16} className="text-amber-400" />
                            }
                            <span className="text-sm font-medium">
                              {r.result === 'OK' ? `Success (${r.status})` :
                               r.result === 'NETWORK_ERROR' ? 'Network Error' :
                               `HTTP ${r.status}`}
                            </span>
                            <span className="text-xs text-jpmc-muted ml-auto">
                              {new Date(r.timestamp).toLocaleTimeString()}
                            </span>
                          </div>

                          {/* Response Headers */}
                          {r.responseHeaders && Object.keys(r.responseHeaders).length > 0 && (
                            <div>
                              <div className="text-xs font-medium text-jpmc-muted uppercase tracking-wider mb-2">Response Headers</div>
                              <div className="bg-jpmc-navy/50 rounded-lg p-3 font-mono text-xs space-y-0.5 max-h-32 overflow-y-auto">
                                {Object.entries(r.responseHeaders).map(([k, v]) => (
                                  <div key={k}>
                                    <span className="text-blue-400">{k}</span>: <span className="text-jpmc-muted">{v}</span>
                                  </div>
                                ))}
                              </div>
                            </div>
                          )}

                          {/* Response Body */}
                          <div>
                            <div className="text-xs font-medium text-jpmc-muted uppercase tracking-wider mb-2">Response Body</div>
                            <pre className="bg-jpmc-navy/50 rounded-lg p-3 font-mono text-xs text-jpmc-muted overflow-auto max-h-60 whitespace-pre-wrap">
                              {JSON.stringify(r.body, null, 2)}
                            </pre>
                          </div>

                          {/* Trace */}
                          {r.traceId && (
                            <div>
                              <button
                                onClick={() => setShowTraceFor(showTraceFor === r.id ? null : r.id)}
                                className="flex items-center gap-2 text-xs text-blue-400 hover:text-blue-300 font-medium transition-colors"
                              >
                                <Activity size={12} />
                                {showTraceFor === r.id ? 'Hide Trace Flow' : 'View Trace Flow'}
                              </button>
                              <AnimatePresence>
                                {showTraceFor === r.id && (
                                  <motion.div
                                    initial={{ height: 0, opacity: 0 }}
                                    animate={{ height: 'auto', opacity: 1 }}
                                    exit={{ height: 0, opacity: 0 }}
                                    className="mt-3 overflow-hidden"
                                  >
                                    <TraceFlow traceId={r.traceId} inline />
                                  </motion.div>
                                )}
                              </AnimatePresence>
                            </div>
                          )}
                        </div>
                      </motion.div>
                    )}
                  </AnimatePresence>
                </div>
              </motion.div>
            )
          })
        )}
      </div>
    </div>
  )
}
