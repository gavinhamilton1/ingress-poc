import React, { useState, useRef, useCallback } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { Network, Play, Square, Pause } from 'lucide-react'

// ── Primitive badges ──────────────────────────────────────────────────────────
const GoBadge    = ({ children }) => <span className="inline-block text-[8px] px-1.5 py-0.5 rounded bg-[#00333a] text-[#00ADD8] border border-[#005a66] ml-1 font-mono">{children}</span>
const CtrlBadge  = ({ children }) => <span className="inline-block text-[8px] px-1.5 py-0.5 rounded bg-[#061020] text-[#4a9edd] border border-[#1a3060] ml-1 font-mono">{children}</span>
const DataBadge  = ({ children }) => <span className="inline-block text-[8px] px-1.5 py-0.5 rounded bg-[#1a0606] text-[#e74c3c] border border-[#3a1010] ml-1 font-mono">{children}</span>
const DRBadge    = ({ children }) => <span className="inline-block text-[8px] px-1.5 py-0.5 rounded bg-[#1a1200] text-[#BA7517] ml-1 font-mono">{children}</span>
const TopicChip  = ({ children }) => <span className="inline-block text-[8px] px-1.5 py-0.5 rounded bg-[#080e18] text-[#2a5a8a] border border-[#1a2a4a] font-mono">{children}</span>
const OtelChip   = ({ children }) => <span className="inline-block text-[8px] px-1.5 py-0.5 rounded bg-[#061208] text-[#2a7a2a] border border-[#1a4a1a] font-mono">{children}</span>
const ServiceChip = ({ c, bg, border, children }) => (
  <span className="inline-block text-[9px] px-2 py-0.5 rounded font-mono" style={{ background: bg, border: `0.5px solid ${border}`, color: c }}>{children}</span>
)

// ── Layout helpers ────────────────────────────────────────────────────────────
const Arrow = ({ label }) => (
  <div className="text-center py-1.5 text-[10px] text-[#333] tracking-widest font-mono select-none">{label || '↓'}</div>
)

const TLSBar = ({ color = '#0e9a9a', gradient = '#0e6a6a', children }) => (
  <div className="flex items-center gap-3 my-2">
    <div className="flex-1 h-px" style={{ background: `linear-gradient(90deg, transparent, ${gradient})` }} />
    <div className="text-[8px] font-bold tracking-wider whitespace-nowrap font-mono px-1" style={{ color }}>{children}</div>
    <div className="flex-1 h-px" style={{ background: `linear-gradient(270deg, transparent, ${gradient})` }} />
  </div>
)

// A labelled layer box
const Layer = ({ label, tag, accentColor = '#4a9edd', borderColor, bgColor, children, id }) => (
  <div id={id} className="relative rounded-xl p-3 mb-1" style={{ border: `0.5px solid ${borderColor}`, background: bgColor }}>
    <div className="absolute -top-[9px] left-3 text-[9px] font-bold tracking-widest px-2 font-mono" style={{ color: accentColor, background: bgColor || '#0a0e1a' }}>
      {label}
    </div>
    {tag && (
      <div className="absolute -top-[9px] right-3 text-[9px] text-[#444] px-2 font-mono" style={{ background: bgColor || '#0a0e1a' }}>
        {tag}
      </div>
    )}
    <div className="mt-2">{children}</div>
  </div>
)

// A content box within a layer
const Box = ({ title, titleColor, sub, border, bg, children, right }) => (
  <div className="rounded-lg p-2.5" style={{ background: bg, border: `0.5px solid ${border}` }}>
    <div className="flex justify-between items-baseline mb-1">
      <div className="text-[10px] font-bold tracking-wide font-mono" style={{ color: titleColor }}>{title}</div>
      {right && <div className="text-[8px] font-mono" style={{ color: titleColor, opacity: 0.5 }}>{right}</div>}
    </div>
    {sub && <div className="text-[9px] leading-relaxed opacity-75 font-mono mb-1">{sub}</div>}
    {children}
  </div>
)

// Auth pipeline stack inside a data-plane box
const AuthStack = ({ title, steps }) => (
  <div className="mt-2 rounded p-2 font-mono" style={{ background: '#0a0416', border: '0.5px solid #2d1b4e' }}>
    <div className="text-[8px] font-bold mb-1.5 tracking-wider" style={{ color: '#9b59b6' }}>{title}</div>
    {steps.map((s, i) => <div key={i} className="text-[8px] leading-loose" style={{ color: '#6a3a7a' }}>{s}</div>)}
  </div>
)

// Section title separator
const SectionTitle = ({ color, children, id }) => (
  <div id={id} className="text-[9px] font-bold tracking-widest mt-6 mb-2 pb-2 border-t font-mono"
    style={{ color, borderColor: '#1a1a2a' }}>
    {children}
  </div>
)

