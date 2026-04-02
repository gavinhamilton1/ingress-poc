import React, { useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useLocation } from 'react-router-dom'
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
import GitOps from './pages/GitOps'
import Architecture from './pages/Architecture'
import AuditLog from './pages/AuditLog'
import Login from './pages/Login'

function RequireAuth({ children }) {
  const { session } = useAuth()
  const location = useLocation()
  if (!session) return <Navigate to="/login" state={{ from: location }} replace />
  return children
}

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
          <Route path="/login" element={<Login />} />
          <Route path="/" element={<RequireAuth><Dashboard /></RequireAuth>} />
          <Route path="/routes" element={<RequireAuth><RoutesPage /></RequireAuth>} />
          <Route path="/fleets" element={<RequireAuth><Fleets /></RequireAuth>} />
          <Route path="/request-tester" element={<RequireAuth><RequestTester /></RequireAuth>} />
          <Route path="/sessions" element={<RequireAuth><Sessions /></RequireAuth>} />
          <Route path="/traces" element={<RequireAuth><Traces /></RequireAuth>} />
          <Route path="/drift" element={<RequireAuth><DriftDashboard /></RequireAuth>} />
          <Route path="/gitops" element={<RequireAuth><GitOps /></RequireAuth>} />
          <Route path="/raw-data" element={<RequireAuth><RawData /></RequireAuth>} />
          <Route path="/architecture" element={<RequireAuth><Architecture /></RequireAuth>} />
          <Route path="/audit-log" element={<RequireAuth><AuditLog /></RequireAuth>} />
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
  const isArchitecturePage = location.pathname === '/architecture'
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
        {/* Environment Banner */}
        {!isLoginPage && !isArchitecturePage && (
          <div className="bg-blue-500/10 border-b border-blue-500/20 px-6 py-2.5 flex items-center gap-2">
            <AlertTriangle size={14} className="text-blue-400 shrink-0" />
            <span className="text-xs text-blue-300/80">
              <span className="font-semibold">GitOps mode</span> -- Fleet and route changes are committed to GitHub and reconciled by Argo CD to the data-plane cluster.
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
