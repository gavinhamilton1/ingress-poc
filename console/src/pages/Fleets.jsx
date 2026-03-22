import React, { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Server, Cpu, Globe, ChevronDown, ChevronRight,
  Activity, ArrowRight, MapPin, Plus, X, Info,
  GitBranch, Cloud, Shield, Box, Layers, Zap, Lock, Hash,
} from 'lucide-react'
import GlassCard from '../components/GlassCard'
import StatusBadge from '../components/StatusBadge'
import { useConfig } from '../context/ConfigContext'

function InstancePill({ inst }) {
  const isApi = inst.gateway_type === 'kong' || inst.context_path.startsWith('/api')
  return (
    <motion.div
      key={inst.id}
      initial={{ opacity: 0, x: -10 }}
      animate={{ opacity: 1, x: 0 }}
      className={`flex items-center gap-2 px-3 py-1.5 rounded-lg border text-xs ${
        inst.status === 'active'
          ? 'bg-emerald-500/10 border-emerald-500/30'
          : 'bg-amber-500/10 border-amber-500/30'
      }`}
    >
      <span className={`w-1.5 h-1.5 rounded-full ${
        inst.status === 'active' ? 'bg-emerald-400' : 'bg-amber-400'
      }`} />
      <code className="text-jpmc-text">{inst.context_path}</code>
      <ArrowRight size={10} className="text-jpmc-muted" />
      <span className="text-jpmc-muted font-mono text-[10px]">{inst.backend.split('://')[1]}</span>
    </motion.div>
  )
}

