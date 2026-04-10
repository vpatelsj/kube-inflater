import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { subscribeToClusterOverview } from '../api/client'
import type { ClusterSnapshot } from '../api/client'
import ClusterResourcesChart from '../components/charts/ClusterResourcesChart'

export default function ClusterOverview() {
  const [snapshot, setSnapshot] = useState<ClusterSnapshot | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const es = subscribeToClusterOverview(
      (snap) => {
        setSnapshot(snap)
        setError(null)
      },
      (err) => setError(err),
    )

    es.onerror = () => {
      setError('Connection lost — retrying…')
    }

    return () => es.close()
  }, [])

  return (
    <div>
      <div className="flex items-center gap-4 mb-6">
        <Link to="/" className="text-sm text-blue-600 hover:underline">← Dashboard</Link>
        <h1 className="text-2xl font-bold">Cluster Overview</h1>
        {snapshot && (
          <span className="flex items-center gap-2 text-sm text-gray-400">
            <span className="relative flex h-2.5 w-2.5">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75"></span>
              <span className="relative inline-flex rounded-full h-2.5 w-2.5 bg-green-500"></span>
            </span>
            Live · {new Date(snapshot.timestamp).toLocaleTimeString()}
          </span>
        )}
      </div>

      {error && (
        <div className="bg-red-50 border border-red-200 text-red-700 px-4 py-2 rounded mb-4 text-sm">
          {error}
        </div>
      )}

      {!snapshot && !error && (
        <p className="text-gray-500">Connecting to cluster…</p>
      )}

      {snapshot && (
        <div className="space-y-4">
          {/* Health & Nodes */}
          <div className="bg-white rounded-lg p-4 shadow-sm border">
            <h3 className="text-sm font-semibold text-gray-500 mb-3">Health & Nodes</h3>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
              <Stat
                label="API Health"
                value={`${snapshot.apiHealthMs.toFixed(0)}ms`}
                color={snapshot.apiHealthMs > 500 ? 'text-red-600' : 'text-green-600'}
              />
              <Stat label="Nodes" value={`${snapshot.readyNodes}/${snapshot.totalNodes}`} sub="ready / total" />
              <Stat label="Elapsed" value={`${snapshot.elapsedSec.toFixed(0)}s`} sub="since page load" />
            </div>
          </div>

          {/* Pods */}
          <div className="bg-white rounded-lg p-4 shadow-sm border">
            <h3 className="text-sm font-semibold text-gray-500 mb-3">Pods</h3>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
              <Stat label="Total" value={snapshot.clusterPods.toLocaleString()} color="text-blue-600" />
              <Stat label="Running" value={snapshot.runningPods.toLocaleString()} color="text-green-600" />
              <Stat label="Pending" value={snapshot.pendingPods.toLocaleString()} color={snapshot.pendingPods > 0 ? 'text-yellow-600' : ''} />
              <Stat label="Failed" value={snapshot.failedPods.toLocaleString()} color={snapshot.failedPods > 0 ? 'text-red-600' : ''} />
            </div>
          </div>

          {/* Resources */}
          <div className="bg-white rounded-lg p-4 shadow-sm border">
            <h3 className="text-sm font-semibold text-gray-500 mb-3">Cluster Resources</h3>
            <div className="grid grid-cols-2 sm:grid-cols-5 gap-4">
              <Stat label="ConfigMaps" value={snapshot.clusterConfigmaps.toLocaleString()} />
              <Stat label="Secrets" value={snapshot.clusterSecrets.toLocaleString()} />
              <Stat label="Services" value={snapshot.clusterServices.toLocaleString()} />
              <Stat label="Jobs" value={snapshot.clusterJobs.toLocaleString()} />
              <Stat label="Namespaces" value={snapshot.clusterNamespaces.toLocaleString()} />
            </div>
          </div>

          {/* Deployed resources chart */}
          <ClusterResourcesChart snapshot={snapshot} />
        </div>
      )}
    </div>
  )
}

function Stat({ label, value, sub, color }: { label: string; value: string; sub?: string; color?: string }) {
  return (
    <div className="text-center">
      <div className={`text-2xl font-bold ${color ?? 'text-gray-800'}`}>{value}</div>
      <div className="text-xs text-gray-500">{label}</div>
      {sub && <div className="text-xs text-gray-400">{sub}</div>}
    </div>
  )
}
