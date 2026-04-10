import './chartSetup'
import { Bar } from 'react-chartjs-2'
import type { ClusterSnapshot } from '../../api/client'

interface Props {
  snapshot: ClusterSnapshot
}

const COLORS = [
  'rgba(59, 130, 246, 0.7)',   // blue
  'rgba(34, 197, 94, 0.7)',    // green
  'rgba(249, 115, 22, 0.7)',   // orange
  'rgba(168, 85, 247, 0.7)',   // purple
  'rgba(236, 72, 153, 0.7)',   // pink
  'rgba(20, 184, 166, 0.7)',   // teal
  'rgba(245, 158, 11, 0.7)',   // amber
]

export default function ClusterResourcesChart({ snapshot }: Props) {
  const resources = [
    { label: 'Nodes', value: snapshot.totalNodes },
    { label: 'Pods', value: snapshot.clusterPods },
    { label: 'ConfigMaps', value: snapshot.clusterConfigmaps },
    { label: 'Secrets', value: snapshot.clusterSecrets },
    { label: 'Services', value: snapshot.clusterServices },
    { label: 'Jobs', value: snapshot.clusterJobs },
    { label: 'Namespaces', value: snapshot.clusterNamespaces },
  ]

  const data = {
    labels: resources.map((r) => r.label),
    datasets: [
      {
        label: 'Count',
        data: resources.map((r) => r.value),
        backgroundColor: COLORS,
        borderWidth: 1,
      },
    ],
  }

  return (
    <div className="bg-white rounded-lg p-4 shadow-sm border">
      <h3 className="text-sm font-semibold text-gray-500 mb-3">Deployed Resources</h3>
      <Bar
        data={data}
        options={{
          responsive: true,
          indexAxis: 'y',
          plugins: {
            legend: { display: false },
          },
          scales: {
            x: { beginAtZero: true, title: { display: true, text: 'Count' } },
          },
        }}
      />
    </div>
  )
}