// ── Main component ────────────────────────────────────────────────────────────
const TOUR_SECTIONS = [
  {
    id: 'client',
    title: 'Client Layer',
    text: 'Three client types connect to the platform: Browser (web portals with DPoP + session cookies), API Client (OAuth bearer tokens with DPoP proof), and M2M Service (mTLS cert-bound client credentials). Every connection terminates TLS at L2 — the client never talks directly to an internal service.',
  },
  {
    id: 'dns',
    title: 'L0 — DNS Control Plane',
    text: 'JPM-authoritative DNS (ns1–ns06.jpmorganchase.com) resolves all public hostnames. Cloudflare acts as a secondary for DR only. Because both GTM failover and the DR ingress path use Cloudflare, a single Cloudflare outage is a correlated failure risk (R1) — tracked as an open risk item.',
  },
  {
    id: 'gtm',
    title: 'L1 — Global Traffic Manager',
    text: 'Akamai GTM provides GeoDNS and health-check-based routing — it selects the correct regional datacenter and injects x-akamai-request-id and W3C traceparent so every request is traceable from the very first hop. Cloudflare Load Balancer covers DR scenarios only and requires a manual flip — it is NOT a functional equivalent to GTM.',
  },
  {
    id: 'cdn',
    title: 'L2 — CDN / Edge + WAF',
    text: 'Akamai Ion CDN terminates TLS here — this is the outer TLS boundary. Kona WAF enforces XSS, path traversal, and bot rules before traffic ever reaches the data centre. Routes are split at this layer: /api paths go to Kong, /web paths go to Envoy. The DR path (Cloudflare Edge) has no WAF equivalent to Kona.',
  },
  {
    id: 'perimeter',
    title: 'L3 — Regional Perimeter',
    text: 'TLS is re-originated from L2 to L3, and a new DPoP proof is bound to this inner connection. PSaaS+ (GKP path) provides perimeter enforcement across NA, EMEA, and APAC datacentres. CTC Edge handles the AWS path. Both inject regional context headers used downstream for routing and observability.',
  },
  {
    id: 'controllers',
    title: 'L4 — Ingress Controllers (no traffic)',
    text: 'The Envoy Gateway Controller and Kong Ingress Controller handle configuration only — zero request traffic flows through them. The Envoy controller pushes xDS config via gRPC ADS (go-control-plane). The Kong controller calls the Kong Admin API. Both expose drift-detection endpoints polled by the Management API every 10 seconds.',
  },
  {
    id: 'dataplane',
    title: 'L4 — Data Plane (auth enforcement)',
    text: 'ALL request traffic flows through the data plane. Each gateway pod runs a 3-stage auth pipeline: ① jwt_authn / JWT plugin — rejects invalid tokens immediately with 401; ② Session Validator sidecar — verifies DPoP binding (htm, htu, iat, jti) and checks the local Revoke Cache; ③ OPA coarse-grained policy — ABAC Rego evaluation in sub-milliseconds with no network hop. Finally, the Context Propagator constructs trusted x-auth-* headers and drops all client-supplied headers before forwarding.',
  },
  {
    id: 'auth',
    title: 'Auth Dependencies — Session Manager + OPA + SpiceDB',
    text: 'Session Manager runs one instance per cloud/region — no cross-region call in the request hot path. It issues session JWTs, maintains a JWKS endpoint cached at each gateway, and replicates session state via the Kafka session-events topic. CAEP revocation events propagate to gateway Revoke Caches in under 1 second. OPA runs as a sidecar for coarse L4 decisions and as a remote AuthZen API for fine-grained L5 decisions. SpiceDB provides ReBAC (desk membership, org hierarchy) and is modelled inside the OPA bundle.',
  },
  {
    id: 'mgmt',
    title: 'Management & Control Plane',
    text: 'The DE Console (React/TypeScript) is the operator interface — routes, drift dashboard, audit log, sessions, traces. The Management API (Go/gin) holds the desired-state Postgres registry and runs a drift-detection goroutine that polls gateway actuals every 10 seconds. In production, changes flow through Bitbucket → Bitbucket Pipelines (policy CI) → ArgoCD → K8s manifests. The POC writes directly to the control plane — a banner is displayed on every console page.',
  },
  {
    id: 'observability',
    title: 'Observability — OpenTelemetry + Dynatrace',
    text: 'Every service uses a shared Go OTEL package that exports spans via OTLP HTTP and propagates W3C traceparent across every hop — from Akamai GTM all the way to the upstream service. In production, Dynatrace OneAgent ingests all telemetry, powers the Davis AI anomaly engine, auto-generates service dependency graphs, and tracks SLOs. In the POC and test environments, Jaeger all-in-one provides the same trace waterfall locally.',
  },
  {
    id: 'sidecar',
    title: 'Sidecar Pattern — per Gateway Pod',
    text: 'Three sidecars run alongside every gateway pod on localhost — no network hop required. The Session Validator (Go, :9001) handles DPoP verification and maintains a Revoke Cache as a Kafka consumer. The OPA PDP (:8181) evaluates local Rego bundles in sub-milliseconds, refreshed via Kafka. The OTEL Collector (:4317) batches and forwards spans to Dynatrace in production or Jaeger in the test environment, with buffering to tolerate backend unavailability.',
  },
]

