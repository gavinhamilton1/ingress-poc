import React, { useState, useEffect } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Globe, Shield, Server, Cpu, Box, ChevronDown, ChevronRight,
  ArrowRight, ExternalLink,
} from 'lucide-react'
import { useConfig } from '../context/ConfigContext'

const SERVICE_MAP = {
  'akamai-gtm': { label: 'GTM', icon: Globe, order: 0 },
  'akamai-edge': { label: 'CDN/WAF', icon: Shield, order: 1 },
  'mock-akamai-gtm': { label: 'GTM', icon: Globe, order: 0 },
  'mock-akamai-edge': { label: 'CDN/WAF', icon: Shield, order: 1 },
  'mock-psaas': { label: 'PSaaS', icon: Server, order: 2 },
  'psaas': { label: 'PSaaS', icon: Server, order: 2 },
  'kong-gateway': { label: 'Kong GW', icon: Cpu, order: 3 },
  'envoy-gateway': { label: 'Envoy GW', icon: Cpu, order: 3 },
  'kong': { label: 'Kong GW', icon: Cpu, order: 3 },
  'envoy': { label: 'Envoy GW', icon: Cpu, order: 3 },
  'svc-api': { label: 'API Backend', icon: Box, order: 4 },
  'svc-web': { label: 'Web Backend', icon: Box, order: 4 },
  'opa': { label: 'OPA Policy', icon: Shield, order: 3.5 },
  'auth-service': { label: 'Auth Service', icon: Shield, order: 3.5 },
}

function getServiceInfo(serviceName) {
  const lower = (serviceName || '').toLowerCase()
  for (const [key, val] of Object.entries(SERVICE_MAP)) {
    if (lower.includes(key)) return val
  }
  return { label: serviceName, icon: Box, order: 5 }
}

function parseTrace(traceData) {
  if (!traceData || !traceData.data || traceData.data.length === 0) return []

  const trace = traceData.data[0]
  const spans = trace.spans || []
  const processes = trace.processes || {}

  const nodes = new Map()

  spans.forEach(span => {
    const process = processes[span.processID]
    const serviceName = process?.serviceName || 'unknown'
    const info = getServiceInfo(serviceName)
    const key = info.label

    if (!nodes.has(key)) {
      nodes.set(key, {
        key,
        label: info.label,
        icon: info.icon,
        order: info.order,
        serviceName,
        status: 'passed',
        spans: [],
        totalDuration: 0,
        minStart: Infinity,
        maxEnd: 0,
      })
    }

    const node = nodes.get(key)
    node.spans.push(span)

    const hasError = span.tags?.some(t => t.key === 'error' && t.value === true)
      || span.tags?.some(t => t.key === 'http.status_code' && t.value >= 500)
    if (hasError) node.status = 'failed'

    const startUs = span.startTime
    const endUs = startUs + span.duration
    node.totalDuration += span.duration
    if (startUs < node.minStart) node.minStart = startUs
    if (endUs > node.maxEnd) node.maxEnd = endUs
  })

  return Array.from(nodes.values())
    .sort((a, b) => a.order - b.order || a.minStart - b.minStart)
    .map((node, idx, arr) => ({
      ...node,
      latencyMs: Math.round(node.totalDuration / 1000),
      connectionLatencyMs: idx > 0
        ? Math.max(0, Math.round((node.minStart - arr[idx - 1].maxEnd) / 1000))
        : 0,
    }))
}

function TraceNode({ node, index, expanded, onToggle }) {
  const Icon = node.icon
  const statusColor = node.status === 'passed'
    ? 'border-emerald-500/50 bg-emerald-500/10'
    : 'border-red-500/50 bg-red-500/10'
  const statusDot = node.status === 'passed' ? 'bg-emerald-400' : 'bg-red-400'

  return (
    <motion.div
      initial={{ opacity: 0, x: -20 }}
      animate={{ opacity: 1, x: 0 }}
      transition={{ duration: 0.3, delay: index * 0.1 }}
      className="flex flex-col items-center"
    >
      <div
        onClick={() => onToggle(node.key)}
        className={`relative p-4 rounded-xl border ${statusColor} cursor-pointer hover:scale-105 transition-transform duration-150 min-w-[100px]`}
      >
        <div className="flex flex-col items-center gap-2">
          <Icon size={20} className={node.status === 'passed' ? 'text-emerald-400' : 'text-red-400'} />
          <span className="text-xs font-medium text-jpmc-text whitespace-nowrap">{node.label}</span>
          <span className={`w-2 h-2 rounded-full ${statusDot}`} />
        </div>
        <div className="absolute -bottom-5 left-1/2 -translate-x-1/2 text-[10px] text-jpmc-muted whitespace-nowrap">
          {node.latencyMs}ms
        </div>
      </div>

      <AnimatePresence>
        {expanded === node.key && (
          <motion.div
            initial={{ opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: 'auto' }}
            exit={{ opacity: 0, height: 0 }}
            className="mt-8 glass-card p-3 text-xs w-64 overflow-hidden"
          >
            <div className="text-jpmc-muted mb-2 font-medium">Span Details ({node.spans.length} spans)</div>
            {node.spans.slice(0, 5).map((span, i) => (
              <div key={i} className="py-1.5 border-b border-jpmc-border/30 last:border-0">
                <div className="text-jpmc-text font-medium truncate">{span.operationName}</div>
                <div className="text-jpmc-muted mt-0.5">
                  {Math.round(span.duration / 1000)}ms
                  {span.tags?.filter(t => ['http.status_code', 'http.method', 'http.url'].includes(t.key)).map(t => (
                    <span key={t.key} className="ml-2">
                      {t.key.split('.').pop()}: {String(t.value).substring(0, 40)}
                    </span>
                  ))}
                </div>
              </div>
            ))}
            {node.spans.length > 5 && (
              <div className="text-jpmc-muted pt-1">+{node.spans.length - 5} more spans</div>
            )}
          </motion.div>
        )}
      </AnimatePresence>
    </motion.div>
  )
}

