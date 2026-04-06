import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { fetchRun, subscribeToRun, subscribeToCluster } from '../api/client'
import type { RunDetail, ClusterSnapshot } from '../api/client'
import type { ReportType } from '../types/benchmark'
import LiveClusterChart from '../components/charts/LiveClusterChart'
import LiveStatsCards from '../components/LiveStatsCards'

function reportRoute(type: string, id: string): string {
  return `/report/${type}/${id}`
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
  const logEndRef = useRef<HTMLDivElement>(null)

  const scrollToBottom = useCallback(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' })
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

  // Determine resource types from the run config for chart labeling
  const resourceTypes = run?.config?.resourceTypes
    ? String(run.config.resourceTypes).split(',').map((s: string) => s.trim())
    : []

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

      {/* Live cluster stats */}
      {(status === 'running' || snapshots.length > 0) && (
        <div className="space-y-4 mb-4">
          <LiveStatsCards snapshot={latestSnapshot} />
          <LiveClusterChart snapshots={snapshots} resourceTypes={resourceTypes} />
        </div>
      )}

      {/* Live log output */}
      <div className="bg-gray-900 rounded-lg p-4 shadow-sm border border-gray-700 font-mono text-sm text-gray-100 overflow-auto max-h-[40vh] min-h-[150px]">
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
        <div ref={logEndRef} />
      </div>

      {/* Actions */}
      <div className="mt-4 flex gap-3">
        {status === 'completed' && reportID && (
          <button
            onClick={() => navigate(reportRoute(run?.type as ReportType ?? 'pod-creation', reportID))}
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
