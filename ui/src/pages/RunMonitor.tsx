import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { fetchRun, subscribeToRun, subscribeToCluster } from '../api/client'
import type { RunDetail, ClusterSnapshot } from '../api/client'
import type { ReportType } from '../types/benchmark'
import LiveStatsCards from '../components/LiveStatsCards'
import LiveClusterChart from '../components/charts/LiveClusterChart'

function reportRoute(type: string, id: string): string {
  return `/report/${type}/${id}`
}

// Cleanup step markers matched against log lines
const CLEANUP_STEPS = [
  { marker: 'Starting full cleanup', label: 'Starting cleanup' },
  { marker: 'Cleaning up hollow node', label: 'Hollow nodes' },
  { marker: 'kubemark nodes', label: 'Kubemark nodes' },
  { marker: 'Cleaning up KWOK', label: 'KWOK infrastructure' },
  { marker: 'kube-inflater namespaces', label: 'Namespaces' },
  { marker: 'Cleaning up watch-agent', label: 'Watch agent' },
  { marker: 'CRD', label: 'CRD' },
  { marker: 'Full cleanup completed', label: 'Done' },
] as const

function useCleanupProgress(logLines: string[], isCleanup: boolean) {
  return useMemo(() => {
    if (!isCleanup) return null
    let completedSteps = 0
    for (const step of CLEANUP_STEPS) {
      if (logLines.some((line) => line.includes(step.marker))) {
        completedSteps++
      } else {
        break
      }
    }
    return { completedSteps, totalSteps: CLEANUP_STEPS.length, steps: CLEANUP_STEPS }
  }, [logLines, isCleanup])
}

