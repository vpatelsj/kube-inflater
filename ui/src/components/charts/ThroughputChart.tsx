import './chartSetup'
import { Bar } from 'react-chartjs-2'
import type { BatchResult } from '../../types/benchmark'

interface Props {
  batches: BatchResult[]
  title?: string
}

export default function ThroughputChart({ batches, title = 'Batch Throughput' }: Props) {
  const data = {
    labels: batches.map((b) => `Batch ${b.batchNum}`),
    datasets: [
      {
        label: 'Throughput (items/sec)',
        data: batches.map((b) => b.throughput),
        backgroundColor: batches.map((b) =>
          b.failed > 0 ? 'rgba(239, 68, 68, 0.7)' : 'rgba(59, 130, 246, 0.7)'
        ),
        borderColor: batches.map((b) =>
          b.failed > 0 ? 'rgb(239, 68, 68)' : 'rgb(59, 130, 246)'
        ),
        borderWidth: 1,
      },
    ],
  }

  return (
    <div className="bg-white rounded-lg p-4 shadow-sm border">
      <Bar
        data={data}
        options={{
          responsive: true,
          plugins: { legend: { display: false }, title: { display: true, text: title } },
          scales: {
            y: { beginAtZero: true, title: { display: true, text: 'items/sec' } },
          },
        }}
      />
    </div>
  )
}
