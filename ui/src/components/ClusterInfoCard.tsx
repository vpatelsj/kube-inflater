import type { ClusterInfo } from '../types/benchmark'

interface Props {
  info: ClusterInfo
}

export default function ClusterInfoCard({ info }: Props) {
  const items = [
    { label: 'Nodes', value: info.nodeCount },
    { label: 'Pods', value: info.podCount },
    { label: 'Namespaces', value: info.namespaceCount },
    { label: 'Services', value: info.serviceCount },
    { label: 'Deployments', value: info.deploymentCount },
    { label: 'ConfigMaps', value: info.configMapCount },
    { label: 'Secrets', value: info.secretCount },
  ]

  return (
    <div className="bg-white rounded-lg p-4 shadow-sm border">
      <h3 className="text-sm font-semibold text-gray-500 mb-3">Cluster State</h3>
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        {items.map((item) => (
          <div key={item.label} className="text-center">
            <div className="text-xl font-bold">{item.value.toLocaleString()}</div>
            <div className="text-xs text-gray-500">{item.label}</div>
          </div>
        ))}
      </div>
    </div>
  )
}
