import './chartSetup'
import { Line } from 'react-chartjs-2'
import type { BatchResult } from '../../types/benchmark'

interface Props {
  batches: BatchResult[]
}

export default function CumulativeChart({ batches }: Props) {
  let cumulative = 0
  const points = batches.map((b) => {
    cumulative += b.created
    return cumulative
  })

  const data = {
    labels: batches.map((b) => `Batch ${b.batchNum}`),
    datasets: [
      {
        label: 'Cumulative Created',
        data: points,
        borderColor: 'rgb(34, 197, 94)',
        backgroundColor: 'rgba(34, 197, 94, 0.1)',
        fill: true,
        tension: 0.3,
      },
    ],
  }

  return (
    <div className="bg-white rounded-lg p-4 shadow-sm border">
      <Line
        data={data}
        options={{
          responsive: true,
          plugins: {
            title: { display: true, text: 'Cumulative Resources Created' },
          },
          scales: {
            y: { beginAtZero: true, title: { display: true, text: 'Total created' } },
          },
        }}
      />
    </div>
  )
}
