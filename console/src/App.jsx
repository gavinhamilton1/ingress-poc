import React, { useState } from 'react'
import { BrowserRouter, Routes, Route, useLocation } from 'react-router-dom'
import { AnimatePresence, motion } from 'framer-motion'
import { AlertTriangle } from 'lucide-react'
import { AuthProvider, useAuth } from './context/AuthContext'
import { ConfigProvider } from './context/ConfigContext'
import Sidebar from './components/Sidebar'
import Dashboard from './pages/Dashboard'
import RoutesPage from './pages/Routes'
import Fleets from './pages/Fleets'
import RequestTester from './pages/RequestTester'
import Sessions from './pages/Sessions'
import Traces from './pages/Traces'
import DriftDashboard from './pages/DriftDashboard'
import RawData from './pages/RawData'
import Login from './pages/Login'

function AnimatedRoutes() {
  const location = useLocation()

  return (
    <AnimatePresence mode="wait">
      <motion.div
        key={location.pathname}
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        exit={{ opacity: 0, y: -8 }}
        transition={{ duration: 0.2 }}
      >
        <Routes location={location}>
          <Route path="/" element={<Dashboard />} />
          <Route path="/routes" element={<RoutesPage />} />
          <Route path="/fleets" element={<Fleets />} />
          <Route path="/request-tester" element={<RequestTester />} />
          <Route path="/sessions" element={<Sessions />} />
          <Route path="/traces" element={<Traces />} />
          <Route path="/drift" element={<DriftDashboard />} />
          <Route path="/raw-data" element={<RawData />} />
          <Route path="/login" element={<Login />} />
        </Routes>
      </motion.div>
    </AnimatePresence>
  )
}

function AppLayout() {
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const { session } = useAuth()
  const location = useLocation()

  const isLoginPage = location.pathname === '/login'
  const sidebarWidth = isLoginPage ? 0 : sidebarCollapsed ? 72 : 260

  return (
    <div className="min-h-screen bg-jpmc-navy text-jpmc-text font-sans">
      {/* Sidebar */}
      {!isLoginPage && (
        <Sidebar
          collapsed={sidebarCollapsed}
          onToggle={() => setSidebarCollapsed(!sidebarCollapsed)}
        />
      )}

      {/* Main content area */}
      <div
        className="transition-all duration-200"
        style={{ marginLeft: sidebarWidth }}
      >
        {/* GitOps POC Banner */}
        {!isLoginPage && (
          <div className="bg-amber-500/10 border-b border-amber-500/20 px-6 py-2.5 flex items-center gap-2">
            <AlertTriangle size={14} className="text-amber-400 shrink-0" />
            <span className="text-xs text-amber-300/80">
              <span className="font-semibold">POC mode</span> -- Route changes write directly to the gateway control plane.
              In production this would commit to Bitbucket and be applied by ArgoCD.
            </span>
          </div>
        )}

        {/* Page content */}
        <main className={`${isLoginPage ? '' : 'p-6 max-w-[1400px] mx-auto'}`}>
          <AnimatedRoutes />
        </main>
      </div>
    </div>
  )
}

export default function App() {
  return (
    <BrowserRouter>
      <ConfigProvider>
        <AuthProvider>
          <AppLayout />
        </AuthProvider>
      </ConfigProvider>
    </BrowserRouter>
  )
}
