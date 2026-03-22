import React, { createContext, useContext, useState, useEffect, useCallback } from 'react'

const AuthContext = createContext(null)

export function AuthProvider({ children }) {
  const [session, setSession] = useState(null)
  const [dpopKeys, setDpopKeys] = useState(null)

  useEffect(() => {
    const stored = sessionStorage.getItem('session')
    if (stored) {
      try { setSession(JSON.parse(stored)) } catch {}
    }
    const keys = sessionStorage.getItem('dpopKeys')
    if (keys) {
      try { setDpopKeys(JSON.parse(keys)) } catch {}
    }
  }, [])

  const login = useCallback((sess, keys) => {
    setSession(sess)
    setDpopKeys(keys)
    sessionStorage.setItem('session', JSON.stringify(sess))
    sessionStorage.setItem('dpopKeys', JSON.stringify(keys))
  }, [])

  const logout = useCallback(() => {
    setSession(null)
    setDpopKeys(null)
    sessionStorage.removeItem('session')
    sessionStorage.removeItem('dpopKeys')
  }, [])

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
