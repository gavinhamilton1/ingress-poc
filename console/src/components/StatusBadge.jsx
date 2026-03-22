import React from 'react'

const statusConfig = {
  active: { dotClass: 'status-dot-active', badgeClass: 'badge-green', label: 'Active' },
  healthy: { dotClass: 'status-dot-active', badgeClass: 'badge-green', label: 'Healthy' },
  inactive: { dotClass: 'status-dot-inactive', badgeClass: 'badge-gray', label: 'Inactive' },
  unhealthy: { dotClass: 'status-dot bg-red-400 shadow-[0_0_8px_rgba(248,113,113,0.5)]', badgeClass: 'badge-red', label: 'Unhealthy' },
  unreachable: { dotClass: 'status-dot bg-red-400 shadow-[0_0_8px_rgba(248,113,113,0.5)]', badgeClass: 'badge-red', label: 'Unreachable' },
  drift: { dotClass: 'status-dot-drift', badgeClass: 'badge-amber', label: 'Drift' },
  drifted: { dotClass: 'status-dot-drift', badgeClass: 'badge-amber', label: 'Drifted' },
  degraded: { dotClass: 'status-dot bg-amber-400 shadow-[0_0_8px_rgba(251,191,36,0.5)]', badgeClass: 'badge-amber', label: 'Degraded' },
  warning: { dotClass: 'status-dot bg-amber-400 shadow-[0_0_8px_rgba(251,191,36,0.5)]', badgeClass: 'badge-amber', label: 'Warning' },
  pending: { dotClass: 'status-dot bg-blue-400 animate-pulse', badgeClass: 'badge-blue', label: 'Pending' },
  revoked: { dotClass: 'status-dot bg-red-400', badgeClass: 'badge-red', label: 'Revoked' },
  'in sync': { dotClass: 'status-dot-active', badgeClass: 'badge-green', label: 'In Sync' },
  unknown: { dotClass: 'status-dot-inactive', badgeClass: 'badge-gray', label: 'Unknown' },
}

export default function StatusBadge({ status, variant = 'badge', className = '' }) {
  const config = statusConfig[status] || statusConfig.unknown
  const displayLabel = config.label

  if (variant === 'dot') {
    return (
      <span className={`flex items-center gap-2 ${className}`}>
        <span className={config.dotClass} />
        <span className="text-sm capitalize">{displayLabel}</span>
      </span>
    )
  }

  return (
    <span className={`${config.badgeClass} ${className}`}>
      <span className={`${config.dotClass} w-1.5 h-1.5 mr-1.5`} />
      {displayLabel}
    </span>
  )
}
