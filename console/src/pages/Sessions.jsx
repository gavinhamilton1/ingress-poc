import React, { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { motion, AnimatePresence } from 'framer-motion'
import { Key, Shield, UserCircle, Loader2 } from 'lucide-react'
import GlassCard from '../components/GlassCard'
import StatusBadge from '../components/StatusBadge'
import { useConfig } from '../context/ConfigContext'

export default function Sessions() {
  const { AUTH_URL } = useConfig()
  const queryClient = useQueryClient()
  const [revoking, setRevoking] = useState(null)

  const { data: sessions = [] } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetch(`${AUTH_URL}/sessions`).then(r => r.json()).catch(() => []),
  })

  const revoke = async (sid) => {
    setRevoking(sid)
    try {
      await fetch(`${AUTH_URL}/session/revoke/${sid}`, { method: 'POST' })
      queryClient.invalidateQueries({ queryKey: ['sessions'] })
    } catch {}
    setRevoking(null)
  }

  const activeSessions = sessions.filter(s => s.status === 'active').length
  const revokedSessions = sessions.filter(s => s.status === 'revoked').length

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-bold text-white">Sessions</h1>
        <p className="text-sm text-jpmc-muted">Active and revoked DPoP-bound sessions</p>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <GlassCard title="Total Sessions" icon={Key} value={sessions.length} subtitle="All time" delay={0} />
        <GlassCard title="Active" icon={Shield} value={activeSessions} subtitle="Currently valid" delay={0.05} />
        <GlassCard title="Revoked" icon={UserCircle} value={revokedSessions} subtitle="Blocked sessions" delay={0.1} />
      </div>

      {/* Session Cards */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {sessions.map((s, idx) => {
          const initials = s.name
            ? s.name.split(' ').map(n => n[0]).join('').toUpperCase().slice(0, 2)
            : '??'
          const isActive = s.status === 'active'

          return (
            <motion.div
              key={s.sid}
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: idx * 0.03 }}
              className={`glass-card p-4 transition-all duration-200 ${
                isActive
                  ? 'border-emerald-500/20 hover:border-emerald-500/40'
                  : 'opacity-60 border-red-500/20'
              }`}
            >
              <div className="flex items-start gap-4">
                {/* Avatar */}
                <div className={`w-12 h-12 rounded-full flex items-center justify-center shrink-0 ${
                  isActive
                    ? 'bg-gradient-to-br from-blue-500 to-indigo-600 shadow-[0_0_12px_rgba(59,130,246,0.3)]'
                    : 'bg-jpmc-border'
                }`}>
                  <span className="text-white text-sm font-semibold">{initials}</span>
                </div>

                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 mb-1">
                    <span className="text-sm font-semibold text-white">{s.name}</span>
                    <StatusBadge status={s.status} />
                  </div>
                  <div className="text-xs text-jpmc-muted mb-2">{s.email}</div>

                  <div className="flex items-center gap-2 flex-wrap mb-2">
                    {(s.roles || []).map(role => (
                      <span key={role} className="badge-blue text-[10px]">{role}</span>
                    ))}
                    {s.entity && <span className="badge-gray text-[10px]">{s.entity}</span>}
                  </div>

                  <div className="flex items-center gap-3 text-[11px] text-jpmc-muted">
                    <span title={s.sid}>SID: {s.sid?.slice(0, 12)}...</span>
                    <span>
                      {s.created_at ? new Date(s.created_at * 1000).toLocaleString() : '--'}
                    </span>
                  </div>
                </div>

                {/* Actions */}
                <div className="shrink-0">
                  {isActive ? (
                    <motion.button
                      whileHover={{ scale: 1.05 }}
                      whileTap={{ scale: 0.95 }}
                      onClick={() => revoke(s.sid)}
                      disabled={revoking === s.sid}
                      className="btn-danger flex items-center gap-1.5 text-xs"
                      title="Next request from this session will be blocked"
                    >
                      {revoking === s.sid ? (
                        <Loader2 size={12} className="animate-spin" />
                      ) : null}
                      {revoking === s.sid ? 'Revoking...' : 'Revoke'}
                    </motion.button>
                  ) : (
                    <span className="badge-red text-[10px]">Session Revoked</span>
                  )}
                </div>
              </div>
            </motion.div>
          )
        })}
      </div>

      {sessions.length === 0 && (
        <GlassCard delay={0.15}>
          <div className="text-center py-12 text-jpmc-muted text-sm">
            No sessions. Sign in to create a session.
          </div>
        </GlassCard>
      )}
    </div>
  )
}
