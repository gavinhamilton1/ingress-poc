import React, { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { motion, AnimatePresence } from 'framer-motion'
import {
  AlertTriangle, CheckCircle2, RefreshCw, Clock,
  ArrowRight, ChevronDown, Loader2, Shield,
} from 'lucide-react'
import GlassCard from '../components/GlassCard'
import StatusBadge from '../components/StatusBadge'
import { useConfig } from '../context/ConfigContext'

export default function DriftDashboard() {
  const { API_URL } = useConfig()
  const queryClient = useQueryClient()
  const [showInSync, setShowInSync] = useState(false)
  const [reconciling, setReconciling] = useState(null)

  const { data: drift = [], isLoading, dataUpdatedAt } = useQuery({
    queryKey: ['drift'],
    queryFn: () => fetch(`${API_URL}/drift`).then(r => r.json()).catch(() => []),
    refetchInterval: 10000,
  })

  const driftedItems = drift.filter(d => d.drift)
  const syncItems = drift.filter(d => !d.drift)
  const lastCheckTime = dataUpdatedAt ? new Date(dataUpdatedAt) : null

  const reconcile = async (item) => {
    setReconciling(item.route_id)
    // In a real implementation, this would call the management API to reconcile
    // For POC, we just refresh the drift data
    await new Promise(r => setTimeout(r, 1000))
    queryClient.invalidateQueries({ queryKey: ['drift'] })
    setReconciling(null)
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-white">Drift Detection</h1>
          <p className="text-sm text-jpmc-muted">
            Monitor desired vs actual state across gateways
          </p>
        </div>
        <div className="flex items-center gap-3">
          {lastCheckTime && (
            <span className="text-xs text-jpmc-muted flex items-center gap-1.5">
              <Clock size={12} />
              Last checked: {lastCheckTime.toLocaleTimeString()}
            </span>
          )}
          <button
            onClick={() => queryClient.invalidateQueries({ queryKey: ['drift'] })}
            disabled={isLoading}
            className="btn-secondary flex items-center gap-2"
          >
            <RefreshCw size={14} className={isLoading ? 'animate-spin' : ''} />
            Refresh
          </button>
        </div>
      </div>

      {/* Summary Stats */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <GlassCard
          title="In Sync"
          icon={CheckCircle2}
          value={syncItems.length}
          subtitle="Routes matching desired state"
          delay={0}
          className="border-emerald-500/20"
        />
        <GlassCard
          title="Drifted"
          icon={AlertTriangle}
          value={driftedItems.length}
          subtitle={driftedItems.length > 0 ? 'Routes need attention' : 'No drift detected'}
          delay={0.05}
          className={driftedItems.length > 0 ? 'border-amber-500/30' : ''}
        />
        <GlassCard
          title="Last Check"
          icon={Clock}
          value={lastCheckTime ? lastCheckTime.toLocaleTimeString() : '--'}
          subtitle="Auto-refreshes every 10s"
          delay={0.1}
        />
      </div>

      {/* Info Banner */}
      <div className="p-3 rounded-lg bg-blue-500/10 border border-blue-500/30 flex items-start gap-3">
        <Shield size={16} className="text-blue-400 mt-0.5 shrink-0" />
        <p className="text-xs text-blue-300/80 leading-relaxed">
          Drift is detected when the Ingress Registry (desired state) does not match what the gateway
          is actually serving (actual state). The gateway control plane reconciles every 5 seconds --
          transient drift is expected during route changes.
        </p>
      </div>

      {/* Drifted Routes */}
      {driftedItems.length > 0 && (
        <div className="space-y-3">
          <h2 className="text-sm font-semibold text-amber-400 flex items-center gap-2">
            <AlertTriangle size={14} />
            Drifted Routes ({driftedItems.length})
          </h2>
          {driftedItems.map((item, idx) => (
            <motion.div
              key={item.route_id}
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: idx * 0.05 }}
              className="glass-card border-amber-500/30 overflow-hidden"
            >
              <div className="p-4">
                <div className="flex items-center justify-between mb-3">
                  <div className="flex items-center gap-3">
                    <div className="status-dot-drift" />
                    <code className="text-sm text-amber-400 font-medium">{item.path}</code>
                    <span className={`badge ${item.gateway_type === 'kong' ? 'badge-blue' : 'badge-gray'}`}>
                      {item.gateway_type}
                    </span>
                  </div>
                  <motion.button
                    whileHover={{ scale: 1.05 }}
                    whileTap={{ scale: 0.95 }}
                    onClick={() => reconcile(item)}
                    disabled={reconciling === item.route_id}
                    className="btn-primary flex items-center gap-2 text-xs"
                  >
                    {reconciling === item.route_id ? (
                      <Loader2 size={12} className="animate-spin" />
                    ) : (
                      <RefreshCw size={12} />
                    )}
                    {reconciling === item.route_id ? 'Reconciling...' : 'Reconcile'}
                  </motion.button>
                </div>

                {/* Side-by-side comparison */}
                <div className="grid grid-cols-2 gap-4">
                  <div className="p-3 rounded-lg bg-emerald-500/5 border border-emerald-500/20">
                    <div className="text-[10px] font-medium text-emerald-400 uppercase tracking-wider mb-2">
                      Desired State
                    </div>
                    <div className="space-y-1.5 text-xs">
                      <div className="flex justify-between">
                        <span className="text-jpmc-muted">Status</span>
                        <span className={item.desired_status === 'active' ? 'text-emerald-400' : 'text-red-400'}>
                          {item.desired_status}
                        </span>
                      </div>
                      <div className="flex justify-between">
                        <span className="text-jpmc-muted">Backend</span>
                        <span className="text-jpmc-text font-mono text-[11px]">{item.desired_backend}</span>
                      </div>
                    </div>
                  </div>
                  <div className="p-3 rounded-lg bg-red-500/5 border border-red-500/20">
                    <div className="text-[10px] font-medium text-red-400 uppercase tracking-wider mb-2">
                      Actual State
                    </div>
                    <div className="space-y-1.5 text-xs">
                      <div className="flex justify-between">
                        <span className="text-jpmc-muted">Status</span>
                        <span className={
                          item.actual_status === 'active' ? 'text-emerald-400' :
                          item.actual_status === 'absent' ? 'text-red-400' : 'text-jpmc-muted'
                        }>
                          {item.actual_status}
                        </span>
                      </div>
                      <div className="flex justify-between">
                        <span className="text-jpmc-muted">Backend</span>
                        <span className="text-jpmc-text font-mono text-[11px]">{item.actual_backend || '--'}</span>
                      </div>
                    </div>
                  </div>
                </div>

                {item.drift_detail && (
                  <div className="mt-3 text-xs text-amber-300/70">
                    {item.drift_detail}
                  </div>
                )}

                <div className="mt-3 text-[11px] text-jpmc-muted">
                  Last checked: {item.last_checked ? new Date(item.last_checked * 1000).toLocaleTimeString() : '--'}
                </div>
              </div>
            </motion.div>
          ))}
        </div>
      )}

      {/* In Sync Routes */}
      {syncItems.length > 0 && (
        <div>
          <button
            onClick={() => setShowInSync(!showInSync)}
            className="flex items-center gap-2 text-sm font-semibold text-emerald-400 hover:text-emerald-300 transition-colors mb-3"
          >
            <CheckCircle2 size={14} />
            Routes in Sync ({syncItems.length})
            <ChevronDown size={14} className={`transition-transform ${showInSync ? '' : '-rotate-90'}`} />
          </button>
          <AnimatePresence>
            {showInSync && (
              <motion.div
                initial={{ height: 0, opacity: 0 }}
                animate={{ height: 'auto', opacity: 1 }}
                exit={{ height: 0, opacity: 0 }}
                className="overflow-hidden"
              >
                <GlassCard>
                  <div className="divide-y divide-jpmc-border/30">
                    {syncItems.map(item => (
                      <div key={item.route_id} className="flex items-center gap-4 px-4 py-3">
                        <CheckCircle2 size={14} className="text-emerald-400 shrink-0" />
                        <code className="text-sm text-jpmc-text">{item.path}</code>
                        <span className={`badge ${item.gateway_type === 'kong' ? 'badge-blue' : 'badge-gray'}`}>
                          {item.gateway_type}
                        </span>
                        <span className="text-xs text-jpmc-muted ml-auto">
                          {item.last_checked ? new Date(item.last_checked * 1000).toLocaleTimeString() : ''}
                        </span>
                      </div>
                    ))}
                  </div>
                </GlassCard>
              </motion.div>
            )}
          </AnimatePresence>
        </div>
      )}

      {drift.length === 0 && !isLoading && (
        <GlassCard>
          <div className="text-center py-12 text-jpmc-muted text-sm">
            No drift data available. Make sure the Management API is running and routes are configured.
          </div>
        </GlassCard>
      )}
    </div>
  )
}
