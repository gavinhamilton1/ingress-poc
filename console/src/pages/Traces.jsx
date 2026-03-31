import React, { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Activity, ExternalLink, Search, Clock, ChevronDown,
  ChevronRight, Filter, Loader2,
} from 'lucide-react'
import GlassCard from '../components/GlassCard'
import TraceFlow from '../components/TraceFlow'
import { useConfig } from '../context/ConfigContext'

// Trace categories — like Chrome DevTools network filter
const TRACE_CATEGORIES = {
  routes:     { label: 'Routes',       desc: 'Data plane requests to application routes', color: 'text-emerald-400 bg-emerald-500/15 border-emerald-500/30' },
  assets:     { label: 'Assets',       desc: 'Static files (JS, CSS, images, fonts)',     color: 'text-blue-400 bg-blue-500/15 border-blue-500/30' },
  api:        { label: 'Console API',  desc: 'Console proxy calls (/_proxy/*)',           color: 'text-cyan-400 bg-cyan-500/15 border-cyan-500/30' },
  control:    { label: 'Control Plane', desc: 'Sync, drift, health checks',               color: 'text-amber-400 bg-amber-500/15 border-amber-500/30' },
  other:      { label: 'Other',        desc: 'Unclassified traces',                       color: 'text-gray-400 bg-gray-500/15 border-gray-500/30' },
}

function classifyTrace(trace) {
  const spans = trace.spans || []
  const paths = new Set()
  for (const s of spans) {
    for (const t of (s.tags || [])) {
      if ((t.key === 'http.target' || t.key === 'http.url' || t.key === 'url.path') && typeof t.value === 'string') {
        paths.add(t.value)
      }
    }
  }
  const allPaths = [...paths].join(' ')

  if (allPaths.includes('/_proxy/'))                                    return 'api'
  if (allPaths.includes('/health-reports') || allPaths.includes('/snapshot/routes') ||
      allPaths.includes('/sync-status/') || allPaths.includes('/clusters?format=') ||
      allPaths.includes('/upstreams') || allPaths.includes('/routes?gateway_type='))  return 'control'
  if (allPaths.includes('/assets/') || allPaths.includes('/favicon') ||
      /\.(js|css|ico|png|svg|woff2?|map|json)(\?|$)/.test(allPaths))   return 'assets'
  // If it has a real hostname route path, it's a data plane route
  if (paths.size > 0)                                                   return 'routes'
  return 'other'
}

