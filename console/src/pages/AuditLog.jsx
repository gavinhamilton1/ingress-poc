import React, { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { motion } from 'framer-motion'
import { ClipboardList, RefreshCw, Clock, Search, Filter } from 'lucide-react'
import GlassCard from '../components/GlassCard'
import { useConfig } from '../context/ConfigContext'

const ACTION_STYLES = {
  CREATE:          { bg: 'bg-emerald-500/15', border: 'border-emerald-500/30', text: 'text-emerald-400' },
  DELETE:          { bg: 'bg-red-500/15',     border: 'border-red-500/30',     text: 'text-red-400' },
  GIT_DELETED:     { bg: 'bg-orange-500/15',  border: 'border-orange-500/30',  text: 'text-orange-400' },
  UPDATE:          { bg: 'bg-blue-500/15',     border: 'border-blue-500/30',    text: 'text-blue-400' },
  NODE_STARTED:    { bg: 'bg-emerald-500/15', border: 'border-emerald-500/30', text: 'text-emerald-400' },
  NODE_STOPPED:    { bg: 'bg-amber-500/15',   border: 'border-amber-500/30',   text: 'text-amber-400' },
  FLEET_SUSPENDED: { bg: 'bg-amber-500/15',   border: 'border-amber-500/30',   text: 'text-amber-400' },
  FLEET_RESUMED:   { bg: 'bg-emerald-500/15', border: 'border-emerald-500/30', text: 'text-emerald-400' },
  RECONCILE:       { bg: 'bg-purple-500/15',  border: 'border-purple-500/30',  text: 'text-purple-400' },
}

function actionStyle(action) {
  return ACTION_STYLES[action] || { bg: 'bg-gray-500/15', border: 'border-gray-500/30', text: 'text-gray-400' }
}

function formatTs(ts) {
  if (!ts) return '--'
  const d = new Date(ts * 1000)
  return d.toLocaleString()
}

function relativeTs(ts) {
  if (!ts) return ''
  const diff = Math.floor((Date.now() / 1000) - ts)
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}

export default function AuditLog() {
  const { API_URL } = useConfig()
  const queryClient = useQueryClient()
  const [search, setSearch] = useState('')
  const [actionFilter, setActionFilter] = useState('all')

  const { data: logs = [], isLoading, dataUpdatedAt } = useQuery({
    queryKey: ['audit-log'],
    queryFn: () => fetch(`${API_URL}/audit-log`).then(r => r.json()).catch(() => []),
    refetchInterval: 15000,
  })

  const allActions = ['all', ...Array.from(new Set(logs.map(l => l.action))).sort()]

  const filtered = logs.filter(l => {
    const matchesAction = actionFilter === 'all' || l.action === actionFilter
    const q = search.toLowerCase()
    const matchesSearch = !q ||
      l.action?.toLowerCase().includes(q) ||
      l.actor?.toLowerCase().includes(q) ||
      l.detail?.toLowerCase().includes(q) ||
      l.route_id?.toLowerCase().includes(q)
    return matchesAction && matchesSearch
  })

  const lastCheckTime = dataUpdatedAt ? new Date(dataUpdatedAt) : null

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-white">Audit Log</h1>
          <p className="text-sm text-jpmc-muted">All route, fleet and node actions</p>
        </div>
        <div className="flex items-center gap-3">
          {lastCheckTime && (
            <span className="text-xs text-jpmc-muted flex items-center gap-1.5">
              <Clock size={12} />
              {lastCheckTime.toLocaleTimeString()}
            </span>
          )}
          <button
            onClick={() => queryClient.invalidateQueries({ queryKey: ['audit-log'] })}
            disabled={isLoading}
            className="btn-secondary flex items-center gap-2"
          >
            <RefreshCw size={14} className={isLoading ? 'animate-spin' : ''} />
            Refresh
          </button>
        </div>
      </div>

      {/* Summary stats */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
        <GlassCard title="Total Events" icon={ClipboardList} value={logs.length} subtitle="Last 100 entries" delay={0} />
        <GlassCard title="Creates" icon={ClipboardList}
          value={logs.filter(l => l.action === 'CREATE').length}
          subtitle="Routes created" delay={0.05} className="border-emerald-500/20" />
        <GlassCard title="Deletes" icon={ClipboardList}
          value={logs.filter(l => l.action === 'DELETE' || l.action === 'GIT_DELETED').length}
          subtitle="Routes removed" delay={0.1} className="border-red-500/20" />
        <GlassCard title="Node Events" icon={ClipboardList}
          value={logs.filter(l => l.action?.startsWith('NODE_') || l.action?.startsWith('FLEET_')).length}
          subtitle="Start / stop events" delay={0.15} />
      </div>

      {/* Filters */}
      <div className="flex items-center gap-3 flex-wrap">
        <div className="relative flex-1 min-w-48">
          <Search size={13} className="absolute left-3 top-1/2 -translate-y-1/2 text-jpmc-muted" />
          <input
            type="text"
            placeholder="Search actor, detail, ID…"
            value={search}
            onChange={e => setSearch(e.target.value)}
            className="w-full pl-8 pr-3 py-1.5 text-xs bg-jpmc-card border border-jpmc-border rounded-lg text-jpmc-text placeholder-jpmc-muted focus:outline-none focus:border-blue-500/50"
          />
        </div>
        <div className="flex items-center gap-2">
          <Filter size={13} className="text-jpmc-muted" />
          <select
            value={actionFilter}
            onChange={e => setActionFilter(e.target.value)}
            className="text-xs bg-jpmc-card border border-jpmc-border rounded-lg text-jpmc-text px-2 py-1.5 focus:outline-none focus:border-blue-500/50"
          >
            {allActions.map(a => (
              <option key={a} value={a}>{a === 'all' ? 'All actions' : a}</option>
            ))}
          </select>
        </div>
        {(search || actionFilter !== 'all') && (
          <button
            onClick={() => { setSearch(''); setActionFilter('all') }}
            className="text-xs text-jpmc-muted hover:text-jpmc-text transition-colors"
          >
            Clear
          </button>
        )}
        <span className="text-xs text-jpmc-muted ml-auto">{filtered.length} entries</span>
      </div>

      {/* Log entries */}
      {isLoading ? (
        <GlassCard>
          <div className="text-center py-12 text-jpmc-muted text-sm">Loading…</div>
        </GlassCard>
      ) : filtered.length === 0 ? (
        <GlassCard>
          <div className="text-center py-12 text-jpmc-muted text-sm">No log entries found.</div>
        </GlassCard>
      ) : (
        <GlassCard>
          <div className="divide-y divide-jpmc-border/20">
            {filtered.map((log, idx) => {
              const style = actionStyle(log.action)
              return (
                <motion.div
                  key={log.id}
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  transition={{ delay: Math.min(idx * 0.02, 0.3) }}
                  className="flex items-start gap-4 px-4 py-3 hover:bg-jpmc-hover/30 transition-colors"
                >
                  {/* Action badge */}
                  <span className={`mt-0.5 shrink-0 text-[9px] font-semibold uppercase tracking-wider px-2 py-0.5 rounded-full border ${style.bg} ${style.border} ${style.text}`}>
                    {log.action}
                  </span>

                  {/* Main content */}
                  <div className="flex-1 min-w-0">
                    <p className="text-xs text-jpmc-text leading-relaxed">{log.detail || '—'}</p>
                    <div className="flex items-center gap-3 mt-1">
                      <span className="text-[10px] text-jpmc-muted">
                        actor: <span className="text-jpmc-text/70">{log.actor || 'system'}</span>
                      </span>
                      {log.route_id && (
                        <span className="text-[10px] text-jpmc-muted font-mono">
                          {log.route_id.length > 20 ? log.route_id.slice(0, 8) + '…' : log.route_id}
                        </span>
                      )}
                    </div>
                  </div>

                  {/* Timestamp */}
                  <div className="shrink-0 text-right">
                    <p className="text-[10px] text-jpmc-muted">{relativeTs(log.ts)}</p>
                    <p className="text-[9px] text-jpmc-muted/60">{formatTs(log.ts)}</p>
                  </div>
                </motion.div>
              )
            })}
          </div>
        </GlassCard>
      )}
    </div>
  )
}
