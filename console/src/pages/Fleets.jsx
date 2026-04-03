import React, { useState, useMemo } from 'react'
import { createPortal } from 'react-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Server, Cpu, Globe, ChevronDown, ChevronRight, ChevronLeft,
  Activity, ArrowRight, MapPin, Plus, X, Info,
  GitBranch, Cloud, Shield, Box, Layers, Zap, Lock, Hash,
  Pause, Play, Search, Filter, ArrowUpDown, Check, AlertTriangle,
  Minus, Container, Monitor, Trash2, RefreshCw, Power, Edit3, Copy, Settings,
} from 'lucide-react'
import GlassCard from '../components/GlassCard'
import StatusBadge from '../components/StatusBadge'
import RouteDetailPanel from '../components/RouteDetailPanel'
import { useConfig } from '../context/ConfigContext'

function InstancePill({ inst, nodeDown = false }) {
  const isApi = inst.gateway_type === 'kong' || inst.context_path.startsWith('/api')
  const effectivelyActive = inst.status === 'active' && !nodeDown
  return (
    <motion.div
      key={inst.id}
      initial={{ opacity: 0, x: -10 }}
      animate={{ opacity: 1, x: 0 }}
      className={`flex items-center gap-2 px-3 py-1.5 rounded-lg border text-xs ${
        effectivelyActive
          ? 'bg-emerald-500/10 border-emerald-500/30'
          : 'bg-amber-500/10 border-amber-500/30'
      }`}
    >
      <span className={`w-1.5 h-1.5 rounded-full ${
        effectivelyActive ? 'bg-emerald-400' : 'bg-amber-400'
      }`} />
      <code className="text-jpmc-text">{inst.context_path}</code>
      <ArrowRight size={10} className="text-jpmc-muted" />
      <span className="text-jpmc-muted font-mono text-[10px]">{inst.backend.split('://')[1]}</span>
    </motion.div>
  )
}

function formatUptime(startedAt) {
  if (!startedAt) return ''
  const diff = Date.now() - new Date(startedAt).getTime()
  if (diff < 0) return 'just now'
  const secs = Math.floor(diff / 1000)
  if (secs < 60) return `Up ${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `Up ${mins}m`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `Up ${hrs}h`
  const days = Math.floor(hrs / 24)
  return `Up ${days}d`
}

function nodeStatusColor(status) {
  if (status === 'running') return { dot: 'bg-emerald-400', glow: 'shadow-[0_0_6px_rgba(52,211,153,0.5)]', border: 'border-emerald-500/30', bg: 'bg-emerald-500/5' }
  if (status === 'starting' || status === 'restarting' || status === 'stopped' || status === 'suspended') return { dot: 'bg-amber-400', glow: 'shadow-[0_0_6px_rgba(251,191,36,0.5)]', border: 'border-amber-500/30', bg: 'bg-amber-500/5' }
  return { dot: 'bg-red-400', glow: 'shadow-[0_0_6px_rgba(248,113,113,0.5)]', border: 'border-red-500/30', bg: 'bg-red-500/5' }
}

/* Helper: derive node type counts from a list of nodes */
function getNodeTypeCounts(nodes) {
  const envoyCount = nodes.filter(n => (n.gateway_type || 'envoy') === 'envoy').length
  const kongCount = nodes.filter(n => (n.gateway_type || 'envoy') === 'kong').length
  return { envoyCount, kongCount }
}

/* Helper: determine what node types a fleet has from its nodes + instances */
function getFleetNodeTypes(fleet, nodes = []) {
  const allItems = [...nodes, ...(fleet.instances || [])]
  const hasEnvoy = allItems.some(n => (n.gateway_type || 'envoy') === 'envoy')
  const hasKong = allItems.some(n => (n.gateway_type || 'envoy') === 'kong')
  return { hasEnvoy, hasKong }
}

/* Routes for one node: simply filter by matching gateway_type.
   All routes in a fleet are served by all nodes of the same type. */
function getRoutesForNode(node, instances) {
  const nodeGwType = (node.gateway_type || 'envoy').toLowerCase()
  return instances.filter(inst => {
    const instType = (inst.gateway_type || (inst.context_path?.startsWith('/api') ? 'kong' : 'envoy')).toLowerCase()
    // Only show active routes — inactive means deleted from git or manually deactivated
    return instType === nodeGwType && (inst.status || 'active') === 'active'
  })
}

const LAMBDA_TEMPLATES = [
  { name: 'Hello World', code: `module.exports = async (req, res) => {\n  res.json({\n    message: 'Hello from Lambda!',\n    path: req.path,\n    method: req.method,\n    timestamp: new Date().toISOString(),\n  })\n}` },
  { name: 'Echo', code: `module.exports = async (req, res) => {\n  res.json({\n    echo: true,\n    method: req.method,\n    path: req.path,\n    headers: req.headers,\n    query: req.query,\n    body: req.body,\n  })\n}` },
  { name: 'Mock API', code: `module.exports = async (req, res) => {\n  const data = [\n    { id: 1, name: 'Item One', status: 'active' },\n    { id: 2, name: 'Item Two', status: 'pending' },\n    { id: 3, name: 'Item Three', status: 'active' },\n  ]\n  if (req.method === 'GET') {\n    res.json({ data, total: data.length, path: req.path })\n  } else if (req.method === 'POST') {\n    const item = { id: data.length + 1, ...req.json, status: 'created' }\n    res.status(201).json(item)\n  } else {\n    res.status(405).json({ error: 'Method not allowed' })\n  }\n}` },
  { name: 'Health Check', code: `module.exports = async (req, res) => {\n  const uptime = process.uptime()\n  const memory = process.memoryUsage()\n  res.json({\n    status: 'healthy',\n    uptime: Math.floor(uptime) + 's',\n    memory: {\n      rss: Math.floor(memory.rss / 1024 / 1024) + 'MB',\n      heap: Math.floor(memory.heapUsed / 1024 / 1024) + 'MB',\n    },\n    node: process.version,\n    timestamp: new Date().toISOString(),\n  })\n}` },
]

const DEFAULT_LAMBDA_CODE = LAMBDA_TEMPLATES[0].code

function makeEmptyRoute() {
  return {
    context_path: '',
    destination_type: 'backend', // 'backend' | 'lambda'
    backend_url: 'http://svc-web:8004',
    function_code: DEFAULT_LAMBDA_CODE,
    methods: ['GET', 'POST', 'PUT', 'DELETE'],
    audience: '',
    // Envoy-specific
    timeout_ms: 30000, retry_count: 3, retry_on: '5xx', rate_limit_rps: 0, cors_enabled: true,
    // Kong-specific
    kong_rate_limit_rps: 0, strip_path: false, plugins: [],
  }
}

function NodeCard({ node, fleetId, apiUrl, onAction, readOnly = false, routes = [] }) {
  const queryClient = useQueryClient()
  const [pendingAction, setPendingAction] = useState(null)
  const [editingRoute, setEditingRoute] = useState(null)
  const [editForm, setEditForm] = useState({
    path: '', destination_type: 'backend', backend_url: '', function_code: '',
    audience: '', status: 'active', methods: ['GET', 'POST', 'PUT', 'DELETE'],
  })
  const sc = nodeStatusColor(pendingAction ? (pendingAction === 'stop' ? 'stopped' : pendingAction === 'start' ? 'running' : 'exited') : (node.status || 'running'))
  const isRunning = pendingAction === 'start' ? true : pendingAction === 'stop' ? false : node.status === 'running'
  // container_name == node_name in the DB — the stop/start/delete endpoints match on node_name,
  // so prefer container_name over container_id (which in K8s mode is a pod UID, not node_name).
  const cid = node.container_name || node.container_id || node.id
  const nodeName = node.container_name || node.name || (cid ? cid.slice(0, 12) : 'unknown')

  const handleAction = async (action) => {
    if (readOnly) return
    if (!cid) { console.error('No container ID for node', node); return }
    if (action === 'delete' && !confirm(`Delete node ${nodeName}? This cannot be undone.`)) return
    setPendingAction(action)
    const method = action === 'delete' ? 'DELETE' : 'POST'
    const url = action === 'delete'
      ? `${apiUrl}/fleets/${fleetId}/nodes/${cid}`
      : `${apiUrl}/fleets/${fleetId}/nodes/${cid}/${action}`
    try {
      const resp = await fetch(url, { method })
      if (!resp.ok) console.error('Node action failed:', resp.status, await resp.text())
    } catch (e) { console.error('Node action error:', e) }
    queryClient.invalidateQueries({ queryKey: ['fleetNodes'] })
    queryClient.invalidateQueries({ queryKey: ['fleets'] })
    setPendingAction(null)
    if (onAction) onAction(action)
  }

  const regionLabel = node.datacenter || node.region || 'unassigned'
  const actionLabel = pendingAction === 'stop' ? 'stopping...' : pendingAction === 'start' ? 'starting...' : pendingAction === 'delete' ? 'removing...' : null
  const gwType = node.gateway_type || 'envoy'
  const gwBadgeClass = gwType === 'kong'
    ? 'bg-blue-500/10 border-blue-500/30 text-blue-400'
    : gwType === 'service'
    ? 'bg-gray-500/10 border-gray-500/30 text-gray-400'
    : 'bg-purple-500/10 border-purple-500/30 text-purple-400'

  return (
    <motion.div
      initial={{ opacity: 0, scale: 0.95 }}
      animate={{ opacity: pendingAction === 'delete' ? 0.5 : 1, scale: 1 }}
      className={`relative rounded-lg border ${sc.border} ${sc.bg} transition-all group ${pendingAction ? 'opacity-70' : ''}`}
    >
      {isRunning && !pendingAction && (
        <motion.div
          className="absolute inset-0 rounded-lg border border-emerald-400/20 pointer-events-none"
          animate={{ opacity: [0, 0.4, 0] }}
          transition={{ duration: 3, repeat: Infinity, ease: 'easeInOut' }}
        />
      )}
      <div className="flex items-center gap-2.5 px-3.5 py-2.5">
        <span className={`w-2 h-2 rounded-full shrink-0 ${sc.dot} ${sc.glow}`} />
        <div className="flex flex-col min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <code className="text-[11px] text-jpmc-text font-mono truncate">{nodeName}</code>
            <span className={`text-[9px] px-1.5 py-0.5 rounded border ${gwBadgeClass}`}>{gwType}</span>
            <span className={`text-[9px] capitalize font-medium ${
              actionLabel ? 'text-amber-400 animate-pulse' : isRunning ? 'text-emerald-400' : (node.status === 'exited' || node.status === 'error') ? 'text-red-400' : 'text-amber-400'
            }`}>{actionLabel || node.status || 'running'}</span>
            {readOnly && (
              <span className="text-[8px] px-1.5 py-0.5 rounded bg-slate-500/10 border border-slate-500/30 text-slate-400">docker-compose managed</span>
            )}
          </div>
          <div className="flex items-center gap-3 mt-0.5">
            <span className="text-[9px] text-jpmc-muted flex items-center gap-1">
              <MapPin size={8} />
              <span>{regionLabel}</span>
            </span>
            {node.port > 0 && (
              <span className="text-[9px] text-jpmc-muted font-mono">port:{node.port}</span>
            )}
            {!readOnly && cid && (
              <span className="text-[9px] text-jpmc-muted font-mono opacity-50">{cid.slice(0, 12)}</span>
            )}
          </div>
        </div>
        {/* Node action buttons -- hidden for read-only (CP) nodes */}
        {!readOnly && (
          <div className={`flex items-center gap-0.5 shrink-0 relative z-10 ${pendingAction ? 'opacity-50 pointer-events-none' : ''}`}>
            {isRunning ? (
              <button onClick={(e) => { e.stopPropagation(); handleAction('stop') }}
                className="p-1.5 rounded hover:bg-amber-500/10 text-jpmc-muted hover:text-amber-400 transition-colors" title="Stop node">
                <Pause size={12} />
              </button>
            ) : (
              <button onClick={(e) => { e.stopPropagation(); handleAction('start') }}
                className="p-1.5 rounded hover:bg-emerald-500/10 text-jpmc-muted hover:text-emerald-400 transition-colors" title="Start node">
                <Play size={12} />
              </button>
            )}
            <button onClick={(e) => { e.stopPropagation(); handleAction('delete') }}
              className="p-1.5 rounded hover:bg-red-500/10 text-jpmc-muted hover:text-red-400 transition-colors" title="Delete node">
              <Trash2 size={12} />
            </button>
          </div>
        )}
      </div>
      {/* Routes assigned to this node */}
      {routes.length > 0 && (
        <div className="mx-3.5 mb-2.5 pt-2 border-t border-jpmc-border/20 space-y-1">
          {routes.map(route => {
            const routeId = route.route_id || route.id  // prefer actual route ID over fleet instance ID
            const instId = route.id  // fleet instance ID for cleanup
            const isEditing = editingRoute === routeId
            const routeStatus = route.status || 'active'
            return isEditing ? (
              <div key={routeId} className="p-2.5 rounded-lg bg-jpmc-navy/70 border border-blue-500/30 space-y-2">
                <div className="flex items-center gap-2">
                  <span className="text-[10px] text-blue-400 font-medium">Edit Route</span>
                  <span className="text-[9px] text-jpmc-muted font-mono">{routeId.slice(0,8)}</span>
                  <button onClick={() => setEditingRoute(null)} className="ml-auto p-0.5 rounded hover:bg-jpmc-hover text-jpmc-muted">
                    <X size={10} />
                  </button>
                </div>
                {/* Path */}
                <div>
                  <label className="text-[9px] text-jpmc-muted">Context Path</label>
                  <input className="input-field text-[10px] py-1 font-mono" value={editForm.path}
                    onChange={e => setEditForm({...editForm, path: e.target.value})} />
                </div>
                {/* Destination */}
                <div>
                  <label className="text-[9px] text-jpmc-muted mb-1 block">Destination</label>
                  <div className="flex items-center gap-2 mb-1.5">
                    {['backend', 'lambda'].map(dt => (
                      <button key={dt} onClick={() => setEditForm({...editForm, destination_type: dt})}
                        className={`px-2 py-0.5 rounded text-[10px] border transition-all ${
                          editForm.destination_type === dt
                            ? 'border-blue-500/50 bg-blue-500/10 text-blue-400'
                            : 'border-jpmc-border/30 text-jpmc-muted hover:border-jpmc-border/60'
                        }`}>
                        {dt === 'backend' ? 'Backend URL' : 'Lambda Function'}
                      </button>
                    ))}
                  </div>
                  {editForm.destination_type === 'backend' ? (
                    <input className="input-field text-[10px] py-1 font-mono" placeholder="http://svc-web:8004"
                      value={editForm.backend_url}
                      onChange={e => setEditForm({...editForm, backend_url: e.target.value})} />
                  ) : (
                    <div className="space-y-1.5">
                      <div className="flex flex-wrap gap-1">
                        {LAMBDA_TEMPLATES.map(t => (
                          <button key={t.name} type="button"
                            onClick={() => setEditForm({...editForm, function_code: t.code})}
                            className="px-1.5 py-0.5 rounded border border-jpmc-border/40 text-[9px] text-jpmc-muted hover:bg-jpmc-hover hover:text-jpmc-text transition-colors">
                            {t.name}
                          </button>
                        ))}
                      </div>
                      <textarea
                        className="w-full p-2 bg-[#0d1117] border border-jpmc-border/40 rounded-lg text-[10px] font-mono text-green-400 resize-y focus:outline-none focus:border-blue-500/50"
                        style={{ minHeight: '8rem' }}
                        spellCheck={false}
                        value={editForm.function_code}
                        onChange={e => setEditForm({...editForm, function_code: e.target.value})}
                      />
                    </div>
                  )}
                </div>
                {/* Methods */}
                <div>
                  <label className="text-[9px] text-jpmc-muted">Methods</label>
                  <div className="flex flex-wrap gap-1 mt-0.5">
                    {['GET', 'POST', 'PUT', 'DELETE', 'PATCH'].map(m => (
                      <button key={m} onClick={() => {
                        const methods = editForm.methods.includes(m)
                          ? editForm.methods.filter(x => x !== m)
                          : [...editForm.methods, m]
                        setEditForm({...editForm, methods})
                      }} className={`px-1.5 py-0.5 rounded text-[9px] border transition-all ${
                        editForm.methods.includes(m)
                          ? 'border-blue-500/50 bg-blue-500/10 text-blue-400'
                          : 'border-jpmc-border/30 text-jpmc-muted'
                      }`}>{m}</button>
                    ))}
                  </div>
                </div>
                {/* Audience + Status */}
                <div className="grid grid-cols-2 gap-2">
                  <div>
                    <label className="text-[9px] text-jpmc-muted">Audience</label>
                    <input className="input-field text-[10px] py-1" placeholder="e.g. jpmm"
                      value={editForm.audience}
                      onChange={e => setEditForm({...editForm, audience: e.target.value})} />
                  </div>
                  <div>
                    <label className="text-[9px] text-jpmc-muted">Status</label>
                    <select className="select-field text-[10px] py-1" value={editForm.status}
                      onChange={e => setEditForm({...editForm, status: e.target.value})}>
                      <option value="active">Active</option>
                      <option value="inactive">Suspended</option>
                    </select>
                  </div>
                </div>
                {/* Save/Cancel */}
                <div className="flex gap-2">
                  <button onClick={async () => {
                    const payload = {
                      path: editForm.path,
                      audience: editForm.audience,
                      status: editForm.status,
                      methods: editForm.methods,
                    }
                    if (editForm.destination_type === 'lambda') {
                      payload.function_code = editForm.function_code
                      payload.function_language = 'javascript'
                    } else {
                      payload.backend_url = editForm.backend_url
                    }
                    await fetch(`${apiUrl}/routes/${routeId}`, {
                      method: 'PUT',
                      headers: {'Content-Type': 'application/json'},
                      body: JSON.stringify(payload),
                    })
                    setEditingRoute(null)
                    queryClient.invalidateQueries({ queryKey: ['fleets'] })
                    queryClient.invalidateQueries({ queryKey: ['routes'] })
                  }} className="btn-primary text-[10px] py-1 px-3">Save</button>
                  <button onClick={() => setEditingRoute(null)}
                    className="text-[10px] py-1 px-3 rounded border border-jpmc-border/40 text-jpmc-muted hover:bg-jpmc-hover">Cancel</button>
                </div>
              </div>
            ) : (
              <div key={routeId}
                className="group/route flex items-center gap-2 text-[10px] px-1 py-0.5 rounded hover:bg-jpmc-hover/30 transition-colors">
                <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${
                  routeStatus === 'active' ? 'bg-emerald-400' : 'bg-amber-400'
                }`} />
                <code className="text-blue-400 font-mono">{route.context_path || route.path}</code>
                <span className="text-jpmc-muted">{'\u2192'}</span>
                <span className="text-jpmc-muted font-mono truncate">{(route.backend || route.backend_url || '').replace('http://', '')}</span>
                <span className={`text-[9px] ${routeStatus === 'active' ? 'text-emerald-400' : 'text-amber-400'}`}>
                  {routeStatus}
                </span>
                {/* Edit/Delete controls -- visible on hover */}
                {!readOnly && (
                  <div className="ml-auto flex items-center gap-0.5 opacity-0 group-hover/route:opacity-100 transition-opacity">
                    <button onClick={(e) => {
                      e.stopPropagation()
                      setEditingRoute(routeId)
                      const hasLambda = !!(route.function_code || route.lambda_container_id)
                      setEditForm({
                        path: route.context_path || route.path || '',
                        destination_type: hasLambda ? 'lambda' : 'backend',
                        backend_url: route.backend || route.backend_url || '',
                        function_code: route.function_code || DEFAULT_LAMBDA_CODE,
                        audience: route.audience || '',
                        status: route.status || 'active',
                        methods: route.methods || ['GET', 'POST', 'PUT', 'DELETE'],
                      })
                    }} className="p-1 rounded hover:bg-blue-500/10 text-jpmc-muted hover:text-blue-400" title="Edit route">
                      <Edit3 size={10} />
                    </button>
                    <button onClick={async (e) => {
                      e.stopPropagation()
                      if (!confirm('Delete this route?')) return
                      // Delete the actual route (cleans up route + assignments + fleet instances by route_id)
                      if (routeId) {
                        await fetch(`${apiUrl}/routes/${routeId}`, { method: 'DELETE' }).catch(() => {})
                      }
                      // Also delete the fleet instance by its own ID (handles orphans with empty route_id)
                      if (instId && instId !== routeId) {
                        await fetch(`${apiUrl}/fleets/${fleetId}/instances/${instId}`, { method: 'DELETE' }).catch(() => {})
                      }
                      // Always refresh
                      queryClient.invalidateQueries({ queryKey: ['fleets'] })
                      queryClient.invalidateQueries({ queryKey: ['routes'] })
                      queryClient.invalidateQueries({ queryKey: ['fleetNodes'] })
                    }} className="p-1 rounded hover:bg-red-500/10 text-jpmc-muted hover:text-red-400" title="Delete route">
                      <Trash2 size={10} />
                    </button>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
      {routes.length === 0 && !readOnly && (
        <div className="mx-3.5 mb-2.5 pt-2 border-t border-jpmc-border/20">
          <span className="text-[9px] text-jpmc-muted italic">No routes assigned</span>
        </div>
      )}
    </motion.div>
  )
}