export default function Traces() {
  const { JAEGER_URL, JAEGER_UI_URL } = useConfig()
  const [selectedTrace, setSelectedTrace] = useState(null)
  const [searchService, setSearchService] = useState('all')
  const [limit, setLimit] = useState(20)
  const [userFilter, setUserFilter] = useState('')
  const [enabledCategories, setEnabledCategories] = useState({ routes: true, assets: false, api: false, control: false, other: false })

  const { data: tracesResp, isLoading, refetch } = useQuery({
    queryKey: ['traces', searchService, limit],
    queryFn: async () => {
      const fetchLimit = Math.max(limit * 10, 200)
      if (searchService === 'all') {
        // Query the entry point service (GTM) which captures all data plane traffic
        const url = `${JAEGER_URL}/api/traces?service=akamai.gtm&limit=${fetchLimit}&lookback=1h`
        const r = await fetch(url)
        if (!r.ok) throw new Error(`Failed to fetch traces (${r.status})`)
        return r.json()
      }
      const url = `${JAEGER_URL}/api/traces?service=${encodeURIComponent(searchService)}&limit=${fetchLimit}&lookback=1h`
      const r = await fetch(url)
      if (!r.ok) throw new Error(`Failed to fetch traces (${r.status})`)
      return r.json()
    },
    refetchInterval: 15000,
    retry: 1,
  })

  // Classify all traces, then filter by enabled categories
  const classifiedTraces = (tracesResp?.data || []).map(trace => ({
    trace,
    category: classifyTrace(trace),
  }))

  // Count per category (for the filter badges)
  const categoryCounts = {}
  for (const { category } of classifiedTraces) {
    categoryCounts[category] = (categoryCounts[category] || 0) + 1
  }

  const toggleCategory = (cat) => {
    setEnabledCategories(prev => ({ ...prev, [cat]: !prev[cat] }))
  }

  const traces = classifiedTraces
    .filter(({ category }) => enabledCategories[category])
    .map(({ trace, category }) => {
    const spans = trace.spans || []
    const processes = trace.processes || {}
    const rootSpan = spans.reduce((min, s) => s.startTime < min.startTime ? s : min, spans[0] || {})
    const totalDuration = spans.reduce((max, s) => {
      const end = s.startTime + s.duration
      return end > max ? end : max
    }, 0) - (rootSpan.startTime || 0)

    const services = new Set()
    spans.forEach(s => {
      const proc = processes[s.processID]
      if (proc) services.add(proc.serviceName)
    })

    const hasError = spans.some(s =>
      s.tags?.some(t => t.key === 'error' && t.value === true) ||
      s.tags?.some(t => t.key === 'http.status_code' && t.value >= 500)
    )

    // Find the deepest backend span (svc-web, svc-api, lambda.*) to identify the endpoint
    const isBackendService = (svc) => ['svc-web', 'svc-api'].includes(svc) || svc.startsWith('lambda.')
    const endpointSpan = spans.find(s => {
      const svc = processes[s.processID]?.serviceName || ''
      return isBackendService(svc) && s.operationName !== s.processID
    })
    // Extract the request path and host from span tags.
    // Supports both old OTel HTTP conventions (http.url, http.target) and
    // the current conventions (url.path, server.address).
    // psaas.hostname is the most authoritative source for the original request hostname.
    let requestPath = ''
    let requestHost = ''
    for (const s of spans) {
      const tagMap = {}
      for (const t of (s.tags || [])) tagMap[t.key] = t.value

      // Current OTel convention: url.path (+ server.address for host)
      const urlPath = tagMap['url.path']
      if (typeof urlPath === 'string' && urlPath.startsWith('/') && !urlPath.includes('/_proxy/')) {
        if (!requestPath || urlPath.length > requestPath.length) {
          requestPath = urlPath
        }
        // Try server.address from any span that carries a real URL path —
        // not just the first one — so we still pick it up even if another
        // span with the same-length path was seen first.
        if (!requestHost) {
          const addr = tagMap['server.address']
          if (addr && addr.includes('.') && !addr.includes('mock-')) {
            requestHost = addr
          }
        }
      }

      // Legacy OTel convention: http.url (full URL)
      const httpUrl = tagMap['http.url']
      if (typeof httpUrl === 'string' && !httpUrl.includes('/_proxy/')) {
        try {
          const u = new URL(httpUrl)
          if (!requestPath || u.pathname.length > requestPath.length) {
            requestPath = u.pathname
            if (!requestHost) requestHost = u.hostname
          }
        } catch {}
      }

      // Legacy OTel convention: http.target
      const httpTarget = tagMap['http.target']
      if (typeof httpTarget === 'string' && httpTarget.startsWith('/') && !httpTarget.includes('/_proxy/')) {
        if (!requestPath || httpTarget.length > requestPath.length) requestPath = httpTarget
      }

      // psaas.hostname is set by the perimeter to the original request Host header —
      // the most reliable source for the public-facing hostname.
      const psaasHost = tagMap['psaas.hostname']
      if (psaasHost && !requestHost) requestHost = psaasHost
    }

    const endpointService = endpointSpan ? (processes[endpointSpan.processID]?.serviceName || 'unknown') : null
    const displayName = requestHost && requestPath
      ? `${requestHost}${requestPath}`
      : requestPath || rootSpan.operationName || 'unknown'
    const displayService = endpointService || processes[rootSpan.processID]?.serviceName || 'unknown'

    // Extract auth.subject from any span
    let authSubject = ''
    for (const s of spans) {
      for (const t of (s.tags || [])) {
        if (t.key === 'auth.subject' && t.value && t.value !== '') {
          authSubject = t.value
          break
        }
      }
      if (authSubject) break
    }

    return {
      traceID: trace.traceID,
      authSubject,
      operationName: displayName,
      serviceName: displayService,
      requestPath,
      requestHost,
      duration: Math.round(totalDuration / 1000),
      spanCount: spans.length,
      services: Array.from(services),
      timestamp: rootSpan.startTime ? new Date(rootSpan.startTime / 1000) : new Date(),
      hasError,
    }
  }).sort((a, b) => b.timestamp - a.timestamp)

  // Build services list dynamically — include any lambda services found in traces
  const lambdaServices = [...new Set(
    traces.flatMap(t => Object.values(t.processes || {}).map(p => p.serviceName))
      .filter(s => s && s.startsWith('lambda.'))
  )]
  const services = ['all', 'akamai.gtm', 'akamai.edge', 'psaas.perimeter', 'envoy-gateway', 'kong-admin-proxy', 'svc-web', 'svc-api', ...lambdaServices, 'auth-service', 'management-api', 'envoy-control-plane', 'watchdog']

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-white">Traces</h1>
          <p className="text-sm text-jpmc-muted">Distributed traces across the ingress stack</p>
        </div>
        <a
          href={JAEGER_UI_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="btn-secondary flex items-center gap-2"
        >
          <ExternalLink size={14} />
          Open Jaeger
        </a>
      </div>

      {/* Search Controls */}
      <GlassCard delay={0.05}>
        <div className="p-4 flex items-center gap-4 flex-wrap">
          <div className="flex items-center gap-2">
            <Filter size={14} className="text-jpmc-muted" />
            <select
              className="select-field w-auto min-w-[160px]"
              value={searchService}
              onChange={e => setSearchService(e.target.value)}
            >
              {services.map(s => (
                <option key={s} value={s}>{s === 'all' ? 'All Services (via GTM)' : s}</option>
              ))}
            </select>
          </div>
          <select
            className="select-field w-auto min-w-[100px]"
            value={limit}
            onChange={e => setLimit(Number(e.target.value))}
          >
            <option value={10}>10 traces</option>
            <option value={20}>20 traces</option>
            <option value={50}>50 traces</option>
          </select>
          <div className="relative">
            <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-jpmc-muted" />
            <input
              className="input-field pl-9 text-sm w-48"
              placeholder="Filter by user..."
              value={userFilter}
              onChange={e => setUserFilter(e.target.value)}
            />
            {userFilter && (
              <button onClick={() => setUserFilter('')} className="absolute right-2 top-1/2 -translate-y-1/2 text-jpmc-muted hover:text-jpmc-text text-xs">✕</button>
            )}
          </div>
          <button onClick={() => refetch()} className="btn-secondary flex items-center gap-2">
            {isLoading && <Loader2 size={14} className="animate-spin" />}
            Refresh
          </button>
          <span className="text-xs text-jpmc-muted ml-auto">
            {traces.length} trace{traces.length !== 1 ? 's' : ''}
            {classifiedTraces.length !== traces.length && ` (${classifiedTraces.length} total)`}
          </span>
        </div>
        {/* Category filter chips — Chrome DevTools style */}
        <div className="px-4 pb-3 flex items-center gap-2 border-t border-jpmc-border/20 pt-3">
          <span className="text-[10px] text-jpmc-muted uppercase tracking-wider mr-1">Filter:</span>
          {Object.entries(TRACE_CATEGORIES).map(([key, cat]) => {
            const count = categoryCounts[key] || 0
            const enabled = enabledCategories[key]
            return (
              <button
                key={key}
                onClick={() => toggleCategory(key)}
                className={`flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[10px] font-medium border transition-all ${
                  enabled ? cat.color : 'text-jpmc-muted/50 bg-transparent border-jpmc-border/20 opacity-50'
                }`}
                title={cat.desc}
              >
                <span className={`w-2 h-2 rounded-sm ${enabled ? 'bg-current' : 'bg-jpmc-muted/30'}`} />
                {cat.label}
                {count > 0 && <span className={`text-[9px] ${enabled ? 'opacity-80' : 'opacity-40'}`}>({count})</span>}
              </button>
            )
          })}
          <button
            onClick={() => setEnabledCategories({ routes: true, assets: true, api: true, control: true, other: true })}
            className="text-[9px] text-jpmc-muted hover:text-jpmc-text ml-auto"
          >All</button>
          <button
            onClick={() => setEnabledCategories({ routes: true, assets: false, api: false, control: false, other: false })}
            className="text-[9px] text-jpmc-muted hover:text-jpmc-text"
          >Routes only</button>
        </div>
      </GlassCard>

      {/* Trace list */}
      {isLoading && traces.length === 0 ? (
        <GlassCard>
          <div className="flex items-center justify-center py-12 gap-3">
            <Loader2 size={16} className="animate-spin text-blue-400" />
            <span className="text-sm text-jpmc-muted">Loading traces from Jaeger...</span>
          </div>
        </GlassCard>
      ) : traces.length === 0 ? (
        <GlassCard>
          <div className="text-center py-12">
            <Activity size={24} className="text-jpmc-muted mx-auto mb-3" />
            <div className="text-sm text-jpmc-muted mb-2">No traces found</div>
            <div className="text-xs text-jpmc-muted">
              Send some requests to generate traces, or check if Jaeger is running at{' '}
              <a href={JAEGER_UI_URL} target="_blank" rel="noopener noreferrer" className="text-blue-400 hover:underline">
                {JAEGER_UI_URL}
              </a>
            </div>
          </div>
        </GlassCard>
      ) : (
        <div className="space-y-2">
          {traces
            .filter(t => !userFilter || (t.authSubject && t.authSubject.toLowerCase().includes(userFilter.toLowerCase())))
            .slice(0, limit).map((trace, idx) => {
            const isSelected = selectedTrace === trace.traceID
            return (
              <motion.div
                key={trace.traceID}
                initial={{ opacity: 0, y: 5 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: idx * 0.02 }}
              >
                <div className={`glass-card overflow-hidden ${isSelected ? 'border-blue-500/30' : ''}`}>
                  <div
                    className="flex items-center gap-4 p-4 cursor-pointer hover:bg-jpmc-hover/50 transition-colors"
                    onClick={() => setSelectedTrace(isSelected ? null : trace.traceID)}
                  >
                    <div className={`w-2 h-2 rounded-full shrink-0 ${
                      trace.hasError ? 'bg-red-400 shadow-[0_0_6px_rgba(248,113,113,0.5)]' : 'bg-emerald-400 shadow-[0_0_6px_rgba(52,211,153,0.5)]'
                    }`} />
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 mb-0.5">
                        <span className="text-sm font-medium text-jpmc-text truncate">{trace.operationName}</span>
                        <span className="badge-gray text-[10px]">{trace.serviceName}</span>
                        {trace.authSubject && (
                          <span className="text-[9px] px-1.5 py-0.5 rounded bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">{trace.authSubject}</span>
                        )}
                      </div>
                      <div className="flex items-center gap-3 text-[11px] text-jpmc-muted">
                        <span>{trace.spanCount} spans</span>
                        <span>{trace.services.length} services</span>
                        <span className="font-mono">{trace.traceID.slice(0, 12)}...</span>
                      </div>
                    </div>
                    <div className="text-right shrink-0">
                      <div className={`text-sm font-semibold ${trace.duration > 500 ? 'text-amber-400' : 'text-jpmc-text'}`}>
                        {trace.duration}ms
                      </div>
                      <div className="text-[11px] text-jpmc-muted flex items-center gap-1 justify-end">
                        <Clock size={10} />
                        {trace.timestamp.toLocaleTimeString()}
                      </div>
                    </div>
                    {isSelected ? <ChevronDown size={14} className="text-jpmc-muted" /> : <ChevronRight size={14} className="text-jpmc-muted" />}
                  </div>

                  <AnimatePresence>
                    {isSelected && (
                      <motion.div
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: 'auto', opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        className="overflow-hidden border-t border-jpmc-border/30"
                      >
                        <div className="p-4">
                          <TraceFlow traceId={trace.traceID} inline />
                        </div>
                      </motion.div>
                    )}
                  </AnimatePresence>
                </div>
              </motion.div>
            )
          })}
        </div>
      )}
    </div>
  )
}
