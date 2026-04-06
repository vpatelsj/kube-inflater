import type { ClusterSnapshot } from '../api/client'

interface Props {
  snapshot: ClusterSnapshot | null
}

export default function LiveStatsCards({ snapshot }: Props) {
  if (!snapshot) return null

  const items = [
    { label: 'Nodes', value: `${snapshot.readyNodes}/${snapshot.totalNodes}`, sub: 'ready/total' },
    { label: 'Total Pods', value: snapshot.totalPods.toLocaleString(), color: 'text-blue-600' },
    { label: 'Running', value: snapshot.runningPods.toLocaleString(), color: 'text-green-600' },
    { label: 'Pending', value: snapshot.pendingPods.toLocaleString(), color: snapshot.pendingPods > 0 ? 'text-yellow-600' : '' },
    { label: 'Failed', value: snapshot.failedPods.toLocaleString(), color: snapshot.failedPods > 0 ? 'text-red-600' : '' },
    { label: 'API Health', value: `${snapshot.apiHealthMs.toFixed(0)}ms`, color: snapshot.apiHealthMs > 500 ? 'text-red-600' : 'text-green-600' },
    { label: 'Elapsed', value: `${snapshot.elapsedSec.toFixed(0)}s` },
  ]

  return (
    <div className="bg-white rounded-lg p-4 shadow-sm border">
      <h3 className="text-sm font-semibold text-gray-500 mb-3">Live Cluster State</h3>
      <div className="grid grid-cols-3 sm:grid-cols-7 gap-3">
        {items.map((item) => (
          <div key={item.label} className="text-center">
            <div className={`text-xl font-bold ${item.color ?? ''}`}>{item.value}</div>
            <div className="text-xs text-gray-500">{item.label}</div>
          </div>
        ))}
      </div>
    </div>
  )
}
