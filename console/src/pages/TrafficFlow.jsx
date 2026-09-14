import React, { useState, useRef, useCallback } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Waypoints, Play, RotateCcw, Monitor, Cloud, Radio, Route as RouteIcon,
  ShieldCheck, ShieldAlert, Server, CheckCircle2, XCircle, Ban, Repeat, MinusCircle, ListChecks, Lock,
  FileWarning,
} from 'lucide-react'

// Slow, deliberate pacing — tuned for narrating over a video recording.
const TIMING = {
  enter: 900,        // packet arrives at a stage, before its checks start revealing
  checkStep: 500,     // delay between each individual check appearing
  checkSettle: 550,   // pause after the last check before moving on
  postEntry: 700,     // pause after a stage resolves (pass/fail) before advancing
  failShake: 400,      // brief pause on a fail before the block bounce starts
  blockedHold: 500,    // 403 packet holds at the failing stage before bouncing back
  blockedReturn: 1500, // 403 packet's return trip to the client
  successHold: 450,    // 201 packet holds at the backend before returning
  successReturn: 1500, // 201 packet's return trip to the client
  loopGap: 2200,       // pause between scenarios in auto-demo
  packetMove: 1.2,     // seconds for the packet to glide between adjacent stages
}

// ── Scenario data ────────────────────────────────────────────────────────────
// full_name is modeled as [safe-looking prefix, injected suffix] so the big
// payload panel can highlight exactly the attempted-compromise part, not
// just show a wall of red text.

const VALID_FIELDS = {
  full_name: 'Ada Lovelace',
  email: 'ada@example.com',
  phone: '+14155552671',
  date_of_birth: '1990-01-01',
  country: 'US',
  marketing_opt_in: false,
}

const MALICIOUS_FIELDS = {
  full_name: ['Robert', "'); DROP TABLE users;--"],
  email: 'ada@example.com',
  phone: '+14155552671',
  date_of_birth: '1990-01-01',
  country: 'US',
  marketing_opt_in: false,
}

// email is flagged (amber highlight) rather than split like the SQLi case —
// this isn't an attack, just a bad format the route's own schema check catches.
const ROUTE_FAIL_FIELDS = {
  full_name: 'Ada Lovelace',
  email: { flag: true, text: 'not-an-email' },
  phone: '+14155552671',
  date_of_birth: '1990-01-01',
  country: 'US',
  marketing_opt_in: false,
}

const STAGES = [
  { id: 'client', icon: Monitor, label: 'Client', sub: 'demo-user-api.jpm.com' },
  { id: 'edge', icon: Cloud, label: 'CDN Edge', sub: 'Akamai / Cloudflare' },
  { id: 'perimeter', icon: Radio, label: 'Regional Perimeter', sub: 'PSaaS+ / AWS WAF' },
  { id: 'gateway', icon: RouteIcon, label: 'Gateway', sub: 'gateway-envoy' },
  { id: 'global', icon: ShieldCheck, label: 'Global Policy', sub: 'every route' },
  { id: 'route', icon: ShieldCheck, label: 'Route Policy', sub: 'this route only' },
  { id: 'backend', icon: Server, label: 'Backend', sub: 'your service' },
]

const N = STAGES.length
const centerPct = (i) => ((i + 0.5) / N) * 100

// Real network tiers (not the policy layers) — matches ProtectionArchitecture.jsx.
// Tier 2 spans the gateway AND both policy checks: that's the trust boundary.
// Client/CDN Edge sit on the public internet, outside JPM-owned tiers.
const TIER_BANDS = [
  { label: 'PUBLIC INTERNET', color: '#7a8a9a', from: 0, to: 1 },
  { label: 'TIER 1 — PERIMETER', color: '#d35400', from: 2, to: 2 },
  { label: 'TIER 2 — GATEWAY', color: '#4a9edd', from: 3, to: 5 },
  { label: 'TIER 3 — TRUSTED INTERNAL', color: '#2ecc71', from: 6, to: 6 },
]

