import { Link, useNavigate } from 'react-router-dom'
import { useReports } from '../hooks/useBenchmarkData'
import { useEffect, useState } from 'react'
import { fetchRuns, deleteAllRuns, startRun } from '../api/client'
import type { RunSummary } from '../api/client'
import type { ReportType } from '../types/benchmark'

const typeBadge: Record<string, { label: string; color: string }> = {
  'resource-creation': { label: 'Resource Creation', color: 'bg-blue-100 text-blue-800' },
  'watch-stress': { label: 'Watch Stress', color: 'bg-purple-100 text-purple-800' },
  'api-latency': { label: 'API Latency', color: 'bg-green-100 text-green-800' },
  'cleanup': { label: 'Cleanup', color: 'bg-orange-100 text-orange-800' },
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
  const navigate = useNavigate()

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

  const hasFinishedRuns = runs.some((r) => r.status === 'completed' || r.status === 'failed')

  const handleClearRuns = async () => {
    if (!confirm('Delete all completed and failed runs?')) return
    await deleteAllRuns()
    fetchRuns().then(setRuns).catch(() => {})
  }

  const handleCleanupAll = async () => {
    if (!confirm('Nuclear cleanup: delete ALL kube-inflater resources from the cluster and clear run history?')) return
    try {
      const { id } = await startRun('cleanup', {})
      navigate(`/run/${id}`)
    } catch (e) {
      alert(`Failed to start cleanup: ${e}`)
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">Benchmark Runs</h1>
        <div className="flex gap-2">
          <button
            onClick={handleCleanupAll}
            className="bg-orange-600 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-orange-700 transition-colors"
          >
            ☢️ Clean All
          </button>
          {hasFinishedRuns && (
            <button
              onClick={handleClearRuns}
              className="bg-red-600 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-red-700 transition-colors"
            >
              🗑 Clear Runs
            </button>
          )}
          <Link
            to="/new-run"
            className="bg-gray-900 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-gray-700 transition-colors"
          >
            🚀 New Run
          </Link>
        </div>
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

      {/* All runs (including completed/failed) */}
      {runs.length > 0 && (
        <div className="mb-6">
          <h2 className="text-sm font-semibold text-gray-500 mb-2 uppercase tracking-wide">Runs</h2>
          <div className="bg-white rounded-lg shadow-sm border overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-gray-500 border-b bg-gray-50">
                  <th className="px-4 py-2">Status</th>
                  <th className="px-4 py-2">Type</th>
                  <th className="px-4 py-2">Run ID</th>
                  <th className="px-4 py-2">Started</th>
                  <th className="px-4 py-2"></th>
                </tr>
              </thead>
              <tbody>
                {runs.map((r) => {
                  const sBadge = statusBadge[r.status]
                  return (
                    <tr key={r.id} className="border-b border-gray-100 hover:bg-gray-50">
                      <td className="px-4 py-2">
                        <span className={`text-xs font-medium px-2 py-0.5 rounded ${sBadge?.color}`}>
                          {sBadge?.label ?? r.status}
                        </span>
                      </td>
                      <td className="px-4 py-2">
                        <span className={`text-xs font-medium px-2 py-0.5 rounded ${typeBadge[r.type as ReportType]?.color ?? 'bg-gray-100'}`}>
                          {typeBadge[r.type as ReportType]?.label ?? r.type}
                        </span>
                      </td>
                      <td className="px-4 py-2 font-mono text-xs text-gray-600">{r.id}</td>
                      <td className="px-4 py-2 text-xs text-gray-400">
                        {new Date(r.startedAt).toLocaleString()}
                      </td>
                      <td className="px-4 py-2 text-right">
                        <Link to={r.reportID ? reportRoute(r.type as ReportType, r.reportID) : `/run/${r.id}`} className="text-blue-600 hover:underline text-xs font-medium">
                          {r.reportID ? 'Report →' : 'View →'}
                        </Link>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Completed reports */}
      {reports.length > 0 && (
        <div>
          <h2 className="text-sm font-semibold text-gray-500 mb-2 uppercase tracking-wide">Reports</h2>
          <div className="bg-white rounded-lg shadow-sm border overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-gray-500 border-b bg-gray-50">
                  <th className="px-4 py-2">Type</th>
                  <th className="px-4 py-2">Run ID</th>
                  <th className="px-4 py-2">Resources</th>
                  <th className="px-4 py-2">Timestamp</th>
                  <th className="px-4 py-2"></th>
                </tr>
              </thead>
              <tbody>
                {reports.map((r) => {
                  const badge = typeBadge[r.type]
                  return (
                    <tr key={r.id} className="border-b border-gray-100 hover:bg-gray-50">
                      <td className="px-4 py-2">
                        <span className={`text-xs font-medium px-2 py-0.5 rounded ${badge?.color}`}>
                          {badge?.label ?? r.type}
                        </span>
                      </td>
                      <td className="px-4 py-2 font-mono text-xs text-gray-600">{r.runID}</td>
                      <td className="px-4 py-2 text-xs text-gray-600">
                        {r.resourceTypes ? r.resourceTypes.join(', ') : '—'}
                      </td>
                      <td className="px-4 py-2 text-xs text-gray-400">
                        {new Date(r.timestamp).toLocaleString()}
                      </td>
                      <td className="px-4 py-2 text-right">
                        <Link to={reportRoute(r.type, r.id)} className="text-blue-600 hover:underline text-xs font-medium">
                          View →
                        </Link>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}