/* Animated health pulse that travels along a connection line */
function AnimatedHealthLine({ status = 'healthy', vertical = false }) {
  // Not deployed = static gray dashed line, no animation
  if (status === 'not_deployed') {
    if (vertical) {
      return <div className="w-px h-6 border-l border-dashed border-slate-600/40 mx-auto" />
    }
    return (
      <div className="relative w-8 shrink-0" style={{ height: '8px' }}>
        <div className="absolute top-1/2 left-0 right-0 border-t border-dashed border-slate-600/40" />
      </div>
    )
  }

  const color = status === 'healthy' ? 'bg-emerald-400' : status === 'degraded' ? 'bg-amber-400' : 'bg-red-400'
  const glow = status === 'healthy' ? 'shadow-[0_0_6px_rgba(52,211,153,0.6)]' : status === 'degraded' ? 'shadow-[0_0_6px_rgba(251,191,36,0.6)]' : 'shadow-[0_0_6px_rgba(248,113,113,0.6)]'
  const borderColor = status === 'healthy' ? 'border-emerald-500/30' : status === 'degraded' ? 'border-amber-500/30' : 'border-red-500/30'

  if (vertical) {
    return (
      <div className={`relative w-px h-6 border-l border-dashed ${borderColor} mx-auto overflow-hidden`}>
        <motion.div
          className={`absolute left-[-1.5px] w-[4px] h-[4px] rounded-full ${color} ${glow}`}
          animate={{ top: ['-4px', '28px'] }}
          transition={{ duration: 2.4, repeat: Infinity, ease: 'linear' }}
        />
      </div>
    )
  }

  return (
    <div className={`relative w-8 shrink-0`} style={{ height: '8px' }}>
      <div className={`absolute top-1/2 left-0 right-0 border-t border-dashed ${borderColor}`} />
      <motion.div
        className={`absolute w-[4px] h-[4px] rounded-full ${color} ${glow}`}
        style={{ top: 'calc(50% - 2px)' }}
        animate={{ left: ['-4px', '36px'] }}
        transition={{ duration: 2.0, repeat: Infinity, ease: 'linear' }}
      />
    </div>
  )
}

function instHealthStatus(inst) {
  if (inst.status === 'active') return 'healthy'
  if (inst.status === 'suspended' || inst.status === 'offline') return 'degraded'
  return 'degraded'
}

