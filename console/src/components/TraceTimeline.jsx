import React, { useState, useMemo } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { ChevronDown, ChevronRight } from 'lucide-react'

// ── Service colour palette ────────────────────────────────────────────────────
const SERVICE_COLORS = {
  'akamai.gtm':       { bar: 'bg-cyan-400',    text: 'text-cyan-400',    dot: 'bg-cyan-400' },
  'akamai.edge':      { bar: 'bg-yellow-400',  text: 'text-yellow-400',  dot: 'bg-yellow-400' },
  'psaas.perimeter':  { bar: 'bg-orange-400',  text: 'text-orange-400',  dot: 'bg-orange-400' },
  'envoy-gateway':    { bar: 'bg-blue-400',    text: 'text-blue-400',    dot: 'bg-blue-400' },
  'kong-admin-proxy': { bar: 'bg-indigo-400',  text: 'text-indigo-400',  dot: 'bg-indigo-400' },
  'auth-service':     { bar: 'bg-violet-400',  text: 'text-violet-400',  dot: 'bg-violet-400' },
  'svc-web':          { bar: 'bg-emerald-400', text: 'text-emerald-400', dot: 'bg-emerald-400' },
  'svc-api':          { bar: 'bg-teal-400',    text: 'text-teal-400',    dot: 'bg-teal-400' },
  'management-api':   { bar: 'bg-purple-400',  text: 'text-purple-400',  dot: 'bg-purple-400' },
  'watchdog':         { bar: 'bg-pink-400',    text: 'text-pink-400',    dot: 'bg-pink-400' },
}

function serviceColor(name) {
  if (SERVICE_COLORS[name]) return SERVICE_COLORS[name]
  if (name?.startsWith('lambda.')) return { bar: 'bg-amber-400', text: 'text-amber-400', dot: 'bg-amber-400' }
  return { bar: 'bg-gray-400', text: 'text-gray-400', dot: 'bg-gray-400' }
}

function fmtDuration(us) {
  if (us >= 1000) return `${(us / 1000).toFixed(2)}ms`
  return `${us}µs`
}

// Build a tree of spans sorted for display (depth-first)
function buildSpanTree(spans, processes) {
  const byId = {}
  spans.forEach(s => { byId[s.spanID] = { ...s, children: [] } })

  const roots = []
  spans.forEach(s => {
    const parentRef = (s.references || []).find(r => r.refType === 'CHILD_OF')
    if (parentRef && byId[parentRef.spanID]) {
      byId[parentRef.spanID].children.push(byId[s.spanID])
    } else {
      roots.push(byId[s.spanID])
    }
  })

  // Flatten depth-first
  const flat = []
  function walk(node, depth) {
    const proc = processes[node.processID]
    flat.push({ ...node, depth, serviceName: proc?.serviceName || 'unknown' })
    node.children
      .sort((a, b) => a.startTime - b.startTime)
      .forEach(c => walk(c, depth + 1))
  }
  roots.sort((a, b) => a.startTime - b.startTime).forEach(r => walk(r, 0))
  return flat
}

