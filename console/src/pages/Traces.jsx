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

export default function Traces() {
  const { JAEGER_URL } = useConfig()
  const [selectedTrace, setSelectedTrace] = useState(null)
  const [searchService, setSearchService] = useState('kong-gateway')
  const [limit, setLimit] = useState(20)

  const { data: tracesResp, isLoading, refetch } = useQuery({
    queryKey: ['traces', searchService, limit],
    queryFn: async () => {
      const url = `${JAEGER_URL}/api/traces?service=${encodeURIComponent(searchService)}&limit=${limit}&lookback=1h`
      const r = await fetch(url)
      if (!r.ok) throw new Error(`Failed to fetch traces (${r.status})`)
      return r.json()
    },
    refetchInterval: 15000,
    retry: 1,
  })

  const traces = (tracesResp?.data || []).map(trace => {
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

    return {
      traceID: trace.traceID,
      operationName: rootSpan.operationName || 'unknown',
      serviceName: processes[rootSpan.processID]?.serviceName || 'unknown',
      duration: Math.round(totalDuration / 1000),
      spanCount: spans.length,
      services: Array.from(services),
      timestamp: rootSpan.startTime ? new Date(rootSpan.startTime / 1000) : new Date(),
      hasError,
    }
  })

  const services = ['kong-gateway', 'envoy-gateway', 'mock-akamai-edge', 'mock-psaas', 'svc-api', 'svc-web', 'opa']

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-white">Traces</h1>
          <p className="text-sm text-jpmc-muted">Distributed traces across the ingress stack</p>
        </div>
        <a
          href={JAEGER_URL}
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
                <option key={s} value={s}>{s}</option>
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
          <button onClick={() => refetch()} className="btn-secondary flex items-center gap-2">
            {isLoading && <Loader2 size={14} className="animate-spin" />}
            Refresh
          </button>
          <span className="text-xs text-jpmc-muted ml-auto">
            {traces.length} traces found
          </span>
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
              <a href={JAEGER_URL} target="_blank" rel="noopener noreferrer" className="text-blue-400 hover:underline">
                {JAEGER_URL}
              </a>
            </div>
          </div>
        </GlassCard>
      ) : (
        <div className="space-y-2">
          {traces.map((trace, idx) => {
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