/* Animated health pulse that travels along a connection line */
function AnimatedHealthLine({ status = 'healthy', vertical = false }) {
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

function GatewayBranch({ label, desc, color, routes, status }) {
  if (routes.length === 0) return null
  const branchStatus = routes.every(r => r.status === 'active') ? 'healthy' : 'degraded'
  return (
    <div className="flex items-center gap-1">
      <AnimatedHealthLine status={branchStatus} />
      <TopoNode icon={Cpu} label={label} desc={desc} color={color} />
      <AnimatedHealthLine status={branchStatus} />
      <div className="flex flex-col gap-1.5">
        {routes.map(inst => <InstancePill key={inst.id} inst={inst} />)}
      </div>
    </div>
  )
}

function FleetTopology({ fleet }) {
  const instances = fleet.instances || []
  // Split by gateway_type field on each instance, fall back to path convention
  const kongRoutes = instances.filter(i => i.gateway_type === 'kong' || (!i.gateway_type && i.context_path.startsWith('/api')))
  const envoyRoutes = instances.filter(i => i.gateway_type === 'envoy' || (!i.gateway_type && !i.context_path.startsWith('/api')))
  const hasBoth = kongRoutes.length > 0 && envoyRoutes.length > 0
  const fleetHealthy = fleet.status === 'healthy'

  return (
    <div className="overflow-x-auto py-4">
      <div className="flex items-start gap-1 min-w-fit">
        {/* Shared front layers: DNS → CDN/WAF → Perimeter */}
        <div className="flex items-center gap-1 shrink-0" style={{ alignSelf: hasBoth ? 'center' : 'flex-start' }}>
          <TopoNode icon={Globe} label={fleet.subdomain.split('.')[0]} desc="DNS entry" color="blue" />
          <AnimatedHealthLine status={fleetHealthy ? 'healthy' : 'degraded'} />
          <TopoNode icon={Shield} label="CDN / WAF" desc="Akamai Edge" color="cyan" />
          <AnimatedHealthLine status={fleetHealthy ? 'healthy' : 'degraded'} />
          <TopoNode icon={Layers} label="PSaaS+" desc="Perimeter" color="indigo" />
        </div>

        {/* Gateway branches — split Kong (API) from Envoy (Web) */}
        {hasBoth ? (
          <div className="flex flex-col gap-3">
            <GatewayBranch label="Kong" desc="API gateway" color="purple" routes={kongRoutes} />
            <GatewayBranch label="Envoy" desc="Web gateway" color="violet" routes={envoyRoutes} />
          </div>
        ) : kongRoutes.length > 0 ? (
          <GatewayBranch label="Kong" desc="API gateway" color="purple" routes={kongRoutes} />
        ) : (
          <GatewayBranch label="Envoy" desc="Web gateway" color="violet" routes={envoyRoutes} />
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

export default function Fleets() {
  const { API_URL } = useConfig()
  const queryClient = useQueryClient()
  const [expandedFleet, setExpandedFleet] = useState(null)
  const [showDeploy, setShowDeploy] = useState(false)
  const [showCreateFleet, setShowCreateFleet] = useState(false)
  const [deployFleetId, setDeployFleetId] = useState('')
  const [deployForm, setDeployForm] = useState({ context_path: '', backend: 'http://svc-api:8005', team: '' })
  const [deploySuccess, setDeploySuccess] = useState(false)
  const [createFleetForm, setCreateFleetForm] = useState({ name: '', portal: '', region: 'us-east' })
  const [createFleetSuccess, setCreateFleetSuccess] = useState(false)

  const computedFqdn = createFleetForm.portal ? `${createFleetForm.portal}.jpm.com` : ''

  const handleCreateFleet = async (e) => {
    e.preventDefault()
    if (!createFleetForm.name || !computedFqdn) return
    try {
      const r = await fetch(`${API_URL}/fleets`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: createFleetForm.name,
          subdomain: computedFqdn,
          region: createFleetForm.region,
        }),
      })
      if (r.ok) {
        setCreateFleetSuccess(true)
        setTimeout(() => { setCreateFleetSuccess(false); setShowCreateFleet(false) }, 2500)
        queryClient.invalidateQueries({ queryKey: ['fleets'] })
        setCreateFleetForm({ name: '', portal: '', region: 'us-east' })
      }
    } catch {}
  }

  const { data: fleets = [] } = useQuery({
    queryKey: ['fleets'],
    queryFn: () => fetch(`${API_URL}/fleets`).then(r => r.json()).catch(() => []),
  })

  const totalInstances = fleets.reduce((sum, f) => sum + (f.instances || []).length, 0)
  const healthyFleets = fleets.filter(f => f.status === 'healthy').length

  const handleDeploy = async (e) => {
    e.preventDefault()
    if (!deployFleetId) return
    try {
      const r = await fetch(`${API_URL}/fleets/${deployFleetId}/deploy`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(deployForm),
      })
      if (r.ok) {
        setDeploySuccess(true)
        setTimeout(() => { setDeploySuccess(false); setShowDeploy(false) }, 3000)
        queryClient.invalidateQueries({ queryKey: ['fleets'] })
        queryClient.invalidateQueries({ queryKey: ['routes'] })
        setDeployForm({ context_path: '', backend: 'http://svc-api:8005', team: '' })
      }
    } catch {}
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-white">Fleet Management</h1>
          <p className="text-sm text-jpmc-muted">Logical gateway groups serving subdomains — each fleet is a Kubernetes Service with scaled pods</p>
        </div>
        <div className="flex items-center gap-2">
          <button onClick={() => { setShowCreateFleet(true); setShowDeploy(false) }}
            className="flex items-center gap-2 px-3 py-2 rounded-lg border border-jpmc-border/50 bg-jpmc-navy/50 text-sm text-jpmc-text hover:border-blue-500/30 hover:bg-blue-500/5 transition-all">
            <Globe size={14} />
            New Fleet
          </button>
          <button onClick={() => { setShowDeploy(!showDeploy); setShowCreateFleet(false) }} className="btn-primary flex items-center gap-2">
            {showDeploy ? <X size={14} /> : <Plus size={14} />}
            {showDeploy ? 'Cancel' : 'Deploy to Fleet'}
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
              Fleet subdomains resolve via internal DNS to the service VIP.
              Routes are pushed to all pods in the fleet via the xDS control plane (Envoy) or declarative config sync (Kong).
              In production, fleet changes are committed to <span className="text-jpmc-text">Bitbucket</span> and
              reconciled by <span className="text-jpmc-text">ArgoCD</span> — this PoC writes directly to the gateway control plane.
            </p>
          </div>
        </div>
      </motion.div>

      {/* Stats */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <GlassCard title="Total Fleets" icon={Server} value={fleets.length} subtitle={`${healthyFleets} healthy`} delay={0} />
        <GlassCard title="Route Instances" icon={Cpu} value={totalInstances} subtitle="Across all fleets" delay={0.05} />
        <GlassCard
          title="Fleet Health"
          icon={Activity}
          value={fleets.length > 0 ? `${Math.round((healthyFleets / fleets.length) * 100)}%` : '—'}
          subtitle={fleets.length === healthyFleets ? 'All fleets healthy' : 'Some fleets degraded'}
          delay={0.1}
        />
      </div>

      {/* Deploy Slide-over */}
      <AnimatePresence>
        {showDeploy && (
          <>
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              className="fixed inset-0 bg-black/40 z-40"
              onClick={() => setShowDeploy(false)}
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
                  <h2 className="text-lg font-bold text-white">Deploy Route to Fleet</h2>
                  <button onClick={() => setShowDeploy(false)} className="p-1.5 rounded-md hover:bg-jpmc-hover text-jpmc-muted">
                    <X size={18} />
                  </button>
                </div>

                {/* GitOps notice */}
                <div className="p-3 rounded-lg bg-amber-500/10 border border-amber-500/30 mb-4">
                  <div className="flex items-center gap-2 text-amber-400 text-xs font-medium mb-1">
                    <GitBranch size={12} />
                    GitOps PoC Mode
                  </div>
                  <p className="text-[11px] text-amber-300/70">
                    In production this deploy would create a PR in Bitbucket, pass CI validation,
                    and be reconciled by ArgoCD to the target fleet's Kubernetes namespace.
                    This PoC writes directly to the ingress registry and gateway control plane.
                  </p>
                </div>

                {/* What happens on deploy */}
                <div className="p-3 rounded-lg bg-jpmc-navy/50 border border-jpmc-border/30 mb-6">
                  <div className="flex items-center gap-2 text-jpmc-text text-xs font-medium mb-2">
                    <Info size={12} className="text-blue-400" />
                    What happens when you deploy
                  </div>
                  <ol className="text-[11px] text-jpmc-muted space-y-1.5 list-decimal list-inside">
                    <li>Route is registered in the <span className="text-jpmc-text">Ingress Registry</span> (desired state)</li>
                    <li>Control plane pushes config to the fleet's <span className="text-jpmc-text">gateway cluster</span> within 5s</li>
                    <li>All gateway pods in the fleet receive the route via <span className="text-jpmc-text">xDS / declarative sync</span></li>
                    <li>Drift detection confirms <span className="text-emerald-400">desired = actual</span> within 10s</li>
                    <li>Route is live and testable at the fleet's <span className="text-jpmc-text">subdomain</span></li>
                  </ol>
                </div>

                <AnimatePresence>
                  {deploySuccess && (
                    <motion.div
                      initial={{ opacity: 0, scale: 0.95 }}
                      animate={{ opacity: 1, scale: 1 }}
                      exit={{ opacity: 0, scale: 0.95 }}
                      className="p-4 rounded-lg bg-emerald-500/10 border border-emerald-500/30 mb-4 text-center"
                    >
                      <Zap size={24} className="text-emerald-400 mx-auto mb-2" />
                      <div className="text-sm font-medium text-emerald-300">Route deployed successfully</div>
                      <div className="text-[11px] text-emerald-400/70 mt-1">Gateway will pick up the route within 5 seconds</div>
                    </motion.div>
                  )}
                </AnimatePresence>

                <form onSubmit={handleDeploy} className="space-y-4">
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Target Fleet</label>
                    <select
                      className="select-field"
                      value={deployFleetId}
                      onChange={e => setDeployFleetId(e.target.value)}
                    >
                      <option value="">Select a fleet...</option>
                      {fleets.map(f => (
                        <option key={f.id} value={f.id}>
                          {f.name} ({f.subdomain}) — {f.gateway_type}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Context Path</label>
                    <input className="input-field" placeholder="/api/new-service" value={deployForm.context_path}
                      onChange={e => setDeployForm({ ...deployForm, context_path: e.target.value })} />
                    <p className="text-[10px] text-jpmc-muted mt-1">
                      The path this route handles within the fleet's subdomain
                    </p>
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Backend Service</label>
                    <select className="select-field" value={deployForm.backend}
                      onChange={e => setDeployForm({ ...deployForm, backend: e.target.value })}>
                      <option value="http://svc-api:8005">svc-api (API services)</option>
                      <option value="http://svc-web:8004">svc-web (Web services)</option>
                    </select>
                    <p className="text-[10px] text-jpmc-muted mt-1">
                      In production this would be a Kubernetes Service name in the target namespace
                    </p>
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Team</label>
                    <input className="input-field" placeholder="markets-team" value={deployForm.team}
                      onChange={e => setDeployForm({ ...deployForm, team: e.target.value })} />
                  </div>
                  <button type="submit" disabled={!deployFleetId || !deployForm.context_path} className="btn-primary w-full py-3 disabled:opacity-40">
                    Deploy to Fleet
                  </button>
                </form>
              </div>
            </motion.div>
          </>
        )}
      </AnimatePresence>

      {/* Create Fleet Slide-over */}
      <AnimatePresence>
        {showCreateFleet && (
          <>
            <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
              className="fixed inset-0 bg-black/40 z-40" onClick={() => setShowCreateFleet(false)} />
            <motion.div
              initial={{ x: '100%' }} animate={{ x: 0 }} exit={{ x: '100%' }}
              transition={{ type: 'spring', damping: 30, stiffness: 300 }}
              className="fixed right-0 top-0 h-screen w-full max-w-md bg-jpmc-dark border-l border-jpmc-border z-50 overflow-y-auto"
            >
              <div className="p-6">
                <div className="flex items-center justify-between mb-6">
                  <h2 className="text-lg font-bold text-white">Create New Fleet</h2>
                  <button onClick={() => setShowCreateFleet(false)} className="p-1.5 rounded-md hover:bg-jpmc-hover text-jpmc-muted">
                    <X size={18} />
                  </button>
                </div>

                <div className="p-3 rounded-lg bg-blue-500/10 border border-blue-500/30 mb-4">
                  <div className="flex items-center gap-2 text-blue-400 text-xs font-medium mb-1">
                    <Globe size={12} />
                    Portal-Based Routing
                  </div>
                  <p className="text-[11px] text-blue-300/70">
                    Each fleet gets a portal hostname: <code className="text-blue-300">[portal].jpm.com</code>.
                    Routes on the portal use path convention: <code className="text-blue-300">/api/*</code> → Kong,
                    everything else → Envoy. The perimeter layer (PSaaS) applies this rule statically.
                  </p>
                </div>

                <AnimatePresence>
                  {createFleetSuccess && (
                    <motion.div
                      initial={{ opacity: 0, scale: 0.95 }} animate={{ opacity: 1, scale: 1 }} exit={{ opacity: 0, scale: 0.95 }}
                      className="p-4 rounded-lg bg-emerald-500/10 border border-emerald-500/30 mb-4 text-center"
                    >
                      <Zap size={24} className="text-emerald-400 mx-auto mb-2" />
                      <div className="text-sm font-medium text-emerald-300">Fleet created successfully</div>
                      <div className="text-[11px] text-emerald-400/70 mt-1">Ready for route deployments</div>
                    </motion.div>
                  )}
                </AnimatePresence>

                <form onSubmit={handleCreateFleet} className="space-y-4">
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Fleet Name</label>
                    <input className="input-field" placeholder="JPMM — Markets" value={createFleetForm.name}
                      onChange={e => setCreateFleetForm({ ...createFleetForm, name: e.target.value })} />
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Portal Name</label>
                    <div className="flex items-center gap-0">
                      <input className="input-field rounded-r-none border-r-0 flex-1 text-xs"
                        placeholder="myportal" value={createFleetForm.portal}
                        onChange={e => setCreateFleetForm({ ...createFleetForm, portal: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '') })} />
                      <span className="px-3 py-2 rounded-r-lg border border-jpmc-border/50 bg-jpmc-navy/80 text-xs text-jpmc-muted">.jpm.com</span>
                    </div>
                    {computedFqdn && (
                      <div className="mt-2 p-2 rounded-lg bg-jpmc-navy/50 border border-jpmc-border/20">
                        <code className="text-xs text-blue-400">{computedFqdn}</code>
                        <div className="mt-1 text-[9px] text-jpmc-muted">
                          Web: <code className="text-violet-400">{computedFqdn}/your-page</code> → Envoy
                          {' · '}API: <code className="text-purple-400">{computedFqdn}/api/your-service</code> → Kong
                        </div>
                        <div className="mt-0.5 text-[9px] text-jpmc-muted">
                          Test: <code className="text-emerald-400/80">curl -sk https://{computedFqdn}/your-path</code>
                        </div>
                      </div>
                    )}
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-jpmc-muted mb-1.5">Region</label>
                    <select className="select-field" value={createFleetForm.region}
                      onChange={e => setCreateFleetForm({ ...createFleetForm, region: e.target.value })}>
                      <option value="us-east">US East</option>
                      <option value="us-west">US West</option>
                      <option value="eu-west">EU West</option>
                      <option value="ap-southeast">AP Southeast</option>
                      <option value="multi">Multi-Region</option>
                    </select>
                  </div>
                  <button type="submit"
                    disabled={!createFleetForm.name || !computedFqdn}
                    className="btn-primary w-full py-3 disabled:opacity-40">
                    Create Fleet
                  </button>
                </form>
              </div>
            </motion.div>
          </>
        )}
      </AnimatePresence>

      {/* Fleet Cards grouped by LOB */}
      <div className="space-y-6">
        {Object.entries(
          fleets.reduce((groups, fleet) => {
            const lob = fleet.lob || 'Other'
            ;(groups[lob] = groups[lob] || []).push(fleet)
            return groups
          }, {})
        ).map(([lob, lobFleets]) => (
          <div key={lob}>
            <div className="flex items-center gap-2 mb-3">
              <h2 className="text-xs font-bold text-jpmc-muted uppercase tracking-widest">{lob}</h2>
              <div className="flex-1 border-t border-jpmc-border/30" />
              <span className="text-[10px] text-jpmc-muted">{lobFleets.length} fleet{lobFleets.length !== 1 ? 's' : ''}</span>
            </div>
            <div className="space-y-3">
        {lobFleets.map((fleet, idx) => {
          const isExpanded = expandedFleet === fleet.id
          const instances = fleet.instances || []
          return (
            <motion.div
              key={fleet.id}
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: 0.1 + idx * 0.05 }}
            >
              <div className={`glass-card overflow-hidden transition-all duration-200 ${
                fleet.status === 'degraded' ? 'border-amber-500/30' : ''
              }`}>
                {/* Fleet Header */}
                <div
                  className="flex items-center gap-4 p-5 cursor-pointer hover:bg-jpmc-hover/50 transition-colors"
                  onClick={() => setExpandedFleet(isExpanded ? null : fleet.id)}
                >
                  <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 ${
                    fleet.status === 'healthy'
                      ? 'bg-emerald-500/15 border border-emerald-500/30'
                      : 'bg-amber-500/15 border border-amber-500/30'
                  }`}>
                    <Server size={18} className={fleet.status === 'healthy' ? 'text-emerald-400' : 'text-amber-400'} />
                  </div>

                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 mb-1">
                      <h3 className="text-sm font-semibold text-white">{fleet.name}</h3>
                      {fleet.lob && <span className="badge badge-blue text-[9px]">{fleet.lob}</span>}
                      <StatusBadge status={fleet.status} />
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
                      {fleet.auth_provider && (
                        <span className="flex items-center gap-1">
                          <Lock size={11} />
                          {fleet.auth_provider}
                        </span>
                      )}
                    </div>
                  </div>

                  <div className="flex items-center gap-4 shrink-0">
                    <span className={`badge ${fleet.gateway_type === 'kong' ? 'badge-blue' : 'badge-gray'}`}>
                      {fleet.gateway_type}
                    </span>
                    <div className="text-right">
                      <div className="text-lg font-bold text-white">{fleet.instances_count || instances.length}</div>
                      <div className="text-[10px] text-jpmc-muted">instances</div>
                    </div>
                    <div className="text-right">
                      <div className="text-lg font-bold text-white">{instances.length}</div>
                      <div className="text-[10px] text-jpmc-muted">routes</div>
                    </div>
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
                            <span className="text-jpmc-text font-medium">{fleet.name}</span> serves
                            traffic for <code className="text-blue-400 text-[10px]">{fleet.subdomain}</code>.
                            In production this fleet runs as a {fleet.gateway_type === 'kong' ? 'Kong' : 'Envoy'} Deployment
                            with <span className="text-jpmc-text">{instances.length} route{instances.length !== 1 ? 's' : ''}</span> configured
                            across <span className="text-jpmc-text">3 replicas</span> in the <span className="text-jpmc-text">{fleet.region}</span> region.
                            Route changes propagate to all pods via {fleet.gateway_type === 'kong' ? 'declarative config sync' : 'xDS'} within 5 seconds.
                          </p>
                        </div>

                        {/* Instances Table */}
                        <div className="mb-4">
                          <div className="text-xs font-medium text-jpmc-muted uppercase tracking-wider mb-3">
                            Route Instances
                            <span className="text-[10px] font-normal ml-2 normal-case">(each served by all pods in this fleet)</span>
                          </div>
                          <div className="space-y-2">
                            {instances.map(inst => (
                              <div key={inst.id} className="flex items-center gap-4 p-3 rounded-lg bg-jpmc-navy/50 border border-jpmc-border/30">
                                <span className={`w-2 h-2 rounded-full shrink-0 ${
                                  inst.status === 'active'
                                    ? 'bg-emerald-400 shadow-[0_0_6px_rgba(52,211,153,0.5)]'
                                    : 'bg-amber-400 shadow-[0_0_6px_rgba(251,191,36,0.5)]'
                                }`} />
                                <code className="text-sm text-blue-400 min-w-[140px]">{inst.context_path}</code>
                                <ArrowRight size={12} className="text-jpmc-muted shrink-0" />
                                <span className="text-xs text-jpmc-muted font-mono flex-1">{inst.backend}</span>
                                <StatusBadge status={inst.status} />
                                <div className="text-right min-w-[60px]">
                                  <div className="text-xs font-medium text-jpmc-text">{inst.latency_p99}ms</div>
                                  <div className="text-[10px] text-jpmc-muted">p99</div>
                                </div>
                              </div>
                            ))}
                          </div>
                        </div>

                        {/* Fleet Topology */}
                        <div>
                          <div className="text-xs font-medium text-jpmc-muted uppercase tracking-wider mb-2">
                            Request Path
                            <span className="text-[10px] font-normal ml-2 normal-case">DNS → CDN → Perimeter → Gateway → Backend</span>
                          </div>
                          <FleetTopology fleet={fleet} />
                        </div>
                      </div>
                    </motion.div>
                  )}
                </AnimatePresence>
              </div>
            </motion.div>
          )
        })}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