// TLS boundaries — outer TLS terminates at the CDN edge, then re-originates
// into JPM's own network at the perimeter (Tier 1 → Tier 2). It happens
// again at the Tier 2 → Tier 3 boundary: the gateway terminates the Tier 2
// session and re-originates a fresh one into the trusted backend network.
const TLS_MARKERS = [
  { afterStage: 1, label: 'TLS TERMINATES' },
  { afterStage: 2, label: 'TLS RE-ORIGINATES' },
  { afterStage: 5, label: 'TLS RE-ENCRYPTS' },
]

const JWT_CHECK = { text: 'JWT / DPoP session validation', status: 'skip', note: 'route authn: none — public endpoint, skipped' }
const ROUTE_MATCH_CHECK = { text: 'Route matched — exact & templated ({param}) paths', status: 'pass' }
const TLS_CLIENT_CHECK = { text: 'TLS 1.2+ negotiated', status: 'pass' }

const VALID_LOG = [
  {
    stage: 0, text: 'Client → HTTPS POST /api/v1/users/register (demo-user-api.jpm.com)',
    checks: [TLS_CLIENT_CHECK],
  },
  {
    stage: 1, text: 'CDN Edge (Cloudflare selected) → WAF ruleset check: no match, forwarding', provider: 'cloudflare',
    checks: [
      { text: 'Bot detection (User-Agent present)', status: 'pass' },
      { text: 'WAF ruleset — XSS / path traversal', status: 'pass' },
    ],
  },
  {
    stage: 2, text: 'Regional Perimeter (PSaaS+ selected) → WAF check: no match; resolved gateway_type=envoy for this route',
    checks: [
      { text: 'WAF check (PSaaS+)', status: 'pass' },
      { text: 'Gateway type resolved: envoy', status: 'pass' },
    ],
  },
  { stage: 3, text: 'Gateway (Envoy) → ext_authz call to auth-service', checks: [ROUTE_MATCH_CHECK, JWT_CHECK] },
  {
    stage: 4, text: 'Global Policy → allow = true (no injection patterns found)', verdict: 'pass',
    checks: [
      { text: 'SQL injection patterns', status: 'pass' },
      { text: 'XSS patterns', status: 'pass' },
      { text: 'NoSQL injection patterns', status: 'pass' },
    ],
  },
  {
    stage: 5, text: 'Route Policy → allow = true (all required fields present, formats valid)', verdict: 'pass',
    checks: [
      { text: 'full_name present & non-empty', status: 'pass' },
      { text: 'email format', status: 'pass' },
      { text: 'phone format (E.164)', status: 'pass' },
      { text: 'date_of_birth format', status: 'pass' },
    ],
  },
  {
    stage: 6, text: 'Backend → 201 Created, user id assigned', verdict: 'pass',
    checks: [
      { text: 'Framework-level validation (Pydantic)', status: 'pass' },
      { text: 'Record persisted', status: 'pass' },
    ],
  },
]

const MALICIOUS_LOG = [
  {
    stage: 0, text: 'Client → HTTPS POST /api/v1/users/register (demo-user-api.jpm.com)',
    checks: [TLS_CLIENT_CHECK],
  },
  {
    stage: 1, text: 'CDN Edge (Akamai selected) → WAF checks path/query only, body not inspected here', provider: 'akamai',
    checks: [
      { text: 'Bot detection (User-Agent present)', status: 'pass' },
      { text: 'WAF ruleset — XSS / path traversal', status: 'pass', note: 'body not inspected at this layer' },
    ],
  },
  {
    stage: 2, text: 'Regional Perimeter (AWS WAF selected) → WAF check: no match; resolved gateway_type=envoy for this route',
    checks: [
      { text: 'WAF check (AWS WAF)', status: 'pass', note: 'body not inspected at this layer' },
      { text: 'Gateway type resolved: envoy', status: 'pass' },
    ],
  },
  { stage: 3, text: 'Gateway (Envoy) → ext_authz call to auth-service', checks: [ROUTE_MATCH_CHECK, JWT_CHECK] },
  {
    stage: 4,
    text: 'Global Policy → allow = false — deny_reason: "request body contains a potentially malicious pattern (possible injection attempt)"',
    verdict: 'fail',
    checks: [
      { text: 'SQL injection patterns', status: 'fail', note: 'matched in full_name: \'); DROP TABLE' },
      { text: 'XSS patterns', status: 'pass' },
      { text: 'NoSQL injection patterns', status: 'pass' },
    ],
  },
  {
    stage: 4, text: 'BLOCKED — 403 returned immediately. Route-specific policy never evaluated.', verdict: 'blocked',
    resultText: '403 Forbidden — payload rejected by the global policy',
  },
]

