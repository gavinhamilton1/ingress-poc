import React, { createContext, useContext } from 'react'

const ConfigContext = createContext(null)

const defaultConfig = {
  AUTH_URL: import.meta.env.VITE_AUTH_SERVICE_URL || 'http://localhost:8001',
  API_URL: import.meta.env.VITE_MANAGEMENT_API_URL || 'http://localhost:8003',
  GATEWAY_URL: import.meta.env.VITE_GATEWAY_URL || 'http://localhost:8010',
  JAEGER_URL: import.meta.env.VITE_JAEGER_URL || 'http://localhost:16686',
}

export function ConfigProvider({ children }) {
  return (
    <ConfigContext.Provider value={defaultConfig}>
      {children}
    </ConfigContext.Provider>
  )
}

export function useConfig() {
  const ctx = useContext(ConfigContext)
  if (!ctx) throw new Error('useConfig must be used within ConfigProvider')
  return ctx
}
