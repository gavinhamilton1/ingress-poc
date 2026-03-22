import React, { useState, useCallback } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Plus, Search, X, ChevronDown, ChevronRight, Filter,
  CheckCircle2, AlertTriangle, ToggleLeft, ToggleRight,
  Trash2, Edit3, GitBranch, Info, Zap, ExternalLink, Loader2,
} from 'lucide-react'
import GlassCard from '../components/GlassCard'
import StatusBadge from '../components/StatusBadge'
import RouteDetailPanel from '../components/RouteDetailPanel'
import { useConfig } from '../context/ConfigContext'

import { useNavigate } from 'react-router-dom'

export default function RoutesPage() {
  const { API_URL, GATEWAY_URL } = useConfig()
  const navigate = useNavigate()
  const [testingRoute, setTestingRoute] = useState(null)
  const [testResult, setTestResult] = useState({})
  const queryClient = useQueryClient()
  const [showForm, setShowForm] = useState(false)
  const [search, setSearch] = useState('')
  const [filterGateway, setFilterGateway] = useState('')
  const [filterStatus, setFilterStatus] = useState('')
  const [filterTeam, setFilterTeam] = useState('')
  const [expandedRow, setExpandedRow] = useState(null)
  const [submitBanner, setSubmitBanner] = useState(false)
  const [errors, setErrors] = useState([])
  const [form, setForm] = useState({
    path: '', backend_url: '', auth_policy: 'authenticated', allowed_roles: '',
    gateway_type: 'kong', team: '', methods: 'GET,POST,PUT,DELETE',
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

  const refreshData = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: ['routes'] })
    queryClient.invalidateQueries({ queryKey: ['actuals'] })
    queryClient.invalidateQueries({ queryKey: ['audit-log'] })
  }, [queryClient])

  const getDriftStatus = (routeId) => {
    const actual = actuals.find(a => a.route_id === routeId)
    if (!actual) return 'unknown'
    if (actual.drift) return 'drifted'
    return 'in sync'
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    setErrors([])
    const data = {
      ...form,
      allowed_roles: form.allowed_roles ? form.allowed_roles.split(',').map(s => s.trim()) : [],
      methods: form.methods.split(',').map(s => s.trim()),
    }
    try {
      const r = await fetch(`${API_URL}/routes`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data),
      })
      if (r.ok) {
        setShowForm(false)
        setSubmitBanner(true)
        setTimeout(() => setSubmitBanner(false), 5000)
        setForm({ path: '', backend_url: '', auth_policy: 'authenticated', allowed_roles: '', gateway_type: 'kong', team: '', methods: 'GET,POST,PUT,DELETE' })
        refreshData()
      } else {
        const err = await r.json()
        setErrors(err.detail?.violations || err.violations || [err.detail || 'Error'])
      }
    } catch (err) { setErrors([err.message]) }
  }

  const toggleStatus = async (route) => {
    const newStatus = route.status === 'active' ? 'inactive' : 'active'
    await fetch(`${API_URL}/routes/${route.id}/status`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status: newStatus }),
    })
    refreshData()
  }

  const deleteRoute = async (route) => {
    await fetch(`${API_URL}/routes/${route.id}`, { method: 'DELETE' })
    refreshData()
  }

  const quickTest = async (route) => {
    setTestingRoute(route.id)
    setTestResult(prev => ({ ...prev, [route.id]: null }))
    try {
      const start = Date.now()
      const r = await fetch(`${GATEWAY_URL}${route.path}`, {
        method: 'GET',
        headers: { 'User-Agent': 'ingress-console-test' },
        signal: AbortSignal.timeout(10000),
      })
      const latency = Date.now() - start
      setTestResult(prev => ({
        ...prev,
        [route.id]: { status: r.status, ok: r.ok, latency },
      }))
    } catch (err) {
      setTestResult(prev => ({
        ...prev,
        [route.id]: { status: 0, ok: false, latency: 0, error: err.message },
      }))
    }
    setTestingRoute(null)
  }

  const teams = [...new Set(routes.map(r => r.team).filter(Boolean))]

  const filtered = routes.filter(r => {
    if (search && !r.path.toLowerCase().includes(search.toLowerCase()) && !r.backend_url?.toLowerCase().includes(search.toLowerCase())) return false
    if (filterGateway && r.gateway_type !== filterGateway) return false
    if (filterStatus && r.status !== filterStatus) return false
    if (filterTeam && r.team !== filterTeam) return false
    return true
  })

  return (
    <div className="space-y-4">
      {/* GitOps Banner */}
      <AnimatePresence>
        {submitBanner && (
          <motion.div
            initial={{ opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: 'auto' }}
            exit={{ opacity: 0, height: 0 }}
            className="p-3 rounded-lg bg-amber-500/10 border border-amber-500/30 text-amber-300 text-sm flex items-center gap-2"
          >
            <GitBranch size={16} />
            In production this change would be committed to Bitbucket and applied by ArgoCD.
            In this POC the Management API writes directly to the gateway control plane.
          </motion.div>
        )}
      </AnimatePresence>

      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-white">Routes</h1>
          <p className="text-sm text-jpmc-muted">{routes.length} routes configured</p>
        </div>
        <button onClick={() => setShowForm(!showForm)} className="btn-primary flex items-center gap-2">
          {showForm ? <X size={14} /> : <Plus size={14} />}
          {showForm ? 'Cancel' : 'Add Route'}
        </button>
      </div>

      {/* Search & Filters */}
      <div className="flex items-center gap-3 flex-wrap">
        <div className="relative flex-1 min-w-[200px]">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-jpmc-muted" />
          <input
            className="input-field pl-9"
            placeholder="Search routes..."
            value={search}
            onChange={e => setSearch(e.target.value)}
          />
        </div>
        <select className="select-field w-auto min-w-[120px]" value={filterGateway} onChange={e => setFilterGateway(e.target.value)}>
          <option value="">All Gateways</option>
          <option value="kong">Kong</option>
          <option value="envoy">Envoy</option>
        </select>
        <select className="select-field w-auto min-w-[120px]" value={filterStatus} onChange={e => setFilterStatus(e.target.value)}>
          <option value="">All Status</option>
          <option value="active">Active</option>
          <option value="inactive">Inactive</option>
        </select>
        {teams.length > 0 && (
          <select className="select-field w-auto min-w-[120px]" value={filterTeam} onChange={e => setFilterTeam(e.target.value)}>
            <option value="">All Teams</option>
            {teams.map(t => <option key={t} value={t}>{t}</option>)}
          </select>
        )}
      </div>

      {/* Slide-over Form */}
      <AnimatePresence>
        {showForm && (
          <>
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              className="fixed inset-0 bg-black/40 z-40"
              onClick={() => setShowForm(false)}
            />
            <motion.div
              initial={{ x: '100%' }}
              animate={{ x: 0 }}
              exit={{ x: '100%' }}
              transition={{ type: 'spring', damping: 30, stiffness: 300 }}
              className="fixed right-0 top-0 h-screen w-full max-w-md bg-jpmc-dark border-l border-jpmc-border z-50 overflow-y-auto"
            >
              <div className="p-6">
                <div className="flex items-center justify-between mb-6">
                  <h2 className="text-lg font-bold text-white">Create Route</h2>
                  <button onClick={() => setShowForm(false)} className="p-1.5 rounded-md hover:bg-jpmc-hover text-jpmc-muted hover:text-white transition-colors">
                    <X size={18} />
                  </button>
                </div>

                {/* GitOps notice */}
                <div className="p-3 rounded-lg bg-amber-500/10 border border-amber-500/30 mb-6">
                  <div className="flex items-center gap-2 text-amber-400 text-xs font-medium mb-1">
                    <Info size={12} />
                    GitOps POC Mode
                  </div>
                  <p className="text-[11px] text-amber-300/70">
                    Changes write directly to the gateway. In production this would go through a Git commit workflow.
                  </p>
                </div>

                <form onSubmit={handleSubmit} className="space-y-4">
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Path</label>
                    <input className="input-field" placeholder="/api/new-endpoint" value={form.path} onChange={e => setForm({ ...form, path: e.target.value })} />
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Backend URL</label>
                    <input className="input-field" placeholder="http://svc-api:8005" value={form.backend_url} onChange={e => setForm({ ...form, backend_url: e.target.value })} />
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Auth Policy</label>
                      <select className="select-field" value={form.auth_policy} onChange={e => setForm({ ...form, auth_policy: e.target.value })}>
                        <option value="public">Public</option>
                        <option value="authenticated">Authenticated</option>
                        <option value="roles">Roles</option>
                      </select>
                    </div>
                    <div>
                      <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Gateway</label>
                      <select className="select-field" value={form.gateway_type} onChange={e => setForm({ ...form, gateway_type: e.target.value })}>
                        <option value="kong">Kong</option>
                        <option value="envoy">Envoy</option>
                      </select>
                    </div>
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Team</label>
                    <input className="input-field" placeholder="platform-team" value={form.team} onChange={e => setForm({ ...form, team: e.target.value })} />
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Allowed Roles (comma-separated)</label>
                    <input className="input-field" placeholder="admin, trader" value={form.allowed_roles} onChange={e => setForm({ ...form, allowed_roles: e.target.value })} />
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Methods</label>
                    <input className="input-field" value={form.methods} onChange={e => setForm({ ...form, methods: e.target.value })} />
                  </div>

                  <AnimatePresence>
                    {errors.length > 0 && (
                      <motion.div
                        initial={{ opacity: 0, height: 0 }}
                        animate={{ opacity: 1, height: 'auto' }}
                        exit={{ opacity: 0, height: 0 }}
                        className="p-3 rounded-lg bg-red-500/10 border border-red-500/30"
                      >
                        {errors.map((e, i) => <div key={i} className="text-red-400 text-sm">{typeof e === 'string' ? e : JSON.stringify(e)}</div>)}
                      </motion.div>
                    )}
                  </AnimatePresence>

                  <button type="submit" className="btn-primary w-full py-3">
                    Create Route
                  </button>
                </form>
              </div>
            </motion.div>
          </>
        )}
      </AnimatePresence>

      {/* Routes Table */}
      <GlassCard delay={0.1}>
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="border-b border-jpmc-border/50">
                {['Sync', 'Path', 'Test URL', 'Auth', 'Gateway', 'Team', 'Status', 'Actions'].map(h => (
                  <th key={h} className="text-left px-4 py-3 text-[11px] font-medium text-jpmc-muted uppercase tracking-wider">{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {filtered.map((route, idx) => {
                const driftStatus = getDriftStatus(route.id)
                const isExpanded = expandedRow === route.id
                return (
                  <React.Fragment key={route.id}>
                    <motion.tr
                      initial={{ opacity: 0 }}
                      animate={{ opacity: 1 }}
                      transition={{ delay: idx * 0.02 }}
                      className={`border-b border-jpmc-border/30 cursor-pointer hover:bg-jpmc-hover/50 transition-colors ${isExpanded ? 'bg-jpmc-hover/30' : ''}`}
                      onClick={() => setExpandedRow(isExpanded ? null : route.id)}
                    >
                      <td className="px-4 py-3">
                        {driftStatus === 'in sync' && <CheckCircle2 size={14} className="text-emerald-400" />}
                        {driftStatus === 'drifted' && <AlertTriangle size={14} className="text-amber-400 animate-pulse-slow" />}
                        {driftStatus === 'unknown' && <span className="w-3 h-3 rounded-full bg-gray-500 inline-block" />}
                      </td>
                      <td className="px-4 py-3">
                        <code className="text-sm text-blue-400">{route.path}</code>
                      </td>
                      <td className="px-4 py-3">
                        {(() => {
                          const h = route.hostname && route.hostname !== '*' ? route.hostname : null
                          const testBase = h ? `https://${h}` : GATEWAY_URL
                          const displayBase = h || GATEWAY_URL.replace('http://', '')
                          return (
                            <>
                              <a
                                href={`${testBase}${route.path}`}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="text-xs text-blue-400 hover:text-blue-300 font-mono flex items-center gap-1 group"
                                onClick={e => e.stopPropagation()}
                              >
                                {displayBase}{route.path}
                                <ExternalLink size={10} className="opacity-0 group-hover:opacity-100 transition-opacity" />
                              </a>
                              <div className="text-[10px] text-jpmc-muted/60 font-mono mt-0.5">
                                → {route.backend_url}
                              </div>
                            </>
                          )
                        })()}
                      </td>
                      <td className="px-4 py-3">
                        <span className={`badge ${
                          route.auth_policy === 'public' ? 'badge-green' :
                          route.auth_policy === 'authenticated' ? 'badge-blue' : 'badge-amber'
                        }`}>
                          {route.auth_policy}
                        </span>
                      </td>
                      <td className="px-4 py-3">
                        <span className={`badge ${route.gateway_type === 'kong' ? 'badge-blue' : 'badge-gray'}`}>
                          {route.gateway_type}
                        </span>
                      </td>
                      <td className="px-4 py-3 text-xs text-jpmc-muted">{route.team || '--'}</td>
                      <td className="px-4 py-3">
                        <StatusBadge status={route.status} />
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-1" onClick={e => e.stopPropagation()}>
                          {/* Quick test */}
                          <button
                            onClick={() => quickTest(route)}
                            disabled={testingRoute === route.id}
                            className="p-1.5 rounded-md hover:bg-cyan-500/10 text-jpmc-muted hover:text-cyan-400 transition-colors disabled:opacity-50"
                            title="Quick test (GET)"
                          >
                            {testingRoute === route.id
                              ? <Loader2 size={14} className="animate-spin text-cyan-400" />
                              : <Zap size={14} />
                            }
                          </button>
                          {/* Test result indicator */}
                          {testResult[route.id] && (
                            <span className={`text-[10px] font-mono font-bold px-1.5 py-0.5 rounded ${
                              testResult[route.id].ok
                                ? 'bg-emerald-500/20 text-emerald-400'
                                : 'bg-red-500/20 text-red-400'
                            }`}>
                              {testResult[route.id].status || 'ERR'}
                              {testResult[route.id].latency ? ` ${testResult[route.id].latency}ms` : ''}
                            </span>
                          )}
                          {/* Open in Request Tester */}
                          <button
                            onClick={() => navigate(`/request-tester?path=${encodeURIComponent(route.path)}`)}
                            className="p-1.5 rounded-md hover:bg-blue-500/10 text-jpmc-muted hover:text-blue-400 transition-colors"
                            title="Open in Request Tester"
                          >
                            <ExternalLink size={14} />
                          </button>
                          {/* Toggle status */}
                          <button
                            onClick={() => toggleStatus(route)}
                            className="p-1.5 rounded-md hover:bg-jpmc-hover text-jpmc-muted hover:text-jpmc-text transition-colors"
                            title={route.status === 'active' ? 'Deactivate' : 'Activate'}
                          >
                            {route.status === 'active' ? <ToggleRight size={16} className="text-emerald-400" /> : <ToggleLeft size={16} />}
                          </button>
                          {/* Delete */}
                          <button
                            onClick={() => deleteRoute(route)}
                            className="p-1.5 rounded-md hover:bg-red-500/10 text-jpmc-muted hover:text-red-400 transition-colors"
                            title="Delete"
                          >
                            <Trash2 size={14} />
                          </button>
                        </div>
                      </td>
                    </motion.tr>
                    <AnimatePresence>
                      {isExpanded && (
                        <motion.tr
                          initial={{ opacity: 0 }}
                          animate={{ opacity: 1 }}
                          exit={{ opacity: 0 }}
                        >
                          <td colSpan={8} className="px-4 py-4 bg-jpmc-navy/30">
                            <RouteDetailPanel
                              route={route}
                              driftStatus={driftStatus}
                              auditEntries={auditLog.filter(a => a.detail?.includes(route.path))}
                              gatewayUrl={GATEWAY_URL}
                            />
                          </td>
                        </motion.tr>
                      )}
                    </AnimatePresence>
                  </React.Fragment>
                )
              })}
            </tbody>
          </table>
          {filtered.length === 0 && (
            <div className="text-center py-12 text-jpmc-muted text-sm">
              {routes.length === 0 ? 'No routes configured yet.' : 'No routes match your filters.'}
            </div>
          )}
        </div>
      </GlassCard>
    </div>
  )
}
