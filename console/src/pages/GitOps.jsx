import React, { useState } from 'react'
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import { motion, AnimatePresence } from 'framer-motion'
import {
  GitBranch, RefreshCw, Clock, CheckCircle2, AlertTriangle,
  Loader2, Server, GitCommit, ChevronDown, ChevronRight,
  ExternalLink, FileText, FolderGit2, GitMerge, Database,
  ArrowRight, XCircle,
} from 'lucide-react'
import GlassCard from '../components/GlassCard'
import { useConfig } from '../context/ConfigContext'

function SyncStatusBadge({ status }) {
  const styles = {
    synced: 'bg-emerald-500/15 border-emerald-500/30 text-emerald-400',
    progressing: 'bg-yellow-500/15 border-yellow-500/30 text-yellow-400',
    'out-of-sync': 'bg-red-500/15 border-red-500/30 text-red-400',
    unknown: 'bg-gray-500/15 border-gray-500/30 text-gray-400',
  }
  return (
    <span className={`text-[10px] px-2 py-0.5 rounded-full font-semibold uppercase tracking-wider border ${
      styles[status] || styles.unknown
    }`}>
      {status || 'unknown'}
    </span>
  )
}

function TypeBadge({ type }) {
  const styles = {
    Fleet: 'bg-blue-500/15 border-blue-500/30 text-blue-400',
    Route: 'bg-purple-500/15 border-purple-500/30 text-purple-400',
    Lambda: 'bg-amber-500/15 border-amber-500/30 text-amber-400',
  }
  return (
    <span className={`text-[10px] px-2 py-0.5 rounded-full font-medium border ${
      styles[type] || 'bg-gray-500/15 border-gray-500/30 text-gray-400'
    }`}>
      {type}
    </span>
  )
}

function SourceBadge({ source }) {
  const isApi = source === 'management-api'
  return (
    <span className={`text-[10px] px-2 py-0.5 rounded-full font-medium border ${
      isApi
        ? 'bg-cyan-500/10 border-cyan-500/30 text-cyan-400'
        : 'bg-orange-500/10 border-orange-500/30 text-orange-400'
    }`}>
      {isApi ? 'Console' : 'Git'}
    </span>
  )
}

function formatTimeAgo(dateStr) {
  if (!dateStr) return '--'
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now - date
  const diffSec = Math.floor(diffMs / 1000)
  if (diffSec < 60) return `${diffSec}s ago`
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  const diffDay = Math.floor(diffHr / 24)
  return `${diffDay}d ago`
}