function GatewayBranch({ routes, nodes = [] }) {
  if (routes.length === 0 && nodes.length === 0) return null
  const anyNodeRunning = nodes.some(n => (n.status || 'running') === 'running')
  const hasNodes = nodes.length > 0
  return (
    <>
      {/* Nodes with their routes shown inline */}
      {hasNodes && (
        <div className="flex flex-col gap-3">
          {nodes.map(node => {
            const nodeRoutes = getRoutesForNode(node, routes)
            return (
              <div key={node.id || node.name} className="flex items-center gap-1">
                <AnimatedHealthLine status={(node.status || 'running') === 'running' ? 'healthy' : 'degraded'} />
                <NodeTopoCard node={node} />
                {nodeRoutes.length > 0 && (
                  <div className="flex flex-col gap-0.5">
                    {nodeRoutes.map(inst => {
                      const nodeDown = node.status && node.status !== 'running'
                      const effectiveStatus = nodeDown ? 'degraded' : instHealthStatus(inst)
                      return (
                        <div key={inst.id} className="flex items-center gap-1">
                          <AnimatedHealthLine status={effectiveStatus} />
                          <InstancePill inst={inst} nodeDown={nodeDown} />
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
      {/* Unattached routes (no nodes) */}
      {!hasNodes && routes.length > 0 && (
        <div className="flex flex-col gap-1">
          {routes.map(inst => (
            <div key={inst.id} className="flex items-center gap-1">
              <AnimatedHealthLine status="not_deployed" />
              <InstancePill inst={inst} />
            </div>
          ))}
        </div>
      )}
    </>
  )
}

function NodeTopoCard({ node }) {
  const isRunning = (node.status || 'running') === 'running'
  const sc = nodeStatusColor(node.status || 'running')
  return (
    <div className={`flex items-center gap-1.5 px-2 py-1 rounded border text-[10px] ${sc.border} ${sc.bg}`}>
      <span className={`w-1.5 h-1.5 rounded-full ${sc.dot}`} />
      <code className="text-jpmc-text">{node.name?.replace(/^fleet-/, '') || 'node'}</code>
      <span className={`text-[8px] px-1 py-0 rounded ${
        (node.gateway_type || 'envoy') === 'kong'
          ? 'bg-blue-500/10 text-blue-400'
          : 'bg-purple-500/10 text-purple-400'
      }`}>{node.gateway_type || 'envoy'}</span>
      {node.port && <span className="text-jpmc-muted font-mono">:{node.port}</span>}
    </div>
  )
}

function FleetTopology({ fleet, nodes = [] }) {
  const instances = fleet.instances || []

  const envoyNodes = nodes.filter(n => (n.gateway_type || 'envoy') === 'envoy')
  const kongNodes = nodes.filter(n => (n.gateway_type || 'envoy') === 'kong')

  const kongRoutes = instances.filter(i => i.gateway_type === 'kong' || (!i.gateway_type && i.context_path.startsWith('/api')))
  const envoyRoutes = instances.filter(i => i.gateway_type === 'envoy' || (!i.gateway_type && !i.context_path.startsWith('/api')))

  const hasEnvoyBranch = envoyNodes.length > 0 || envoyRoutes.length > 0
  const hasKongBranch = kongNodes.length > 0 || kongRoutes.length > 0
  const hasBoth = hasEnvoyBranch && hasKongBranch

  const anyRouteActive = instances.some(i => i.status === 'active')
  const anyNodeRunning = nodes.some(n => (n.status || 'running') === 'running')
  const hasNodes = nodes.length > 0
  const infraStatus = !hasNodes ? 'not_deployed' : (anyRouteActive || anyNodeRunning) ? 'healthy' : fleet.status === 'offline' ? 'degraded' : 'healthy'
  const isAws = fleet.host_env === 'aws'

  return (
    <div className="overflow-x-auto py-4">
      <div className="flex items-start gap-1 min-w-fit">
        <div className="flex items-center gap-1 shrink-0" style={{ alignSelf: hasBoth ? 'center' : 'flex-start' }}>
          <TopoNode icon={Globe} label={fleet.subdomain.split('.')[0]} desc="DNS entry" color="blue" />
          <AnimatedHealthLine status={infraStatus} />
          <TopoNode icon={Shield} label="CDN / WAF" desc="Akamai Edge" color="cyan" />
          <AnimatedHealthLine status={infraStatus} />
          {isAws
            ? <TopoNode icon={Cloud} label="AWS WAF" desc="Cloud perimeter" color="orange" />
            : <TopoNode icon={Layers} label="PSaaS+" desc="On-prem perimeter" color="indigo" />
          }
        </div>

        {hasNodes ? (
          <div className="flex flex-col gap-3">
            {hasEnvoyBranch && envoyNodes.length > 0 && (
              <GatewayBranch routes={envoyRoutes} nodes={envoyNodes} />
            )}
            {hasKongBranch && kongNodes.length > 0 && (
              <GatewayBranch routes={kongRoutes} nodes={kongNodes} />
            )}
          </div>
        ) : instances.length > 0 ? (
          <div className="flex items-center gap-1">
            <AnimatedHealthLine status="not_deployed" />
            <div className="px-3 py-2 rounded-lg border border-dashed border-slate-600/40 text-[10px] text-slate-500">
              No gateway nodes deployed -- {instances.length} route{instances.length !== 1 ? 's' : ''} waiting
            </div>
          </div>
        ) : (
          <div className="flex items-center gap-1">
            <AnimatedHealthLine status="not_deployed" />
            <div className="px-3 py-2 rounded-lg border border-dashed border-slate-600/40 text-[10px] text-slate-500">
              No routes or nodes deployed
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function TopoNode({ icon: Icon, label, desc, color }) {
  const colors = {
    blue: 'bg-blue-500/15 border-blue-500/30 text-blue-400',
    cyan: 'bg-cyan-500/15 border-cyan-500/30 text-cyan-400',
    indigo: 'bg-indigo-500/15 border-indigo-500/30 text-indigo-400',
    orange: 'bg-orange-500/15 border-orange-500/30 text-orange-400',
    purple: 'bg-purple-500/15 border-purple-500/30 text-purple-400',
    violet: 'bg-violet-500/15 border-violet-500/30 text-violet-400',
  }
  return (
    <div className="flex flex-col items-center gap-1 min-w-[64px]">
      <div className={`w-10 h-10 rounded-lg border flex items-center justify-center ${colors[color]}`}>
        <Icon size={16} />
      </div>
      <span className="text-[10px] font-medium text-jpmc-text capitalize leading-tight text-center">{label}</span>
      <span className="text-[9px] text-jpmc-muted leading-tight text-center">{desc}</span>
    </div>
  )
}

/* ========== Route Card inside Deploy Node panel ========== */
function DeployRouteCard({ route, index, nodeType, onUpdate, onRemove }) {
  const [showGwConfig, setShowGwConfig] = useState(false)
  const isEnvoy = nodeType === 'envoy'

  const update = (field, value) => onUpdate(index, { ...route, [field]: value })

  return (
    <div className="p-3 rounded-lg bg-jpmc-navy/50 border border-jpmc-border/30 space-y-3 relative">
      <button onClick={() => onRemove(index)}
        className="absolute top-2 right-2 p-1 rounded hover:bg-red-500/10 text-jpmc-muted hover:text-red-400 transition-colors" title="Remove route">
        <X size={13} />
      </button>

      <div className="flex items-center gap-2 pr-6">
        <span className="text-[10px] text-jpmc-muted font-medium">Route {index + 1}</span>
      </div>

      {/* Context Path */}
      <div>
        <label className="text-[9px] text-jpmc-muted">Context Path</label>
        <input className="input-field text-xs font-mono" placeholder="/research" value={route.context_path}
          onChange={e => update('context_path', e.target.value)} />
      </div>

      {/* Destination toggle */}
      <div>
        <label className="text-[9px] text-jpmc-muted mb-1.5 block">Destination</label>
        <div className="flex items-center gap-2 mb-2">
          {[
            { key: 'backend', label: 'Backend URL' },
            { key: 'lambda', label: 'Lambda Function' },
          ].map(dt => (
            <button key={dt.key}
              onClick={() => update('destination_type', dt.key)}
              className={`px-2.5 py-1 rounded-lg text-[10px] border transition-all ${
                route.destination_type === dt.key
                  ? 'border-blue-500/50 bg-blue-500/10 text-blue-400'
                  : 'border-jpmc-border/30 text-jpmc-muted hover:border-jpmc-border/60'
              }`}>
              {dt.label}
            </button>
          ))}
        </div>

        {route.destination_type === 'backend' ? (
          <input className="input-field text-xs font-mono" placeholder="http://svc-web:8004"
            value={route.backend_url} onChange={e => update('backend_url', e.target.value)} />
        ) : (
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <span className="badge badge-blue text-[9px]">JavaScript (Node.js 20)</span>
            </div>
            <div className="flex flex-wrap gap-1">
              {LAMBDA_TEMPLATES.map(t => (
                <button key={t.name} type="button"
                  onClick={() => update('function_code', t.code)}
                  className="px-2 py-0.5 rounded border border-jpmc-border/40 text-[9px] text-jpmc-muted hover:bg-jpmc-hover hover:text-jpmc-text transition-colors">
                  {t.name}
                </button>
              ))}
            </div>
            <textarea
              className="w-full p-2.5 bg-[#0d1117] border border-jpmc-border/40 rounded-lg text-xs font-mono text-green-400 resize-y focus:outline-none focus:border-blue-500/50"
              style={{ minHeight: '12rem' }}
              spellCheck={false}
              value={route.function_code}
              onChange={e => update('function_code', e.target.value)}
              placeholder="module.exports = async (req, res) => { ... }"
            />
            <p className="text-[9px] text-jpmc-muted">
              A container will be created running your function. The route backend will point to this container automatically.
            </p>
          </div>
        )}
      </div>

      {/* HTTP Methods */}
      <div>
        <label className="text-[9px] text-jpmc-muted">HTTP Methods</label>
        <div className="flex flex-wrap gap-1.5 mt-1">
          {['GET', 'POST', 'PUT', 'DELETE', 'PATCH'].map(m => (
            <button key={m} type="button"
              onClick={() => {
                const methods = route.methods.includes(m)
                  ? route.methods.filter(x => x !== m)
                  : [...route.methods, m]
                update('methods', methods)
              }}
              className={`px-2 py-0.5 rounded text-[10px] border transition-all ${
                route.methods.includes(m)
                  ? 'border-blue-500/50 bg-blue-500/10 text-blue-400'
                  : 'border-jpmc-border/30 text-jpmc-muted hover:border-jpmc-border/60'
              }`}>
              {m}
            </button>
          ))}
        </div>
      </div>

      {/* Audience */}
      <div>
        <label className="text-[9px] text-jpmc-muted">Audience</label>
        <input className="input-field text-xs" placeholder="e.g. jpmm, execute, access"
          value={route.audience}
          onChange={e => update('audience', e.target.value)} />
        <p className="text-[8px] text-jpmc-muted mt-0.5">JWT aud claim. Leave empty for unauthenticated.</p>
      </div>

      {/* Gateway-specific config */}
      <div>
        <button onClick={() => setShowGwConfig(!showGwConfig)}
          className="flex items-center gap-1.5 text-[10px] text-jpmc-muted hover:text-jpmc-text transition-colors">
          {showGwConfig ? <ChevronDown size={10} /> : <ChevronRight size={10} />}
          {isEnvoy ? 'Envoy Config' : 'Kong Config'}
        </button>
        {showGwConfig && isEnvoy && (
          <div className="mt-2 space-y-2 p-2 rounded-lg bg-jpmc-navy/30 border border-jpmc-border/20">
            <div className="grid grid-cols-2 gap-2">
              <div>
                <label className="text-[9px] text-jpmc-muted">Timeout (ms)</label>
                <input type="number" className="input-field text-[10px] py-1" value={route.timeout_ms}
                  onChange={e => update('timeout_ms', parseInt(e.target.value) || 30000)} />
              </div>
              <div>
                <label className="text-[9px] text-jpmc-muted">Retry Count</label>
                <input type="number" className="input-field text-[10px] py-1" value={route.retry_count} min={0} max={10}
                  onChange={e => update('retry_count', parseInt(e.target.value) || 0)} />
              </div>
            </div>
            <div>
              <label className="text-[9px] text-jpmc-muted">Retry Policy</label>
              <select className="select-field text-[10px] py-1" value={route.retry_on}
                onChange={e => update('retry_on', e.target.value)}>
                <option value="5xx">5xx errors</option>
                <option value="gateway-error">Gateway errors</option>
                <option value="reset">Connection reset</option>
                <option value="retriable-4xx">Retriable 4xx</option>
              </select>
            </div>
            <div>
              <label className="text-[9px] text-jpmc-muted">Rate Limit (req/s)</label>
              <input type="number" className="input-field text-[10px] py-1" value={route.rate_limit_rps} min={0}
                onChange={e => update('rate_limit_rps', parseInt(e.target.value) || 0)} />
              <p className="text-[8px] text-jpmc-muted mt-0.5">0 = unlimited</p>
            </div>
            <label className="flex items-center gap-2 cursor-pointer">
              <input type="checkbox" checked={route.cors_enabled}
                onChange={e => update('cors_enabled', e.target.checked)}
                className="w-3 h-3 rounded border-jpmc-border" />
              <span className="text-[10px] text-jpmc-text">Enable CORS</span>
            </label>
          </div>
        )}
        {showGwConfig && !isEnvoy && (
          <div className="mt-2 space-y-2 p-2 rounded-lg bg-jpmc-navy/30 border border-jpmc-border/20">
            <div className="grid grid-cols-2 gap-2">
              <div>
                <label className="text-[9px] text-jpmc-muted">Rate Limit (req/s)</label>
                <input type="number" className="input-field text-[10px] py-1" value={route.kong_rate_limit_rps} min={0}
                  onChange={e => update('kong_rate_limit_rps', parseInt(e.target.value) || 0)} />
                <p className="text-[8px] text-jpmc-muted mt-0.5">0 = unlimited</p>
              </div>
              <div>
                <label className="text-[9px] text-jpmc-muted">Strip Path</label>
                <select className="select-field text-[10px] py-1" value={route.strip_path ? 'true' : 'false'}
                  onChange={e => update('strip_path', e.target.value === 'true')}>
                  <option value="false">Keep path prefix</option>
                  <option value="true">Strip path prefix</option>
                </select>
              </div>
            </div>
            <div>
              <label className="text-[9px] text-jpmc-muted">Kong Plugins</label>
              <div className="flex flex-wrap gap-1 mt-1">
                {['rate-limiting', 'cors', 'jwt', 'key-auth', 'ip-restriction', 'request-transformer', 'response-transformer', 'acl'].map(p => (
                  <label key={p} className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded border cursor-pointer text-[9px] transition-all ${
                    route.plugins.includes(p)
                      ? 'border-blue-500/50 bg-blue-500/10 text-blue-400'
                      : 'border-jpmc-border/30 text-jpmc-muted hover:border-jpmc-border/60'
                  }`}>
                    <input type="checkbox" className="sr-only"
                      checked={route.plugins.includes(p)}
                      onChange={e => {
                        const plugins = e.target.checked ? [...route.plugins, p] : route.plugins.filter(x => x !== p)
                        update('plugins', plugins)
                      }} />
                    {p}
                  </label>
                ))}
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

export default function Fleets() {
  const { API_URL } = useConfig()
  const queryClient = useQueryClient()
  const [expandedFleet, setExpandedFleet] = useState(null)
  const [expandedInstance, setExpandedInstance] = useState(null)
  const [searchTerm, setSearchTerm] = useState('')
  const [showDeployNodeInline, setShowDeployNodeInline] = useState(false)
  const [deployingRegion, setDeployingRegion] = useState(null)
  const [deployNodeType, setDeployNodeType] = useState('envoy')
  const [deployNodeDc, setDeployNodeDc] = useState('us-east-1')

  // Fleet-level add route
  const [showAddFleetRoute, setShowAddFleetRoute] = useState(null) // null, 'envoy', or 'kong'
  const makeEmptyFleetRouteForm = (gwType = null) => ({
    context_path: '', destination_type: 'backend',
    backend_url: gwType === 'kong' ? 'http://svc-api:8005' : 'http://svc-web:8004',
    function_code: DEFAULT_LAMBDA_CODE, audience: '',
    methods: ['GET', 'POST', 'PUT', 'DELETE'],
    // Envoy-specific
    timeout_ms: 30000,
    retry_count: 3,
    retry_on: ['5xx', 'gateway-error', 'connect-failure'],
    cors_enabled: false,
    cors_origins: '*',
    rate_limit_rps: 0,
    priority: 0,
    // Kong-specific
    strip_path: false,
    kong_rate_limit_rps: 0,
    kong_rate_limit_rpm: 0,
    kong_rate_limit_rph: 0,
    upstream_connect_timeout_ms: 60000,
    upstream_read_timeout_ms: 60000,
    upstream_write_timeout_ms: 60000,
    plugins: [],
  })
  const [fleetRouteForm, setFleetRouteForm] = useState(makeEmptyFleetRouteForm())
  const [fleetRouteError, setFleetRouteError] = useState('')
  const [fleetRouteSuccess, setFleetRouteSuccess] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')
  const [gatewayFilter, setGatewayFilter] = useState('all')
  const [sortBy, setSortBy] = useState('lob')

  // ========== New Fleet slide-over ==========
  const [showNewFleet, setShowNewFleet] = useState(false)
  const [isFleetSubmitting, setIsFleetSubmitting] = useState(false)
  const defaultFleetForm = {
    name: '', lob: 'Markets', portal: '', description: '',
    hostEnv: 'psaas', authProvider: 'Janus',
    gatewayType: 'envoy', trafficType: 'web',
    regions: ['us-east-1'],
    resourceProfile: 'medium', containerCount: 2,
    autoscaleEnabled: false, autoscaleMin: 2, autoscaleMax: 16, autoscaleCpuThreshold: 70,
  }
  const [newFleetForm, setNewFleetForm] = useState({ ...defaultFleetForm })
  const [newFleetSuccess, setNewFleetSuccess] = useState(false)
  const [editingFleet, setEditingFleet] = useState(null) // null = create mode, fleet object = edit mode
  const [fleetFormSections, setFleetFormSections] = useState({ gateway: false, regions: false, scaling: false })
  const newFleetFqdn = newFleetForm.portal ? `${newFleetForm.portal}.jpm.com` : ''

  const AVAILABLE_REGIONS = ['us-east-1', 'us-east-2', 'eu-west-1', 'ap-southeast-1']

  const openEditFleet = (fleet) => {
    let fleetRegions = ['us-east-1']
    try { if (fleet.regions) fleetRegions = JSON.parse(fleet.regions) } catch {}
    setEditingFleet(fleet)
    setNewFleetForm({
      name: fleet.name || '', lob: fleet.lob || 'Markets',
      portal: (fleet.subdomain || '').replace('.jpm.com', ''),
      description: fleet.description || '', hostEnv: fleet.host_env || 'psaas',
      authProvider: fleet.auth_provider || 'Janus',
      gatewayType: fleet.gateway_type || 'envoy', trafficType: fleet.traffic_type || 'web',
      regions: fleetRegions,
      resourceProfile: fleet.resource_profile || 'medium', containerCount: 2,
      autoscaleEnabled: fleet.autoscale_enabled || false,
      autoscaleMin: fleet.autoscale_min || 2, autoscaleMax: fleet.autoscale_max || 16,
      autoscaleCpuThreshold: fleet.autoscale_cpu_threshold || 70,
    })
    setShowNewFleet(true)
  }

  const handleCreateOrUpdateFleet = async () => {
    if (isFleetSubmitting) return          // guard against double-click / slow network
    setIsFleetSubmitting(true)
    try {
      const isEdit = !!editingFleet
      const payload = {
        name: newFleetForm.name,
        subdomain: newFleetFqdn,
        lob: newFleetForm.lob,
        description: newFleetForm.description,
        host_env: newFleetForm.hostEnv,
        auth_provider: newFleetForm.authProvider,
        gateway_type: newFleetForm.gatewayType,
        traffic_type: newFleetForm.trafficType,
        regions: newFleetForm.regions,
        resource_profile: newFleetForm.resourceProfile,
        container_count: newFleetForm.containerCount,
        autoscale_enabled: newFleetForm.autoscaleEnabled,
        autoscale_min: newFleetForm.autoscaleMin,
        autoscale_max: newFleetForm.autoscaleMax,
        autoscale_cpu_threshold: newFleetForm.autoscaleCpuThreshold,
      }
      const resp = await fetch(
        isEdit ? `${API_URL}/fleets/${editingFleet.id}` : `${API_URL}/fleets`,
        {
          method: isEdit ? 'PUT' : 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        }
      )
      if (!resp.ok) return
      setNewFleetSuccess(true)
      queryClient.invalidateQueries({ queryKey: ['fleets'] })
      setTimeout(() => {
        setNewFleetSuccess(false)
        setShowNewFleet(false)
        setEditingFleet(null)
        setNewFleetForm({ ...defaultFleetForm })
        setIsFleetSubmitting(false)
      }, 2500)
    } catch {
      setIsFleetSubmitting(false)
    }
  }
  // Backwards compat alias
  const handleCreateFleet = handleCreateOrUpdateFleet

  // ========== Deploy Node slide-over ==========
  const [showDeployNode, setShowDeployNode] = useState(false)
  const [deployNodeForm, setDeployNodeForm] = useState({
    fleetId: '', nodeName: '', nodeType: 'envoy', healthCheckPath: '/health', datacenter: 'us-east-1',
  })
  const [deployNodeRoutes, setDeployNodeRoutes] = useState([makeEmptyRoute()])
  const [routesSectionOpen, setRoutesSectionOpen] = useState(true)
  const [showCopyFromExisting, setShowCopyFromExisting] = useState(false)
  const [deployNodeSuccess, setDeployNodeSuccess] = useState(false)

  const updateDeployRoute = (index, updatedRoute) => {
    setDeployNodeRoutes(prev => prev.map((r, i) => i === index ? updatedRoute : r))
  }
  const removeDeployRoute = (index) => {
    setDeployNodeRoutes(prev => prev.filter((_, i) => i !== index))
  }
  const addDeployRoute = () => {
    setDeployNodeRoutes(prev => [...prev, makeEmptyRoute()])
  }

  const [isDeployingNode, setIsDeployingNode] = useState(false)
  const handleDeployNode = async () => {
    if (!deployNodeForm.fleetId || deployNodeRoutes.length === 0 || isDeployingNode) return
    setIsDeployingNode(true)
    try {
      // 1. Deploy the node
      const nodePayload = {
        gateway_type: deployNodeForm.nodeType,
        datacenter: deployNodeForm.datacenter,
        health_check_path: deployNodeForm.healthCheckPath,
      }
      if (deployNodeForm.nodeName.trim()) {
        nodePayload.name = deployNodeForm.nodeName.trim()
      }
      const nodeResp = await fetch(`${API_URL}/fleets/${deployNodeForm.fleetId}/nodes/deploy`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(nodePayload),
      })
      if (!nodeResp.ok) return
      const nodeData = await nodeResp.json()
      const newNodeId = nodeData.container_id || nodeData.node?.container_id || nodeData.id

      // 2. Deploy each route targeting the new node
      for (const route of deployNodeRoutes) {
        const payload = {
          context_path: route.context_path,
          gateway_type: deployNodeForm.nodeType,
          audience: route.audience,
          methods: route.methods,
          target_nodes: newNodeId ? [newNodeId] : [],
        }
        if (route.destination_type === 'lambda') {
          payload.function_enabled = true
          payload.function_code = route.function_code
          payload.function_language = 'javascript'
        } else {
          payload.backend_url = route.backend_url
        }
        await fetch(`${API_URL}/fleets/${deployNodeForm.fleetId}/deploy`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        })
      }

      setDeployNodeSuccess(true)
      queryClient.invalidateQueries({ queryKey: ['fleets'] })
      queryClient.invalidateQueries({ queryKey: ['routes'] })
      queryClient.invalidateQueries({ queryKey: ['fleetNodes'] })
      setTimeout(() => {
        setDeployNodeSuccess(false)
        setShowDeployNode(false)
        setIsDeployingNode(false)
        setDeployNodeForm({ fleetId: '', nodeName: '', nodeType: 'envoy', healthCheckPath: '/health', datacenter: 'us-east-1' })
        setDeployNodeRoutes([makeEmptyRoute()])
      }, 2500)
    } catch {
      setIsDeployingNode(false)
    }
  }

  // ========== Queries ==========
  const { data: fleets = [] } = useQuery({
    queryKey: ['fleets'],
    queryFn: () => fetch(`${API_URL}/fleets`).then(r => r.json()).catch(() => []),
  })

  const { data: routes = [] } = useQuery({
    queryKey: ['routes'],
    queryFn: () => fetch(`${API_URL}/routes`).then(r => r.json()).catch(() => []),
  })

  const { data: actuals = [] } = useQuery({
    queryKey: ['actuals'],
    queryFn: () => fetch(`${API_URL}/actuals`).then(r => r.json()).catch(() => []),
  })

  const { data: auditLog = [] } = useQuery({
    queryKey: ['audit-log'],
    queryFn: () => fetch(`${API_URL}/audit-log`).then(r => r.json()).catch(() => []),
  })

  // Fetch nodes for the currently expanded fleet
  const { data: fleetNodes = {} } = useQuery({
    queryKey: ['fleetNodes', expandedFleet],
    queryFn: () => expandedFleet
      ? fetch(`${API_URL}/fleets/${expandedFleet}/nodes`).then(r => r.json()).catch(() => [])
      : Promise.resolve([]),
    enabled: !!expandedFleet,
    refetchInterval: 5000,
  })

  // Normalize: API might return an array or { nodes: [...] }
  const currentFleetNodes = useMemo(() => {
    if (!fleetNodes) return []
    if (Array.isArray(fleetNodes)) return fleetNodes
    if (Array.isArray(fleetNodes.nodes)) return fleetNodes.nodes
    return []
  }, [fleetNodes])

  // Scale fleet mutation
  const [scaleCount, setScaleCount] = useState(null)
  const scaleMutation = useMutation({
    mutationFn: ({ fleetId, count }) =>
      fetch(`${API_URL}/fleets/${fleetId}/scale`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ count }),
      }).then(r => r.json()),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['fleets'] })
      queryClient.invalidateQueries({ queryKey: ['fleetNodes', expandedFleet] })
    },
  })

  const findRouteForInstance = (inst, fleet) => {
    return routes.find(r =>
      r.path === inst.context_path &&
      r.hostname === fleet.subdomain
    )
  }

  const getDriftStatus = (routeId) => {
    const actual = actuals.find(a => a.route_id === routeId)
    if (!actual) return 'unknown'
    if (actual.drift) return 'drifted'
    return 'in sync'
  }

  const toggleRouteStatus = async (route) => {
    if (!route) return
    const newStatus = route.status === 'active' ? 'inactive' : 'active'
    try {
      await fetch(`${API_URL}/routes/${route.id}/status`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ status: newStatus }),
      })
      queryClient.invalidateQueries({ queryKey: ['routes'] })
      queryClient.invalidateQueries({ queryKey: ['fleets'] })
      queryClient.invalidateQueries({ queryKey: ['actuals'] })
    } catch {}
  }

  const dataPlaneFleets = fleets.filter(f => f.fleet_type !== 'control')
  const controlPlaneFleets = fleets.filter(f => f.fleet_type === 'control')
  const totalInstances = dataPlaneFleets.reduce((sum, f) => sum + (f.instances || []).length, 0)
  const healthyFleets = dataPlaneFleets.filter(f => f.status === 'healthy').length

  // Build "copy from existing" data: all routes grouped by fleet
  const existingRoutesByFleet = useMemo(() => {
    const groups = {}
    for (const fleet of dataPlaneFleets) {
      const insts = fleet.instances || []
      if (insts.length > 0) {
        groups[fleet.id] = { name: fleet.name, subdomain: fleet.subdomain, routes: insts }
      }
    }
    return groups
  }, [dataPlaneFleets])

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-white">Fleet Management</h1>
          <p className="text-sm text-jpmc-muted">Logical gateway groups serving subdomains -- each fleet can have both Envoy and Kong nodes</p>
        </div>
        <div className="flex items-center gap-2">
          <button onClick={() => { setShowNewFleet(true); setShowDeployNode(false) }}
            className="flex items-center gap-2 px-3 py-2 rounded-lg border border-jpmc-border/50 bg-jpmc-navy/50 text-sm text-jpmc-text hover:border-blue-500/30 hover:bg-blue-500/5 transition-all">
            <Globe size={14} />
            New Fleet
          </button>
          <button onClick={() => { setShowDeployNode(!showDeployNode); setShowNewFleet(false) }} className="btn-primary flex items-center gap-2">
            {showDeployNode ? <X size={14} /> : <Plus size={14} />}
            {showDeployNode ? 'Cancel' : 'Deploy Node'}
          </button>
        </div>
      </div>

      {/* Production Architecture Banner */}
      <motion.div
        initial={{ opacity: 0, y: -5 }}
        animate={{ opacity: 1, y: 0 }}
        className="p-4 rounded-xl bg-gradient-to-r from-blue-500/5 to-indigo-500/5 border border-blue-500/20"
      >
        <div className="flex items-start gap-3">
          <div className="p-2 rounded-lg bg-blue-500/10 shrink-0 mt-0.5">
            <Cloud size={16} className="text-blue-400" />
          </div>
          <div>
            <div className="text-sm font-medium text-blue-300 mb-1">Production Architecture</div>
            <p className="text-xs text-jpmc-muted leading-relaxed">
              Each fleet maps to a <span className="text-jpmc-text">Kubernetes Deployment</span> with
              horizontally-scaled gateway pods behind an internal <span className="text-jpmc-text">ClusterIP Service</span>.
              A fleet can host both <span className="text-purple-400">Envoy (web)</span> and <span className="text-blue-400">Kong (API)</span> nodes.
              Routes are pushed to compatible nodes via the xDS control plane (Envoy) or declarative config sync (Kong).
              Fleet changes are committed to <span className="text-jpmc-text">GitHub</span> and
              reconciled by <span className="text-jpmc-text">Argo CD</span> to the data-plane cluster.
            </p>
          </div>
        </div>
      </motion.div>

      {/* Stats */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <GlassCard title="Data Plane Fleets" icon={Server} value={dataPlaneFleets.length} subtitle={`${healthyFleets} healthy + ${controlPlaneFleets.length} CP services`} delay={0} />
        <GlassCard title="Route Instances" icon={Cpu} value={totalInstances} subtitle="Across all fleets" delay={0.05} />
        <GlassCard
          title="Fleet Health"
          icon={Activity}
          value={dataPlaneFleets.length > 0 ? `${Math.round((healthyFleets / dataPlaneFleets.length) * 100)}%` : '--'}
          subtitle={dataPlaneFleets.length === healthyFleets ? 'All fleets healthy' : 'Some fleets degraded'}
          delay={0.1}
        />
      </div>

      {/* ========== Deploy Node Slide-over ========== */}
      {createPortal(
      <AnimatePresence>
        {showDeployNode && (
          <>
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              className="fixed inset-0 bg-black/40 z-[9998]"
              onClick={() => setShowDeployNode(false)}
            />
            <motion.div
              initial={{ x: '100%' }}
              animate={{ x: 0 }}
              exit={{ x: '100%' }}
              transition={{ type: 'spring', damping: 30, stiffness: 300 }}
              className="fixed top-0 right-0 bottom-0 w-full max-w-lg bg-jpmc-dark border-l border-jpmc-border z-[9999] flex flex-col overflow-hidden"
            >
              {/* Header */}
              <div className="p-6 pb-4 shrink-0">
                <div className="flex items-center justify-between mb-2">
                  <h2 className="text-lg font-bold text-white">Deploy Node</h2>
                  <button onClick={() => setShowDeployNode(false)} className="p-1.5 rounded-md hover:bg-jpmc-hover text-jpmc-muted">
                    <X size={18} />
                  </button>
                </div>
              </div>

              {/* Success overlay */}
              <AnimatePresence>
                {deployNodeSuccess && (
                  <motion.div
                    initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
                    className="absolute inset-0 z-10 flex items-center justify-center bg-jpmc-dark/90"
                  >
                    <motion.div initial={{ scale: 0.8 }} animate={{ scale: 1 }} className="text-center">
                      <motion.div
                        initial={{ scale: 0 }} animate={{ scale: 1 }}
                        transition={{ type: 'spring', damping: 10, stiffness: 200 }}
                        className="w-20 h-20 rounded-full bg-emerald-500/20 border-2 border-emerald-500 flex items-center justify-center mx-auto mb-4"
                      >
                        <Check size={36} className="text-emerald-400" />
                      </motion.div>
                      <div className="text-lg font-bold text-emerald-300">Node Deployed Successfully</div>
                      <div className="text-sm text-emerald-400/70 mt-1">Routes will be live within 5 seconds</div>
                    </motion.div>
                  </motion.div>
                )}
              </AnimatePresence>

              {/* Scrollable content */}
              <div className="flex-1 min-h-0 overflow-y-auto px-6 pb-6">
                {/* GitOps notice */}
                <div className="p-3 rounded-lg bg-blue-500/10 border border-blue-500/30 mb-4">
                  <div className="flex items-center gap-2 text-blue-400 text-xs font-medium mb-1">
                    <GitBranch size={12} />
                    GitOps Deployment
                  </div>
                  <p className="text-[11px] text-blue-300/70">
                    This deploy commits a Fleet CRD manifest to the fleet's GitHub repo and applies it
                    directly to the data-plane cluster. Argo CD reconciles the desired state continuously.
                  </p>
                </div>

                {/* Section 1: Node Config */}
                <div className="space-y-4 mb-6">
                  <div className="text-xs font-bold text-jpmc-muted uppercase tracking-wider">Node Configuration</div>

                  {/* Target Fleet */}
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Target Fleet</label>
                    <select className="select-field" value={deployNodeForm.fleetId}
                      onChange={e => setDeployNodeForm({ ...deployNodeForm, fleetId: e.target.value })}>
                      <option value="">Select a fleet...</option>
                      {dataPlaneFleets.map(f => (
                        <option key={f.id} value={f.id}>
                          {f.name} ({f.subdomain})
                        </option>
                      ))}
                    </select>
                  </div>

                  {/* Node Name */}
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Node Name</label>
                    <input className="input-field" placeholder={`e.g. ${deployNodeForm.fleetId || 'fleet'}-${deployNodeForm.nodeType}-prod-1`}
                      value={deployNodeForm.nodeName}
                      onChange={e => setDeployNodeForm({ ...deployNodeForm, nodeName: e.target.value })} />
                    <p className="text-[10px] text-jpmc-muted mt-1">
                      {deployNodeForm.nodeName
                        ? `Container: ${deployNodeForm.nodeName}`
                        : 'Leave blank for auto-generated name'}
                    </p>
                  </div>

                  {/* Node Type */}
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-2">Node Type</label>
                    <div className="grid grid-cols-2 gap-3">
                      {[
                        { key: 'envoy', label: 'Web Gateway (Envoy)', icon: Globe, desc: 'HTML, CSS, JS, static assets', color: 'purple' },
                        { key: 'kong', label: 'API Gateway (Kong)', icon: Cpu, desc: 'REST, gRPC, GraphQL endpoints', color: 'blue' },
                      ].map(opt => (
                        <div key={opt.key}
                          onClick={() => setDeployNodeForm({ ...deployNodeForm, nodeType: opt.key })}
                          className={`cursor-pointer p-3 rounded-lg border-2 transition-all ${
                            deployNodeForm.nodeType === opt.key
                              ? opt.color === 'purple' ? 'border-purple-500 bg-purple-500/5' : 'border-blue-500 bg-blue-500/5'
                              : 'border-jpmc-border/50 bg-jpmc-navy/30 hover:border-jpmc-border'
                          }`}
                        >
                          <opt.icon size={18} className={
                            deployNodeForm.nodeType === opt.key
                              ? opt.color === 'purple' ? 'text-purple-400 mb-1' : 'text-blue-400 mb-1'
                              : 'text-jpmc-muted mb-1'
                          } />
                          <div className="text-xs font-medium text-white">{opt.label}</div>
                          <div className="text-[10px] text-jpmc-muted mt-0.5">{opt.desc}</div>
                        </div>
                      ))}
                    </div>
                  </div>

                  {/* Health Check Path */}
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Health Check Path</label>
                    <input className="input-field text-xs font-mono" value={deployNodeForm.healthCheckPath}
                      onChange={e => setDeployNodeForm({ ...deployNodeForm, healthCheckPath: e.target.value })} />
                  </div>

                  {/* Datacenter */}
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Datacenter</label>
                    <select className="select-field" value={deployNodeForm.datacenter}
                      onChange={e => setDeployNodeForm({ ...deployNodeForm, datacenter: e.target.value })}>
                      {['us-east-1', 'us-east-2', 'eu-west-1', 'ap-southeast-1'].map(dc => (
                        <option key={dc} value={dc}>{dc}</option>
                      ))}
                    </select>
                  </div>
                </div>

                {/* Section 2: Routes */}
                <div className="mb-6">
                  <div className="flex items-center justify-between mb-3">
                    <button onClick={() => setRoutesSectionOpen(!routesSectionOpen)}
                      className="flex items-center gap-2 text-xs font-bold text-jpmc-muted uppercase tracking-wider hover:text-jpmc-text transition-colors">
                      {routesSectionOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
                      Routes
                      <span className="px-1.5 py-0.5 rounded-full bg-blue-500/10 text-blue-400 text-[10px] font-medium normal-case">
                        {deployNodeRoutes.length}
                      </span>
                    </button>
                    <div className="flex items-center gap-2">
                      <div className="relative">
                        <button
                          onClick={() => setShowCopyFromExisting(!showCopyFromExisting)}
                          className="flex items-center gap-1 px-2 py-1 rounded text-[10px] border border-jpmc-border/40 text-jpmc-muted hover:text-jpmc-text hover:bg-jpmc-hover transition-colors">
                          <Copy size={10} /> Copy from existing
                        </button>
                        {showCopyFromExisting && (
                          <div className="absolute right-0 top-full mt-1 w-72 max-h-60 overflow-y-auto bg-jpmc-dark border border-jpmc-border rounded-lg shadow-xl z-50">
                            {Object.entries(existingRoutesByFleet).length === 0 ? (
                              <div className="p-3 text-[10px] text-jpmc-muted text-center">No existing routes found</div>
                            ) : (
                              Object.entries(existingRoutesByFleet).map(([fid, fdata]) => (
                                <div key={fid}>
                                  <div className="px-3 py-1.5 text-[9px] font-bold text-jpmc-muted uppercase tracking-wider bg-jpmc-navy/50 border-b border-jpmc-border/20">
                                    {fdata.name} <span className="font-normal text-jpmc-muted">({fdata.subdomain})</span>
                                  </div>
                                  {fdata.routes.map(r => (
                                    <button key={r.id}
                                      className="w-full text-left px-3 py-1.5 text-[10px] hover:bg-jpmc-hover transition-colors flex items-center gap-2"
                                      onClick={() => {
                                        const newRoute = makeEmptyRoute()
                                        newRoute.context_path = r.context_path || r.path || ''
                                        newRoute.backend_url = r.backend || r.backend_url || 'http://svc-web:8004'
                                        newRoute.audience = r.audience || ''
                                        setDeployNodeRoutes(prev => [...prev, newRoute])
                                        setShowCopyFromExisting(false)
                                      }}>
                                      <code className="text-blue-400 font-mono">{r.context_path}</code>
                                      <span className="text-jpmc-muted">{'\u2192'}</span>
                                      <span className="text-jpmc-muted font-mono truncate">{(r.backend || '').replace('http://', '')}</span>
                                    </button>
                                  ))}
                                </div>
                              ))
                            )}
                          </div>
                        )}
                      </div>
                      <button onClick={addDeployRoute}
                        className="flex items-center gap-1 px-2 py-1 rounded text-[10px] bg-blue-500/10 border border-blue-500/30 text-blue-400 hover:bg-blue-500/20 transition-colors">
                        <Plus size={10} /> Add Route
                      </button>
                    </div>
                  </div>

                  {routesSectionOpen && (
                    <div className="space-y-3">
                      {deployNodeRoutes.length === 0 && (
                        <div className="p-4 rounded-lg border-2 border-dashed border-jpmc-border/30 text-center">
                          <div className="text-xs text-jpmc-muted">At least 1 route is required to deploy</div>
                          <button onClick={addDeployRoute}
                            className="mt-2 flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-dashed border-blue-500/30 text-xs text-blue-400 hover:bg-blue-500/5 transition-all mx-auto">
                            <Plus size={12} /> Add Route
                          </button>
                        </div>
                      )}
                      {deployNodeRoutes.map((route, idx) => (
                        <DeployRouteCard
                          key={idx}
                          route={route}
                          index={idx}
                          nodeType={deployNodeForm.nodeType}
                          onUpdate={updateDeployRoute}
                          onRemove={removeDeployRoute}
                        />
                      ))}
                    </div>
                  )}
                </div>

                {/* Deploy Button */}
                <button
                  onClick={handleDeployNode}
                  disabled={!deployNodeForm.fleetId || deployNodeRoutes.length === 0 || deployNodeRoutes.some(r => !r.context_path) || isDeployingNode}
                  className="btn-primary w-full py-3 disabled:opacity-40"
                >
                  {isDeployingNode ? (
                    <span className="flex items-center justify-center gap-2">
                      <span className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                      Deploying...
                    </span>
                  ) : (
                    `Deploy Node with ${deployNodeRoutes.length} Route${deployNodeRoutes.length !== 1 ? 's' : ''}`
                  )}
                </button>
              </div>
            </motion.div>
          </>
        )}
      </AnimatePresence>
      , document.body)}

      {/* ========== New Fleet Slide-over ========== */}
      {createPortal(
      <AnimatePresence>
        {showNewFleet && (
          <>
            <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
              className="fixed inset-0 bg-black/40 z-[9998]" onClick={() => { setShowNewFleet(false); setEditingFleet(null); setNewFleetForm({ ...defaultFleetForm }) }} />
            <motion.div
              initial={{ x: '100%' }} animate={{ x: 0 }} exit={{ x: '100%' }}
              transition={{ type: 'spring', damping: 30, stiffness: 300 }}
              className="fixed top-0 right-0 bottom-0 w-full max-w-lg bg-jpmc-dark border-l border-jpmc-border z-[9999] flex flex-col overflow-hidden"
            >
              {/* Header */}
              <div className="p-6 pb-4 shrink-0">
                <div className="flex items-center justify-between mb-2">
                  <h2 className="text-lg font-bold text-white">{editingFleet ? 'Edit Fleet' : 'Create New Fleet'}</h2>
                  <button onClick={() => { setShowNewFleet(false); setEditingFleet(null); setNewFleetForm({ ...defaultFleetForm }) }} className="p-1.5 rounded-md hover:bg-jpmc-hover text-jpmc-muted">
                    <X size={18} />
                  </button>
                </div>
              </div>

              {/* Success overlay */}
              <AnimatePresence>
                {newFleetSuccess && (
                  <motion.div
                    initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
                    className="absolute inset-0 z-10 flex items-center justify-center bg-jpmc-dark/90"
                  >
                    <motion.div initial={{ scale: 0.8 }} animate={{ scale: 1 }} className="text-center">
                      <motion.div
                        initial={{ scale: 0 }} animate={{ scale: 1 }}
                        transition={{ type: 'spring', damping: 10, stiffness: 200 }}
                        className="w-20 h-20 rounded-full bg-emerald-500/20 border-2 border-emerald-500 flex items-center justify-center mx-auto mb-4"
                      >
                        <Check size={36} className="text-emerald-400" />
                      </motion.div>
                      <div className="text-lg font-bold text-emerald-300">{editingFleet ? 'Fleet Updated' : 'Fleet Created Successfully'}</div>
                      <div className="text-sm text-emerald-400/70 mt-1">Deploy nodes and routes to start serving traffic</div>
                    </motion.div>
                  </motion.div>
                )}
              </AnimatePresence>

              {/* Form */}
              <div className="flex-1 min-h-0 overflow-y-auto px-6 pb-6 space-y-4">
                {/* Name */}
                <div>
                  <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Fleet Name <span className="text-red-400">*</span></label>
                  <input className="input-field" placeholder="JPMM Markets Gateway" value={newFleetForm.name}
                    onChange={e => setNewFleetForm({ ...newFleetForm, name: e.target.value })} />
                </div>

                {/* LOB */}
                <div>
                  <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Line of Business</label>
                  <select className="select-field" value={newFleetForm.lob} onChange={e => setNewFleetForm({ ...newFleetForm, lob: e.target.value })}>
                    {['Markets', 'Payments', 'Global Banking', 'Security Services', 'xCIB'].map(l => (
                      <option key={l} value={l}>{l}</option>
                    ))}
                  </select>
                </div>

                {/* Portal Hostname */}
                <div>
                  <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Portal Hostname <span className="text-red-400">*</span></label>
                  <div className="flex items-center gap-0">
                    <input className="input-field rounded-r-none border-r-0 flex-1 text-xs"
                      placeholder="myportal" value={newFleetForm.portal}
                      onChange={e => setNewFleetForm({ ...newFleetForm, portal: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '') })} />
                    <span className="px-3 py-2 rounded-r-lg border border-jpmc-border/50 bg-jpmc-navy/80 text-xs text-jpmc-muted">.jpm.com</span>
                  </div>
                  {newFleetFqdn && (
                    <div className="mt-2 p-2 rounded-lg bg-jpmc-navy/50 border border-jpmc-border/20">
                      <code className="text-xs text-blue-400">{newFleetFqdn}</code>
                    </div>
                  )}
                </div>

                {/* Description */}
                <div>
                  <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Description <span className="text-jpmc-muted text-[10px]">(optional)</span></label>
                  <textarea className="input-field min-h-[80px] resize-y" placeholder="Describe this fleet's purpose..."
                    value={newFleetForm.description} onChange={e => setNewFleetForm({ ...newFleetForm, description: e.target.value })} />
                </div>

                {/* Hosting Environment */}
                <div>
                  <label className="block text-xs font-medium text-jpmc-muted mb-2">Hosting Environment</label>
                  <div className="grid grid-cols-2 gap-3">
                    {[
                      { key: 'psaas', label: 'On-Prem (PSaaS)', icon: Layers, desc: 'Private data center', color: 'indigo' },
                      { key: 'aws', label: 'Public Cloud (AWS)', icon: Cloud, desc: 'Multi-AZ cloud', color: 'orange' },
                    ].map(opt => (
                      <div key={opt.key}
                        onClick={() => setNewFleetForm({ ...newFleetForm, hostEnv: opt.key })}
                        className={`cursor-pointer p-4 rounded-lg border-2 transition-all ${
                          newFleetForm.hostEnv === opt.key
                            ? opt.color === 'indigo' ? 'border-indigo-500 bg-indigo-500/5' : 'border-orange-500 bg-orange-500/5'
                            : 'border-jpmc-border/50 bg-jpmc-navy/30 hover:border-jpmc-border'
                        }`}
                      >
                        <opt.icon size={18} className={
                          newFleetForm.hostEnv === opt.key
                            ? opt.color === 'indigo' ? 'text-indigo-400 mb-1' : 'text-orange-400 mb-1'
                            : 'text-jpmc-muted mb-1'
                        } />
                        <div className="text-sm font-medium text-white">{opt.label}</div>
                        <div className="text-[10px] text-jpmc-muted mt-0.5">{opt.desc}</div>
                      </div>
                    ))}
                  </div>
                </div>

                {/* Auth Provider */}
                <div>
                  <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Auth Provider</label>
                  <select className="select-field" value={newFleetForm.authProvider}
                    onChange={e => setNewFleetForm({ ...newFleetForm, authProvider: e.target.value })}>
                    {['Janus', 'AuthE1.0', 'AuthE2.0', 'Sentry', 'Chase', 'N/A', 'Unauthenticated'].map(v => (
                      <option key={v} value={v}>{v}</option>
                    ))}
                  </select>
                </div>

                {/* Gateway Configuration (collapsible) */}
                <div className="border border-jpmc-border/30 rounded-lg overflow-hidden">
                  <button type="button" onClick={() => setFleetFormSections(s => ({ ...s, gateway: !s.gateway }))}
                    className="w-full flex items-center justify-between p-3 text-sm font-medium text-jpmc-muted hover:text-white transition-colors">
                    <span>Gateway Configuration</span>
                    <ChevronDown size={14} className={`transition-transform ${fleetFormSections.gateway ? 'rotate-180' : ''}`} />
                  </button>
                  {fleetFormSections.gateway && (
                    <div className="px-3 pb-3 space-y-3">
                      {/* Gateway Type */}
                      <div>
                        <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Gateway Type</label>
                        <div className="grid grid-cols-3 gap-2">
                          {['envoy', 'kong', 'mixed'].map(gt => (
                            <div key={gt} onClick={() => setNewFleetForm({ ...newFleetForm, gatewayType: gt })}
                              className={`cursor-pointer p-3 rounded-lg border-2 text-center transition-all ${
                                newFleetForm.gatewayType === gt
                                  ? 'border-blue-500 bg-blue-500/10 text-blue-300'
                                  : 'border-jpmc-border/50 bg-jpmc-navy/30 text-jpmc-muted hover:border-jpmc-border'
                              }`}>
                              <div className="text-sm font-medium capitalize">{gt}</div>
                            </div>
                          ))}
                        </div>
                      </div>
                      {/* Traffic Type */}
                      <div>
                        <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Traffic Type</label>
                        <div className="grid grid-cols-2 gap-2">
                          {[{ key: 'web', label: 'Web' }, { key: 'api', label: 'API' }].map(tt => (
                            <div key={tt.key} onClick={() => setNewFleetForm({ ...newFleetForm, trafficType: tt.key })}
                              className={`cursor-pointer p-3 rounded-lg border-2 text-center transition-all ${
                                newFleetForm.trafficType === tt.key
                                  ? 'border-blue-500 bg-blue-500/10 text-blue-300'
                                  : 'border-jpmc-border/50 bg-jpmc-navy/30 text-jpmc-muted hover:border-jpmc-border'
                              }`}>
                              <div className="text-sm font-medium">{tt.label}</div>
                            </div>
                          ))}
                        </div>
                      </div>
                    </div>
                  )}
                </div>

                {/* Regions (collapsible) */}
                <div className="border border-jpmc-border/30 rounded-lg overflow-hidden">
                  <button type="button" onClick={() => setFleetFormSections(s => ({ ...s, regions: !s.regions }))}
                    className="w-full flex items-center justify-between p-3 text-sm font-medium text-jpmc-muted hover:text-white transition-colors">
                    <span>Regions ({newFleetForm.regions.length} selected)</span>
                    <ChevronDown size={14} className={`transition-transform ${fleetFormSections.regions ? 'rotate-180' : ''}`} />
                  </button>
                  {fleetFormSections.regions && (
                    <div className="px-3 pb-3 space-y-2">
                      {AVAILABLE_REGIONS.map(region => (
                        <label key={region} className="flex items-center gap-2 cursor-pointer p-2 rounded-md hover:bg-jpmc-hover">
                          <input type="checkbox" checked={newFleetForm.regions.includes(region)}
                            onChange={e => {
                              const regions = e.target.checked
                                ? [...newFleetForm.regions, region]
                                : newFleetForm.regions.filter(r => r !== region)
                              setNewFleetForm({ ...newFleetForm, regions: regions.length ? regions : [region] })
                            }}
                            className="rounded border-jpmc-border text-blue-500" />
                          <span className="text-sm text-jpmc-text">{region}</span>
                        </label>
                      ))}
                    </div>
                  )}
                </div>

                {/* Scaling (collapsible) */}
                <div className="border border-jpmc-border/30 rounded-lg overflow-hidden">
                  <button type="button" onClick={() => setFleetFormSections(s => ({ ...s, scaling: !s.scaling }))}
                    className="w-full flex items-center justify-between p-3 text-sm font-medium text-jpmc-muted hover:text-white transition-colors">
                    <span>Scaling &amp; Resources</span>
                    <ChevronDown size={14} className={`transition-transform ${fleetFormSections.scaling ? 'rotate-180' : ''}`} />
                  </button>
                  {fleetFormSections.scaling && (
                    <div className="px-3 pb-3 space-y-3">
                      {/* Resource Profile */}
                      <div>
                        <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Resource Profile</label>
                        <div className="grid grid-cols-3 gap-2">
                          {[
                            { key: 'small', label: 'Small', desc: '256Mi / 0.25 CPU' },
                            { key: 'medium', label: 'Medium', desc: '512Mi / 0.5 CPU' },
                            { key: 'large', label: 'Large', desc: '1Gi / 1 CPU' },
                          ].map(rp => (
                            <div key={rp.key} onClick={() => setNewFleetForm({ ...newFleetForm, resourceProfile: rp.key })}
                              className={`cursor-pointer p-2.5 rounded-lg border-2 text-center transition-all ${
                                newFleetForm.resourceProfile === rp.key
                                  ? 'border-blue-500 bg-blue-500/10'
                                  : 'border-jpmc-border/50 bg-jpmc-navy/30 hover:border-jpmc-border'
                              }`}>
                              <div className="text-xs font-medium text-white">{rp.label}</div>
                              <div className="text-[10px] text-jpmc-muted">{rp.desc}</div>
                            </div>
                          ))}
                        </div>
                      </div>
                      {/* Initial Replicas */}
                      {!editingFleet && (
                        <div>
                          <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Initial Replicas</label>
                          <input type="number" min="1" max="50" className="input-field w-24"
                            value={newFleetForm.containerCount}
                            onChange={e => setNewFleetForm({ ...newFleetForm, containerCount: parseInt(e.target.value) || 2 })} />
                        </div>
                      )}
                      {/* Autoscale */}
                      <div>
                        <label className="flex items-center gap-2 cursor-pointer">
                          <input type="checkbox" checked={newFleetForm.autoscaleEnabled}
                            onChange={e => setNewFleetForm({ ...newFleetForm, autoscaleEnabled: e.target.checked })}
                            className="rounded border-jpmc-border text-blue-500" />
                          <span className="text-xs font-medium text-jpmc-muted">Enable Autoscaling</span>
                        </label>
                        {newFleetForm.autoscaleEnabled && (
                          <div className="mt-2 grid grid-cols-3 gap-2">
                            <div>
                              <label className="block text-[10px] text-jpmc-muted mb-1">Min</label>
                              <input type="number" min="1" className="input-field text-xs" value={newFleetForm.autoscaleMin}
                                onChange={e => setNewFleetForm({ ...newFleetForm, autoscaleMin: parseInt(e.target.value) || 2 })} />
                            </div>
                            <div>
                              <label className="block text-[10px] text-jpmc-muted mb-1">Max</label>
                              <input type="number" min="1" className="input-field text-xs" value={newFleetForm.autoscaleMax}
                                onChange={e => setNewFleetForm({ ...newFleetForm, autoscaleMax: parseInt(e.target.value) || 16 })} />
                            </div>
                            <div>
                              <label className="block text-[10px] text-jpmc-muted mb-1">CPU %</label>
                              <input type="number" min="10" max="95" className="input-field text-xs" value={newFleetForm.autoscaleCpuThreshold}
                                onChange={e => setNewFleetForm({ ...newFleetForm, autoscaleCpuThreshold: parseInt(e.target.value) || 70 })} />
                            </div>
                          </div>
                        )}
                      </div>
                    </div>
                  )}
                </div>

                {/* Submit */}
                <button
                  onClick={handleCreateOrUpdateFleet}
                  disabled={!newFleetForm.name || !newFleetForm.portal || isFleetSubmitting}
                  className="btn-primary w-full py-3 disabled:opacity-40"
                >
                  {isFleetSubmitting
                    ? (editingFleet ? 'Updating…' : 'Creating…')
                    : (editingFleet ? 'Update Fleet' : 'Create Fleet')}
                </button>
              </div>
            </motion.div>
          </>
        )}
      </AnimatePresence>
      , document.body)}

      {/* Search, Filter, Sort Bar */}
      <div className="flex items-center gap-3 flex-wrap">
        <div className="relative flex-1 min-w-[200px]">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-jpmc-muted" />
          <input className="input-field pl-9 text-sm" placeholder="Search fleets, subdomains, LOBs..."
            value={searchTerm} onChange={e => setSearchTerm(e.target.value)} />
        </div>
        <select className="select-field text-xs w-auto" value={statusFilter}
          onChange={e => setStatusFilter(e.target.value)}>
          <option value="all">All Status</option>
          <option value="healthy">Healthy</option>
          <option value="degraded">Degraded</option>
          <option value="offline">Offline</option>
        </select>
        <select className="select-field text-xs w-auto" value={gatewayFilter}
          onChange={e => setGatewayFilter(e.target.value)}>
          <option value="all">All Gateways</option>
          <option value="envoy">Envoy</option>
          <option value="kong">Kong</option>
        </select>
        <select className="select-field text-xs w-auto" value={sortBy}
          onChange={e => setSortBy(e.target.value)}>
          <option value="lob">Sort: LOB</option>
          <option value="name">Sort: Name</option>
          <option value="status">Sort: Status</option>
          <option value="instances">Sort: Instances</option>
        </select>
      </div>

      {/* Fleet Cards grouped by LOB */}
      <div className="space-y-6">
        {(() => {
          // Filter
          let filtered = fleets.filter(f => {
            if (searchTerm) {
              const q = searchTerm.toLowerCase()
              const match = (f.name || '').toLowerCase().includes(q) ||
                (f.subdomain || '').toLowerCase().includes(q) ||
                (f.lob || '').toLowerCase().includes(q) ||
                (f.auth_provider || '').toLowerCase().includes(q) ||
                (f.notes || '').toLowerCase().includes(q) ||
                (f.fleet_type || '').toLowerCase().includes(q)
              if (!match) return false
            }
            if (statusFilter !== 'all' && f.status !== statusFilter) return false
            if (gatewayFilter !== 'all') {
              const insts = f.instances || []
              const hasGateway = insts.some(i => (i.gateway_type || 'envoy') === gatewayFilter)
              if (!hasGateway && f.gateway_type !== gatewayFilter) return false
            }
            return true
          })
          // Sort
          if (sortBy === 'name') filtered.sort((a, b) => a.name.localeCompare(b.name))
          else if (sortBy === 'status') filtered.sort((a, b) => {
            const order = { offline: 0, degraded: 1, healthy: 2 }
            return (order[a.status] ?? 9) - (order[b.status] ?? 9)
          })
          else if (sortBy === 'instances') filtered.sort((a, b) => (b.instances || []).length - (a.instances || []).length)

          // Separate data plane and control plane fleets
          const dataPlane = filtered.filter(f => f.fleet_type !== 'control')
          const controlPlane = filtered.filter(f => f.fleet_type === 'control')

          // Group data plane by LOB
          const groups = dataPlane.reduce((g, fleet) => {
            const lob = fleet.lob || 'Other'
            ;(g[lob] = g[lob] || []).push(fleet)
            return g
          }, {})
          const lobOrder = ['Markets', 'Payments', 'Global Banking', 'Security Services', 'xCIB', 'CIB']
          const sortedEntries = Object.entries(groups).sort(([a], [b]) => {
            const ai = lobOrder.indexOf(a), bi = lobOrder.indexOf(b)
            return (ai === -1 ? 999 : ai) - (bi === -1 ? 999 : bi)
          })

          const renderFleetCard = (fleet, idx) => {
          const isControlPlane = fleet.fleet_type === 'control'
          const isExpanded = expandedFleet === fleet.id
          const instances = fleet.instances || []
          const fleetEnvoyCount = instances.filter(i => (i.gateway_type || 'envoy') === 'envoy' || (!i.gateway_type && !i.context_path?.startsWith('/api'))).length
          const fleetKongCount = instances.filter(i => i.gateway_type === 'kong' || (!i.gateway_type && i.context_path?.startsWith('/api'))).length
          const liveNodes = isExpanded ? currentFleetNodes : (fleet.nodes || [])
          const { envoyCount: liveEnvoy, kongCount: liveKong } = getNodeTypeCounts(liveNodes)
          const headerEnvoy = liveEnvoy
          const headerKong = liveKong

          return (
            <motion.div
              key={fleet.id}
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: 0.1 + idx * 0.05 }}
            >
              <div className={`overflow-hidden transition-all duration-200 ${
                isControlPlane
                  ? 'rounded-xl border border-dashed border-slate-500/30 bg-jpmc-dark/80'
                  : `glass-card ${fleet.status === 'degraded' ? 'border-amber-500/30' : ''}`
              }`}>
                {/* Fleet Header */}
                <div
                  className="flex items-center gap-4 p-5 cursor-pointer hover:bg-jpmc-hover/50 transition-colors"
                  onClick={() => { setExpandedFleet(isExpanded ? null : fleet.id); setScaleCount(null) }}
                >
                  <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 ${
                    fleet.status === 'healthy' ? 'bg-emerald-500/15 border border-emerald-500/30'
                    : fleet.status === 'suspended' ? 'bg-amber-500/15 border border-amber-500/30'
                    : fleet.status === 'offline' ? 'bg-red-500/15 border border-red-500/30'
                    : fleet.status === 'degraded' ? 'bg-amber-500/15 border border-amber-500/30'
                    : 'bg-gray-500/15 border border-gray-500/30'
                  }`}>
                    {fleet.status === 'suspended' ? (
                      <Pause size={18} className="text-amber-400" />
                    ) : fleet.status === 'offline' ? (
                      <Power size={18} className="text-red-400" />
                    ) : (
                      <Server size={18} className={fleet.status === 'healthy' ? 'text-emerald-400' : 'text-amber-400'} />
                    )}
                  </div>

                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 mb-1">
                      <h3 className="text-sm font-semibold text-white">{fleet.name}</h3>
                      {fleet.fleet_type === 'control' && (
                        <span className="text-[8px] px-1.5 py-0.5 rounded-full bg-slate-500/15 border border-slate-500/30 text-slate-400 font-semibold uppercase tracking-wider">CP</span>
                      )}
                      {fleet.lob && <span className="badge badge-blue text-[9px]">{fleet.lob}</span>}
                      <StatusBadge status={fleet.status} />
                      {(fleet.sync_status === 'synced' || fleet.sync_status === 'progressing' || fleet.sync_status === 'out-of-sync') && (
                        <span className={`text-[8px] px-1.5 py-0.5 rounded-full font-semibold uppercase tracking-wider ${
                          fleet.sync_status === 'synced' ? 'bg-emerald-500/15 border border-emerald-500/30 text-emerald-400'
                          : fleet.sync_status === 'progressing' ? 'bg-yellow-500/15 border border-yellow-500/30 text-yellow-400'
                          : 'bg-red-500/15 border border-red-500/30 text-red-400'
                        }`}>
                          {fleet.sync_status}
                        </span>
                      )}
                      {fleet.git_commit_sha && (
                        <span className="text-[8px] px-1.5 py-0.5 rounded bg-slate-500/10 border border-slate-500/30 text-slate-400 font-mono">
                          {fleet.git_commit_sha.slice(0, 7)}
                        </span>
                      )}
                    </div>
                    <div className="flex items-center gap-3 text-xs text-jpmc-muted">
                      <span className="flex items-center gap-1">
                        <Globe size={11} />
                        {fleet.subdomain}
                      </span>
                      <span className="flex items-center gap-1">
                        <MapPin size={11} />
                        {(fleet.regions || []).join(', ') || fleet.region}
                      </span>
                      {fleet.auth_provider && fleet.auth_provider !== '' && (
                        <span className="flex items-center gap-1">
                          <Lock size={11} />
                          {fleet.auth_provider}
                        </span>
                      )}
                    </div>
                    {isControlPlane && fleet.notes && (
                      <p className="text-[10px] text-slate-400 mt-1 leading-relaxed line-clamp-2">{fleet.notes}</p>
                    )}
                  </div>

                  <div className="flex items-center gap-4 shrink-0">
                    <span className={`badge text-[9px] ${fleet.host_env === 'aws' ? 'bg-orange-500/10 text-orange-400 border border-orange-500/30' : 'bg-indigo-500/10 text-indigo-400 border border-indigo-500/30'}`}>
                      {fleet.host_env === 'aws' ? 'AWS' : 'PSaaS'}
                    </span>
                    <div className="flex items-center gap-1.5">
                      {headerEnvoy > 0 && (
                        <span className="text-[9px] px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-400 border border-purple-500/30 font-medium">
                          {headerEnvoy} Envoy
                        </span>
                      )}
                      {headerKong > 0 && (
                        <span className="text-[9px] px-1.5 py-0.5 rounded bg-blue-500/10 text-blue-400 border border-blue-500/30 font-medium">
                          {headerKong} Kong
                        </span>
                      )}
                      {headerEnvoy === 0 && headerKong === 0 && (
                        <span className="text-[9px] px-1.5 py-0.5 rounded bg-gray-500/10 text-jpmc-muted border border-jpmc-border/30">
                          No nodes
                        </span>
                      )}
                    </div>
                    <div className="text-right">
                      <div className="text-lg font-bold text-white">{fleet.instances_count || instances.length}</div>
                      <div className="text-[10px] text-jpmc-muted">instances</div>
                    </div>
                    <div className="text-right">
                      <div className="text-lg font-bold text-white">{instances.length}</div>
                      <div className="text-[10px] text-jpmc-muted">routes</div>
                    </div>
                    {/* Fleet Suspend/Resume/Restart -- hidden for CP fleets */}
                    {fleet.fleet_type !== 'control' && (
                    <div className="flex items-center gap-1" onClick={e => e.stopPropagation()}>
                      {fleet.status === 'healthy' || fleet.status === 'degraded' ? (
                        <button
                          onClick={async () => {
                            if (!confirm(`Suspend fleet "${fleet.name}"?\n\nThis will:\n- Stop all gateway containers\n- Deactivate all routes\n- Stop serving traffic for ${fleet.subdomain}`)) return
                            await fetch(`${API_URL}/fleets/${fleet.id}/suspend`, { method: 'POST' })
                            queryClient.invalidateQueries({ queryKey: ['fleets'] })
                            queryClient.invalidateQueries({ queryKey: ['fleetNodes', fleet.id] })
                          }}
                          className="p-1.5 rounded-md hover:bg-amber-500/10 text-jpmc-muted hover:text-amber-400 transition-colors"
                          title="Suspend fleet"
                        >
                          <Pause size={14} />
                        </button>
                      ) : fleet.status === 'offline' ? (
                        <button
                          onClick={async () => {
                            await fetch(`${API_URL}/fleets/${fleet.id}/resume`, { method: 'POST' })
                            queryClient.invalidateQueries({ queryKey: ['fleets'] })
                            queryClient.invalidateQueries({ queryKey: ['fleetNodes', fleet.id] })
                          }}
                          className="p-1.5 rounded-md bg-red-500/10 border border-red-500/30 text-red-400 hover:bg-red-500/20 transition-colors"
                          title="Restart fleet"
                        >
                          <RefreshCw size={14} />
                        </button>
                      ) : (
                        <button
                          onClick={async () => {
                            await fetch(`${API_URL}/fleets/${fleet.id}/resume`, { method: 'POST' })
                            queryClient.invalidateQueries({ queryKey: ['fleets'] })
                            queryClient.invalidateQueries({ queryKey: ['fleetNodes', fleet.id] })
                          }}
                          className="p-1.5 rounded-md hover:bg-emerald-500/10 text-jpmc-muted hover:text-emerald-400 transition-colors"
                          title="Start fleet"
                        >
                          <Play size={14} />
                        </button>
                      )}
                      <button
                        onClick={(e) => { e.stopPropagation(); openEditFleet(fleet) }}
                        className="p-1.5 rounded-md hover:bg-blue-500/10 text-jpmc-muted hover:text-blue-400 transition-colors"
                        title="Edit fleet"
                      >
                        <Settings size={14} />
                      </button>
                      <button
                        onClick={async () => {
                          if (!confirm(`Delete fleet "${fleet.name}"?\n\nThis will permanently remove:\n- All gateway containers\n- All routes for ${fleet.subdomain}\n- All fleet configuration`)) return
                          await fetch(`${API_URL}/fleets/${fleet.id}`, { method: 'DELETE' })
                          queryClient.invalidateQueries({ queryKey: ['fleets'] })
                        }}
                        className="p-1.5 rounded-md hover:bg-red-500/10 text-jpmc-muted hover:text-red-400 transition-colors"
                        title="Delete fleet"
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                    )}
                    {isExpanded ? <ChevronDown size={16} className="text-jpmc-muted" /> : <ChevronRight size={16} className="text-jpmc-muted" />}
                  </div>
                </div>

                {/* Expanded Content */}
                <AnimatePresence>
                  {isExpanded && (
                    <motion.div
                      initial={{ height: 0, opacity: 0 }}
                      animate={{ height: 'auto', opacity: 1 }}
                      exit={{ height: 0, opacity: 0 }}
                      transition={{ duration: 0.2 }}
                      className="overflow-hidden"
                    >
                      <div className="border-t border-jpmc-border/30 px-5 py-4">
                        {/* Production context */}
                        <div className="p-3 rounded-lg bg-jpmc-navy/50 border border-jpmc-border/20 mb-4">
                          <p className="text-[11px] text-jpmc-muted leading-relaxed">
                            <span className="text-jpmc-text font-medium">{fleet.name}</span>{' '}
                            {fleet.fleet_type === 'control'
                              ? <>is a control-plane service managed by docker-compose.</>
                              : <>serves traffic for <code className="text-blue-400 text-[10px]">{fleet.subdomain}</code>.
                                This fleet supports mixed gateway types with
                                <span className="text-jpmc-text"> {instances.length} route{instances.length !== 1 ? 's' : ''}</span> configured.
                                Route changes propagate to compatible nodes via xDS (Envoy) or declarative sync (Kong) within 5 seconds.</>
                            }
                          </p>
                          {fleet.notes && (
                            <p className="text-[11px] text-jpmc-text mt-2 leading-relaxed border-t border-jpmc-border/20 pt-2">
                              {fleet.notes}
                            </p>
                          )}
                        </div>

                        {/* Running Nodes -- grouped by type */}
                        <div className="mb-4">
                          <div className="flex items-center justify-between mb-3">
                            <div className="text-xs font-medium text-jpmc-muted uppercase tracking-wider">
                              {fleet.fleet_type === 'control' ? 'Service Nodes' : 'Running Nodes'}
                              <span className="text-[10px] font-normal ml-2 normal-case">
                                {fleet.fleet_type === 'control' ? '(docker-compose managed)' : '(gateway containers in this fleet)'}
                              </span>
                            </div>
                          </div>
                          {fleet.fleet_type === 'control' ? (
                            /* Control plane: show all nodes as read-only */
                            currentFleetNodes.length > 0 ? (
                              <div className="space-y-1.5">
                                {currentFleetNodes.map(node => (
                                  <NodeCard key={node.container_id || node.id || node.name} node={node} fleetId={fleet.id} apiUrl={API_URL} readOnly />
                                ))}
                              </div>
                            ) : (
                              <div className="p-3 rounded-lg border border-dashed border-jpmc-border/30 text-center text-[11px] text-jpmc-muted">
                                Loading control-plane nodes...
                              </div>
                            )
                          ) : currentFleetNodes.length > 0 ? (
                            <>
                              {/* Envoy Gateway Nodes Section */}
                              {(() => {
                                const envoyNodes = currentFleetNodes.filter(n => (n.gateway_type || 'envoy') === 'envoy')
                                if (envoyNodes.length === 0) return null
                                return (
                                  <div className="mb-3">
                                    <div className="flex items-center gap-2 mb-1.5">
                                      <span className="text-[10px] font-medium text-purple-400 uppercase tracking-wider">Envoy Gateway Nodes</span>
                                      <span className="text-[9px] text-jpmc-muted">({envoyNodes.length})</span>
                                    </div>
                                    <div className="space-y-4">
                                      {envoyNodes.map(node => (
                                        <NodeCard key={node.container_id || node.id || node.name} node={node} fleetId={fleet.id} apiUrl={API_URL} routes={getRoutesForNode(node, instances)} />
                                      ))}
                                    </div>
                                  </div>
                                )
                              })()}
                              {/* Kong Gateway Nodes Section */}
                              {(() => {
                                const kongNodes = currentFleetNodes.filter(n => (n.gateway_type || 'envoy') === 'kong')
                                if (kongNodes.length === 0) return null
                                return (
                                  <div className="mb-3">
                                    <div className="flex items-center gap-2 mb-1.5">
                                      <span className="text-[10px] font-medium text-blue-400 uppercase tracking-wider">Kong Gateway Nodes</span>
                                      <span className="text-[9px] text-jpmc-muted">({kongNodes.length})</span>
                                    </div>
                                    <div className="space-y-4">
                                      {kongNodes.map(node => (
                                        <NodeCard key={node.container_id || node.id || node.name} node={node} fleetId={fleet.id} apiUrl={API_URL} routes={getRoutesForNode(node, instances)} />
                                      ))}
                                    </div>
                                  </div>
                                )
                              })()}
                              {/* Deploy Node inline + Fleet Route buttons */}
                              {(() => {
                                const regions = [
                                  { id: 'us-east-1', label: 'US East (N. Virginia)' },
                                  { id: 'us-east-2', label: 'US East (Ohio)' },
                                  { id: 'eu-west-1', label: 'EU West (Ireland)' },
                                  { id: 'ap-southeast-1', label: 'AP Southeast (Singapore)' },
                                ]
                                const fleetHasEnvoy = currentFleetNodes.some(n => (n.gateway_type || 'envoy') === 'envoy')
                                const fleetHasKong = currentFleetNodes.some(n => (n.gateway_type || 'envoy') === 'kong')
                                const handleFleetRouteSubmit = async () => {
                                  setFleetRouteError('')
                                  const payload = {
                                    context_path: fleetRouteForm.context_path,
                                    backend_url: fleetRouteForm.destination_type === 'backend' ? fleetRouteForm.backend_url : undefined,
                                    gateway_type: showAddFleetRoute, // 'envoy' or 'kong'
                                    audience: fleetRouteForm.audience,
                                    methods: fleetRouteForm.methods,
                                  }
                                  if (fleetRouteForm.destination_type === 'lambda') {
                                    payload.function_enabled = true
                                    payload.function_code = fleetRouteForm.function_code
                                    payload.function_language = 'javascript'
                                  }
                                  if (showAddFleetRoute === 'envoy') {
                                    payload.timeout_ms = Number(fleetRouteForm.timeout_ms) || 30000
                                    payload.retry_count = Number(fleetRouteForm.retry_count) || 0
                                    payload.retry_on = fleetRouteForm.retry_on
                                    payload.cors_enabled = fleetRouteForm.cors_enabled
                                    if (fleetRouteForm.cors_enabled) payload.cors_origins = fleetRouteForm.cors_origins || '*'
                                    if (Number(fleetRouteForm.rate_limit_rps) > 0) payload.rate_limit_rps = Number(fleetRouteForm.rate_limit_rps)
                                    payload.priority = Number(fleetRouteForm.priority) || 0
                                  } else if (showAddFleetRoute === 'kong') {
                                    payload.strip_path = fleetRouteForm.strip_path
                                    if (Number(fleetRouteForm.kong_rate_limit_rps) > 0) payload.rate_limit_rps = Number(fleetRouteForm.kong_rate_limit_rps)
                                    if (Number(fleetRouteForm.kong_rate_limit_rpm) > 0) payload.rate_limit_rpm = Number(fleetRouteForm.kong_rate_limit_rpm)
                                    if (Number(fleetRouteForm.kong_rate_limit_rph) > 0) payload.rate_limit_rph = Number(fleetRouteForm.kong_rate_limit_rph)
                                    payload.upstream_connect_timeout_ms = Number(fleetRouteForm.upstream_connect_timeout_ms) || 60000
                                    payload.upstream_read_timeout_ms = Number(fleetRouteForm.upstream_read_timeout_ms) || 60000
                                    payload.upstream_write_timeout_ms = Number(fleetRouteForm.upstream_write_timeout_ms) || 60000
                                    if (fleetRouteForm.plugins.length > 0) payload.plugins = fleetRouteForm.plugins
                                  }
                                  try {
                                    const r = await fetch(`${API_URL}/fleets/${fleet.id}/deploy`, {
                                      method: 'POST',
                                      headers: { 'Content-Type': 'application/json' },
                                      body: JSON.stringify(payload),
                                    })
                                    if (r.ok) {
                                      setShowAddFleetRoute(null)
                                      setFleetRouteForm(makeEmptyFleetRouteForm())
                                      queryClient.invalidateQueries({ queryKey: ['fleets'] })
                                      queryClient.invalidateQueries({ queryKey: ['routes'] })
                                      queryClient.invalidateQueries({ queryKey: ['fleetNodes'] })
                                      setFleetRouteSuccess('Route deployed successfully')
                                      setTimeout(() => setFleetRouteSuccess(''), 3000)
                                    } else {
                                      const err = await r.json().catch(() => ({ detail: 'Deploy failed' }))
                                      setFleetRouteError(err.detail || `Error ${r.status}`)
                                    }
                                  } catch (e) { setFleetRouteError(e.message || 'Network error') }
                                }
                                return (
                                  <>
                                  <div className="flex items-center gap-3 p-3 rounded-lg bg-jpmc-navy/30 border border-jpmc-border/20 flex-wrap">
                                    <div className="text-xs text-jpmc-muted">
                                      <span className="font-medium">{currentFleetNodes.length}</span> node{currentFleetNodes.length !== 1 ? 's' : ''}
                                    </div>
                                    <div className="flex-1" />
                                    {/* Fleet-level Add Route buttons */}
                                    {fleetHasEnvoy && (
                                      <button
                                        onClick={() => {
                                          setShowAddFleetRoute('envoy')
                                          setFleetRouteForm(makeEmptyFleetRouteForm('envoy'))
                                          setFleetRouteError('')
                                        }}
                                        className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border text-xs font-medium transition-colors text-purple-400 border-purple-500/30 bg-purple-500/10 hover:bg-purple-500/20"
                                      >
                                        <Plus size={12} /> Web Route
                                      </button>
                                    )}
                                    {fleetHasKong && (
                                      <button
                                        onClick={() => {
                                          setShowAddFleetRoute('kong')
                                          setFleetRouteForm(makeEmptyFleetRouteForm('kong'))
                                          setFleetRouteError('')
                                        }}
                                        className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border text-xs font-medium transition-colors text-blue-400 border-blue-500/30 bg-blue-500/10 hover:bg-blue-500/20"
                                      >
                                        <Plus size={12} /> API Route
                                      </button>
                                    )}
                                    {!showDeployNodeInline ? (
                                      <button
                                        onClick={() => setShowDeployNodeInline(true)}
                                        className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-blue-500/10 border border-blue-500/30 text-blue-400 text-xs font-medium hover:bg-blue-500/20 transition-colors"
                                      >
                                        <Plus size={12} /> Deploy Node
                                      </button>
                                    ) : (
                                      <div className="flex items-center gap-3 flex-wrap">
                                        <div className="flex items-center gap-1.5">
                                          <span className="text-[10px] text-jpmc-muted">Type:</span>
                                          <button
                                            onClick={() => setDeployNodeType('envoy')}
                                            className={`px-2 py-1 rounded-lg border text-[10px] transition-all ${
                                              deployNodeType === 'envoy'
                                                ? 'border-purple-500/50 bg-purple-500/10 text-purple-400'
                                                : 'border-jpmc-border/30 text-jpmc-muted hover:border-jpmc-border/60'
                                            }`}
                                          >
                                            Envoy (Web)
                                          </button>
                                          <button
                                            onClick={() => setDeployNodeType('kong')}
                                            className={`px-2 py-1 rounded-lg border text-[10px] transition-all ${
                                              deployNodeType === 'kong'
                                                ? 'border-blue-500/50 bg-blue-500/10 text-blue-400'
                                                : 'border-jpmc-border/30 text-jpmc-muted hover:border-jpmc-border/60'
                                            }`}
                                          >
                                            Kong (API)
                                          </button>
                                        </div>
                                        <div className="flex items-center gap-1.5">
                                          <span className="text-[10px] text-jpmc-muted">DC:</span>
                                          <select
                                            className="select-field text-[10px] py-1 px-2 w-auto"
                                            value={deployNodeDc}
                                            onChange={e => setDeployNodeDc(e.target.value)}
                                          >
                                            {regions.map(r => (
                                              <option key={r.id} value={r.id}>{r.label}</option>
                                            ))}
                                          </select>
                                        </div>
                                        <button
                                          disabled={deployingRegion !== null}
                                          onClick={async () => {
                                            setDeployingRegion(deployNodeDc)
                                            await fetch(`${API_URL}/fleets/${fleet.id}/scale`, {
                                              method: 'POST',
                                              headers: { 'Content-Type': 'application/json' },
                                              body: JSON.stringify({
                                                count: currentFleetNodes.length + 1,
                                                datacenter: deployNodeDc,
                                                gateway_type: deployNodeType,
                                              }),
                                            })
                                            queryClient.invalidateQueries({ queryKey: ['fleetNodes'] })
                                            queryClient.invalidateQueries({ queryKey: ['fleets'] })
                                            setDeployingRegion(null)
                                            setShowDeployNodeInline(false)
                                          }}
                                          className="px-3 py-1.5 rounded-lg bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 text-[10px] font-medium hover:bg-emerald-500/20 transition-colors disabled:opacity-40"
                                        >
                                          {deployingRegion ? 'Deploying...' : 'Deploy'}
                                        </button>
                                        <button onClick={() => setShowDeployNodeInline(false)}
                                          className="p-1 rounded hover:bg-jpmc-hover text-jpmc-muted">
                                          <X size={12} />
                                        </button>
                                      </div>
                                    )}
                                  </div>
                                  {/* Fleet-level success message */}
                                  {fleetRouteSuccess && (
                                    <div className="p-2 rounded-lg bg-emerald-500/10 border border-emerald-500/30 text-[11px] text-emerald-400 text-center">
                                      {fleetRouteSuccess}
                                    </div>
                                  )}
                                  {/* Fleet-level Add Route form */}
                                  {showAddFleetRoute && (
                                    <div className="p-3 rounded-lg bg-jpmc-navy/70 border border-jpmc-border/40 space-y-2.5 mt-2">
                                      <div className="flex items-center justify-between">
                                        <span className={`text-[11px] font-medium ${showAddFleetRoute === 'kong' ? 'text-blue-400' : 'text-purple-400'}`}>
                                          New {showAddFleetRoute === 'kong' ? 'API' : 'Web'} Route
                                        </span>
                                        <button onClick={() => setShowAddFleetRoute(null)} className="p-0.5 rounded hover:bg-jpmc-hover text-jpmc-muted"><X size={10} /></button>
                                      </div>
                                      {/* Context Path */}
                                      <div>
                                        <label className="text-[9px] text-jpmc-muted">Context Path</label>
                                        <input className="bg-jpmc-navy/50 border border-jpmc-border/50 rounded px-2 py-1 text-xs text-white w-full" placeholder="/my-route"
                                          value={fleetRouteForm.context_path}
                                          onChange={e => setFleetRouteForm({...fleetRouteForm, context_path: e.target.value})} />
                                      </div>
                                      {/* Destination toggle */}
                                      <div>
                                        <label className="text-[9px] text-jpmc-muted mb-1 block">Destination</label>
                                        <div className="flex items-center gap-2 mb-1.5">
                                          {['backend', 'lambda'].map(dt => (
                                            <button key={dt} onClick={() => setFleetRouteForm({...fleetRouteForm, destination_type: dt})}
                                              className={`px-2 py-0.5 rounded text-[10px] border transition-all ${
                                                fleetRouteForm.destination_type === dt
                                                  ? 'border-blue-500/50 bg-blue-500/10 text-blue-400'
                                                  : 'border-jpmc-border/30 text-jpmc-muted hover:border-jpmc-border/60'
                                              }`}>
                                              {dt === 'backend' ? 'Backend' : 'Lambda'}
                                            </button>
                                          ))}
                                        </div>
                                        {fleetRouteForm.destination_type === 'backend' ? (
                                          <input className="bg-jpmc-navy/50 border border-jpmc-border/50 rounded px-2 py-1 text-xs text-white font-mono w-full"
                                            placeholder={showAddFleetRoute === 'kong' ? 'http://svc-api:8005' : 'http://svc-web:8004'}
                                            value={fleetRouteForm.backend_url}
                                            onChange={e => setFleetRouteForm({...fleetRouteForm, backend_url: e.target.value})} />
                                        ) : (
                                          <div className="space-y-1.5">
                                            <div className="flex flex-wrap gap-1">
                                              {LAMBDA_TEMPLATES.map(t => (
                                                <button key={t.name} type="button"
                                                  onClick={() => setFleetRouteForm({...fleetRouteForm, function_code: t.code})}
                                                  className="px-1.5 py-0.5 rounded border border-jpmc-border/40 text-[9px] text-jpmc-muted hover:bg-jpmc-hover hover:text-jpmc-text transition-colors">
                                                  {t.name}
                                                </button>
                                              ))}
                                            </div>
                                            <textarea
                                              className="w-full p-2 bg-[#0d1117] border border-jpmc-border/40 rounded-lg text-[10px] font-mono text-green-400 resize-y focus:outline-none focus:border-blue-500/50"
                                              style={{ minHeight: '8rem' }}
                                              spellCheck={false}
                                              value={fleetRouteForm.function_code}
                                              onChange={e => setFleetRouteForm({...fleetRouteForm, function_code: e.target.value})}
                                            />
                                          </div>
                                        )}
                                      </div>
                                      {/* Audience */}
                                      <div>
                                        <label className="text-[9px] text-jpmc-muted">Audience</label>
                                        <input className="bg-jpmc-navy/50 border border-jpmc-border/50 rounded px-2 py-1 text-xs text-white w-full" placeholder="e.g. jpmm"
                                          value={fleetRouteForm.audience}
                                          onChange={e => setFleetRouteForm({...fleetRouteForm, audience: e.target.value})} />
                                        <p className="text-[8px] text-jpmc-muted mt-0.5">JWT aud claim. Leave empty for unauthenticated.</p>
                                      </div>
                                      {/* Methods */}
                                      <div>
                                        <label className="text-[9px] text-jpmc-muted">Methods</label>
                                        <div className="flex flex-wrap gap-1 mt-0.5">
                                          {['GET', 'POST', 'PUT', 'DELETE'].map(m => (
                                            <button key={m} onClick={() => {
                                              const methods = fleetRouteForm.methods.includes(m)
                                                ? fleetRouteForm.methods.filter(x => x !== m)
                                                : [...fleetRouteForm.methods, m]
                                              setFleetRouteForm({...fleetRouteForm, methods})
                                            }}
                                              className={`px-1.5 py-0.5 rounded text-[9px] border transition-all ${
                                                fleetRouteForm.methods.includes(m)
                                                  ? 'border-blue-500/50 bg-blue-500/10 text-blue-400'
                                                  : 'border-jpmc-border/30 text-jpmc-muted'
                                              }`}>{m}</button>
                                          ))}
                                        </div>
                                      </div>
                                      {/* ── Envoy-specific config ── */}
                                      {showAddFleetRoute === 'envoy' && (
                                        <div className="pt-2 border-t border-jpmc-border/30 space-y-2.5">
                                          <div className="text-[9px] font-semibold uppercase tracking-wider text-purple-400/70">Envoy Configuration</div>
                                          {/* Timeout */}
                                          <div className="grid grid-cols-2 gap-2">
                                            <div>
                                              <label className="text-[9px] text-jpmc-muted">Timeout (ms)</label>
                                              <input type="number" min="0" step="1000"
                                                className="bg-jpmc-navy/50 border border-jpmc-border/50 rounded px-2 py-1 text-xs text-white w-full"
                                                value={fleetRouteForm.timeout_ms}
                                                onChange={e => setFleetRouteForm({...fleetRouteForm, timeout_ms: e.target.value})} />
                                            </div>
                                            <div>
                                              <label className="text-[9px] text-jpmc-muted">Retry Count</label>
                                              <input type="number" min="0" max="10"
                                                className="bg-jpmc-navy/50 border border-jpmc-border/50 rounded px-2 py-1 text-xs text-white w-full"
                                                value={fleetRouteForm.retry_count}
                                                onChange={e => setFleetRouteForm({...fleetRouteForm, retry_count: e.target.value})} />
                                            </div>
                                          </div>
                                          {/* Retry On */}
                                          <div>
                                            <label className="text-[9px] text-jpmc-muted block mb-1">Retry On</label>
                                            <div className="flex flex-wrap gap-1">
                                              {['5xx', 'gateway-error', 'connect-failure', 'reset', 'retriable-4xx'].map(cond => (
                                                <button key={cond} onClick={() => {
                                                  const retry_on = fleetRouteForm.retry_on.includes(cond)
                                                    ? fleetRouteForm.retry_on.filter(x => x !== cond)
                                                    : [...fleetRouteForm.retry_on, cond]
                                                  setFleetRouteForm({...fleetRouteForm, retry_on})
                                                }}
                                                  className={`px-1.5 py-0.5 rounded text-[9px] border transition-all ${
                                                    fleetRouteForm.retry_on.includes(cond)
                                                      ? 'border-purple-500/50 bg-purple-500/10 text-purple-400'
                                                      : 'border-jpmc-border/30 text-jpmc-muted hover:border-jpmc-border/60'
                                                  }`}>{cond}</button>
                                              ))}
                                            </div>
                                          </div>
                                          {/* CORS */}
                                          <div>
                                            <div className="flex items-center gap-2 mb-1">
                                              <label className="text-[9px] text-jpmc-muted">CORS</label>
                                              <button onClick={() => setFleetRouteForm({...fleetRouteForm, cors_enabled: !fleetRouteForm.cors_enabled})}
                                                className={`w-7 h-3.5 rounded-full border transition-all relative ${
                                                  fleetRouteForm.cors_enabled ? 'bg-purple-500/40 border-purple-500/60' : 'bg-jpmc-navy/60 border-jpmc-border/40'
                                                }`}>
                                                <span className={`absolute top-0.5 w-2.5 h-2.5 rounded-full transition-all ${
                                                  fleetRouteForm.cors_enabled ? 'left-3.5 bg-purple-400' : 'left-0.5 bg-jpmc-muted'
                                                }`} />
                                              </button>
                                              <span className={`text-[9px] ${fleetRouteForm.cors_enabled ? 'text-purple-400' : 'text-jpmc-muted'}`}>
                                                {fleetRouteForm.cors_enabled ? 'Enabled' : 'Disabled'}
                                              </span>
                                            </div>
                                            {fleetRouteForm.cors_enabled && (
                                              <input className="bg-jpmc-navy/50 border border-jpmc-border/50 rounded px-2 py-1 text-xs text-white w-full"
                                                placeholder="Allowed origins (e.g. * or https://app.jpm.com)"
                                                value={fleetRouteForm.cors_origins}
                                                onChange={e => setFleetRouteForm({...fleetRouteForm, cors_origins: e.target.value})} />
                                            )}
                                          </div>
                                          {/* Rate Limit + Priority */}
                                          <div className="grid grid-cols-2 gap-2">
                                            <div>
                                              <label className="text-[9px] text-jpmc-muted">Rate Limit RPS <span className="opacity-50">(0=off)</span></label>
                                              <input type="number" min="0"
                                                className="bg-jpmc-navy/50 border border-jpmc-border/50 rounded px-2 py-1 text-xs text-white w-full"
                                                value={fleetRouteForm.rate_limit_rps}
                                                onChange={e => setFleetRouteForm({...fleetRouteForm, rate_limit_rps: e.target.value})} />
                                            </div>
                                            <div>
                                              <label className="text-[9px] text-jpmc-muted">Priority</label>
                                              <div className="flex gap-1 mt-1">
                                                {[{v:0,l:'Normal'},{v:1,l:'High'}].map(({v,l}) => (
                                                  <button key={v} onClick={() => setFleetRouteForm({...fleetRouteForm, priority: v})}
                                                    className={`flex-1 px-1.5 py-0.5 rounded text-[9px] border transition-all ${
                                                      fleetRouteForm.priority === v
                                                        ? 'border-purple-500/50 bg-purple-500/10 text-purple-400'
                                                        : 'border-jpmc-border/30 text-jpmc-muted hover:border-jpmc-border/60'
                                                    }`}>{l}</button>
                                                ))}
                                              </div>
                                            </div>
                                          </div>
                                        </div>
                                      )}

                                      {/* ── Kong-specific config ── */}
                                      {showAddFleetRoute === 'kong' && (
                                        <div className="pt-2 border-t border-jpmc-border/30 space-y-2.5">
                                          <div className="text-[9px] font-semibold uppercase tracking-wider text-blue-400/70">Kong Configuration</div>
                                          {/* Strip Path */}
                                          <div className="flex items-center gap-2">
                                            <label className="text-[9px] text-jpmc-muted">Strip Path</label>
                                            <button onClick={() => setFleetRouteForm({...fleetRouteForm, strip_path: !fleetRouteForm.strip_path})}
                                              className={`w-7 h-3.5 rounded-full border transition-all relative ${
                                                fleetRouteForm.strip_path ? 'bg-blue-500/40 border-blue-500/60' : 'bg-jpmc-navy/60 border-jpmc-border/40'
                                              }`}>
                                              <span className={`absolute top-0.5 w-2.5 h-2.5 rounded-full transition-all ${
                                                fleetRouteForm.strip_path ? 'left-3.5 bg-blue-400' : 'left-0.5 bg-jpmc-muted'
                                              }`} />
                                            </button>
                                            <span className="text-[9px] text-jpmc-muted">Remove context path prefix before forwarding</span>
                                          </div>
                                          {/* Rate Limiting */}
                                          <div>
                                            <label className="text-[9px] text-jpmc-muted block mb-1">Rate Limiting <span className="opacity-50">(0=off)</span></label>
                                            <div className="grid grid-cols-3 gap-2">
                                              {[
                                                {key:'kong_rate_limit_rps',label:'Per Second'},
                                                {key:'kong_rate_limit_rpm',label:'Per Minute'},
                                                {key:'kong_rate_limit_rph',label:'Per Hour'},
                                              ].map(({key,label}) => (
                                                <div key={key}>
                                                  <label className="text-[8px] text-jpmc-muted">{label}</label>
                                                  <input type="number" min="0"
                                                    className="bg-jpmc-navy/50 border border-jpmc-border/50 rounded px-2 py-1 text-xs text-white w-full"
                                                    value={fleetRouteForm[key]}
                                                    onChange={e => setFleetRouteForm({...fleetRouteForm, [key]: e.target.value})} />
                                                </div>
                                              ))}
                                            </div>
                                          </div>
                                          {/* Upstream Timeouts */}
                                          <div>
                                            <label className="text-[9px] text-jpmc-muted block mb-1">Upstream Timeouts (ms)</label>
                                            <div className="grid grid-cols-3 gap-2">
                                              {[
                                                {key:'upstream_connect_timeout_ms',label:'Connect'},
                                                {key:'upstream_read_timeout_ms',label:'Read'},
                                                {key:'upstream_write_timeout_ms',label:'Write'},
                                              ].map(({key,label}) => (
                                                <div key={key}>
                                                  <label className="text-[8px] text-jpmc-muted">{label}</label>
                                                  <input type="number" min="0" step="1000"
                                                    className="bg-jpmc-navy/50 border border-jpmc-border/50 rounded px-2 py-1 text-xs text-white w-full"
                                                    value={fleetRouteForm[key]}
                                                    onChange={e => setFleetRouteForm({...fleetRouteForm, [key]: e.target.value})} />
                                                </div>
                                              ))}
                                            </div>
                                          </div>
                                          {/* Plugins */}
                                          <div>
                                            <label className="text-[9px] text-jpmc-muted block mb-1">Plugins</label>
                                            <div className="flex flex-wrap gap-1">
                                              {['rate-limiting','cors','jwt','key-auth','oauth2','proxy-cache','request-transformer','response-transformer'].map(plugin => (
                                                <button key={plugin} onClick={() => {
                                                  const plugins = fleetRouteForm.plugins.includes(plugin)
                                                    ? fleetRouteForm.plugins.filter(x => x !== plugin)
                                                    : [...fleetRouteForm.plugins, plugin]
                                                  setFleetRouteForm({...fleetRouteForm, plugins})
                                                }}
                                                  className={`px-1.5 py-0.5 rounded text-[9px] border transition-all ${
                                                    fleetRouteForm.plugins.includes(plugin)
                                                      ? 'border-blue-500/50 bg-blue-500/10 text-blue-400'
                                                      : 'border-jpmc-border/30 text-jpmc-muted hover:border-jpmc-border/60'
                                                  }`}>{plugin}</button>
                                              ))}
                                            </div>
                                          </div>
                                        </div>
                                      )}

                                      {/* Error */}
                                      {fleetRouteError && (
                                        <div className="p-1.5 rounded bg-red-500/10 border border-red-500/30 text-[10px] text-red-400">
                                          {fleetRouteError}
                                        </div>
                                      )}
                                      {/* Actions */}
                                      <div className="flex gap-2">
                                        <button
                                          disabled={!fleetRouteForm.context_path}
                                          onClick={handleFleetRouteSubmit}
                                          className="btn-primary text-[10px] py-1 px-3 disabled:opacity-40">
                                          Deploy Route
                                        </button>
                                        <button onClick={() => setShowAddFleetRoute(null)}
                                          className="text-[10px] py-1 px-3 rounded border border-jpmc-border/40 text-jpmc-muted hover:bg-jpmc-hover">Cancel</button>
                                      </div>
                                    </div>
                                  )}
                                  </>
                                )
                              })()}
                            </>
                          ) : (
                            <div className="p-4 rounded-lg border-2 border-dashed border-jpmc-border/30 text-center">
                              <Monitor size={20} className="text-jpmc-muted mx-auto mb-2 opacity-50" />
                              <div className="text-xs text-jpmc-muted">Fleet not yet deployed -- click <span className="text-blue-400 font-medium">Deploy Node</span> to spin up gateway instances</div>
                            </div>
                          )}

                        </div>

                        {/* Unattached Routes */}
                        {!isControlPlane && currentFleetNodes.length === 0 && instances.length > 0 && (
                          <div className="mb-4">
                            <div className="flex items-center gap-2 mb-3">
                              <AlertTriangle size={12} className="text-amber-400" />
                              <span className="text-xs font-medium text-amber-400 uppercase tracking-wider">
                                Unattached Routes
                              </span>
                              <span className="text-[10px] font-normal text-amber-300/70 normal-case">(no nodes to serve these)</span>
                            </div>
                            <div className="space-y-1.5">
                              {instances.map(inst => (
                                <div key={inst.id}
                                  className="flex items-center gap-3 px-3 py-2 rounded-lg bg-amber-500/5 border border-dashed border-amber-500/30">
                                  <span className="w-1.5 h-1.5 rounded-full bg-slate-400 shrink-0" />
                                  <code className="text-xs text-blue-400 font-mono">{inst.context_path}</code>
                                  <ArrowRight size={10} className="text-jpmc-muted shrink-0" />
                                  <span className="text-[10px] text-jpmc-muted font-mono flex-1">{(inst.backend || '').replace('http://', '')}</span>
                                  <span className={`text-[9px] px-1.5 py-0.5 rounded ${
                                    (inst.gateway_type || 'envoy') === 'kong'
                                      ? 'bg-blue-500/10 text-blue-400 border border-blue-500/30'
                                      : 'bg-purple-500/10 text-purple-400 border border-purple-500/30'
                                  }`}>{inst.gateway_type || 'envoy'}</span>
                                  <span className="text-[9px] text-slate-400">unattached</span>
                                </div>
                              ))}
                            </div>
                          </div>
                        )}

                        {/* Fleet Topology -- hidden for CP fleets */}
                        {!isControlPlane && (
                        <div>
                          <div className="text-xs font-medium text-jpmc-muted uppercase tracking-wider mb-2">
                            Request Path
                            <span className="text-[10px] font-normal ml-2 normal-case">DNS -> CDN -> Perimeter -> Gateway -> Backend</span>
                          </div>
                          <FleetTopology fleet={fleet} nodes={isExpanded ? currentFleetNodes : []} />
                        </div>
                        )}
                      </div>
                    </motion.div>
                  )}
                </AnimatePresence>
              </div>
            </motion.div>
          )
          }

          return (
            <>
              {/* Data Plane Fleets grouped by LOB */}
              {sortedEntries.map(([lob, lobFleets]) => (
                <div key={lob}>
                  <div className="flex items-center gap-2 mb-3">
                    <h2 className="text-xs font-bold text-jpmc-muted uppercase tracking-widest">{lob}</h2>
                    <div className="flex-1 border-t border-jpmc-border/30" />
                    <span className="text-[10px] text-jpmc-muted">{lobFleets.length} fleet{lobFleets.length !== 1 ? 's' : ''}</span>
                  </div>
                  <div className="space-y-3">
                    {lobFleets.map((fleet, idx) => renderFleetCard(fleet, idx))}
                  </div>
                </div>
              ))}

              {/* Control Plane Fleets */}
              {controlPlane.length > 0 && (
                <div>
                  <div className="flex items-center gap-2 mb-3 mt-8">
                    <div className="flex items-center gap-2">
                      <h2 className="text-xs font-bold text-slate-400 uppercase tracking-widest">Control Plane</h2>
                      <span className="text-[8px] px-1.5 py-0.5 rounded-full bg-slate-500/15 border border-slate-500/30 text-slate-400 font-semibold uppercase tracking-wider">Infrastructure</span>
                    </div>
                    <div className="flex-1 border-t border-dashed border-slate-500/30" />
                    <span className="text-[10px] text-slate-500">{controlPlane.length} service{controlPlane.length !== 1 ? 's' : ''}</span>
                  </div>
                  <div className="space-y-2">
                    {controlPlane.map((fleet, idx) => renderFleetCard(fleet, idx))}
                  </div>
                </div>
              )}
            </>
          )
        })()}
      </div>
    </div>
  )
}