export default function RunMonitor() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [run, setRun] = useState<RunDetail | null>(null)
  const [logLines, setLogLines] = useState<string[]>([])
  const [status, setStatus] = useState<string>('pending')
  const [reportID, setReportID] = useState<string>('')
  const [error, setError] = useState<string | null>(null)
  const [snapshots, setSnapshots] = useState<ClusterSnapshot[]>([])
  const logContainerRef = useRef<HTMLDivElement>(null)

  const scrollToBottom = useCallback(() => {
    const el = logContainerRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [])

  // Load initial run data
  useEffect(() => {
    if (!id) return
    fetchRun(id)
      .then((r) => {
        setRun(r)
        setStatus(r.status)
        setLogLines(r.logLines ?? [])
        if (r.reportID) setReportID(r.reportID)
      })
      .catch((e: Error) => setError(e.message))
  }, [id])

  // Subscribe to SSE for live log updates
  useEffect(() => {
    if (!id || status === 'completed' || status === 'failed') return

    const es = subscribeToRun(
      id,
      (line) => {
        setLogLines((prev) => [...prev, line])
      },
      (newStatus, newReportID) => {
        setStatus(newStatus)
        if (newReportID) setReportID(newReportID)
        if (newStatus === 'completed' || newStatus === 'failed') {
          es.close()
        }
      },
    )

    es.onerror = () => es.close()
    return () => es.close()
  }, [id, status])

  // Subscribe to SSE for live cluster snapshots
  useEffect(() => {
    if (!id || status === 'completed' || status === 'failed') return

    const es = subscribeToCluster(
      id,
      (snap) => {
        setSnapshots((prev) => {
          const next = [...prev, snap]
          // Keep last 300 snapshots (~10 minutes at 2s interval)
          return next.length > 300 ? next.slice(-300) : next
        })
      },
      () => {}, // ignore errors silently
    )

    es.onerror = () => es.close()
    return () => es.close()
  }, [id, status])

  // Auto-scroll logs
  useEffect(() => {
    scrollToBottom()
  }, [logLines, scrollToBottom])

  if (error) return <p className="text-red-600">Error: {error}</p>
  if (!id) return <p>No run ID</p>

  const statusColor: Record<string, string> = {
    pending: 'bg-yellow-100 text-yellow-800',
    running: 'bg-blue-100 text-blue-800',
    completed: 'bg-green-100 text-green-800',
    failed: 'bg-red-100 text-red-800',
  }

  const latestSnapshot = snapshots.length > 0 ? snapshots[snapshots.length - 1]! : null
  const isCleanup = run?.type === 'cleanup'
  const cleanupProgress = useCleanupProgress(logLines, isCleanup)

  return (
    <div>
      <div className="flex items-center gap-4 mb-4">
        <Link to="/" className="text-sm text-blue-600 hover:underline">← Dashboard</Link>
        <span className={`text-xs font-medium px-2 py-1 rounded ${statusColor[status] ?? 'bg-gray-100'}`}>
          {status}
        </span>
        <span className="font-mono text-sm text-gray-600">{id}</span>
        {run && (
          <span className="text-sm text-gray-400">
            Started {new Date(run.startedAt).toLocaleString()}
          </span>
        )}
      </div>

      {/* Cleanup progress bar */}
      {cleanupProgress && (
        <div className="mb-4 bg-white rounded-lg shadow-sm border p-4">
          <div className="flex items-center justify-between mb-2">
            <span className="text-sm font-semibold text-gray-700">Cleanup Progress</span>
            <span className="text-xs text-gray-500">
              {cleanupProgress.completedSteps} / {cleanupProgress.totalSteps} steps
            </span>
          </div>
          <div className="w-full bg-gray-200 rounded-full h-3 mb-3">
            <div
              className="bg-orange-500 h-3 rounded-full transition-all duration-500"
              style={{ width: `${(cleanupProgress.completedSteps / cleanupProgress.totalSteps) * 100}%` }}
            />
          </div>
          <div className="grid grid-cols-4 gap-2">
            {cleanupProgress.steps.map((step, i) => (
              <div key={i} className="flex items-center gap-1.5 text-xs">
                <span className={
                  i < cleanupProgress.completedSteps
                    ? 'text-green-600'
                    : i === cleanupProgress.completedSteps && status === 'running'
                    ? 'text-orange-500 animate-pulse'
                    : 'text-gray-400'
                }>
                  {i < cleanupProgress.completedSteps ? '✓' : i === cleanupProgress.completedSteps && status === 'running' ? '●' : '○'}
                </span>
                <span className={i < cleanupProgress.completedSteps ? 'text-gray-700' : 'text-gray-400'}>
                  {step.label}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Live cluster stats */}
      {(status === 'running' || snapshots.length > 0) && (
        <div className="space-y-4 mb-4">
          <LiveStatsCards snapshot={latestSnapshot} />
          <LiveClusterChart snapshots={snapshots} resourceTypes={[]} />
        </div>
      )}

      {/* Live log output */}
      <div ref={logContainerRef} className="bg-gray-900 rounded-lg p-4 shadow-sm border border-gray-700 font-mono text-sm text-gray-100 overflow-auto max-h-[40vh] min-h-[150px]">
        {logLines.length === 0 && status === 'pending' && (
          <p className="text-gray-500">Waiting for output…</p>
        )}
        {logLines.map((line, i) => (
          <div key={i} className={`whitespace-pre-wrap ${
            line.startsWith('❌') ? 'text-red-400' :
            line.startsWith('✅') ? 'text-green-400' :
            line.startsWith('[WARN]') ? 'text-yellow-400' :
            line.startsWith('[ERROR]') ? 'text-red-400' :
            line.startsWith('$') ? 'text-blue-400' :
            ''
          }`}>
            {line}
          </div>
        ))}
      </div>

      {/* Actions */}
      <div className="mt-4 flex gap-3">
        {status === 'completed' && reportID && (
          <button
            onClick={() => navigate(reportRoute(run?.type as ReportType ?? 'resource-creation', reportID))}
            className="bg-green-600 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-green-500 transition-colors"
          >
            📊 View Report
          </button>
        )}
        {status === 'running' && (
          <div className="flex items-center gap-2 text-blue-600 text-sm">
            <span className="animate-pulse">●</span> Running…
          </div>
        )}
        <Link
          to="/new-run"
          className="bg-gray-200 text-gray-700 px-4 py-2 rounded-lg text-sm font-medium hover:bg-gray-300 transition-colors"
        >
          Start Another Run
        </Link>
      </div>
    </div>
  )
}
