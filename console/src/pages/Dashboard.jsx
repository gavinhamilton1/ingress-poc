import React from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { motion } from 'framer-motion'
import {
  Route, Key, Cpu, AlertTriangle, Clock, Zap,
  Activity, Plus, ArrowRight, CheckCircle2, XCircle, Globe,
  Shield, Server, Box, Cloud, ShieldCheck, Layers,
} from 'lucide-react'
import GlassCard from '../components/GlassCard'
import StatusBadge from '../components/StatusBadge'
import { useAuth } from '../context/AuthContext'
import { useConfig } from '../context/ConfigContext'

/* ───────────── Animated pulse flowing down a vertical connector ───────────── */
function FlowPulse({ delay = 0, color = 'bg-blue-400', height = 24, speed = 1.8, active = true }) {
  if (!active) return (
    <div className="flex justify-center" style={{ height }}>
      <div className="w-px border-l border-dashed border-jpmc-border/40 h-full" />
    </div>
  )
  return (
    <div className="flex justify-center relative overflow-hidden" style={{ height }}>
      <div className="w-px border-l border-dashed border-jpmc-border/40 h-full" />
      <motion.div
        className={`absolute w-1.5 h-1.5 rounded-full ${color}`}
        style={{ left: '50%', marginLeft: -3, filter: 'drop-shadow(0 0 6px currentColor)' }}
        animate={{ top: ['-6px', `${height + 4}px`] }}
        transition={{ duration: speed, repeat: Infinity, delay, ease: 'linear' }}
      />
    </div>
  )
}

/* ───────────── Infrastructure node ───────────── */
function InfraNode({ icon: Icon, label, desc, color, delay = 0, passive, alt, health, small }) {
  const opacity = passive ? 'opacity-50' : alt ? 'opacity-70' : 'opacity-100'
  const size = small ? 'w-10 h-10' : 'w-12 h-12'
  const iconSize = small ? 16 : 20
  const healthGlow = health === 'healthy'
    ? 'ring-2 ring-emerald-400/40'
    : health === 'degraded'
    ? 'ring-2 ring-amber-400/50'
    : health === 'offline'
    ? 'ring-2 ring-red-400/50'
    : ''
  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ delay }}
      className={`flex flex-col items-center gap-1 ${opacity}`}
    >
      <div className={`${size} rounded-xl bg-gradient-to-br ${color} flex items-center justify-center shadow-lg ${healthGlow}`}>
        <Icon size={iconSize} className="text-white" />
      </div>
      <div className="text-center">
        <div className="text-[10px] font-semibold text-jpmc-text leading-tight">{label}</div>
        <div className="text-[8px] text-jpmc-muted leading-tight">{desc}</div>
      </div>
    </motion.div>
  )
}

/* ───────────── Fleet card for L5 layer ───────────── */
function FleetNode({ fleet, delay = 0, onClick }) {
  const isHealthy = fleet.status === 'healthy'
  const isDegraded = fleet.status === 'degraded'
  const dotColor = isHealthy ? 'bg-emerald-400' : isDegraded ? 'bg-amber-400' : 'bg-red-400'
  const borderColor = isHealthy ? 'border-emerald-500/30' : isDegraded ? 'border-amber-500/30' : 'border-red-500/30'
  const bgColor = isHealthy ? 'bg-emerald-500/10' : isDegraded ? 'bg-amber-500/10' : 'bg-red-500/10'
  const textColor = isHealthy ? 'text-emerald-400' : isDegraded ? 'text-amber-400' : 'text-red-400'
  const instances = fleet.instances || []
  const envoyCount = instances.filter(i => (i.gateway_type || 'envoy') === 'envoy' || (!i.gateway_type && !i.context_path?.startsWith('/api'))).length
  const kongCount = instances.filter(i => i.gateway_type === 'kong' || (!i.gateway_type && i.context_path?.startsWith('/api'))).length

  const nodeParts = []
  if (envoyCount > 0) nodeParts.push(`${envoyCount} Envoy`)
  if (kongCount > 0) nodeParts.push(`${kongCount} Kong`)
  const nodeLabel = nodeParts.length > 0 ? nodeParts.join(' \u00b7 ') : 'No nodes'

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ delay }}
      onClick={onClick}
      className={onClick ? 'cursor-pointer' : ''}
    >
      <div className={`px-3 py-2 rounded-lg ${bgColor} border ${borderColor} flex flex-col items-center gap-0.5 min-w-[90px] hover:brightness-125 transition-all`}>
        <div className="flex items-center gap-1.5">
          <span className={`w-1.5 h-1.5 rounded-full ${dotColor} ${isDegraded ? 'animate-pulse' : ''}`} />
          <span className="text-[10px] font-semibold text-white leading-tight">{fleet.name}</span>
        </div>
        <div className="flex items-center gap-1 flex-wrap justify-center">
          {envoyCount > 0 && (
            <span className="text-[7px] px-1 py-px rounded bg-purple-500/20 text-purple-300 font-medium">{envoyCount} Envoy</span>
          )}
          {kongCount > 0 && (
            <span className="text-[7px] px-1 py-px rounded bg-blue-500/20 text-blue-300 font-medium">{kongCount} Kong</span>
          )}
          {envoyCount === 0 && kongCount === 0 && (
            <span className="text-[7px] text-jpmc-muted">No nodes</span>
          )}
        </div>
        <div className="text-[7px] text-jpmc-muted leading-tight truncate max-w-[100px]">{fleet.subdomain}</div>
      </div>
    </motion.div>
  )
}

