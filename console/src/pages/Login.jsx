import React, { useState, useEffect } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import { motion, AnimatePresence } from 'framer-motion'
import { Shield, User, Check, Loader2 } from 'lucide-react'
import { useAuth } from '../context/AuthContext'
import { useConfig } from '../context/ConfigContext'

function base64urlEncode(buffer) {
  const bytes = new Uint8Array(buffer)
  let str = ''
  bytes.forEach(b => str += String.fromCharCode(b))
  return btoa(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
}

export default function Login() {
  const navigate = useNavigate()
  const location = useLocation()
  const { login } = useAuth()
  const { AUTH_URL } = useConfig()
  const [users, setUsers] = useState([])
  const [selectedEmail, setSelectedEmail] = useState('')
  const [password, setPassword] = useState('demo1234')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    fetch(`${AUTH_URL}/demo/users`).then(r => r.json()).then(setUsers).catch(() => {})
  }, [AUTH_URL])

  const handleLogin = async (e) => {
    e.preventDefault()
    if (!selectedEmail) return
    setError('')
    setLoading(true)

    try {
      // Generate PKCE
      const codeVerifier = base64urlEncode(crypto.getRandomValues(new Uint8Array(32)))
      const codeChallenge = base64urlEncode(
        await crypto.subtle.digest('SHA-256', new TextEncoder().encode(codeVerifier))
      )

      // Generate DPoP keypair
      const keyPair = await crypto.subtle.generateKey(
        { name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify']
      )
      const publicJwk = await crypto.subtle.exportKey('jwk', keyPair.publicKey)
      const privateJwk = await crypto.subtle.exportKey('jwk', keyPair.privateKey)

      // Step 1: Authorize
      const authResp = await fetch(`${AUTH_URL}/auth/authorize`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          email: selectedEmail, password, client_id: 'ingress-console',
          redirect_uri: window.location.origin + '/callback',
          code_challenge: codeChallenge, code_challenge_method: 'S256',
        }),
      })
      if (!authResp.ok) throw new Error('Invalid credentials')
      const { code } = await authResp.json()

      // Step 2: Token exchange
      const tokenResp = await fetch(`${AUTH_URL}/auth/token`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          grant_type: 'authorization_code', code,
          redirect_uri: window.location.origin + '/callback',
          client_id: 'ingress-console', code_verifier: codeVerifier,
        }),
      })
      if (!tokenResp.ok) throw new Error('Token exchange failed')
      const tokenData = await tokenResp.json()

      // Step 3: Create session
      const sessionResp = await fetch(`${AUTH_URL}/session/create`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          access_token: tokenData.access_token,
          dpop_jwk: publicJwk,
        }),
      })
      if (!sessionResp.ok) throw new Error('Session creation failed')
      const sessionData = await sessionResp.json()

      // Parse the session JWT claims
      const jwtParts = sessionData.session_jwt.split('.')
      const claims = JSON.parse(atob(jwtParts[1].replace(/-/g, '+').replace(/_/g, '/')))

      const sessionInfo = {
        session_jwt: sessionData.session_jwt,
        sid: sessionData.sid,
        email: claims.email,
        name: claims.name,
        roles: claims.roles,
        entity: claims.entity,
        sub: claims.sub,
      }

      login(sessionInfo, { publicKey: publicJwk, privateKey: privateJwk })

      // Set a cookie on localhost so same-origin requests (console) carry the session
      document.cookie = `ingress_session=${sessionData.session_jwt}; Path=/; SameSite=Lax; Max-Age=${60 * 60 * 24}`

      // Also plant the cookie on the gateway domain (jpmm.jpm.com) so that browser
      // page refreshes on gateway-served routes don't redirect back to login.
      // This is a best-effort call — auth still works via Bearer header for API calls.
      try {
        await fetch('https://jpmm.jpm.com/_set_cookie', {
          method: 'POST',
          credentials: 'include',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ session_jwt: sessionData.session_jwt }),
        })
      } catch (_) {
        // Non-fatal: gateway may not be reachable
      }

      navigate(location.state?.from?.pathname || '/')
    } catch (err) {
      setError(err.message)
    }
    setLoading(false)
  }

  return (
    <div className="min-h-screen flex items-center justify-center p-4 -ml-0">
      <motion.div
        initial={{ opacity: 0, y: 20 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5 }}
        className="w-full max-w-lg"
      >
        {/* Header */}
        <div className="text-center mb-8">
          <div className="flex items-center justify-center gap-3 mb-4">
            <div className="w-12 h-12 rounded-2xl bg-gradient-to-br from-blue-500 to-cyan-400 flex items-center justify-center">
              <Shield className="text-white" size={24} />
            </div>
          </div>
          <h1 className="text-2xl font-bold text-white mb-1">CIB Ingress Admin Console</h1>
          <p className="text-jpmc-muted text-sm">J.P. Morgan CIB -- Next Generation Ingress</p>
        </div>

        {/* Login Card */}
        <div className="glass-card p-6">
          <form onSubmit={handleLogin}>
            {/* User Selection */}
            <div className="mb-5">
              <label className="block text-xs font-medium text-jpmc-muted uppercase tracking-wider mb-3">
                Select Demo User
              </label>
              <div className="space-y-2">
                {users.map(u => (
                  <motion.div
                    key={u.email}
                    whileHover={{ scale: 1.01 }}
                    whileTap={{ scale: 0.99 }}
                    onClick={() => setSelectedEmail(u.email)}
                    className={`p-3 rounded-lg border cursor-pointer transition-all duration-150 ${
                      selectedEmail === u.email
                        ? 'border-blue-500/50 bg-blue-500/10'
                        : 'border-jpmc-border/50 bg-jpmc-navy/50 hover:border-jpmc-border'
                    }`}
                  >
                    <div className="flex items-center gap-3">
                      <div className={`w-10 h-10 rounded-full flex items-center justify-center shrink-0 ${
                        selectedEmail === u.email
                          ? 'bg-gradient-to-br from-blue-500 to-indigo-600'
                          : 'bg-jpmc-border'
                      }`}>
                        {selectedEmail === u.email ? (
                          <Check size={16} className="text-white" />
                        ) : (
                          <User size={16} className="text-jpmc-muted" />
                        )}
                      </div>
                      <div className="flex-1 min-w-0">
                        <div className="text-sm font-medium text-jpmc-text">{u.name || u.email}</div>
                        <div className="text-xs text-jpmc-muted truncate">{u.email}</div>
                      </div>
                      <div className="flex flex-wrap gap-1 shrink-0">
                        {(u.roles || []).map(role => (
                          <span key={role} className="badge-blue text-[10px]">{role}</span>
                        ))}
                      </div>
                      {u.entity && (
                        <span className="badge-gray text-[10px] shrink-0">{u.entity}</span>
                      )}
                    </div>
                  </motion.div>
                ))}
                {users.length === 0 && (
                  <div className="text-center py-6 text-jpmc-muted text-sm">
                    <Loader2 size={16} className="animate-spin inline mr-2" />
                    Loading demo users...
                  </div>
                )}
              </div>
            </div>

            {/* Password */}
            <div className="mb-5">
              <label className="block text-xs font-medium text-jpmc-muted uppercase tracking-wider mb-2">
                Password
              </label>
              <input
                type="password"
                className="input-field"
                value={password}
                onChange={e => setPassword(e.target.value)}
                placeholder="Enter password"
              />
            </div>

            {/* Error */}
            <AnimatePresence>
              {error && (
                <motion.div
                  initial={{ opacity: 0, height: 0 }}
                  animate={{ opacity: 1, height: 'auto' }}
                  exit={{ opacity: 0, height: 0 }}
                  className="mb-4 p-3 rounded-lg bg-red-500/10 border border-red-500/30 text-red-400 text-sm"
                >
                  {error}
                </motion.div>
              )}
            </AnimatePresence>

            {/* Submit */}
            <motion.button
              type="submit"
              disabled={loading || !selectedEmail}
              whileHover={{ scale: loading ? 1 : 1.01 }}
              whileTap={{ scale: loading ? 1 : 0.99 }}
              className="w-full py-3 bg-gradient-to-r from-blue-600 to-blue-500 hover:from-blue-500 hover:to-blue-400 disabled:from-gray-600 disabled:to-gray-600 disabled:cursor-not-allowed text-white rounded-lg font-semibold text-sm transition-all duration-150 flex items-center justify-center gap-2"
            >
              {loading ? (
                <>
                  <Loader2 size={16} className="animate-spin" />
                  Authenticating...
                </>
              ) : (
                'Sign In with PKCE + DPoP'
              )}
            </motion.button>
          </form>

          {/* Footer info */}
          <div className="mt-5 pt-4 border-t border-jpmc-border/30 text-center">
            <p className="text-[11px] text-jpmc-muted leading-relaxed">
              This is a demonstration environment. Authentication uses PKCE + DPoP flow
              with ECDSA keypair generation and session-bound tokens.
            </p>
          </div>
        </div>
      </motion.div>
    </div>
  )
}
