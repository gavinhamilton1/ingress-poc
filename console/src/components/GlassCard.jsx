import React from 'react'
import { motion } from 'framer-motion'

export default function GlassCard({
  children,
  className = '',
  hover = false,
  delay = 0,
  title,
  icon: Icon,
  value,
  subtitle,
  trend,
  trendUp,
  onClick,
}) {
  const baseClass = hover ? 'glass-card-hover' : 'glass-card'
  const clickClass = onClick ? 'cursor-pointer' : ''

  // Stat card mode
  if (value !== undefined) {
    return (
      <motion.div
        initial={{ opacity: 0, y: 10 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3, delay }}
        className={`${baseClass} p-5 ${clickClass} ${className}`}
        onClick={onClick}
      >
        <div className="flex items-start justify-between mb-3">
          <div className="text-xs font-medium text-jpmc-muted uppercase tracking-wider">{title}</div>
          {Icon && (
            <div className="p-2 rounded-lg bg-blue-500/10">
              <Icon size={16} className="text-blue-400" />
            </div>
          )}
        </div>
        <div className="text-3xl font-bold text-white mb-1">{value}</div>
        {subtitle && <div className="text-xs text-jpmc-muted">{subtitle}</div>}
        {trend && (
          <div className={`text-xs mt-2 flex items-center gap-1 ${trendUp ? 'text-emerald-400' : 'text-red-400'}`}>
            <span>{trendUp ? '\u2191' : '\u2193'}</span>
            <span>{trend}</span>
          </div>
        )}
      </motion.div>
    )
  }

  // Generic card mode
  return (
    <motion.div
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3, delay }}
      className={`${baseClass} ${clickClass} ${className}`}
      onClick={onClick}
    >
      {title && (
        <div className="flex items-center gap-2 px-5 pt-4 pb-3">
          {Icon && <Icon size={16} className="text-jpmc-muted" />}
          <h3 className="text-sm font-semibold text-jpmc-text">{title}</h3>
        </div>
      )}
      {children}
    </motion.div>
  )
}