/* ───────────── Main Dashboard ───────────── */
export default function Dashboard() {
  const { session } = useAuth()
  const { API_URL, AUTH_URL } = useConfig()
  const navigate = useNavigate()

  const { data: routes = [] } = useQuery({
    queryKey: ['routes'],
    queryFn: () => fetch(`${API_URL}/routes`).then(r => r.json()).catch(() => []),
  })
  const { data: drift = [] } = useQuery({
    queryKey: ['drift'],
    queryFn: () => fetch(`${API_URL}/drift`).then(r => r.json()).catch(() => []),
  })
  const { data: sessions = [] } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetch(`${AUTH_URL}/sessions`).then(r => r.json()).catch(() => []),
  })
  const { data: auditLog = [] } = useQuery({
    queryKey: ['audit-log'],
    queryFn: () => fetch(`${API_URL}/audit-log`).then(r => r.json()).catch(() => []),
  })
  const { data: fleets = [] } = useQuery({
    queryKey: ['fleets'],
    queryFn: () => fetch(`${API_URL}/fleets`).then(r => r.json()).catch(() => []),
    refetchInterval: 10000,
  })
  const { data: health = {} } = useQuery({
    queryKey: ['health'],
    queryFn: async () => {
      const services = { 'Auth Service': AUTH_URL, 'Management API': API_URL }
      const h = {}
      for (const [name, url] of Object.entries(services)) {
        try {
          const r = await fetch(`${url}/health`, { signal: AbortSignal.timeout(3000) })
          h[name] = r.ok ? 'healthy' : 'unhealthy'
        } catch { h[name] = 'unreachable' }
      }
      return h
    },
    refetchInterval: 10000,
  })

  const activeRoutes = routes.filter(r => r.status === 'active').length
  const activeSessions = sessions.filter(s => s.status === 'active').length
  const driftedRoutes = drift.filter(d => d.drift).length
  const now = new Date()
  const greeting = now.getHours() < 12 ? 'Good morning' : now.getHours() < 18 ? 'Good afternoon' : 'Good evening'

  // Shared infra hops are always green — only per-fleet last-mile shows fleet health
  const greenPulse = 'bg-emerald-400'

  return (
    <div className="space-y-6">
      {/* Welcome Header */}
      <motion.div initial={{ opacity: 0, y: -10 }} animate={{ opacity: 1, y: 0 }}
        className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white">
            {greeting}{session ? `, ${session.name?.split(' ')[0] || ''}` : ''}
          </h1>
          <p className="text-sm text-jpmc-muted mt-1">
            {now.toLocaleDateString('en-US', { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })}
            {' '}&middot;{' '}
            {now.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit' })}
          </p>
        </div>
      </motion.div>

      {/* Stat Cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <GlassCard title="Total Routes" icon={Route} value={routes.length} subtitle={`${activeRoutes} active`} delay={0} />
        <GlassCard title="Active Sessions" icon={Key} value={activeSessions} subtitle={`${sessions.length} total`} delay={0.05} />
        <GlassCard title="Gateway Health" icon={Cpu} delay={0.1}
          value={
            <div className="flex items-center gap-3 text-base">
              {Object.entries(health).map(([name, status]) => (
                <span key={name} className="flex items-center gap-1.5">
                  {status === 'healthy'
                    ? <CheckCircle2 size={14} className="text-emerald-400" />
                    : <XCircle size={14} className="text-red-400" />}
                  <span className="text-sm text-jpmc-text">{name.split(' ')[0]}</span>
                </span>
              ))}
            </div>
          }
          subtitle="Service health checks"
        />
        <GlassCard title="Drift Status" icon={AlertTriangle} value={driftedRoutes} delay={0.15}
          subtitle={driftedRoutes > 0 ? 'Routes drifted' : 'All routes in sync'}
          className={driftedRoutes > 0 ? 'border-amber-500/30' : ''} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        {/* Recent Activity */}
        <GlassCard title="Recent Activity" icon={Clock} delay={0.2} className="lg:col-span-2">
          <div className="px-5 pb-4 max-h-80 overflow-y-auto">
            {auditLog.length === 0 ? (
              <div className="text-jpmc-muted text-sm text-center py-6">No recent activity</div>
            ) : (
              <div className="space-y-0">
                {auditLog.slice(0, 10).map((entry, idx) => (
                  <motion.div key={entry.id || idx} initial={{ opacity: 0, x: -10 }} animate={{ opacity: 1, x: 0 }}
                    transition={{ delay: 0.2 + idx * 0.03 }}
                    className="flex items-start gap-3 py-3 border-b border-jpmc-border/30 last:border-0">
                    <div className={`w-2 h-2 rounded-full mt-1.5 shrink-0 ${
                      entry.action === 'CREATE' ? 'bg-emerald-400' : entry.action === 'DELETE' ? 'bg-red-400' : 'bg-amber-400'
                    }`} />
                    <div className="flex-1 min-w-0">
                      <div className="text-sm text-jpmc-text">{entry.detail}</div>
                      <div className="text-xs text-jpmc-muted mt-0.5">{entry.ts ? new Date(entry.ts * 1000).toLocaleString() : ''}</div>
                    </div>
                    <span className={`text-[10px] font-medium px-2 py-0.5 rounded ${
                      entry.action === 'CREATE' ? 'bg-emerald-500/20 text-emerald-400'
                      : entry.action === 'DELETE' ? 'bg-red-500/20 text-red-400'
                      : 'bg-amber-500/20 text-amber-400'
                    }`}>{entry.action}</span>
                  </motion.div>
                ))}
              </div>
            )}
          </div>
        </GlassCard>

        {/* Quick Actions */}
        <GlassCard title="Quick Actions" icon={Zap} delay={0.25}>
          <div className="px-5 pb-5 space-y-3">
            {[
              { label: 'New Route', icon: Plus, color: 'blue', path: '/routes' },
              { label: 'Test Request', icon: Zap, color: 'cyan', path: '/request-tester' },
              { label: 'View Traces', icon: Activity, color: 'emerald', path: '/traces' },
              { label: 'Manage Fleets', icon: Server, color: 'purple', path: '/fleets' },
            ].map(({ label, icon: BtnIcon, color, path }) => (
              <button key={path} onClick={() => navigate(path)}
                className="w-full flex items-center gap-3 p-3 rounded-lg bg-jpmc-navy/50 border border-jpmc-border/50 hover:border-jpmc-border transition-all group">
                <div className="p-2 rounded-lg bg-jpmc-navy transition-colors">
                  <BtnIcon size={14} className="text-jpmc-muted group-hover:text-white transition-colors" />
                </div>
                <span className="text-sm text-jpmc-text">{label}</span>
                <ArrowRight size={14} className="text-jpmc-muted ml-auto group-hover:text-white transition-colors" />
              </button>
            ))}
          </div>
        </GlassCard>
      </div>

      {/* ═══════════ Infrastructure Overview with animated health pulses ═══════════ */}
      <GlassCard title="Infrastructure Overview" icon={Layers} delay={0.3}>
        <div className="px-5 pb-6">
          <div className="flex items-center justify-between mb-6">
            <p className="text-xs text-jpmc-muted">
              Live topology — animated signals trace request flow through each layer based on fleet health
            </p>
            <div className="flex items-center gap-3 text-[9px]">
              <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-emerald-400" /> Healthy</span>
              <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-amber-400 animate-pulse" /> Degraded</span>
              <span className="flex items-center gap-1 text-jpmc-muted"><span className="w-2 h-2 rounded-full bg-orange-400/50" /> Passive</span>
            </div>
          </div>

          {/* ── L1: Client ── */}
          <div className="flex justify-center">
            <InfraNode icon={Globe} label="Client" desc="End User" color="from-slate-500 to-slate-600" delay={0.3} health="healthy" />
          </div>
          <FlowPulse delay={0} color={greenPulse} height={28} speed={1.6} />

          {/* ── L2: Akamai GTM ── */}
          <div className="flex justify-center">
            <InfraNode icon={Globe} label="Akamai GTM" desc="L2 — Global Traffic Mgr" color="from-blue-500 to-blue-600" delay={0.34} health="healthy" />
          </div>
          <FlowPulse delay={0.3} color={greenPulse} height={24} speed={1.4} />

          {/* ── L3: CDN/WAF — Akamai Edge (active) + Cloudflare (passive failover) ── */}
          <div className="grid grid-cols-5 items-start">
            {/* Cloudflare passive stack */}
            <div className="col-span-1 flex flex-col items-center">
              <InfraNode icon={Cloud} label="CF Edge" desc="Cloudflare CDN" color="from-orange-500/50 to-orange-600/50" delay={0.4} passive />
              <span className="text-[8px] text-orange-400/50 mt-1 font-medium uppercase tracking-wider">Passive Standby</span>
            </div>

            {/* failover label */}
            <div className="col-span-1 flex items-center justify-center pt-4">
              <div className="w-full border-t border-dotted border-orange-400/50 relative">
                <span className="absolute -top-3.5 left-1/2 -translate-x-1/2 text-[7px] text-orange-300/70 whitespace-nowrap uppercase tracking-wider">failover</span>
              </div>
            </div>

            {/* Akamai Edge primary */}
            <div className="col-span-1 flex flex-col items-center">
              <InfraNode icon={Shield} label="Akamai Edge" desc="L3 — CDN + Kona WAF" color="from-cyan-500 to-cyan-600" delay={0.38} health="healthy" />
              <span className="text-[8px] text-cyan-400 mt-1 font-medium uppercase tracking-wider">Active Primary</span>
            </div>

            <div className="col-span-2" />
          </div>

          {/* ── Connector: CDN/WAF → L4 Perimeter ── */}
          <FlowPulse delay={0.6} color={greenPulse} height={28} speed={1.6} />

          {/* ── L4: Perimeter — PSaaS (left) + AWS WAF (right) side by side ── */}
          <div className="grid grid-cols-11 gap-x-1 items-start">
            <div className="col-span-5 flex flex-col items-center">
              <InfraNode icon={Server} label="PSaaS+" desc="L4 — On-Prem Perimeter" color="from-indigo-500 to-indigo-600" delay={0.48} health="healthy" />
            </div>

            {/* Center perimeter connector */}
            <div className="col-span-1 flex items-center pt-4">
              <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} transition={{ delay: 0.5 }}
                className="w-full relative">
                <div className="flex items-center w-[300%] -mx-[100%]">
                  <div className="flex-1 relative overflow-hidden h-2">
                    <div className="absolute top-1/2 w-full border-t border-dashed border-indigo-400/40" />
                    <motion.div
                      className="absolute w-1.5 h-1.5 rounded-full bg-emerald-400"
                      style={{ top: 1, filter: 'drop-shadow(0 0 6px currentColor)' }}
                      animate={{ right: ['calc(0% - 6px)', 'calc(100% + 6px)'] }}
                      transition={{ duration: 2.4, repeat: Infinity, delay: 0.9, ease: 'linear' }}
                    />
                  </div>
                  <span className="px-2 text-[7px] text-indigo-300/80 font-bold tracking-widest uppercase whitespace-nowrap shrink-0">Perimeter</span>
                  <div className="flex-1 relative overflow-hidden h-2">
                    <div className="absolute top-1/2 w-full border-t border-dashed border-indigo-400/40" />
                    <motion.div
                      className="absolute w-1.5 h-1.5 rounded-full bg-emerald-400"
                      style={{ top: 1, filter: 'drop-shadow(0 0 6px currentColor)' }}
                      animate={{ left: ['calc(0% - 6px)', 'calc(100% + 6px)'] }}
                      transition={{ duration: 2.4, repeat: Infinity, delay: 0.9, ease: 'linear' }}
                    />
                  </div>
                </div>
              </motion.div>
            </div>

            <div className="col-span-5 flex flex-col items-center">
              <InfraNode icon={ShieldCheck} label="AWS WAF" desc="L4 — WAF v2 + Shield" color="from-orange-500 to-orange-600" delay={0.5} health="healthy" />
            </div>
          </div>

          {/* ── L5: Fleets — grouped by host_env under their perimeter ── */}
          <div className="grid grid-cols-11 gap-x-1 items-start mt-1">
            {/* PSaaS fleets (left) */}
            <div className="col-span-5 flex flex-col items-center">
              <div className="flex flex-wrap justify-center gap-3">
                {fleets.filter(f => f.host_env !== 'aws').map((f, i) => {
                  const fleetPulse = f.status === 'healthy' ? 'bg-emerald-400'
                    : f.status === 'degraded' ? 'bg-amber-400' : 'bg-red-400'
                  return (
                    <div key={f.id} className="flex flex-col items-center">
                      <FlowPulse delay={1.0 + i * 0.15} color={fleetPulse} height={22} speed={1.4} />
                      <FleetNode fleet={f} delay={0.6 + i * 0.04} onClick={() => navigate(`/fleets?fleet=${f.id}`)} />
                    </div>
                  )
                })}
                {fleets.filter(f => f.host_env !== 'aws').length === 0 && (
                  <div className="flex flex-col items-center">
                    <FlowPulse active={false} height={22} />
                    <div className="text-[9px] text-jpmc-muted italic py-2">No PSaaS fleets</div>
                  </div>
                )}
              </div>
            </div>

            {/* Center spacer */}
            <div className="col-span-1" />

            {/* AWS fleets (right) */}
            <div className="col-span-5 flex flex-col items-center">
              <div className="flex flex-wrap justify-center gap-3">
                {fleets.filter(f => f.host_env === 'aws').map((f, i) => {
                  const fleetPulse = f.status === 'healthy' ? 'bg-emerald-400'
                    : f.status === 'degraded' ? 'bg-amber-400' : 'bg-red-400'
                  return (
                    <div key={f.id} className="flex flex-col items-center">
                      <FlowPulse delay={1.1 + i * 0.15} color={fleetPulse} height={22} speed={1.4} />
                      <FleetNode fleet={f} delay={0.62 + i * 0.04} onClick={() => navigate(`/fleets?fleet=${f.id}`)} />
                    </div>
                  )
                })}
                {fleets.filter(f => f.host_env === 'aws').length === 0 && (
                  <div className="flex flex-col items-center">
                    <FlowPulse active={false} height={22} />
                    <div className="text-[9px] text-jpmc-muted italic py-2">No AWS fleets</div>
                  </div>
                )}
              </div>
            </div>
          </div>

          {/* ── Connector: Fleets → L6 Backend ── */}
          <FlowPulse delay={1.5} color={greenPulse} height={28} speed={1.6} />

          {/* ── L6: Backend Services ── */}
          <div className="flex justify-center gap-6">
            <InfraNode icon={Box} label="svc-web" desc="L6 — Frontend" color="from-teal-500 to-teal-600" delay={0.7} small health="healthy" />
            <InfraNode icon={Box} label="svc-api" desc="L6 — API Backend" color="from-teal-500 to-teal-600" delay={0.74} small health="healthy" />
          </div>
        </div>
      </GlassCard>
    </div>
  )
}