// ── Span row ─────────────────────────────────────────────────────────────────
function SpanRow({ span, traceStart, totalDuration, isExpanded, onToggle }) {
  const colors = serviceColor(span.serviceName)
  const hasChildren = span.children?.length > 0

  const leftPct = totalDuration > 0 ? ((span.startTime - traceStart) / totalDuration) * 100 : 0
  const widthPct = totalDuration > 0 ? Math.max((span.duration / totalDuration) * 100, 0.3) : 0.3

  const hasError = span.tags?.some(t => (t.key === 'error' && t.value === true) ||
    (t.key === 'http.status_code' && parseInt(t.value) >= 500))

  // Tag map for the detail panel
  const tagMap = {}
  ;(span.tags || []).forEach(t => { tagMap[t.key] = t.value })
  const httpStatus = tagMap['http.status_code'] || tagMap['http.response.status_code']
  const method = tagMap['http.method'] || tagMap['http.request.method']

  return (
    <div className="group">
      <div
        className="flex items-center hover:bg-white/[0.03] transition-colors cursor-pointer border-b border-white/[0.04]"
        style={{ minHeight: 36 }}
        onClick={() => onToggle(span.spanID)}
      >
        {/* Left: service + operation name */}
        <div className="w-[45%] shrink-0 flex items-center gap-1 pr-3 py-2 pl-4 overflow-hidden">
          <span style={{ width: span.depth * 16 + 'px', flexShrink: 0 }} />
          {hasChildren ? (
            isExpanded
              ? <ChevronDown size={12} className="text-jpmc-muted shrink-0" />
              : <ChevronRight size={12} className="text-jpmc-muted shrink-0" />
          ) : (
            <span className="w-3 shrink-0" />
          )}
          <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${hasError ? 'bg-red-400' : colors.dot}`} />
          <div className="min-w-0 ml-1">
            <div className={`text-[10px] font-medium truncate ${colors.text}`}>{span.serviceName}</div>
            <div className="text-[10px] text-jpmc-muted/80 truncate">{span.operationName}</div>
          </div>
          {method && (
            <span className={`shrink-0 ml-1 text-[9px] font-bold px-1 py-0.5 rounded ${
              method === 'GET'    ? 'text-emerald-400 bg-emerald-500/15' :
              method === 'POST'   ? 'text-blue-400 bg-blue-500/15' :
              method === 'PUT'    ? 'text-amber-400 bg-amber-500/15' :
              'text-red-400 bg-red-500/15'
            }`}>{method}</span>
          )}
          {httpStatus && (
            <span className={`shrink-0 ml-1 text-[9px] font-mono ${parseInt(httpStatus) >= 400 ? 'text-red-400' : 'text-emerald-400'}`}>
              {httpStatus}
            </span>
          )}
        </div>

        {/* Right: timeline bar */}
        <div className="flex-1 relative h-full flex items-center py-2 pr-4">
          <div className="relative w-full h-4">
            <div
              className={`absolute top-0 h-full rounded-sm opacity-80 ${hasError ? 'bg-red-500' : colors.bar}`}
              style={{ left: `${leftPct}%`, width: `${widthPct}%`, minWidth: 2 }}
            />
            <span
              className="absolute top-1/2 -translate-y-1/2 text-[9px] text-jpmc-muted whitespace-nowrap pl-1"
              style={{ left: `${Math.min(leftPct + widthPct, 95)}%` }}
            >
              {fmtDuration(span.duration)}
            </span>
          </div>
        </div>
      </div>

      {/* Detail panel */}
      <AnimatePresence>
        {isExpanded && (
          <motion.div
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: 'auto', opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            className="overflow-hidden"
          >
            <div className="mx-4 mb-2 mt-1 p-3 rounded-lg bg-white/[0.03] border border-white/10 text-[10px] space-y-2">
              <div className="flex items-center gap-4 pb-2 border-b border-white/10">
                <span className="text-jpmc-muted">Span ID: <code className="text-jpmc-text/70">{span.spanID}</code></span>
                <span className="text-jpmc-muted">Start: <span className="text-jpmc-text">{fmtDuration(span.startTime - traceStart)} into trace</span></span>
                <span className="text-jpmc-muted">Duration: <span className={`font-medium ${colors.text}`}>{fmtDuration(span.duration)}</span></span>
              </div>
              {span.tags?.length > 0 && (
                <div className="grid grid-cols-2 gap-x-6 gap-y-0.5">
                  {span.tags.map((t, i) => (
                    <div key={i} className="flex items-start gap-2 overflow-hidden">
                      <span className="text-jpmc-muted shrink-0 min-w-[100px] truncate">{t.key}</span>
                      <span className={`font-mono break-all ${
                        (t.key === 'error' && t.value === true) ? 'text-red-400' :
                        (t.key.includes('status') && parseInt(t.value) >= 400) ? 'text-red-400' :
                        (t.key.includes('status') && parseInt(t.value) < 400) ? 'text-emerald-400' :
                        'text-jpmc-text/80'
                      }`}>{String(t.value).slice(0, 100)}</span>
                    </div>
                  ))}
                </div>
              )}
              {span.logs?.length > 0 && (
                <div className="pt-2 border-t border-white/10">
                  <div className="text-jpmc-muted mb-1">Logs</div>
                  {span.logs.map((log, i) => (
                    <div key={i} className="text-jpmc-text/70">{log.fields?.map(f => `${f.key}=${f.value}`).join(' ')}</div>
                  ))}
                </div>
              )}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}

// ── Tick marks for the timeline header ───────────────────────────────────────
function TimelineHeader({ totalDuration }) {
  const ticks = [0, 0.25, 0.5, 0.75, 1]
  return (
    <div className="relative h-6 border-b border-white/10">
      {ticks.map(t => (
        <span
          key={t}
          className="absolute top-1 text-[9px] text-jpmc-muted -translate-x-1/2"
          style={{ left: `${t * 100}%` }}
        >
          {fmtDuration(Math.round(totalDuration * t))}
        </span>
      ))}
    </div>
  )
}

// ── Legend ────────────────────────────────────────────────────────────────────
function Legend({ services }) {
  return (
    <div className="flex items-center gap-4 px-4 py-2 border-b border-white/10 flex-wrap">
      {services.map(svc => {
        const c = serviceColor(svc)
        return (
          <div key={svc} className="flex items-center gap-1.5">
            <span className={`w-2.5 h-2.5 rounded-sm ${c.bar}`} />
            <span className={`text-[10px] ${c.text}`}>{svc}</span>
          </div>
        )
      })}
    </div>
  )
}

// ── Main export ───────────────────────────────────────────────────────────────
export default function TraceTimeline({ traceData }) {
  const [expandedSpans, setExpandedSpans] = useState({})

  const { flatSpans, traceStart, totalDuration, services } = useMemo(() => {
    if (!traceData?.data?.[0]) return { flatSpans: [], traceStart: 0, totalDuration: 1, services: [] }
    const trace = traceData.data[0]
    const spans = trace.spans || []
    const processes = trace.processes || {}

    const flat = buildSpanTree(spans, processes)
    const traceStart = Math.min(...spans.map(s => s.startTime))
    const traceEnd = Math.max(...spans.map(s => s.startTime + s.duration))
    const totalDuration = traceEnd - traceStart || 1

    const services = [...new Set(flat.map(s => s.serviceName))].sort()

    return { flatSpans: flat, traceStart, totalDuration, services }
  }, [traceData])

  const toggle = (spanID) => {
    setExpandedSpans(prev => ({ ...prev, [spanID]: !prev[spanID] }))
  }

  if (!flatSpans.length) {
    return <div className="text-center py-8 text-jpmc-muted text-sm">No spans to display</div>
  }

  return (
    <div className="rounded-lg border border-white/10 overflow-hidden bg-[#0d1117] text-white">
      {/* Legend */}
      <Legend services={services} />

      {/* Column headers */}
      <div className="flex border-b border-white/10 bg-white/[0.02]">
        <div className="w-[45%] shrink-0 px-4 py-2 text-[9px] text-jpmc-muted uppercase tracking-wider">
          Service &amp; Operation
        </div>
        <div className="flex-1 px-4">
          <TimelineHeader totalDuration={totalDuration} />
        </div>
      </div>

      {/* Tick grid lines overlay */}
      <div className="relative">
        {/* Vertical grid lines */}
        <div className="absolute inset-0 pointer-events-none" style={{ left: '45%' }}>
          {[0.25, 0.5, 0.75].map(t => (
            <div
              key={t}
              className="absolute top-0 bottom-0 border-l border-white/[0.05]"
              style={{ left: `${t * 100}%` }}
            />
          ))}
        </div>

        {/* Span rows */}
        {flatSpans.map(span => (
          <SpanRow
            key={span.spanID}
            span={span}
            traceStart={traceStart}
            totalDuration={totalDuration}
            isExpanded={!!expandedSpans[span.spanID]}
            onToggle={toggle}
          />
        ))}
      </div>

      {/* Footer */}
      <div className="px-4 py-2 border-t border-white/10 bg-white/[0.02] flex items-center gap-4 text-[10px] text-jpmc-muted">
        <span>{flatSpans.length} spans</span>
        <span>Total: {fmtDuration(totalDuration)}</span>
        <span>{services.length} services</span>
      </div>
    </div>
  )
}