const ROUTE_FAIL_LOG = [
  {
    stage: 0, text: 'Client → HTTPS POST /api/v1/users/register (demo-user-api.jpm.com)',
    checks: [TLS_CLIENT_CHECK],
  },
  {
    stage: 1, text: 'CDN Edge (Cloudflare selected) → WAF ruleset check: no match, forwarding', provider: 'cloudflare',
    checks: [
      { text: 'Bot detection (User-Agent present)', status: 'pass' },
      { text: 'WAF ruleset — XSS / path traversal', status: 'pass' },
    ],
  },
  {
    stage: 2, text: 'Regional Perimeter (PSaaS+ selected) → WAF check: no match; resolved gateway_type=envoy for this route',
    checks: [
      { text: 'WAF check (PSaaS+)', status: 'pass' },
      { text: 'Gateway type resolved: envoy', status: 'pass' },
    ],
  },
  { stage: 3, text: 'Gateway (Envoy) → ext_authz call to auth-service', checks: [ROUTE_MATCH_CHECK, JWT_CHECK] },
  {
    stage: 4, text: 'Global Policy → allow = true (no injection patterns found)', verdict: 'pass',
    checks: [
      { text: 'SQL injection patterns', status: 'pass' },
      { text: 'XSS patterns', status: 'pass' },
      { text: 'NoSQL injection patterns', status: 'pass' },
    ],
  },
  {
    stage: 5,
    text: 'Route Policy → allow = false — deny_reason: "email is not a valid email address"',
    verdict: 'fail',
    checks: [
      { text: 'full_name present & non-empty', status: 'pass' },
      { text: 'email format', status: 'fail', note: '"not-an-email" does not match required pattern' },
      { text: 'phone format (E.164)', status: 'pass' },
      { text: 'date_of_birth format', status: 'pass' },
    ],
  },
  {
    stage: 5,
    text: "BLOCKED — 403 returned. This route's own schema check failed; the global policy had already passed.",
    verdict: 'blocked',
    resultText: '403 Forbidden — payload rejected by the route-specific policy',
  },
]

// One place per scenario for everything that varies — the packet color, the
// payload panel's framing, and which fields/log drive the animation.
const SCENARIOS = {
  valid: {
    fields: VALID_FIELDS, log: VALID_LOG, color: '#a78bfa',
    badge: 'valid payload', badgeColor: '#4ade80', border: '#1a3a2a', text: '#8ab8a0',
  },
  malicious: {
    fields: MALICIOUS_FIELDS, log: MALICIOUS_LOG, color: '#f39c12',
    badge: 'malicious payload — attempted SQL injection', badgeColor: '#f39c12', border: '#5a2a10', text: '#e0a878',
  },
  route_fail: {
    fields: ROUTE_FAIL_FIELDS, log: ROUTE_FAIL_LOG, color: '#fbbf24',
    badge: 'invalid payload — malformed email', badgeColor: '#fbbf24', border: '#4a3a10', text: '#d8c090',
  },
}
const SCENARIO_ORDER = ['valid', 'malicious', 'route_fail']

// ── Small building blocks ────────────────────────────────────────────────────