export default function Architecture() {
  const [showLegend, setShowLegend] = useState(true)
  const [tourRunning, setTourRunning] = useState(false)
  const [tourPaused, setTourPaused] = useState(false)
  const [tooltip, setTooltip] = useState({ visible: false, title: '', text: '' })
  const tourCancelRef = useRef(false)
  const tourPausedRef = useRef(false)

  const sleep = (ms) => new Promise(resolve => setTimeout(resolve, ms))

  // Holds for `ms` but pauses when tourPausedRef is true; cancels if tourCancelRef is true
  const waitHold = async (ms) => {
    const tick = 100
    let elapsed = 0
    while (elapsed < ms) {
      if (tourCancelRef.current) return
      if (!tourPausedRef.current) elapsed += tick
      await sleep(tick)
    }
  }

  const startTour = useCallback(async () => {
    setTourRunning(true)
    setTourPaused(false)
    tourCancelRef.current = false
    tourPausedRef.current = false

    for (let i = 0; i < TOUR_SECTIONS.length; i++) {
      const section = TOUR_SECTIONS[i]
      if (tourCancelRef.current) break

      const el = document.getElementById(section.id)
      if (el) {
        let absTop = 0
        let node = el
        while (node) {
          absTop += node.offsetTop
          node = node.offsetParent
        }
        const offset = i === 0 ? -85 : -25
        window.scrollTo({ top: Math.max(0, absTop + offset), behavior: 'smooth' })
      }

      // Wait for scroll to settle
      await sleep(1200)
      if (tourCancelRef.current) break

      // Fade in tooltip
      setTooltip({ visible: true, title: section.title, text: section.text })

      // Hold (pauseable)
      await waitHold(5000)
      if (tourCancelRef.current) {
        setTooltip({ visible: false, title: '', text: '' })
        break
      }

      // Fade out
      setTooltip({ visible: false, title: '', text: '' })
      await sleep(600)
    }

    setTourRunning(false)
    setTourPaused(false)
    tourCancelRef.current = false
    tourPausedRef.current = false
  }, [])

  const stopTour = useCallback(() => {
    tourCancelRef.current = true
    tourPausedRef.current = false
    setTooltip({ visible: false, title: '', text: '' })
    setTourRunning(false)
    setTourPaused(false)
  }, [])

  const togglePause = useCallback(() => {
    const next = !tourPausedRef.current
    tourPausedRef.current = next
    setTourPaused(next)
  }, [])

  return (
    <div className="space-y-2">
      {/* Page header */}
      <div className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-xl font-bold text-white flex items-center gap-2">
            <Network size={20} className="text-blue-400" />
            Architecture
          </h1>
          <p className="text-sm text-jpmc-muted">Target state — Data Plane + Control Plane + Observability</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setShowLegend(v => !v)}
            className="btn-secondary text-xs"
          >
            {showLegend ? 'Hide' : 'Show'} Legend
          </button>
          {tourRunning ? (
            <button
              onClick={stopTour}
              className="flex items-center gap-1.5 text-xs px-3 py-1.5 rounded font-mono font-semibold"
              style={{ background: '#2a0a0a', border: '0.5px solid #7a1a1a', color: '#e74c3c' }}
            >
              <Square size={12} /> Stop Tour
            </button>
          ) : (
            <button
              onClick={startTour}
              className="flex items-center gap-1.5 text-xs px-3 py-1.5 rounded font-mono font-semibold"
              style={{ background: '#0a1a2a', border: '0.5px solid #1a4a7a', color: '#4a9edd' }}
            >
              <Play size={12} /> Start Tour
            </button>
          )}
        </div>
      </div>

      {/* ── Tour tooltip overlay ── */}
      <AnimatePresence>
        {tooltip.visible && (
          <motion.div
            key="tour-tooltip"
            initial={{ opacity: 0, y: 12 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: 12 }}
            transition={{ duration: 0.4 }}
            className="fixed z-50 font-mono"
            style={{
              bottom: '1.5rem',
              left: '50%',
              transform: 'translateX(-50%)',
              width: 'min(580px, calc(100vw - 280px))',
              maxHeight: 'calc(100vh - 8rem)',
              overflowY: 'auto',
              background: 'rgba(6, 14, 26, 0.97)',
              border: '0.5px solid #1a4a7a',
              borderRadius: '12px',
              boxShadow: '0 0 40px rgba(74, 158, 221, 0.25), 0 8px 32px rgba(0,0,0,0.7)',
              padding: '1.25rem 1.5rem',
            }}
          >
            <div className="flex items-start gap-3">
              <div className="mt-0.5 shrink-0 w-2 h-2 rounded-full mt-1.5" style={{ background: '#4a9edd', boxShadow: '0 0 6px #4a9edd' }} />
              <div>
                <div className="text-[11px] font-bold mb-1.5 tracking-wide" style={{ color: '#4a9edd' }}>
                  {tooltip.title}
                </div>
                <div className="text-[10px] leading-relaxed" style={{ color: '#94a3b8' }}>
                  {tooltip.text}
                </div>
              </div>
            </div>
            <div className="mt-2 pt-2 flex items-center justify-between" style={{ borderTop: '0.5px solid #1a2a3a' }}>
              <div className="flex items-center gap-2">
                <div className="text-[8px] tracking-widest" style={{ color: '#2a4a6a' }}>ARCHITECTURE TOUR</div>
                <button
                  onClick={togglePause}
                  className="flex items-center gap-1 text-[8px] px-2 py-0.5 rounded font-mono"
                  style={{
                    background: tourPaused ? '#0a1a0a' : '#0a1a2a',
                    border: `0.5px solid ${tourPaused ? '#1a4a1a' : '#1a3a5a'}`,
                    color: tourPaused ? '#2ecc71' : '#4a9edd',
                  }}
                >
                  {tourPaused ? <><Play size={8} /> Resume</> : <><Pause size={8} /> Pause</>}
                </button>
              </div>
              <div className="flex gap-1">
                {TOUR_SECTIONS.map((s) => (
                  <div
                    key={s.id}
                    className="w-1.5 h-1.5 rounded-full"
                    style={{ background: s.title === tooltip.title ? '#4a9edd' : '#1a2a3a' }}
                  />
                ))}
              </div>
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* ── Diagram canvas ── */}
      <div className="rounded-xl p-4 font-mono text-[#c9d1d9] text-[11px]"
        style={{ background: '#0d1117', border: '0.5px solid #1a1a2a' }}>

        {/* Title block */}
        <div className="mb-5">
          <div className="text-[9px] tracking-[0.18em] uppercase mb-1" style={{ color: '#4a9edd' }}>
            Unified Ingress · v4 · Full Architecture
          </div>
          <div className="text-[17px] font-bold text-white">Data Plane + Control Plane + Observability</div>
          <div className="text-[10px] mt-1 font-mono" style={{ color: '#444' }}>
            Go services · gRPC ADS · OPA + SpiceDB · OTEL + Dynatrace · Kafka · DPoP · Context Propagator · Drift detection
          </div>
        </div>

        {/* ── CLIENT ── */}
        <Layer id="client" label="CLIENT" borderColor="#1a1a1a" bgColor="#090909" accentColor="#555">
          <div className="grid grid-cols-3 gap-2">
            {[
              { t: 'Browser',      s: 'HTTPS · session cookie + DPoP · web portals' },
              { t: 'API Client',   s: 'Bearer token + DPoP proof · OAuth · managed APIs' },
              { t: 'M2M Service',  s: 'mTLS cert · client credentials · short TTL' },
            ].map(({ t, s }) => (
              <Box key={t} title={t} titleColor="#666" sub={s} border="#1a1a1a" bg="#0c0c0c" />
            ))}
          </div>
        </Layer>

        <Arrow label="↓ DNS resolution" />

        {/* ── L0 DNS ── */}
        <Layer id="dns" label="L0 · T0 — DNS CONTROL PLANE" accentColor="#2ecc71" borderColor="#1a3a22" bgColor="#08110a">
          <div className="grid grid-cols-2 gap-2">
            <Box title="JPM DNS (primary)" titleColor="#2ecc71"
              sub="ns1–ns06.jpmorganchase.com · authoritative"
              border="#1a3a1a" bg="#060e08" />
            <Box
              title={<>Cloudflare DNS <DRBadge>secondary</DRBadge></>}
              titleColor="#6a5800"
              sub={<>ns0098 · ns0134 · ns0221<br /><span className="text-[#5a2800]">⚠ Same provider as DR ingress — correlated failure risk (R1)</span></>}
              border="#2a2800" bg="#0c0c00" />
          </div>
        </Layer>

        <Arrow label="↓ IP returned · client connects" />

        {/* ── L1 GTM ── */}
        <Layer id="gtm" label="L1 · T0 — GLOBAL STEERING" tag="GeoDNS + health routing" accentColor="#c8a020" borderColor="#3a3010" bgColor="#0e0d06">
          <div className="grid grid-cols-2 gap-2">
            <Box title="Akamai GTM (primary)"
              titleColor="#c8a020"
              sub="GeoDNS · health-check routing · datacenter selection · injects x-akamai-request-id + traceparent · OTEL instrumented at gateway boundary"
              border="#2a2800" bg="#0c0b00" />
            <Box title={<>Cloudflare LB <DRBadge>DR only · manual flip</DRBadge></>}
              titleColor="#5a4500"
              sub="Not functionally equivalent to GTM · no automatic failover · critical functions only"
              border="#222000" bg="#0c0a00" />
          </div>
        </Layer>

        <Arrow label="↓ routed to CDN / edge PoP" />

        {/* ── L2 CDN/WAF ── */}
        <Layer id="cdn" label="L2 · T0 — CDN / EDGE + WAF" tag="TLS terminates here" accentColor="#e67e22" borderColor="#3a2010" bgColor="#100c05">
          <div className="grid grid-cols-2 gap-2">
            <Box title="Akamai Ion CDN + Kona WAF"
              titleColor="#e67e22"
              sub="WAF enforcement (XSS/traversal/bot rules) · CDN caching · injects full Akamai edge headers · routes /api→Kong · /web→Envoy · OTEL instrumented at gateway boundary"
              border="#2a1800" bg="#0d0800" />
            <Box title={<>Cloudflare Edge DR <DRBadge>DR only</DRBadge></>}
              titleColor="#5a4500"
              sub={<>Edge CDN · basic DDoS<br /><span className="font-bold" style={{ color: '#8B0000' }}>⚠ No WAF equivalent to Kona</span></>}
              border="#202000" bg="#0c0a00" />
          </div>
        </Layer>

        <TLSBar color="#0e9a9a" gradient="#0e6a6a">
          ⬦ TLS TERMINATES AT L2 — client TLS session ends at Akamai / Cloudflare ⬦
        </TLSBar>

        {/* ── L3 Perimeter ── */}
        <Layer id="perimeter" label="L3 · T1 — REGIONAL PERIMETER" tag="TLS re-originated L2→L3 · paths diverge to GKP and AWS here" accentColor="#d35400" borderColor="#3a2a10" bgColor="#0f0c07">
          <div className="grid grid-cols-2 gap-2">
            <div className="rounded-lg p-2" style={{ border: '0.5px solid #1a3a5a' }}>
              <div className="text-[9px] font-bold tracking-wider mb-2 pb-1.5 border-b font-mono" style={{ color: '#4a9edd', borderColor: '#1a1a1a' }}>
                → GKP PATH — PSaaS+
              </div>
              <Box title="PSaaS+"
                titleColor="#d35400"
                sub={<><strong className="text-[#7a6050]">NA:</strong> CDC1, CDC2, RDC &nbsp; <strong className="text-[#7a6050]">EMEA:</strong> Farn, Basi &nbsp; <strong className="text-[#7a6050]">APAC:</strong> Equi(HK), Cave(HK), SG-C01/C02<br />Injects x-psaas-region/datacenter · TLS re-origination · OTEL instrumented at perimeter boundary</>}
                border="#0a2040" bg="#060d18" />
            </div>
            <div className="rounded-lg p-2" style={{ border: '0.5px solid #2a1800' }}>
              <div className="text-[9px] font-bold tracking-wider mb-2 pb-1.5 border-b font-mono" style={{ color: '#d35400', borderColor: '#1a1a1a' }}>
                → AWS PATH — CTC Edge
              </div>
              <Box title="CTC Edge"
                titleColor="#d35400"
                sub={<><strong className="text-[#7a6050]">APAC:</strong> ap-south-1, ap-southeast-1 &nbsp; <strong className="text-[#7a6050]">NA:</strong> us-east-1/2, us-west-2 &nbsp; <strong className="text-[#7a6050]">EMEA:</strong> eu-central-1, eu-west-1/2</>}
                border="#2a1400" bg="#0e0800" />
            </div>
          </div>
        </Layer>

        <TLSBar color="#0e8a9a" gradient="#0e5a7a">
          ⬦ TLS RE-ORIGINATED L3→L4 — DPoP proof bound to this inner connection ⬦
        </TLSBar>

        {/* ── L4 CONTROLLERS ── */}
        <div id="controllers" className="grid grid-cols-2 gap-2 mb-1">
          {/* GKP Controllers */}
          <Layer label={<>L4 CONTROLLERS — GKP <CtrlBadge>CONTROL PLANE · no traffic</CtrlBadge></>}
            accentColor="#4a9edd" borderColor="#0a2040" bgColor="#050e1a">
            <div className="text-[9px] mb-2 font-mono" style={{ color: '#1a4a6a' }}>
              K8S clusters: na-ne &amp; na-nw · farn, basi · sgc
            </div>
            <div className="grid grid-cols-2 gap-1.5">
              <Box title={<>Envoy GW Controller <GoBadge>Go · gRPC ADS</GoBadge></>}
                titleColor="#4a9edd"
                sub="Watches Gateway/HTTPRoute · provisions proxy pods · pushes xDS via gRPC ADS (go-control-plane) · /snapshot/routes for drift detection · handles zero requests"
                border="#0a1e34" bg="#040c14" />
              <Box title={<>Kong Ingress Ctrl <GoBadge>Go</GoBadge></>}
                titleColor="#4a9edd"
                sub="Watches KongPlugin/HTTPRoute · calls Kong Admin API · /sync-status/routes for drift detection · handles zero requests"
                border="#0a1e34" bg="#040c14" />
            </div>
          </Layer>
          {/* AWS Controllers */}
          <Layer label={<>L4 CONTROLLERS — AWS <CtrlBadge>CONTROL PLANE · no traffic</CtrlBadge></>}
            accentColor="#d35400" borderColor="#2a1400" bgColor="#0f0800">
            <div className="text-[9px] mb-2 font-mono" style={{ color: '#5a2a00' }}>
              EKS clusters: ap-southeast-1/2 · eu-west-1/2/central-1 · us-east-1 · us-west-2
            </div>
            <div className="grid grid-cols-2 gap-1.5">
              <Box title={<>Envoy GW Controller <GoBadge>Go · gRPC ADS</GoBadge></>}
                titleColor="#d35400"
                sub="Watches Gateway/HTTPRoute · provisions proxy pods · pushes xDS via gRPC ADS · handles zero requests"
                border="#1e1000" bg="#0c0600" />
              <Box title={<>Kong Ingress Ctrl <GoBadge>Go</GoBadge></>}
                titleColor="#d35400"
                sub="Watches KongPlugin/HTTPRoute · calls Kong Admin API · handles zero requests"
                border="#1e1000" bg="#0c0600" />
            </div>
          </Layer>
        </div>

        <Arrow label="↓ xDS push via gRPC ADS (Envoy) · Admin API config sync (Kong)" />

        {/* ── L4 DATA PLANE ── */}
        <div id="dataplane" className="grid grid-cols-2 gap-2 mb-1">
          {/* GKP Data */}
          <Layer label={<>L4 DATA — GKP <DataBadge>DATA PLANE · auth enforcement</DataBadge></>}
            accentColor="#e74c3c" borderColor="#3a1010" bgColor="#120808">
            <div className="space-y-2">
              <Box title="Envoy Proxy Pods" titleColor="#e74c3c" right="*.web.[region].gkp.jpmorgan.net"
                sub="Web traffic · browser · developer portal · streaming · WebSocket"
                border="#2a1010" bg="#0e0505">
                <AuthStack title="AUTH PIPELINE — 3 components" steps={[
                  <><span className="text-[#7a4a9a] font-bold mr-1">pre</span>jwt_authn — local JWKS cache · reject 401 immediately</>,
                  <><span className="text-[#7a4a9a] font-bold mr-1">①</span>Session Validator (ext_authz · Go sidecar) — DPoP htm+htu+iat+jti · Revoke Cache</>,
                  <><span className="text-[#7a4a9a] font-bold mr-1">②</span>OPA coarse (Go sidecar · local bundle) + SpiceDB mock → allow + obligations</>,
                  <><span className="text-[#7a4a9a] font-bold mr-1">③</span>Context Propagator — construct x-auth-* allowlist · drop all client headers</>,
                ]} />
              </Box>
              <Box title="Kong GW Instances" titleColor="#e74c3c" right="*.api.[region].gkp.jpmorgan.net"
                sub="API traffic · OAuth clients · managed APIs · developer portal"
                border="#2a1010" bg="#0e0505">
                <AuthStack title="AUTH PIPELINE — 3 components + plugin chain" steps={[
                  <><span className="text-[#7a4a9a] font-bold mr-1">pre</span>JWT plugin — local JWKS cache · reject 401 immediately</>,
                  <><span className="text-[#7a4a9a] font-bold mr-1">①</span>Session Validator (pre-function Lua → ext_authz · Go) — DPoP · Revoke Cache</>,
                  <><span className="text-[#7a4a9a] font-bold mr-1">+</span>Plugin chain — rate limit per consumer · request transform · correlation-id</>,
                  <><span className="text-[#7a4a9a] font-bold mr-1">②</span>OPA coarse + SpiceDB mock → allow + obligations</>,
                  <><span className="text-[#7a4a9a] font-bold mr-1">③</span>Context Propagator — construct x-auth-* · drop all client headers</>,
                ]} />
              </Box>
            </div>
          </Layer>
          {/* AWS Data */}
          <Layer label={<>L4 DATA — AWS <DataBadge>DATA PLANE · auth enforcement</DataBadge></>}
            accentColor="#e67e22" borderColor="#3a1800" bgColor="#120a04">
            <div className="space-y-2">
              <Box title="Envoy Proxy Pods" titleColor="#e67e22" right="*.web.[region].aws.jpmorgan.net"
                sub="Web traffic · browser · streaming · WebSocket"
                border="#2a1400" bg="#0f0700">
                <AuthStack title="AUTH PIPELINE — 3 components" steps={[
                  <><span className="text-[#7a4a9a] font-bold mr-1">pre</span>jwt_authn — local JWKS · reject 401 immediately</>,
                  <><span className="text-[#7a4a9a] font-bold mr-1">①</span>Session Validator (ext_authz · Go sidecar) — DPoP · Revoke Cache</>,
                  <><span className="text-[#7a4a9a] font-bold mr-1">②</span>OPA coarse + SpiceDB mock → allow + obligations</>,
                  <><span className="text-[#7a4a9a] font-bold mr-1">③</span>Context Propagator — construct x-auth-* · drop all client headers</>,
                ]} />
              </Box>
              <Box title="Kong GW Instances" titleColor="#e67e22" right="*.api.[region].aws.jpmorgan.net"
                sub="API traffic · OAuth clients · managed APIs"
                border="#2a1400" bg="#0f0700">
                <AuthStack title="AUTH PIPELINE — 3 components + plugin chain" steps={[
                  <><span className="text-[#7a4a9a] font-bold mr-1">pre</span>JWT plugin · <span className="text-[#7a4a9a] font-bold">①</span> Session Validator (Go) · <span className="text-[#7a4a9a] font-bold">+</span> Plugin chain</>,
                  <><span className="text-[#7a4a9a] font-bold mr-1">②</span>OPA coarse + SpiceDB mock · <span className="text-[#7a4a9a] font-bold">③</span> Context Propagator</>,
                ]} />
              </Box>
            </div>
          </Layer>
        </div>

        {/* ── Auth Dependencies ── */}
        <div id="auth" className="mt-2 mb-2">
          <div className="text-[9px] font-bold mb-2 tracking-wider font-mono" style={{ color: '#534AB7' }}>
            AUTH DEPENDENCIES — shared across all L4 instances (multi-region active/active)
          </div>
          <div className="grid grid-cols-2 gap-2">
            <div className="rounded-lg p-2.5" style={{ background: '#0a0416', border: '0.5px solid #2d1b4e' }}>
              <div className="text-[9px] font-bold mb-2 font-mono" style={{ color: '#9b59b6' }}>
                Session Manager — multi-region A/A <GoBadge>Go</GoBadge>
              </div>
              <div className="text-[9px] leading-loose font-mono" style={{ color: '#4a2a6a' }}>
                One instance per cloud/region · no cross-region call in request path<br />
                Session JWT issuer + JWKS authority (not IdP)<br />
                JWKS cached at gateway · background refresh<br />
                Session state replicated via Kafka <span style={{ color: '#2a5a8a' }}>session-events</span><br />
                CAEP receiver → Revoke Events → local Revoke Cache at gateway<br />
                Key rotation: publish to all regions before signing with new key
              </div>
            </div>
            <div className="rounded-lg p-2.5" style={{ background: '#0a0416', border: '0.5px solid #2d1b4e' }}>
              <div className="text-[9px] font-bold mb-2 font-mono" style={{ color: '#9b59b6' }}>
                OPA + SpiceDB — policy decisions
              </div>
              <div className="text-[9px] leading-loose font-mono" style={{ color: '#4a2a6a' }}>
                <strong style={{ color: '#7a4a9a' }}>OPA (coarse — L4):</strong> sidecar · local bundle · Rego ABAC · sub-ms evaluation<br />
                <strong style={{ color: '#7a4a9a' }}>OPA (fine — L5):</strong> AuthZen API · resource entitlement · obligations<br />
                <strong style={{ color: '#7a4a9a' }}>SpiceDB (mocked in OPA):</strong> ReBAC — desk membership · org hierarchy<br />
                Bundles refreshed via Kafka <span style={{ color: '#2a5a8a' }}>policy-bundle-push</span><br />
                Drift monitored — alert if L4/L5 diverge &gt;60s
              </div>
            </div>
          </div>
        </div>

        {/* ── Kafka bar ── */}
        <div className="flex flex-wrap items-center gap-3 rounded-lg px-3 py-2 my-2 font-mono"
          style={{ background: '#0a0a14', border: '0.5px solid #1a2a3a' }}>
          <div className="text-[9px] font-bold" style={{ color: '#2a5a8a' }}>KAFKA TOPICS</div>
          <TopicChip>session-events · A/A replication</TopicChip>
          <TopicChip>revocation-events · &lt;1s propagation · CAEP</TopicChip>
          <TopicChip>risk-signals · fraud / Interdiction feed</TopicChip>
          <TopicChip>policy-bundle-push · OPA refresh</TopicChip>
        </div>

        {/* ── M2M + Cartographer ── */}
        <div className="grid grid-cols-2 gap-2 mb-2">
          <div className="rounded-lg px-3 py-2 text-[9px] font-mono" style={{ background: '#0a1208', border: '0.5px solid #1a3a1a', color: '#2a4a2a' }}>
            <span className="font-bold" style={{ color: '#2ecc71' }}>M2M PATH</span>
            {' '}— client credentials + mTLS · access token from IdP directly · mTLS cert thumbprint replaces DPoP · Token Validator verifies IdP JWKS + cert↔client_id · separate OPA service entitlement policy · 5-min token TTL
          </div>
          <div className="rounded-lg px-3 py-2 text-[9px] font-mono" style={{ background: '#0a0a1a', border: '0.5px solid #1a2a3a', color: '#2a3a4a' }}>
            <span className="font-bold" style={{ color: '#546e9a' }}>CARTOGRAPHER (Palantir)</span>
            {' '}— reads L4 Data proxy/gateway instances as externally exposed assets · feeds Interdiction &amp; Risk · risk signals return via Kafka risk-signals → Session Manager → revocation · does not configure ingress
          </div>
        </div>

        <TLSBar color="#2ecc71" gradient="#1a5a1a">
          ⬦ VERIFIED REQUEST — x-auth-* headers constructed by Context Propagator · trusted internal zone ⬦
        </TLSBar>

        <Arrow label="↓ clean request forwarded to L5" />

        {/* ── L5 Services ── */}
        <div className="grid grid-cols-2 gap-2 mb-1">
          <Layer label="L5 · T3 — SERVICES (GKP)" accentColor="#27ae60" borderColor="#1a3a1a" bgColor="#07100a">
            <div className="flex flex-wrap gap-1.5 mb-2">
              {['GAP','GKP','G-VSI','GCP'].map(s => (
                <ServiceChip key={s} c="#27ae60" bg="#0a160c" border="#1a3a1a">{s}</ServiceChip>
              ))}
            </div>
            <div className="text-[9px] leading-relaxed font-mono" style={{ color: '#1a4a1a' }}>
              OPA fine-grained (AuthZen API) + SpiceDB (ReBAC) · AuthZ Engine · PIP/SoR Connector · Admin Center · Contact master · COPS
            </div>
          </Layer>
          <Layer label="L5 · T3 — SERVICES (AWS)" accentColor="#27ae60" borderColor="#1a2a0a" bgColor="#07100a">
            <div className="flex flex-wrap gap-1.5 mb-2">
              {['EKS','ECS','EC2','Lambda'].map(s => (
                <ServiceChip key={s} c="#3B6D11" bg="#090e05" border="#1a2a0a">{s}</ServiceChip>
              ))}
            </div>
            <div className="text-[9px] leading-relaxed font-mono" style={{ color: '#1a3a0a' }}>
              OPA fine-grained (AuthZen API) + SpiceDB (ReBAC) · AuthZ Engine · PIP/SoR Connector
            </div>
          </Layer>
        </div>

        {/* ══ MANAGEMENT & CONTROL PLANE ══ */}
        <SectionTitle id="mgmt" color="#4a9edd">MANAGEMENT &amp; CONTROL PLANE</SectionTitle>

        <div className="grid grid-cols-2 gap-2 mb-2">
          <Layer label="MANAGEMENT PLANE" accentColor="#4a9edd" borderColor="#1a3a5a" bgColor="#060e1a">
            <div className="space-y-2">
              <Box title={<>DE Console <span style={{ color: '#2a4a6a', fontWeight: 400 }}>TypeScript · React</span></>}
                titleColor="#4a9edd"
                border="#0a1e34" bg="#040c14">
                <div className="text-[9px] leading-relaxed mt-1 font-mono" style={{ color: '#94a3b8', opacity: 0.75 }}>
                  Dashboard · Routes · Drift Dashboard · Request Log · Sessions · Traces · Login<br />
                  <span style={{ color: '#BA7517' }}>POC banner: "Changes write direct to control plane — prod uses Bitbucket+ArgoCD"</span>
                </div>
              </Box>
              <Box title={<>Management API <GoBadge>Go · gin · pgx</GoBadge></>}
                titleColor="#4a9edd"
                border="#0a1e34" bg="#040c14">
                <div className="text-[9px] leading-relaxed mt-1 font-mono" style={{ color: '#94a3b8', opacity: 0.75 }}>
                  Route intent → policy validation → Postgres (desired state)<br />
                  Drift detection goroutine (10s) — polls /snapshot/routes + /sync-status/routes<br />
                  Endpoints: /routes · /actuals · /drift · /audit-log · /policy/validate
                </div>
              </Box>
            </div>
          </Layer>
          <Layer label="INGRESS REGISTRY — Postgres" accentColor="#2ecc71" borderColor="#1a3a1a" bgColor="#06100a">
            <div className="space-y-2">
              <div className="grid grid-cols-2 gap-1.5">
                <Box title="Desired State" titleColor="#2ecc71"
                  sub="Routes table · canonical intent · above all control plane targets"
                  border="#0a2010" bg="#040e06" />
                <Box title="Actual State" titleColor="#2ecc71"
                  sub="actual_routes table · polled from gateways every 10s"
                  border="#0a2010" bg="#040e06" />
              </div>
              <Box title="Drift Detector + Audit Log" titleColor="#2ecc71"
                sub="Compares desired vs actual · surfaces divergence in Drift Dashboard · immutable audit log of all changes"
                border="#0a2010" bg="#040e06" />
            </div>
          </Layer>
        </div>

        {/* GitOps note */}
        <div className="rounded-lg px-3 py-2 text-[9px] font-mono mb-2" style={{ background: '#0f0c00', border: '0.5px solid #3a2800', color: '#5a4a00' }}>
          <span className="font-bold" style={{ color: '#BA7517' }}>GITOPS PATH (production)</span>
          {' '}— Bitbucket on-prem → Bitbucket Pipelines (policy CI) → ArgoCD → apply HTTPRoute/KongPlugin manifests → controllers configure proxy instances.{' '}
          <span style={{ color: '#3a2800' }}>POC: Management API writes directly to control plane. Banner shown on all Console pages.</span>
        </div>

        {/* ══ OBSERVABILITY ══ */}
        <SectionTitle id="observability" color="#27ae60">OBSERVABILITY STACK</SectionTitle>

        <div className="grid grid-cols-2 gap-2 mb-2">
          <div className="rounded-lg p-2.5" style={{ background: '#080e08', border: '0.5px solid #1a3a1a' }}>
            <div className="text-[9px] font-bold mb-2 font-mono" style={{ color: '#27ae60' }}>OpenTelemetry — every service</div>
            <div className="text-[9px] leading-loose font-mono" style={{ color: '#1a4a1a' }}>
              <strong style={{ color: '#3a7a3a' }}>Shared Go package:</strong> shared/otel — OTLP HTTP export · W3C TraceContext + Baggage · AlwaysSample<br />
              <strong style={{ color: '#3a7a3a' }}>W3C traceparent</strong> propagated across all hops end-to-end<br />
              <strong style={{ color: '#3a7a3a' }}>Akamai fallback:</strong> x-akamai-request-id → SHA256 → synthesised traceparent at gateway<br />
              <strong style={{ color: '#3a7a3a' }}>Service names:</strong> akamai.gtm · akamai.edge · psaas.perimeter · envoy-gateway · kong-gateway · auth-service · opa-policy · management-api · svc-api · svc-web
            </div>
          </div>
          <div className="rounded-lg p-2.5" style={{ background: '#080e08', border: '0.5px solid #1a3a1a' }}>
            <div className="text-[9px] font-bold mb-2 font-mono" style={{ color: '#27ae60' }}>Dynatrace <span style={{ color: '#3a5a3a', fontWeight: 400 }}>— production APM + tracing</span></div>
            <div className="text-[9px] leading-loose font-mono" style={{ color: '#1a4a1a' }}>
              OTLP ingest · OneAgent deployed per node · full-stack observability<br />
              <strong style={{ color: '#3a7a3a' }}>Trace waterfall:</strong> akamai.gtm → akamai.edge → psaas → kong/envoy → auth pipeline → svc<br />
              <strong style={{ color: '#3a7a3a' }}>Span attributes:</strong> auth.step · dpop.valid · opa.allow · opa.deny_reason · session.roles<br />
              <strong style={{ color: '#3a7a3a' }}>AI-powered:</strong> Davis engine · anomaly detection · automatic baseline · SLO tracking<br />
              <strong style={{ color: '#3a7a3a' }}>Service dependency graph:</strong> auto-generated from trace and metric data
            </div>
            <div className="mt-2 rounded px-2 py-1.5 text-[8px] font-mono" style={{ background: '#060e06', border: '0.5px dashed #1a3a1a', color: '#2a4a2a' }}>
              <span style={{ color: '#4a7a4a', fontWeight: 700 }}>POC / TEST:</span> Jaeger all-in-one — OTLP HTTP :4318 · UI :16686 · used locally in place of Dynatrace
            </div>
          </div>
        </div>

        {/* OTel spans bar */}
        <div className="flex flex-wrap items-center gap-2 rounded-lg px-3 py-2 mb-2 font-mono"
          style={{ background: '#0a1208', border: '0.5px solid #1a3a1a' }}>
          <div className="text-[9px] font-bold" style={{ color: '#27ae60' }}>KEY SPANS</div>
          {[
            'akamai.gtm.forward', 'akamai.edge.request', 'psaas.perimeter.forward',
            'auth.pre_filter', 'auth.session_validator', 'dpop.verify',
            'revoke_cache.check', 'auth.opa_coarse', 'auth.context_propagator',
            'service.request', 'opa.fine', 'registry.drift_check',
          ].map(s => <OtelChip key={s}>{s}</OtelChip>)}
        </div>

        {/* ══ SIDECAR PATTERN ══ */}
        <SectionTitle id="sidecar" color="#9b59b6">SIDECAR PATTERN — per gateway Pod (production Kubernetes)</SectionTitle>

        <div className="grid grid-cols-3 gap-2 mb-4">
          {[
            {
              title: <>Session Validator <GoBadge>Go · explicit call</GoBadge></>,
              body: 'localhost:9001 · DPoP verify · Revoke Cache (Kafka consumer) · ext_authz handler · hot path — no network hop',
            },
            {
              title: <>OPA PDP <span className="text-[8px] ml-1" style={{ color: '#2a4a6a' }}>(OPA · explicit call)</span></>,
              body: 'localhost:8181 · local policy bundle · sub-ms Rego evaluation · Kafka-notified bundle refresh · no network hop',
            },
            {
              title: <>OTEL Collector <span className="text-[8px] ml-1" style={{ color: '#2a4a6a' }}>(explicit call)</span></>,
              body: 'localhost:4317 · receives spans · batches · forwards to Dynatrace (prod) or Jaeger (test) · buffers if backend unavailable',
            },
          ].map(({ title, body }, i) => (
            <div key={i} className="rounded-lg p-2.5 text-[9px] font-mono" style={{ background: '#0a0416', border: '0.5px solid #2d1b4e', color: '#4a2a6a' }}>
              <div className="font-bold mb-1.5 text-[9px]" style={{ color: '#9b59b6' }}>{title}</div>
              {body}
            </div>
          ))}
        </div>

        {/* ── Legend ── */}
        {showLegend && (
          <motion.div
            initial={{ opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: 'auto' }}
            exit={{ opacity: 0, height: 0 }}
            className="flex flex-wrap gap-3 pt-3 mt-2 font-mono"
            style={{ borderTop: '0.5px solid #1a1a1a' }}
          >
            {[
              { c: '#2ecc71',  label: 'L0 DNS' },
              { c: '#c8a020',  label: 'L1 GTM' },
              { c: '#e67e22',  label: 'L2 CDN/WAF' },
              { c: '#d35400',  label: 'L3 Perimeter' },
              { c: '#4a9edd',  label: 'L4 Controller (no traffic)' },
              { c: '#e74c3c',  label: 'L4 Data GKP' },
              { c: '#e67e22',  label: 'L4 Data AWS' },
              { c: '#9b59b6',  label: 'Auth + Sidecars' },
              { c: '#27ae60',  label: 'L5 + Observability + Control plane' },
              { c: '#00ADD8',  label: 'Go service' },
              { c: '#BA7517',  label: 'DR path (manual)', dim: true },
            ].map(({ c, label, dim }) => (
              <div key={label} className="flex items-center gap-1.5 text-[9px]" style={{ color: '#555', opacity: dim ? 0.6 : 1 }}>
                <div className="w-2 h-2 rounded-full shrink-0" style={{ background: c }} />
                {label}
              </div>
            ))}
          </motion.div>
        )}
      </div>
    </div>
  )
}