function ConnectionLine({ latencyMs, index }) {
  return (
    <motion.div
      initial={{ opacity: 0, scaleX: 0 }}
      animate={{ opacity: 1, scaleX: 1 }}
      transition={{ duration: 0.2, delay: index * 0.1 + 0.05 }}
      className="flex items-center self-start mt-7"
    >
      <div className="w-8 border-t border-dashed border-jpmc-border relative">
        <ArrowRight size={10} className="absolute right-0 -top-[5px] text-jpmc-border" />
        {latencyMs > 0 && (
          <span className="absolute -top-4 left-1/2 -translate-x-1/2 text-[9px] text-jpmc-muted whitespace-nowrap">
            +{latencyMs}ms
          </span>
        )}
      </div>
    </motion.div>
  )
}

export default function TraceFlow({ traceId, inline = false }) {
  const { JAEGER_URL } = useConfig()
  const [traceData, setTraceData] = useState(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [expanded, setExpanded] = useState(null)

  useEffect(() => {
    if (!traceId) return
    setLoading(true)
    setError(null)

    fetch(`${JAEGER_URL}/api/traces/${traceId}`)
      .then(r => {
        if (!r.ok) throw new Error(`Trace not found (${r.status})`)
        return r.json()
      })
      .then(data => {
        setTraceData(data)
        setLoading(false)
      })
      .catch(err => {
        setError(err.message)
        setLoading(false)
      })
  }, [traceId, JAEGER_URL])

  const toggleExpand = (key) => {
    setExpanded(expanded === key ? null : key)
  }

  if (!traceId) {
    return (
      <div className="text-jpmc-muted text-sm text-center py-8">
        No trace ID available
      </div>
    )
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-8 gap-3">
        <div className="w-4 h-4 border-2 border-blue-500 border-t-transparent rounded-full animate-spin" />
        <span className="text-sm text-jpmc-muted">Loading trace...</span>
      </div>
    )
  }

  if (error) {
    return (
      <div className="text-center py-8">
        <div className="text-red-400 text-sm mb-2">Failed to load trace</div>
        <div className="text-jpmc-muted text-xs">{error}</div>
        <a
          href={`${JAEGER_URL}/trace/${traceId}`}
          target="_blank"
          rel="noopener noreferrer"
          className="text-blue-400 text-xs mt-2 inline-flex items-center gap-1 hover:underline"
        >
          Open in Jaeger <ExternalLink size={10} />
        </a>
      </div>
    )
  }

  const nodes = traceData ? parseTrace(traceData) : []

  if (nodes.length === 0) {
    return (
      <div className="text-center py-8 text-jpmc-muted text-sm">
        No spans found in trace
      </div>
    )
  }

  return (
    <div className={`${inline ? '' : 'glass-card p-5'}`}>
      <div className="flex items-center justify-between mb-4">
        <div className="text-xs text-jpmc-muted">
          Trace: <code className="text-blue-400">{traceId.slice(0, 16)}...</code>
        </div>
        <a
          href={`${JAEGER_URL}/trace/${traceId}`}
          target="_blank"
          rel="noopener noreferrer"
          className="text-xs text-blue-400 hover:underline flex items-center gap-1"
        >
          Open in Jaeger <ExternalLink size={10} />
        </a>
      </div>

      <div className="flex items-start gap-0 overflow-x-auto pb-8 pt-2">
        {/* Client node */}
        <motion.div
          initial={{ opacity: 0, x: -20 }}
          animate={{ opacity: 1, x: 0 }}
          className="flex flex-col items-center"
        >
          <div className="p-4 rounded-xl border border-blue-500/50 bg-blue-500/10 min-w-[100px]">
            <div className="flex flex-col items-center gap-2">
              <Globe size={20} className="text-blue-400" />
              <span className="text-xs font-medium text-jpmc-text">Client</span>
              <span className="w-2 h-2 rounded-full bg-blue-400" />
            </div>
          </div>
        </motion.div>

        {nodes.map((node, idx) => (
          <React.Fragment key={node.key}>
            <ConnectionLine latencyMs={node.connectionLatencyMs} index={idx} />
            <TraceNode
              node={node}
              index={idx + 1}
              expanded={expanded}
              onToggle={toggleExpand}
            />
          </React.Fragment>
        ))}
      </div>
    </div>
  )
}