const STATUS_RING = {
  idle: { border: '#1a2a3a', glow: 'none', color: '#4a5a70' },
  active: { border: '#4a9edd', glow: '0 0 18px rgba(74,158,221,0.55)', color: '#4a9edd' },
  pass: { border: '#2ecc71', glow: '0 0 18px rgba(46,204,113,0.5)', color: '#2ecc71' },
  fail: { border: '#e74c3c', glow: '0 0 18px rgba(231,76,60,0.6)', color: '#e74c3c' },
}

const CHECK_ICON = { pass: CheckCircle2, fail: XCircle, skip: MinusCircle }
const CHECK_COLOR = { pass: '#4ade80', fail: '#f87171', skip: '#6a7a8a' }

function FieldValue({ value }) {
  if (Array.isArray(value)) {
    const [safe, injected] = value
    return (
      <>
        <span>{safe}</span>
        <span style={{ background: 'rgba(231,76,60,0.28)', color: '#ff8a8a', fontWeight: 700, borderRadius: 3, padding: '0 2px' }}>
          {injected}
        </span>
      </>
    )
  }
  if (value && typeof value === 'object' && value.flag) {
    return (
      <span style={{ background: 'rgba(251,191,36,0.28)', color: '#fde68a', fontWeight: 700, borderRadius: 3, padding: '0 2px' }}>
        {value.text}
      </span>
    )
  }
  return <span>{String(value)}</span>
}

