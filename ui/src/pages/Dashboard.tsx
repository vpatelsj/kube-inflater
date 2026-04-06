import { Link } from 'react-router-dom'
import { useReports } from '../hooks/useBenchmarkData'
import { useEffect, useState } from 'react'
import { fetchRuns } from '../api/client'
import type { RunSummary } from '../api/client'
import type { ReportType } from '../types/benchmark'

const typeBadge: Record<ReportType, { label: string; color: string }> = {
  'pod-creation': { label: 'Pod Creation', color: 'bg-blue-100 text-blue-800' },
  'watch-stress': { label: 'Watch Stress', color: 'bg-purple-100 text-purple-800' },
  'api-latency': { label: 'API Latency', color: 'bg-green-100 text-green-800' },
}

const statusBadge: Record<string, { label: string; color: string }> = {
  pending: { label: 'Pending', color: 'bg-yellow-100 text-yellow-800' },
  running: { label: 'Running', color: 'bg-blue-100 text-blue-800 animate-pulse' },
  completed: { label: 'Completed', color: 'bg-green-100 text-green-800' },
  failed: { label: 'Failed', color: 'bg-red-100 text-red-800' },
}

function reportRoute(type: ReportType, id: string): string {
  return `/report/${type}/${id}`
}

export default function Dashboard() {
  const { reports, loading, error } = useReports()
  const [runs, setRuns] = useState<RunSummary[]>([])

  useEffect(() => {
    fetchRuns().then(setRuns).catch(() => {})
    const interval = setInterval(() => {
      fetchRuns().then(setRuns).catch(() => {})
    }, 3000)
    return () => clearInterval(interval)
  }, [])

  if (loading) return <p className="text-gray-500">Loading reports…</p>
  if (error) return <p className="text-red-600">Error: {error}</p>

  const activeRuns = runs.filter((r) => r.status === 'running' || r.status === 'pending')

  if (reports.length === 0 && runs.length === 0) {
    return (
      <div className="text-center py-20">
        <h2 className="text-2xl font-semibold mb-2">No benchmark reports yet</h2>
        <p className="text-gray-500 max-w-md mx-auto mb-6">
          Start a benchmark run to generate reports with interactive charts.
        </p>
        <Link
          to="/new-run"
          className="inline-block bg-gray-900 text-white px-6 py-3 rounded-lg font-medium hover:bg-gray-700 transition-colors"
        >
          🚀 Start a Benchmark Run
        </Link>
      </div>
    )
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">Benchmark Runs</h1>
        <Link
          to="/new-run"
          className="bg-gray-900 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-gray-700 transition-colors"
        >
          🚀 New Run
        </Link>
      </div>

      {/* Active runs */}
      {activeRuns.length > 0 && (
        <div className="mb-6">
          <h2 className="text-sm font-semibold text-gray-500 mb-2 uppercase tracking-wide">Active Runs</h2>
          <div className="grid gap-2">
            {activeRuns.map((r) => {
              const sBadge = statusBadge[r.status]
              return (
                <Link
                  key={r.id}
                  to={`/run/${r.id}`}
                  className="block bg-white rounded-lg shadow-sm border border-blue-200 p-4 hover:shadow-md transition-shadow"
                >
                  <div className="flex items-center gap-3">
                    <span className={`text-xs font-medium px-2 py-1 rounded ${sBadge?.color}`}>
                      {sBadge?.label ?? r.status}
                    </span>
                    <span className={`text-xs font-medium px-2 py-1 rounded ${typeBadge[r.type as ReportType]?.color ?? 'bg-gray-100'}`}>
                      {typeBadge[r.type as ReportType]?.label ?? r.type}
                    </span>
                    <span className="font-mono text-sm text-gray-600">{r.id}</span>
                    <span className="ml-auto text-sm text-gray-400">
                      {new Date(r.startedAt).toLocaleString()}
                    </span>
                  </div>
                </Link>
              )
            })}
          </div>
        </div>
      )}

      {/* Completed reports */}
      {reports.length > 0 && (
        <div>
          <h2 className="text-sm font-semibold text-gray-500 mb-2 uppercase tracking-wide">Reports</h2>
          <div className="grid gap-2">
            {reports.map((r) => {
              const badge = typeBadge[r.type]
              return (
                <Link
                  key={r.id}
                  to={reportRoute(r.type, r.id)}
                  className="block bg-white rounded-lg shadow-sm border border-gray-200 p-4 hover:shadow-md transition-shadow"
                >
                  <div className="flex items-center gap-3">
                    <span className={`text-xs font-medium px-2 py-1 rounded ${badge?.color}`}>
                      {badge?.label ?? r.type}
                    </span>
                    <span className="font-mono text-sm text-gray-600">{r.runID}</span>
                    <span className="ml-auto text-sm text-gray-400">
                      {new Date(r.timestamp).toLocaleString()}
                    </span>
                  </div>
                </Link>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
