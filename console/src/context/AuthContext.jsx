import React, { createContext, useContext, useState, useEffect, useCallback } from 'react'

const AuthContext = createContext(null)

export function AuthProvider({ children }) {
  const [session, setSession] = useState(() => {
    try {
      const stored = localStorage.getItem('session')
      return stored ? JSON.parse(stored) : null
    } catch { return null }
  })
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