function StageNode({ stage, status }) {
  const Icon = stage.icon
  const ring = STATUS_RING[status] || STATUS_RING.idle
  return (
    <div className="flex flex-col items-center text-center" style={{ width: `${100 / N}%` }}>
      <motion.div
        animate={{
          borderColor: ring.border,
          boxShadow: ring.glow,
          scale: status === 'active' ? 1.12 : 1,
        }}
        transition={{ duration: 0.35 }}
        className="w-14 h-14 rounded-2xl flex items-center justify-center relative"
        style={{ background: '#0a0e1a', border: '1.5px solid' }}
      >
        <Icon size={22} style={{ color: ring.color }} />
        <AnimatePresence>
          {status === 'pass' && (
            <motion.div
              initial={{ scale: 0, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} exit={{ scale: 0, opacity: 0 }}
              className="absolute -top-1.5 -right-1.5 bg-jpmc-navy rounded-full"
            >
              <CheckCircle2 size={16} className="text-green-400" />
            </motion.div>
          )}
          {status === 'fail' && (
            <motion.div
              initial={{ scale: 0, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} exit={{ scale: 0, opacity: 0 }}
              className="absolute -top-1.5 -right-1.5 bg-jpmc-navy rounded-full"
            >
              <XCircle size={16} className="text-red-400" />
            </motion.div>
          )}
        </AnimatePresence>
      </motion.div>
      <div className="mt-2 text-[11px] font-semibold whitespace-nowrap" style={{ color: status === 'idle' ? '#94a3b8' : '#e2e8f0' }}>
        {stage.label}
      </div>
      <div className="text-[9px] font-mono mt-0.5 whitespace-nowrap" style={{ color: '#4a5a70' }}>
        {stage.sub}
      </div>
    </div>
  )
}

function TLSMark({ leftPct, label }) {
  return (
    <div className="absolute top-7 z-20" style={{ left: `${leftPct}%`, transform: 'translate(-50%, -50%)' }}>
      <div
        className="flex items-center gap-1 px-1.5 py-0.5 rounded-full text-[7px] font-bold font-mono whitespace-nowrap"
        style={{ background: '#04141a', border: '0.5px solid #0e9a9a', color: '#4de8e8' }}
      >
        <Lock size={8} /> {label}
      </div>
    </div>
  )
}

function Packet({ leftPct, color, label, visible }) {
  return (
    <AnimatePresence>
      {visible && (
        <motion.div
          initial={{ opacity: 0 }}
          animate={{ left: `${leftPct}%`, opacity: 1 }}
          exit={{ opacity: 0 }}
          transition={{ left: { duration: TIMING.packetMove, ease: 'easeInOut' }, opacity: { duration: 0.2 } }}
          className="absolute top-7 z-10"
          style={{ transform: 'translate(-50%, -50%)' }}
        >
          <div className="relative flex flex-col items-center">
            <div
              className="w-3.5 h-3.5 rounded-full"
              style={{ background: color, boxShadow: `0 0 12px ${color}` }}
            />
            {label && (
              <div
                className="absolute top-4 whitespace-nowrap text-[9px] font-mono font-bold px-1.5 py-0.5 rounded"
                style={{ background: 'rgba(6,10,20,0.9)', color, border: `0.5px solid ${color}` }}
              >
                {label}
              </div>
            )}
          </div>
        </motion.div>
      )}
    </AnimatePresence>
  )
}

function LiveChecks({ active }) {
  return (
    <div className="rounded-lg p-3 min-h-[64px]" style={{ background: '#0a0e1a', border: '0.5px solid #1a2a3a' }}>
      <div className="flex items-center gap-1.5 text-[9px] tracking-[0.14em] uppercase mb-1.5" style={{ color: '#4a9edd' }}>
        <ListChecks size={11} /> {active ? `Checks — ${active.label}` : 'Live Checks'}
      </div>
      {!active && <div className="text-[10.5px] text-[#3a4a5a] italic">Individual checks appear here as the request reaches each stage.</div>}
      <AnimatePresence>
        {active && (
          <div className="space-y-1">
            {active.items.map((chk, i) => {
              const Icon = CHECK_ICON[chk.status] || CheckCircle2
              const color = CHECK_COLOR[chk.status] || '#4ade80'
              return (
                <motion.div
                  key={`${active.label}-${i}`}
                  initial={{ opacity: 0, x: -8 }}
                  animate={{ opacity: 1, x: 0 }}
                  transition={{ duration: 0.25 }}
                  className="flex items-start gap-1.5 text-[10.5px] leading-snug"
                >
                  <Icon size={13} className="shrink-0 mt-0.5" style={{ color }} />
                  <span style={{ color: chk.status === 'fail' ? '#f87171' : '#b0c0d0' }}>
                    {chk.text}
                    {chk.note && <span style={{ color: '#6a7a8a' }}> — {chk.note}</span>}
                  </span>
                </motion.div>
              )
            })}
          </div>
        )}
      </AnimatePresence>
    </div>
  )
}

function LogPanel({ lines }) {
  return (
    <div className="rounded-xl p-3 font-mono text-[11px] h-full overflow-y-auto" style={{ background: '#0a0e1a', border: '0.5px solid #1a2a3a', minHeight: 280, maxHeight: 360 }}>
      <div className="text-[9px] tracking-[0.18em] uppercase mb-2" style={{ color: '#4a9edd' }}>Trace</div>
      <AnimatePresence>
        {lines.length === 0 && (
          <div className="text-[#3a4a5a] italic">Send a request to see the trace…</div>
        )}
        {lines.map((l, i) => (
          <motion.div
            key={i}
            initial={{ opacity: 0, x: -8 }}
            animate={{ opacity: 1, x: 0 }}
            transition={{ duration: 0.3 }}
            className="py-1 leading-snug flex items-start gap-1.5"
            style={{
              color: l.verdict === 'fail' || l.verdict === 'blocked' ? '#f87171' : l.verdict === 'pass' ? '#4ade80' : '#94a3b8',
              borderBottom: '0.5px solid #131a28',
            }}
          >
            <span style={{ color: '#2a3a4a' }}>{String(i + 1).padStart(2, '0')}</span>
            <span>{l.text}</span>
          </motion.div>
        ))}
      </AnimatePresence>
    </div>
  )
}

// ── Main page ─────────────────────────────────────────────────────────────────

export default function TrafficFlow() {
  const [stageStatus, setStageStatus] = useState(Array(N).fill('idle'))
  const [packet, setPacket] = useState({ visible: false, leftPct: centerPct(0), color: '#4a9edd', label: '' })
  const [logLines, setLogLines] = useState([])
  const [resultBanner, setResultBanner] = useState(null) // { ok, text }
  const [running, setRunning] = useState(false)
  const [autoLoop, setAutoLoop] = useState(false)
  const [activePayload, setActivePayload] = useState(null) // 'valid' | 'malicious' | 'route_fail' | null
  const [activeChecks, setActiveChecks] = useState(null)
  const cancelRef = useRef(false)

  const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

  const reset = useCallback(() => {
    setStageStatus(Array(N).fill('idle'))
    setPacket({ visible: false, leftPct: centerPct(0), color: '#4a9edd', label: '' })
    setLogLines([])
    setResultBanner(null)
    setActivePayload(null)
    setActiveChecks(null)
  }, [])

  const runScenario = useCallback(async (kind) => {
    if (running) return
    cancelRef.current = false
    setRunning(true)
    reset()
    await sleep(150)
    setActivePayload(kind)

    const { log, color: requestColor } = SCENARIOS[kind]

    setPacket({ visible: true, leftPct: centerPct(0), color: requestColor, label: 'REQUEST' })
    const newStatus = Array(N).fill('idle')

    for (const entry of log) {
      if (cancelRef.current) return
      const i = entry.stage

      // The "blocked" line re-describes the same stage as the "fail" line
      // right before it — skip re-entering/re-activating that node so its
      // red fail ring stays put instead of flashing back to blue.
      if (entry.verdict !== 'blocked') {
        setPacket((p) => ({ ...p, leftPct: centerPct(i) }))
        newStatus[i] = 'active'
        setStageStatus([...newStatus])
        setActiveChecks(entry.checks ? { label: STAGES[i].label, items: [] } : null)
        await sleep(TIMING.enter)
        if (cancelRef.current) return

        if (entry.checks) {
          for (const chk of entry.checks) {
            await sleep(TIMING.checkStep)
            if (cancelRef.current) return
            setActiveChecks((prev) => (prev ? { ...prev, items: [...prev.items, chk] } : prev))
          }
          await sleep(TIMING.checkSettle)
          if (cancelRef.current) return
        }
      }

      setLogLines((prev) => [...prev, entry])

      if (entry.verdict === 'pass') {
        newStatus[i] = 'pass'
        setStageStatus([...newStatus])
      } else if (entry.verdict === 'fail') {
        newStatus[i] = 'fail'
        setStageStatus([...newStatus])
        // shake the packet in place to signal rejection
        await sleep(TIMING.failShake)
      } else if (entry.verdict === 'blocked') {
        // bounce the request packet back to the client as a 403 response,
        // from whichever stage actually rejected it
        setPacket({ visible: true, leftPct: centerPct(i), color: '#e74c3c', label: '403 BLOCKED' })
        await sleep(TIMING.blockedHold)
        setPacket((p) => ({ ...p, leftPct: centerPct(0) }))
        await sleep(TIMING.blockedReturn)
        setPacket({ visible: false, leftPct: centerPct(0), color: requestColor, label: '' })
        setResultBanner({ ok: false, text: entry.resultText })
      }
      await sleep(TIMING.postEntry)
    }

    if (kind === 'valid') {
      // success: send a response packet all the way back to the client
      await sleep(TIMING.postEntry)
      setPacket({ visible: true, leftPct: centerPct(N - 1), color: '#2ecc71', label: '201 CREATED' })
      await sleep(TIMING.successHold)
      setPacket((p) => ({ ...p, leftPct: centerPct(0) }))
      await sleep(TIMING.successReturn)
      setPacket({ visible: false, leftPct: centerPct(0), color: requestColor, label: '' })
      setResultBanner({ ok: true, text: '201 Created — user registered successfully' })
    }

    setRunning(false)

    if (autoLoop && !cancelRef.current) {
      await sleep(TIMING.loopGap)
      if (!cancelRef.current) {
        runScenario(SCENARIO_ORDER[(SCENARIO_ORDER.indexOf(kind) + 1) % SCENARIO_ORDER.length])
      }
    }
  }, [running, autoLoop, reset])

  const stopAll = useCallback(() => {
    cancelRef.current = true
    setRunning(false)
    setAutoLoop(false)
  }, [])

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between mb-2">
        <div>
          <h1 className="text-xl font-bold text-white flex items-center gap-2">
            <Waypoints size={20} className="text-blue-400" />
            Traffic &amp; Policy Enforcement Flow
          </h1>
          <p className="text-sm text-jpmc-muted">
            A request's real path from the public domain to the backend — and exactly where a bad one gets stopped, and why.
          </p>
        </div>
      </div>

      {/* Controls */}
      <div className="flex flex-wrap items-center gap-2 rounded-xl p-3" style={{ background: '#0d1117', border: '0.5px solid #1a1a2a' }}>
        <button
          disabled={running}
          onClick={() => runScenario('valid')}
          className="flex items-center gap-1.5 text-xs px-3 py-1.5 rounded font-mono font-semibold disabled:opacity-40"
          style={{ background: '#06170e', border: '0.5px solid #1a5a3a', color: '#4ade80' }}
        >
          <Play size={12} /> Send Valid Request
        </button>
        <button
          disabled={running}
          onClick={() => runScenario('malicious')}
          className="flex items-center gap-1.5 text-xs px-3 py-1.5 rounded font-mono font-semibold disabled:opacity-40"
          style={{ background: '#1a0e06', border: '0.5px solid #7a3a1a', color: '#f39c12' }}
        >
          <ShieldAlert size={12} /> Send Malicious Request
        </button>
        <button
          disabled={running}
          onClick={() => runScenario('route_fail')}
          className="flex items-center gap-1.5 text-xs px-3 py-1.5 rounded font-mono font-semibold disabled:opacity-40"
          style={{ background: '#1a1606', border: '0.5px solid #7a6a1a', color: '#fbbf24' }}
        >
          <FileWarning size={12} /> Send Invalid Request
        </button>
        <button
          onClick={() => { setAutoLoop((v) => !v); if (!autoLoop && !running) runScenario('valid') }}
          className="flex items-center gap-1.5 text-xs px-3 py-1.5 rounded font-mono font-semibold"
          style={{
            background: autoLoop ? '#0a1a2a' : '#0a0a14',
            border: `0.5px solid ${autoLoop ? '#1a4a7a' : '#1a2a3a'}`,
            color: autoLoop ? '#4a9edd' : '#7a8a9a',
          }}
        >
          <Repeat size={12} /> {autoLoop ? 'Auto-Demo: On' : 'Auto-Demo (loop for video)'}
        </button>
        <button
          onClick={stopAll}
          className="flex items-center gap-1.5 text-xs px-3 py-1.5 rounded font-mono font-semibold ml-auto"
          style={{ background: '#0a0a14', border: '0.5px solid #1a2a3a', color: '#7a8a9a' }}
        >
          <RotateCcw size={12} /> Reset
        </button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        {/* Diagram */}
        <div className="lg:col-span-2 rounded-xl p-6" style={{ background: '#0d1117', border: '0.5px solid #1a1a2a' }}>
          {/* Payload preview — spawns from near the Client box (bottom-left,
              roughly where stage 0 sits below) to read as "this is what's
              inside the request that's about to leave from there." */}
          <div className="mb-6 min-h-[86px]">
            <AnimatePresence mode="wait">
              {activePayload && (
                <motion.div
                  key={activePayload}
                  initial={{ opacity: 0, scale: 0.12, y: 14 }}
                  animate={{ opacity: 1, scale: 1, y: 0 }}
                  exit={{ opacity: 0, scale: 0.4, y: 6 }}
                  transition={{ type: 'spring', stiffness: 240, damping: 20 }}
                  style={{
                    transformOrigin: `${centerPct(0)}% 100%`,
                    background: '#080b12',
                    border: `0.5px solid ${SCENARIOS[activePayload].border}`,
                    color: SCENARIOS[activePayload].text,
                  }}
                  className="rounded-lg p-3 font-mono text-[10.5px] leading-relaxed"
                >
                  <div className="text-[9px] tracking-widest uppercase mb-1" style={{ color: SCENARIOS[activePayload].badgeColor }}>
                    POST /api/v1/users/register ({SCENARIOS[activePayload].badge})
                  </div>
                  <div>{'{'}</div>
                  {Object.entries(SCENARIOS[activePayload].fields).map(([key, value], idx, arr) => {
                    const quoted = typeof value === 'string' || Array.isArray(value) || (value && typeof value === 'object' && 'flag' in value)
                    return (
                      <div key={key} className="pl-4">
                        "{key}": {quoted ? '"' : ''}<FieldValue value={value} />{quoted ? '"' : ''}{idx < arr.length - 1 ? ',' : ''}
                      </div>
                    )
                  })}
                  <div>{'}'}</div>
                </motion.div>
              )}
              {!activePayload && (
                <div className="rounded-lg p-3 text-[11px] text-[#3a4a5a] italic" style={{ background: '#080b12', border: '0.5px dashed #1a2a3a' }}>
                  Choose a scenario above to see the request payload here.
                </div>
              )}
            </AnimatePresence>
          </div>

          {/* Tier bands — real network zones, matching Protection Architecture */}
          <div className="flex mb-2">
            {TIER_BANDS.map((band) => (
              <div key={band.label} style={{ width: `${((band.to - band.from + 1) / N) * 100}%` }} className="px-0.5">
                <div
                  className="text-center text-[8px] font-bold tracking-wider font-mono py-1 rounded truncate"
                  style={{ color: band.color, background: `${band.color}1a`, border: `0.5px solid ${band.color}55` }}
                  title={band.label}
                >
                  {band.label}
                </div>
              </div>
            ))}
          </div>

          {/* Track */}
          <div className="relative pt-2 pb-10">
            {/* base line */}
            <div className="absolute left-0 right-0 top-7 h-[2px]" style={{ background: '#1a2a3a', marginLeft: `${centerPct(0)}%`, marginRight: `${100 - centerPct(N - 1)}%` }} />
            {TLS_MARKERS.map((m) => (
              <TLSMark key={m.label} leftPct={((m.afterStage + 1) / N) * 100} label={m.label} />
            ))}
            <Packet leftPct={packet.leftPct} color={packet.color} label={packet.label} visible={packet.visible} />
            <div className="flex relative" style={{ zIndex: 1 }}>
              {STAGES.map((s, i) => (
                <StageNode key={s.id} stage={s} status={stageStatus[i]} />
              ))}
            </div>
          </div>

          {/* Live checks — what's actually being evaluated right now */}
          <LiveChecks active={activeChecks} />

          {/* Result banner */}
          <AnimatePresence>
            {resultBanner && (
              <motion.div
                initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }}
                className="rounded-lg px-4 py-2.5 mt-3 text-sm font-semibold flex items-center gap-2"
                style={{
                  background: resultBanner.ok ? 'rgba(46,204,113,0.08)' : 'rgba(231,76,60,0.08)',
                  border: `0.5px solid ${resultBanner.ok ? '#1a5a3a' : '#5a1a1a'}`,
                  color: resultBanner.ok ? '#4ade80' : '#f87171',
                }}
              >
                {resultBanner.ok ? <CheckCircle2 size={16} /> : <Ban size={16} />}
                {resultBanner.text}
              </motion.div>
            )}
          </AnimatePresence>

        </div>

        {/* Log panel */}
        <LogPanel lines={logLines} />
      </div>
    </div>
  )
}