export default function GitOps() {
  const { GITOPS_URL } = useConfig()
  const queryClient = useQueryClient()
  const [syncing, setSyncing] = useState(false)
  const [expandedRepo, setExpandedRepo] = useState(null)

  const { data: statusData, isLoading: statusLoading } = useQuery({
    queryKey: ['gitopsStatus'],
    queryFn: () => fetch(`${GITOPS_URL}/status`).then(r => r.json()).catch(() => null),
    refetchInterval: 10000,
  })

  const { data: commitsData, isLoading: commitsLoading } = useQuery({
    queryKey: ['gitopsCommits'],
    queryFn: () => fetch(`${GITOPS_URL}/commits`).then(r => r.json()).catch(() => ({ commits: [] })),
    refetchInterval: 15000,
  })

  const { data: reposData, isLoading: reposLoading } = useQuery({
    queryKey: ['gitopsRepos'],
    queryFn: () => fetch(`${GITOPS_URL}/repos`).then(r => r.json()).catch(() => []),
    refetchInterval: 15000,
  })

  const { data: reconcileStatus, refetch: refetchReconcileStatus } = useQuery({
    queryKey: ['reconcileStatus'],
    queryFn: () => fetch(`${GITOPS_URL}/reconcile/status`).then(r => r.json()).catch(() => null),
    refetchInterval: 30000,
  })

  const reconcileMutation = useMutation({
    mutationFn: () => fetch(`${GITOPS_URL}/reconcile`, { method: 'POST' }).then(r => r.json()),
    onSuccess: (data) => {
      queryClient.setQueryData(['reconcileStatus'], data)
      queryClient.invalidateQueries({ queryKey: ['gitopsStatus'] })
      queryClient.invalidateQueries({ queryKey: ['gitopsRepos'] })
    },
  })

  const clusters = statusData?.clusters || []
  const commits = commitsData?.commits || []
  const repos = reposData || []
  const mode = statusData?.mode || 'unknown'

  const syncedCount = clusters.filter(c => c.sync_status === 'synced').length
  const totalClusters = clusters.length
  const totalManifests = repos.reduce((sum, r) => sum + (r.manifests?.length || 0), 0)

  const handleSync = async () => {
    setSyncing(true)
    try {
      await fetch(`${GITOPS_URL}/sync`, { method: 'POST' })
      queryClient.invalidateQueries({ queryKey: ['gitopsStatus'] })
      queryClient.invalidateQueries({ queryKey: ['gitopsCommits'] })
      queryClient.invalidateQueries({ queryKey: ['gitopsRepos'] })
    } catch (e) {
      console.error('Sync failed:', e)
    }
    setSyncing(false)
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-white">GitOps</h1>
          <p className="text-sm text-jpmc-muted">
            Authoritative config sources, repository state, and Argo CD sync status
          </p>
        </div>
        <div className="flex items-center gap-3">
          <span className={`text-[10px] px-2 py-1 rounded border font-mono ${
            mode === 'k8s'
              ? 'bg-blue-500/10 border-blue-500/30 text-blue-400'
              : 'bg-gray-500/10 border-gray-500/30 text-gray-400'
          }`}>
            {mode === 'k8s' ? 'K8s / GitOps' : 'Docker mode'}
          </span>
          <button
            onClick={() => reconcileMutation.mutate()}
            disabled={reconcileMutation.isPending}
            title="Pull latest Git state and correct any DB drift (Git is authoritative)"
            className="flex items-center gap-2 px-3 py-1.5 rounded text-xs font-medium
              bg-emerald-600/20 border border-emerald-500/40 text-emerald-300
              hover:bg-emerald-600/30 hover:border-emerald-500/60
              disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            {reconcileMutation.isPending ? (
              <Loader2 size={13} className="animate-spin" />
            ) : (
              <GitMerge size={13} />
            )}
            {reconcileMutation.isPending ? 'Reconciling...' : 'Reconcile from Git'}
          </button>
          <button
            onClick={handleSync}
            disabled={syncing}
            className="btn-primary flex items-center gap-2"
          >
            {syncing ? (
              <Loader2 size={14} className="animate-spin" />
            ) : (
              <RefreshCw size={14} />
            )}
            {syncing ? 'Syncing...' : 'Trigger Sync'}
          </button>
        </div>
      </div>

      {/* Summary Cards */}
      <div className="grid grid-cols-1 sm:grid-cols-4 gap-4">
        <GlassCard
          title="Clusters"
          icon={Server}
          value={totalClusters}
          subtitle={`${syncedCount} synced`}
          delay={0}
          className={syncedCount === totalClusters && totalClusters > 0 ? 'border-emerald-500/20' : ''}
        />
        <GlassCard
          title="Fleet Repositories"
          icon={FolderGit2}
          value={repos.length}
          subtitle={`${totalManifests} manifests`}
          delay={0.05}
        />
        <GlassCard
          title="Recent Commits"
          icon={GitCommit}
          value={commits.length}
          subtitle="Across all repos"
          delay={0.1}
        />
        <GlassCard
          title="Mode"
          icon={GitBranch}
          value={mode === 'k8s' ? 'GitOps' : 'Direct'}
          subtitle={mode === 'k8s' ? 'Argo CD reconciliation' : 'Docker orchestration'}
          delay={0.15}
        />
      </div>

      {/* Reconcile Status Panel */}
      {(reconcileStatus || reconcileMutation.data) && (() => {
        const result = reconcileMutation.data || reconcileStatus
        if (!result || result.status === 'never_run') return null
        const changes = result.changes || []
        const errors = result.errors || []
        const duration = result.finished_at && result.started_at
          ? ((new Date(result.finished_at) - new Date(result.started_at)) / 1000).toFixed(1)
          : null

        return (
          <div className="space-y-3">
            <h2 className="text-sm font-semibold text-white flex items-center gap-2">
              <GitMerge size={14} />
              Last Reconcile Pass
              <span className="text-[10px] text-jpmc-muted font-normal ml-1">
                Git → DB drift correction
              </span>
            </h2>
            <div className={`glass-card p-4 ${
              errors.length > 0 ? 'border-red-500/20' : changes.length > 0 ? 'border-yellow-500/20' : 'border-emerald-500/20'
            }`}>
              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-3 text-xs text-jpmc-muted">
                  {result.finished_at && (
                    <span className="flex items-center gap-1">
                      <Clock size={10} />
                      {formatTimeAgo(result.finished_at)}
                      {duration && ` (${duration}s)`}
                    </span>
                  )}
                  <span className={`flex items-center gap-1 ${changes.length > 0 ? 'text-yellow-400' : 'text-emerald-400'}`}>
                    {changes.length > 0 ? <AlertTriangle size={10} /> : <CheckCircle2 size={10} />}
                    {changes.length} change{changes.length !== 1 ? 's' : ''}
                  </span>
                  {errors.length > 0 && (
                    <span className="flex items-center gap-1 text-red-400">
                      <XCircle size={10} />
                      {errors.length} error{errors.length !== 1 ? 's' : ''}
                    </span>
                  )}
                </div>
              </div>

              {changes.length > 0 && (
                <div className="space-y-1 mb-3">
                  {changes.map((change, idx) => (
                    <div key={idx} className="flex items-start gap-3 text-xs py-1.5 px-3 rounded bg-jpmc-bg/30 border border-jpmc-border/10">
                      <Database size={11} className="text-jpmc-muted mt-0.5 shrink-0" />
                      <span className={`shrink-0 text-[10px] px-1.5 py-0.5 rounded font-medium border ${
                        change.action === 'added' ? 'bg-emerald-500/10 border-emerald-500/20 text-emerald-400'
                        : change.action === 'updated' ? 'bg-yellow-500/10 border-yellow-500/20 text-yellow-400'
                        : 'bg-orange-500/10 border-orange-500/20 text-orange-400'
                      }`}>
                        {change.action}
                      </span>
                      <span className="text-[10px] px-1.5 py-0.5 rounded bg-blue-500/10 border border-blue-500/20 text-blue-400 shrink-0">
                        {change.kind}
                      </span>
                      <span className="text-jpmc-muted shrink-0">{change.fleet}</span>
                      <ArrowRight size={10} className="text-jpmc-muted mt-0.5 shrink-0" />
                      <code className="text-jpmc-text font-mono text-[10px] shrink-0">{change.id}</code>
                      {change.detail && (
                        <span className="text-jpmc-muted text-[10px] truncate">{change.detail}</span>
                      )}
                    </div>
                  ))}
                </div>
              )}

              {errors.length > 0 && (
                <div className="space-y-1">
                  {errors.map((err, idx) => (
                    <div key={idx} className="flex items-start gap-2 text-xs py-1.5 px-3 rounded bg-red-500/5 border border-red-500/15">
                      <XCircle size={11} className="text-red-400 mt-0.5 shrink-0" />
                      <span className="text-red-300/80 text-[10px]">{err}</span>
                    </div>
                  ))}
                </div>
              )}

              {changes.length === 0 && errors.length === 0 && (
                <div className="flex items-center gap-2 text-xs text-emerald-400">
                  <CheckCircle2 size={12} />
                  DB is in sync with Git — no corrections needed
                </div>
              )}
            </div>
          </div>
        )
      })()}

      {/* Fleet Repositories — Authoritative Config Sources */}
      <div className="space-y-3">
        <h2 className="text-sm font-semibold text-white flex items-center gap-2">
          <FolderGit2 size={14} />
          Fleet Repositories
          <span className="text-[10px] text-jpmc-muted font-normal ml-1">
            Authoritative source of truth for each fleet
          </span>
        </h2>

        {reposLoading ? (
          <GlassCard>
            <div className="text-center py-8 text-jpmc-muted text-sm flex items-center justify-center gap-2">
              <Loader2 size={14} className="animate-spin" />
              Loading repositories...
            </div>
          </GlassCard>
        ) : repos.length > 0 ? (
          <GlassCard>
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-jpmc-muted border-b border-jpmc-border/30">
                    <th className="text-left px-4 py-2.5 font-medium w-8"></th>
                    <th className="text-left px-4 py-2.5 font-medium">Fleet</th>
                    <th className="text-left px-4 py-2.5 font-medium">Repository</th>
                    <th className="text-center px-4 py-2.5 font-medium">Files</th>
                    <th className="text-center px-4 py-2.5 font-medium">Status</th>
                    <th className="text-left px-4 py-2.5 font-medium">Commit</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-jpmc-border/20">
                  {repos.map((repo, idx) => {
                    const isExpanded = expandedRepo === repo.fleet_id
                    const repoShortName = repo.repo_url
                      ? repo.repo_url.replace('https://github.com/', '')
                      : '--'
                    return (
                      <React.Fragment key={repo.fleet_id}>
                        <motion.tr
                          initial={{ opacity: 0 }}
                          animate={{ opacity: 1 }}
                          transition={{ delay: idx * 0.03 }}
                          className="hover:bg-jpmc-hover/30 transition-colors cursor-pointer"
                          onClick={() => setExpandedRepo(isExpanded ? null : repo.fleet_id)}
                        >
                          <td className="px-4 py-3">
                            {isExpanded
                              ? <ChevronDown size={12} className="text-jpmc-muted" />
                              : <ChevronRight size={12} className="text-jpmc-muted" />
                            }
                          </td>
                          <td className="px-4 py-3">
                            <div className="text-white font-medium">{repo.fleet_name}</div>
                            <div className="text-jpmc-muted text-[10px]">{repo.subdomain}</div>
                          </td>
                          <td className="px-4 py-3">
                            {repo.repo_url ? (
                              <a
                                href={repo.repo_url}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="text-blue-400 hover:text-blue-300 flex items-center gap-1 font-mono text-[11px]"
                                onClick={(e) => e.stopPropagation()}
                              >
                                {repoShortName}
                                <ExternalLink size={10} />
                              </a>
                            ) : (
                              <span className="text-jpmc-muted">--</span>
                            )}
                          </td>
                          <td className="px-4 py-3 text-center">
                            <span className="text-white font-medium">
                              {repo.manifests?.length || 0}
                            </span>
                          </td>
                          <td className="px-4 py-3 text-center">
                            <SyncStatusBadge status={repo.sync_status} />
                          </td>
                          <td className="px-4 py-3">
                            <code className="text-blue-400 font-mono text-[11px]">
                              {repo.git_commit_sha ? repo.git_commit_sha.slice(0, 7) : '--'}
                            </code>
                          </td>
                        </motion.tr>

                        {/* Expanded manifest list */}
                        <AnimatePresence>
                          {isExpanded && repo.manifests && repo.manifests.length > 0 && (
                            <motion.tr
                              initial={{ opacity: 0, height: 0 }}
                              animate={{ opacity: 1, height: 'auto' }}
                              exit={{ opacity: 0, height: 0 }}
                            >
                              <td colSpan={6} className="px-8 py-3 bg-jpmc-hover/10">
                                <div className="space-y-2">
                                  <div className="text-[10px] text-jpmc-muted uppercase tracking-wider font-medium mb-2">
                                    Manifest Files (authoritative config)
                                  </div>
                                  {repo.manifests.map((manifest, mIdx) => (
                                    <div
                                      key={mIdx}
                                      className="flex items-center gap-4 py-1.5 px-3 rounded bg-jpmc-bg/30 border border-jpmc-border/10"
                                    >
                                      <FileText size={12} className="text-jpmc-muted shrink-0" />
                                      <code className="text-[11px] text-jpmc-text font-mono flex-1">
                                        {manifest.path}
                                      </code>
                                      <TypeBadge type={manifest.type} />
                                      <SourceBadge source={manifest.source} />
                                    </div>
                                  ))}
                                  {repo.repo_url && (
                                    <div className="text-[10px] text-jpmc-muted mt-2 flex items-center gap-1">
                                      <GitBranch size={10} />
                                      All changes to this repository are the authoritative source for this fleet's desired state.
                                      Argo CD syncs these manifests to the data-plane cluster.
                                    </div>
                                  )}
                                </div>
                              </td>
                            </motion.tr>
                          )}
                        </AnimatePresence>
                      </React.Fragment>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </GlassCard>
        ) : (
          <GlassCard>
            <div className="text-center py-8 text-jpmc-muted text-sm">
              {mode === 'docker'
                ? 'Fleet repositories are not available in Docker mode. Switch to K8s orchestration to use GitOps.'
                : 'No fleet repositories configured. Create a fleet to auto-create its GitOps repository.'}
            </div>
          </GlassCard>
        )}
      </div>

      {/* Cluster Sync Status */}
      {clusters.length > 0 && (
        <div className="space-y-3">
          <h2 className="text-sm font-semibold text-white flex items-center gap-2">
            <Server size={14} />
            Cluster Sync Status
          </h2>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {clusters.map((cluster, idx) => (
              <motion.div
                key={cluster.cluster}
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: idx * 0.05 }}
                className={`glass-card p-4 ${
                  cluster.sync_status === 'synced' ? 'border-emerald-500/20'
                  : cluster.sync_status === 'progressing' ? 'border-yellow-500/20'
                  : cluster.sync_status === 'out-of-sync' ? 'border-red-500/20'
                  : ''
                }`}
              >
                <div className="flex items-center justify-between mb-3">
                  <code className="text-sm text-white font-medium">{cluster.cluster}</code>
                  <SyncStatusBadge status={cluster.sync_status} />
                </div>
                <div className="flex items-center gap-4 text-xs text-jpmc-muted">
                  <span className="flex items-center gap-1">
                    <GitBranch size={10} />
                    {cluster.fleet_count} fleet{cluster.fleet_count !== 1 ? 's' : ''}
                  </span>
                  {cluster.sync_status === 'synced' && (
                    <span className="flex items-center gap-1 text-emerald-400">
                      <CheckCircle2 size={10} />
                      In sync
                    </span>
                  )}
                  {cluster.sync_status === 'out-of-sync' && (
                    <span className="flex items-center gap-1 text-red-400">
                      <AlertTriangle size={10} />
                      Needs attention
                    </span>
                  )}
                </div>
              </motion.div>
            ))}
          </div>
        </div>
      )}

      {/* Recent Commits */}
      <div className="space-y-3">
        <h2 className="text-sm font-semibold text-white flex items-center gap-2">
          <GitCommit size={14} />
          Recent Commits
        </h2>
        {commitsLoading ? (
          <GlassCard>
            <div className="text-center py-8 text-jpmc-muted text-sm flex items-center justify-center gap-2">
              <Loader2 size={14} className="animate-spin" />
              Loading commits...
            </div>
          </GlassCard>
        ) : commits.length > 0 ? (
          <GlassCard>
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-jpmc-muted border-b border-jpmc-border/30">
                    <th className="text-left px-4 py-2.5 font-medium">SHA</th>
                    <th className="text-left px-4 py-2.5 font-medium">Message</th>
                    <th className="text-left px-4 py-2.5 font-medium">Fleet</th>
                    <th className="text-left px-4 py-2.5 font-medium">Author</th>
                    <th className="text-left px-4 py-2.5 font-medium">Time</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-jpmc-border/20">
                  {commits.map((commit, idx) => (
                    <motion.tr
                      key={`${commit.sha}-${idx}`}
                      initial={{ opacity: 0 }}
                      animate={{ opacity: 1 }}
                      transition={{ delay: idx * 0.02 }}
                      className="hover:bg-jpmc-hover/30 transition-colors"
                    >
                      <td className="px-4 py-2.5">
                        <code className="text-blue-400 font-mono text-[11px]">
                          {commit.sha ? commit.sha.slice(0, 7) : '--'}
                        </code>
                      </td>
                      <td className="px-4 py-2.5 text-jpmc-text max-w-md truncate">
                        {commit.message}
                      </td>
                      <td className="px-4 py-2.5">
                        {commit.fleet_name ? (
                          <span className="text-[10px] px-2 py-0.5 rounded bg-blue-500/10 border border-blue-500/20 text-blue-400">
                            {commit.fleet_name}
                          </span>
                        ) : (
                          <span className="text-jpmc-muted">shared</span>
                        )}
                      </td>
                      <td className="px-4 py-2.5 text-jpmc-muted">
                        {commit.author}
                      </td>
                      <td className="px-4 py-2.5 text-jpmc-muted whitespace-nowrap">
                        <span className="flex items-center gap-1">
                          <Clock size={10} />
                          {formatTimeAgo(commit.date)}
                        </span>
                      </td>
                    </motion.tr>
                  ))}
                </tbody>
              </table>
            </div>
          </GlassCard>
        ) : (
          <GlassCard>
            <div className="text-center py-8 text-jpmc-muted text-sm">
              {mode === 'docker'
                ? 'GitOps commits are not available in Docker mode. Switch to K8s orchestration mode to see GitOps history.'
                : 'No commits found in any GitOps repository.'}
            </div>
          </GlassCard>
        )}
      </div>

      {/* No clusters message */}
      {clusters.length === 0 && !statusLoading && (
        <div className="p-3 rounded-lg bg-blue-500/10 border border-blue-500/30 flex items-start gap-3">
          <GitBranch size={16} className="text-blue-400 mt-0.5 shrink-0" />
          <p className="text-xs text-blue-300/80 leading-relaxed">
            {mode === 'docker'
              ? 'The management API is running in Docker orchestration mode. To use GitOps features, set ORCHESTRATION_MODE=k8s and configure GITOPS_REPO_PATH.'
              : 'No cluster data available. Ensure the GitOps repository is configured and accessible.'}
          </p>
        </div>
      )}
    </div>
  )
}
