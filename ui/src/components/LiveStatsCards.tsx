import type { ClusterSnapshot } from '../api/client'

interface Props {
  snapshot: ClusterSnapshot | null
}

export default function LiveStatsCards({ snapshot }: Props) {
  if (!snapshot) return null

  const runItems = [
    { label: 'Nodes', value: `${snapshot.readyNodes}/${snapshot.totalNodes}`, sub: 'ready/total' },
    { label: 'Total Pods', value: snapshot.totalPods.toLocaleString(), color: 'text-blue-600' },
    { label: 'Running', value: snapshot.runningPods.toLocaleString(), color: 'text-green-600' },
    { label: 'Pending', value: snapshot.pendingPods.toLocaleString(), color: snapshot.pendingPods > 0 ? 'text-yellow-600' : '' },
    { label: 'Failed', value: snapshot.failedPods.toLocaleString(), color: snapshot.failedPods > 0 ? 'text-red-600' : '' },
    { label: 'Watches', value: snapshot.watchConnections.toLocaleString(), color: 'text-sky-600' },
    { label: 'API Health', value: `${snapshot.apiHealthMs.toFixed(0)}ms`, color: snapshot.apiHealthMs > 500 ? 'text-red-600' : 'text-green-600' },
    { label: 'Elapsed', value: `${snapshot.elapsedSec.toFixed(0)}s` },
  ]

  const clusterItems = [
    { label: 'Pods', run: snapshot.totalPods, cluster: snapshot.clusterPods },
    { label: 'ConfigMaps', run: snapshot.configmaps, cluster: snapshot.clusterConfigmaps },
    { label: 'Secrets', run: snapshot.secrets, cluster: snapshot.clusterSecrets },
    { label: 'Services', run: snapshot.services, cluster: snapshot.clusterServices },
    { label: 'Jobs', run: snapshot.jobs, cluster: snapshot.clusterJobs },
    { label: 'StatefulSets', run: snapshot.statefulsets, cluster: snapshot.clusterStatefulsets },
    { label: 'ServiceAccounts', run: snapshot.serviceAccounts, cluster: snapshot.clusterServiceAccounts },
    { label: 'CRs', run: snapshot.customResources, cluster: snapshot.clusterCustomResources },
    { label: 'Namespaces', run: snapshot.namespaces, cluster: snapshot.clusterNamespaces },
  ]

  return (
    <div className="space-y-3">
      <div className="bg-white rounded-lg p-4 shadow-sm border">
        <h3 className="text-sm font-semibold text-gray-500 mb-3">Live Run Stats</h3>
        <div className="grid grid-cols-3 sm:grid-cols-7 gap-3">
          {runItems.map((item) => (
            <div key={item.label} className="text-center">
              <div className={`text-xl font-bold ${item.color ?? ''}`}>{item.value}</div>
              <div className="text-xs text-gray-500">{item.label}</div>
            </div>
          ))}
        </div>
      </div>

      <div className="bg-white rounded-lg p-4 shadow-sm border">
        <h3 className="text-sm font-semibold text-gray-500 mb-3">Cluster Totals</h3>
        <div className="grid grid-cols-3 sm:grid-cols-6 gap-3">
          {clusterItems.map((item) => (
            <div key={item.label} className="text-center">
              <div className="text-xl font-bold text-gray-800">{item.cluster.toLocaleString()}</div>
              <div className="text-xs text-gray-500">{item.label}</div>
              <div className="text-xs text-blue-500">{item.run.toLocaleString()} from run</div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
