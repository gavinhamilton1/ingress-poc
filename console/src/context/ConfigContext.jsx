import React, { createContext, useContext } from 'react'

const ConfigContext = createContext(null)

const defaultConfig = {
  AUTH_URL: import.meta.env.VITE_AUTH_SERVICE_URL || '/_proxy/auth',
  API_URL: import.meta.env.VITE_MANAGEMENT_API_URL || '/_proxy/management',
  GATEWAY_URL: import.meta.env.VITE_GATEWAY_URL || '/_proxy/gateway',
  JAEGER_URL: import.meta.env.VITE_JAEGER_URL || '/_proxy/jaeger',
  JAEGER_UI_URL: import.meta.env.VITE_JAEGER_UI_URL || 'http://localhost:16686',
  WATCHDOG_URL: import.meta.env.VITE_WATCHDOG_URL || '/_proxy/watchdog',
  GITOPS_URL: import.meta.env.VITE_GITOPS_URL || '/_proxy/management/gitops',
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
