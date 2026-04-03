import React, { createContext, useContext, useState, useEffect, useCallback } from 'react'

const AuthContext = createContext(null)

// Decode the JWT payload and return true if the exp claim is in the past
function jwtExpired(jwt) {
  try {
    const parts = jwt.split('.')
    if (parts.length < 2) return true
    const claims = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/')))
    return claims.exp ? Date.now() / 1000 > claims.exp : false
  } catch { return true }
}

function loadStoredSession() {
  try {
    const stored = localStorage.getItem('session')
    if (!stored) return null
    const sess = JSON.parse(stored)
    if (sess?.session_jwt && jwtExpired(sess.session_jwt)) {
      localStorage.removeItem('session')
      localStorage.removeItem('dpopKeys')
      return null
    }
    return sess
  } catch { return null }
}

export function AuthProvider({ children }) {
  const [session, setSession] = useState(() => loadStoredSession())
  const [dpopKeys, setDpopKeys] = useState(() => {
    try {
      const stored = localStorage.getItem('dpopKeys')
      return stored ? JSON.parse(stored) : null
    } catch { return null }
  })

  const login = useCallback((sess, keys) => {
    setSession(sess)
    setDpopKeys(keys)
    localStorage.setItem('session', JSON.stringify(sess))
    localStorage.setItem('dpopKeys', JSON.stringify(keys))
  }, [])

  const logout = useCallback(() => {
    setSession(null)
    setDpopKeys(null)
    localStorage.removeItem('session')
    localStorage.removeItem('dpopKeys')
    document.cookie = 'ingress_session=; Path=/; Max-Age=0'
  }, [])

  // Periodically check whether the stored JWT has expired and log out if so
  useEffect(() => {
    const check = () => {
      const jwt = session?.session_jwt
      if (jwt && jwtExpired(jwt)) {
        logout()
      }
    }
    check() // immediate check on mount / session change
    const id = setInterval(check, 30_000) // re-check every 30 s
    return () => clearInterval(id)
  }, [session, logout])

  return (
    <AuthContext.Provider value={{ session, dpopKeys, login, logout }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
