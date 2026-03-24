import React, { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { motion } from 'framer-motion'
import { Database, ChevronDown, ChevronRight, RefreshCw, Table2, Search } from 'lucide-react'
import { useConfig } from '../context/ConfigContext'

const TABLES = [
  { key: 'routes', label: 'Routes', endpoint: '/routes', desc: '18 columns — desired route state in the ingress registry' },
  { key: 'actuals', label: 'Actual Routes', endpoint: '/actuals', desc: '9 columns — gateway-reported actual state for drift detection' },
  { key: 'fleets', label: 'Fleets', endpoint: '/fleets', desc: '13 columns — logical gateway groups serving subdomains' },
  { key: 'audit', label: 'Audit Log', endpoint: '/audit-log', desc: '6 columns — change history for all route operations' },
  { key: 'health', label: 'Health Reports', endpoint: '/health-reports', desc: '10 columns — gateway health probe results' },
  { key: 'drift', label: 'Drift Analysis', endpoint: '/drift', desc: '10 columns — joined view of desired vs actual with drift status' },
]

function formatValue(val) {
  if (val === null || val === undefined) return <span className="text-jpmc-muted/40">null</span>
  if (typeof val === 'boolean') return <span className={val ? 'text-emerald-400' : 'text-red-400'}>{val.toString()}</span>
  if (Array.isArray(val)) {
    if (val.length === 0) return <span className="text-jpmc-muted/40">[]</span>
    return (
      <span className="inline-flex flex-wrap gap-0.5">
        {val.map((v, i) => (
          <code key={i} className="px-1 py-0.5 rounded bg-blue-500/10 text-blue-400 text-[9px] border border-blue-500/20">{typeof v === 'object' ? JSON.stringify(v) : String(v)}</code>
        ))}
      </span>
    )
  }
  if (typeof val === 'object') {
    // Fleet instances nested array
    if (val.instances) return <span className="text-jpmc-muted text-[9px]">{val.instances.length} instances</span>
    return <code className="text-[9px] text-jpmc-muted">{JSON.stringify(val).slice(0, 60)}</code>
  }
  if (typeof val === 'number') {
    // Timestamps (epoch seconds > year 2000)
    if (val > 1_000_000_000 && val < 2_000_000_000) {
      return <span className="text-jpmc-muted" title={new Date(val * 1000).toISOString()}>{new Date(val * 1000).toLocaleString()}</span>
    }
    return <span className="text-cyan-400">{Number.isInteger(val) ? val : val.toFixed(1)}</span>
  }
  // Status strings
  const statusColors = {
    active: 'text-emerald-400', healthy: 'text-emerald-400', 'in sync': 'text-emerald-400',
    inactive: 'text-gray-400', suspended: 'text-amber-400', degraded: 'text-amber-400',
    warning: 'text-amber-400', drifted: 'text-amber-400',
    offline: 'text-red-400', unhealthy: 'text-red-400', absent: 'text-red-400',
  }
  const lower = String(val).toLowerCase()
  if (statusColors[lower]) return <span className={`font-medium ${statusColors[lower]}`}>{val}</span>
  if (String(val).length > 80) return <span className="text-jpmc-text" title={val}>{String(val).slice(0, 80)}...</span>
  return <span className="text-jpmc-text">{String(val)}</span>
}

function DataTable({ data, search }) {
  if (!data || data.length === 0) return <div className="text-sm text-jpmc-muted p-4">No data</div>

  // Flatten fleet instances for display
  const rows = data.map(row => {
    if (row.instances) {
      const { instances, ...rest } = row
      return { ...rest, instances_count_actual: instances.length }
    }
    return row
  })

  // Get all columns from first row
  const columns = Object.keys(rows[0])

  // Filter rows by search
  const filtered = search
    ? rows.filter(row => JSON.stringify(row).toLowerCase().includes(search.toLowerCase()))
    : rows

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-[11px]">
        <thead>
          <tr>
            {columns.map(col => (
              <th key={col} className="px-2 py-2 text-left text-[9px] font-bold text-jpmc-muted uppercase tracking-wider border-b border-jpmc-border/30 whitespace-nowrap bg-jpmc-navy/50 sticky top-0">
                {col.replace(/_/g, ' ')}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {filtered.map((row, i) => (
            <tr key={i} className="border-b border-jpmc-border/10 hover:bg-jpmc-hover/30 transition-colors">
              {columns.map(col => (
                <td key={col} className="px-2 py-1.5 whitespace-nowrap max-w-[300px] overflow-hidden">
                  {formatValue(row[col])}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <div className="px-2 py-1.5 text-[10px] text-jpmc-muted border-t border-jpmc-border/20">
        {filtered.length} row{filtered.length !== 1 ? 's' : ''}{search ? ` (filtered from ${rows.length})` : ''}
      </div>
    </div>
  )
}

export default function RawData() {
  const { API_URL } = useConfig()
  const [expanded, setExpanded] = useState('routes')
  const [search, setSearch] = useState('')
  const [refreshKey, setRefreshKey] = useState(0)

  const queries = {}
  for (const table of TABLES) {
    queries[table.key] = useQuery({
      queryKey: [table.key, refreshKey],
      queryFn: () => fetch(`${API_URL}${table.endpoint}`).then(r => r.json()).catch(() => []),
    })
  }

  const totalRows = TABLES.reduce((sum, t) => sum + (queries[t.key]?.data?.length || 0), 0)

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-white flex items-center gap-2">
            <Database size={20} className="text-blue-400" />
            Raw Data
          </h1>
          <p className="text-sm text-jpmc-muted">PostgreSQL registry — {TABLES.length} tables, {totalRows} total rows</p>
        </div>
        <div className="flex items-center gap-3">
          <div className="relative">
            <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-jpmc-muted" />
            <input className="input-field pl-9 text-sm w-64" placeholder="Search across all tables..."
              value={search} onChange={e => setSearch(e.target.value)} />
          </div>
          <button onClick={() => setRefreshKey(k => k + 1)}
            className="flex items-center gap-2 px-3 py-2 rounded-lg border border-jpmc-border/50 bg-jpmc-navy/50 text-sm text-jpmc-text hover:border-blue-500/30 hover:bg-blue-500/5 transition-all">
            <RefreshCw size={14} />
            Refresh
          </button>
        </div>
      </div>

      <div className="space-y-3">
        {TABLES.map((table, idx) => {
          const isExpanded = expanded === table.key
          const query = queries[table.key]
          const rowCount = query?.data?.length || 0
          const colCount = query?.data?.[0] ? Object.keys(query.data[0]).length : 0

          return (
            <motion.div
              key={table.key}
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: idx * 0.03 }}
              className="glass-card overflow-hidden"
            >
              <div
                className="flex items-center gap-4 p-4 cursor-pointer hover:bg-jpmc-hover/50 transition-colors"
                onClick={() => setExpanded(isExpanded ? null : table.key)}
              >
                <div className="w-9 h-9 rounded-lg bg-blue-500/15 border border-blue-500/30 flex items-center justify-center shrink-0">
                  <Table2 size={16} className="text-blue-400" />
                </div>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <h3 className="text-sm font-semibold text-white">{table.label}</h3>
                    <code className="text-[9px] text-jpmc-muted bg-jpmc-navy/50 px-1.5 py-0.5 rounded">{table.key}</code>
                  </div>
                  <p className="text-[11px] text-jpmc-muted">{table.desc}</p>
                </div>
                <div className="flex items-center gap-4 shrink-0">
                  <div className="text-right">
                    <div className="text-lg font-bold text-white">{rowCount}</div>
                    <div className="text-[10px] text-jpmc-muted">rows</div>
                  </div>
                  <div className="text-right">
                    <div className="text-lg font-bold text-jpmc-text">{colCount}</div>
                    <div className="text-[10px] text-jpmc-muted">cols</div>
                  </div>
                  {isExpanded
                    ? <ChevronDown size={16} className="text-jpmc-muted" />
                    : <ChevronRight size={16} className="text-jpmc-muted" />}
                </div>
              </div>

              {isExpanded && (
                <div className="border-t border-jpmc-border/30 max-h-[500px] overflow-auto">
                  {query?.isLoading ? (
                    <div className="p-4 text-sm text-jpmc-muted">Loading...</div>
                  ) : (
                    <DataTable data={query?.data || []} search={search} />
                  )}
                </div>
              )}
            </motion.div>
          )
        })}
      </div>
    </div>
  )
}
