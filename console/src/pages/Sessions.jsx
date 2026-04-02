import React, { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { motion } from 'framer-motion'
import { Key, Shield, UserCircle, Loader2, Clock, Ban, LogOut } from 'lucide-react'
import GlassCard from '../components/GlassCard'
import { useConfig } from '../context/ConfigContext'

function sessionState(s) {
  const now = Date.now() / 1000
  if (s.status === 'active' && s.expires_at > now) return 'active'
  // If expires_at is in the past the session ended naturally (expired or replaced by new login)
  // Only treat as explicitly revoked if it was killed before its natural expiry
  if (s.status === 'revoked' && s.expires_at > now) return 'revoked'
  return 'expired'
}

function StateTag({ state }) {
  if (state === 'active') return (
    <span className="flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-full font-semibold bg-emerald-500/15 border border-emerald-500/30 text-emerald-400">
      <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 shadow-[0_0_4px_rgba(52,211,153,0.8)]" />
      Active
    </span>
  )
  if (state === 'revoked') return (
    <span className="flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-full font-semibold bg-red-500/15 border border-red-500/30 text-red-400">
      <Ban size={9} />
      Revoked
    </span>
  )
  return (
    <span className="flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-full font-semibold bg-gray-500/15 border border-gray-500/30 text-gray-400">
      <LogOut size={9} />
      Expired
    </span>
  )
}

function fmtTime(ts) {
  if (!ts) return '--'
  return new Date(ts * 1000).toLocaleString()
}

function relativeExpiry(ts) {
  if (!ts) return ''
  const diff = ts - Date.now() / 1000
  if (diff <= 0) return 'Expired'
  const m = Math.floor(diff / 60)
  if (m < 60) return `${m}m remaining`
  return `${Math.floor(m / 60)}h ${m % 60}m remaining`
}

export default function Sessions() {
  const { AUTH_URL } = useConfig()
  const queryClient = useQueryClient()
  const [revoking, setRevoking] = useState(null)

  const { data: sessions = [] } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetch(`${AUTH_URL}/sessions`).then(r => r.json()).catch(() => []),
    refetchInterval: 5000,
  })

  const revoke = async (sid) => {
    setRevoking(sid)
    try {
      await fetch(`${AUTH_URL}/session/revoke/${sid}`, { method: 'POST' })
      queryClient.invalidateQueries({ queryKey: ['sessions'] })
    } catch {}
    setRevoking(null)
  }

  // Annotate and sort: active first, then revoked, then expired
  const sortOrder = { active: 0, revoked: 1, expired: 2 }
  const annotated = sessions
    .map(s => ({ ...s, _state: sessionState(s) }))
    .sort((a, b) => sortOrder[a._state] - sortOrder[b._state] || b.created_at - a.created_at)

  const activeCount  = annotated.filter(s => s._state === 'active').length
  const revokedCount = annotated.filter(s => s._state === 'revoked').length
  const expiredCount = annotated.filter(s => s._state === 'expired').length

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-bold text-white">Sessions</h1>
        <p className="text-sm text-jpmc-muted">Active and historical DPoP-bound sessions</p>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-3 gap-4">
        <GlassCard title="Active" icon={Shield} value={activeCount}
          subtitle="Currently valid" delay={0} className="border-emerald-500/20" />
        <GlassCard title="Revoked" icon={Ban} value={revokedCount}
          subtitle="Administratively blocked" delay={0.05} className={revokedCount > 0 ? 'border-red-500/20' : ''} />
        <GlassCard title="Expired" icon={Clock} value={expiredCount}
          subtitle="Naturally expired / logged out" delay={0.1} />
      </div>

      {/* Table */}
      {annotated.length === 0 ? (
        <GlassCard delay={0.15}>
          <div className="text-center py-12 text-jpmc-muted text-sm">
            No sessions. Sign in to create a session.
          </div>
        </GlassCard>
      ) : (
        <GlassCard>
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="text-jpmc-muted border-b border-jpmc-border/30">
                  <th className="text-left px-4 py-3 font-medium">User</th>
                  <th className="text-left px-4 py-3 font-medium">Roles</th>
                  <th className="text-left px-4 py-3 font-medium">State</th>
                  <th className="text-left px-4 py-3 font-medium">Created</th>
                  <th className="text-left px-4 py-3 font-medium">Expires</th>
                  <th className="text-left px-4 py-3 font-medium">SID</th>
                  <th className="px-4 py-3" />
                </tr>
              </thead>
              <tbody className="divide-y divide-jpmc-border/20">
                {annotated.map((s, idx) => {
                  const initials = s.name
                    ? s.name.split(' ').map(n => n[0]).join('').toUpperCase().slice(0, 2)
                    : '??'
                  const isActive = s._state === 'active'
                  const isRevoked = s._state === 'revoked'

                  return (
                    <motion.tr
                      key={s.sid}
                      initial={{ opacity: 0 }}
                      animate={{ opacity: 1 }}
                      transition={{ delay: idx * 0.02 }}
                      className={`transition-colors hover:bg-white/[0.02] ${!isActive ? 'opacity-60' : ''}`}
                    >
                      {/* User */}
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2.5">
                          <div className={`w-7 h-7 rounded-full flex items-center justify-center shrink-0 text-[10px] font-semibold text-white ${
                            isActive ? 'bg-gradient-to-br from-blue-500 to-indigo-600' : 'bg-jpmc-border'
                          }`}>
                            {initials}
                          </div>
                          <div>
                            <div className="text-jpmc-text font-medium">{s.name}</div>
                            <div className="text-jpmc-muted text-[10px]">{s.email}</div>
                          </div>
                        </div>
                      </td>

                      {/* Roles */}
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-1 flex-wrap">
                          {(s.roles || []).map(role => (
                            <span key={role} className="badge-blue text-[9px]">{role}</span>
                          ))}
                          {s.entity && <span className="badge-gray text-[9px]">{s.entity}</span>}
                        </div>
                      </td>

                      {/* State */}
                      <td className="px-4 py-3">
                        <StateTag state={s._state} />
                        {isRevoked && (
                          <div className="text-[9px] text-red-400/70 mt-1">Admin action</div>
                        )}
                      </td>

                      {/* Created */}
                      <td className="px-4 py-3 text-jpmc-muted whitespace-nowrap">
                        {fmtTime(s.created_at)}
                      </td>

                      {/* Expires */}
                      <td className="px-4 py-3 whitespace-nowrap">
                        <div className="text-jpmc-muted">{fmtTime(s.expires_at)}</div>
                        {isActive && (
                          <div className="text-[9px] text-emerald-400/70 mt-0.5">{relativeExpiry(s.expires_at)}</div>
                        )}
                      </td>

                      {/* SID */}
                      <td className="px-4 py-3">
                        <code className="text-[10px] text-jpmc-muted/60">{s.sid?.slice(0, 12)}…</code>
                      </td>

                      {/* Action */}
                      <td className="px-4 py-3 text-right">
                        {isActive && (
                          <motion.button
                            whileHover={{ scale: 1.05 }}
                            whileTap={{ scale: 0.95 }}
                            onClick={() => revoke(s.sid)}
                            disabled={revoking === s.sid}
                            className="btn-danger flex items-center gap-1.5 text-[10px] ml-auto"
                          >
                            {revoking === s.sid
                              ? <Loader2 size={11} className="animate-spin" />
                              : <Ban size={11} />}
                            {revoking === s.sid ? 'Revoking…' : 'Revoke'}
                          </motion.button>
                        )}
                      </td>
                    </motion.tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </GlassCard>
      )}
    </div>
  )
}
