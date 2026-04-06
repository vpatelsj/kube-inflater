import './chartSetup'
import { Line } from 'react-chartjs-2'
import type { ClusterSnapshot } from '../../api/client'

interface Props {
  snapshots: ClusterSnapshot[]
  resourceTypes: string[]
}

const COLORS = [
  'rgb(59, 130, 246)',   // blue
  'rgb(34, 197, 94)',    // green
  'rgb(249, 115, 22)',   // orange
  'rgb(168, 85, 247)',   // purple
  'rgb(236, 72, 153)',   // pink
  'rgb(20, 184, 166)',   // teal
]

export default function LiveClusterChart({ snapshots, resourceTypes }: Props) {
  if (snapshots.length === 0) return null

  const labels = snapshots.map((s) => `${s.elapsedSec.toFixed(0)}s`)

  // Build a dataset for each resource type present in resourceCounts
  const types = resourceTypes.length > 0
    ? resourceTypes
    : Object.keys(snapshots[snapshots.length - 1]?.resourceCounts ?? {}).filter(
        (k) => (snapshots[snapshots.length - 1]?.resourceCounts[k] ?? 0) > 0
      )

  const datasets = types.map((type, i) => ({
    label: type,
    data: snapshots.map((s) => s.resourceCounts[type] ?? 0),
    borderColor: COLORS[i % COLORS.length],
    backgroundColor: COLORS[i % COLORS.length]?.replace('rgb', 'rgba').replace(')', ', 0.1)'),
    fill: false,
    tension: 0.3,
    pointRadius: 1,
  }))

  const data = { labels, datasets }

  // Current totals
  const latest = snapshots[snapshots.length - 1]

  return (
    <div className="bg-white rounded-lg p-4 shadow-sm border">
      <div className="flex items-center justify-between mb-3">
        <h3 className="font-semibold text-gray-700">Live Resource Count</h3>
        <div className="flex gap-4 text-sm">
          {types.map((type) => (
            <span key={type} className="font-mono">
              {type}: <strong>{(latest?.resourceCounts[type] ?? 0).toLocaleString()}</strong>
            </span>
          ))}
        </div>
      </div>
      <Line
        data={data}
        options={{
          responsive: true,
          animation: { duration: 300 },
          plugins: {
            legend: { display: types.length > 1, position: 'bottom' },
          },
          scales: {
            x: { title: { display: true, text: 'Elapsed Time' } },
            y: { beginAtZero: true, title: { display: true, text: 'Count' } },
          },
        }}
      />
    </div>
  )
}
