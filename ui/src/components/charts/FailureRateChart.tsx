import './chartSetup'
import { Bar } from 'react-chartjs-2'
import type { BatchResult } from '../../types/benchmark'

interface Props {
  batches: BatchResult[]
}

export default function FailureRateChart({ batches }: Props) {
  const data = {
    labels: batches.map((b) => `Batch ${b.batchNum}`),
    datasets: [
      {
        label: 'Failure Rate (%)',
        data: batches.map((b) => (b.size > 0 ? (b.failed / b.size) * 100 : 0)),
        backgroundColor: 'rgba(239, 68, 68, 0.7)',
        borderColor: 'rgb(239, 68, 68)',
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
          plugins: {
            legend: { display: false },
            title: { display: true, text: 'Failure Rate per Batch' },
          },
          scales: {
            y: { beginAtZero: true, max: 100, title: { display: true, text: '%' } },
          },
        }}
      />
    </div>
  )
}
