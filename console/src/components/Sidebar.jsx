import React from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import { motion } from 'framer-motion'
import {
  LayoutDashboard, Route, Server, Zap, Key, Activity,
  AlertTriangle, ChevronLeft, ChevronRight, LogOut, LogIn,
} from 'lucide-react'
import { useAuth } from '../context/AuthContext'

const navItems = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/fleets', label: 'Fleets', icon: Server },
  { to: '/routes', label: 'Routes', icon: Route },
  { to: '/request-tester', label: 'Request Tester', icon: Zap },
  { to: '/sessions', label: 'Sessions', icon: Key },
  { to: '/traces', label: 'Traces', icon: Activity },
  { to: '/drift', label: 'Drift', icon: AlertTriangle },
]

export default function Sidebar({ collapsed, onToggle }) {
  const { session, logout } = useAuth()
  const navigate = useNavigate()

  const handleLogout = () => {
    logout()
    navigate('/login')
  }

  const initials = session?.name
    ? session.name.split(' ').map(n => n[0]).join('').toUpperCase().slice(0, 2)
    : '??'

  return (
    <motion.aside
      initial={false}
      animate={{ width: collapsed ? 72 : 260 }}
      transition={{ duration: 0.2, ease: 'easeInOut' }}
      className="fixed left-0 top-0 h-screen bg-jpmc-dark border-r border-jpmc-border/50 flex flex-col z-50 overflow-hidden"
    >
      {/* Logo */}
      <div className="flex items-center gap-3 px-5 h-16 border-b border-jpmc-border/50 shrink-0">
        <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-blue-500 to-cyan-400 flex items-center justify-center shrink-0">
          <span className="text-white font-bold text-xs">IN</span>
        </div>
        {!collapsed && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ delay: 0.1 }}
            className="flex flex-col min-w-0"
          >
            <span className="text-sm font-bold text-white tracking-wide">INGRESS</span>
            <span className="text-[10px] text-jpmc-muted font-light tracking-widest">CONTROL PLANE</span>
          </motion.div>
        )}
      </div>

      {/* Navigation */}
      <nav className="flex-1 py-4 px-3 space-y-1 overflow-y-auto">
        {navItems.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              `flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-all duration-150 group relative ${
                isActive
                  ? 'bg-blue-600/15 text-blue-400'
                  : 'text-jpmc-muted hover:text-jpmc-text hover:bg-jpmc-hover'
              }`
            }
          >
            {({ isActive }) => (
              <>
                {isActive && (
                  <motion.div
                    layoutId="sidebar-active"
                    className="absolute left-0 top-1/2 -translate-y-1/2 w-[3px] h-6 bg-blue-500 rounded-r-full"
                    transition={{ type: 'spring', stiffness: 300, damping: 30 }}
                  />
                )}
                <Icon size={18} className="shrink-0" />
                {!collapsed && (
                  <motion.span
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    transition={{ delay: 0.05 }}
                  >
                    {label}
                  </motion.span>
                )}
              </>
            )}
          </NavLink>
        ))}
      </nav>

      {/* User section */}
      <div className="border-t border-jpmc-border/50 p-3 shrink-0">
        {session ? (
          <div className={`flex items-center gap-3 ${collapsed ? 'justify-center' : ''}`}>
            <div className="w-9 h-9 rounded-full bg-gradient-to-br from-blue-500 to-indigo-600 flex items-center justify-center shrink-0">
              <span className="text-white text-xs font-semibold">{initials}</span>
            </div>
            {!collapsed && (
              <motion.div
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                className="flex-1 min-w-0"
              >
                <div className="text-sm font-medium text-jpmc-text truncate">{session.name}</div>
                <div className="flex items-center gap-1.5 mt-0.5">
                  {(session.roles || []).slice(0, 2).map(role => (
                    <span key={role} className="badge-blue text-[9px] px-1.5 py-0">{role}</span>
                  ))}
                </div>
              </motion.div>
            )}
            {!collapsed && (
              <button
                onClick={handleLogout}
                className="p-1.5 rounded-md text-jpmc-muted hover:text-red-400 hover:bg-red-500/10 transition-colors"
                title="Logout"
              >
                <LogOut size={16} />
              </button>
            )}
          </div>
        ) : (
          <NavLink
            to="/login"
            className="flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium text-jpmc-muted hover:text-jpmc-text hover:bg-jpmc-hover transition-all"
          >
            <LogIn size={18} className="shrink-0" />
            {!collapsed && <span>Sign In</span>}
          </NavLink>
        )}
      </div>

      {/* Collapse toggle */}
      <div className="border-t border-jpmc-border/50 p-3 shrink-0">
        <button
          onClick={onToggle}
          className="w-full flex items-center justify-center py-1.5 rounded-lg text-jpmc-muted hover:text-jpmc-text hover:bg-jpmc-hover transition-colors"
        >
          {collapsed ? <ChevronRight size={16} /> : <ChevronLeft size={16} />}
        </button>
      </div>
    </motion.aside>
  )
}
