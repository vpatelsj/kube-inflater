import './chartSetup'
import { Bar } from 'react-chartjs-2'
import type { LatencyMeasurement } from '../../types/benchmark'

interface Props {
  measurements: LatencyMeasurement[]
}

export default function LatencyHistogram({ measurements }: Props) {
  const latencies = measurements.filter((m) => m.success).map((m) => m.latencyMs)
  if (latencies.length === 0) return null

  const maxVal = Math.max(...latencies)
  const bucketSize = Math.max(1, Math.ceil(maxVal / 20))
  const buckets: number[] = []
  const labels: string[] = []

  for (let i = 0; i * bucketSize < maxVal + bucketSize; i++) {
    const lo = i * bucketSize
    const hi = lo + bucketSize
    const count = latencies.filter((v) => v >= lo && v < hi).length
    buckets.push(count)
    labels.push(`${lo}-${hi}`)
  }

  const data = {
    labels,
    datasets: [
      {
        label: 'Endpoints',
        data: buckets,
        backgroundColor: 'rgba(59, 130, 246, 0.7)',
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
            title: { display: true, text: 'Latency Distribution' },
          },
          scales: {
            x: { title: { display: true, text: 'Latency (ms)' } },
            y: { beginAtZero: true, title: { display: true, text: 'Count' } },
          },
        }}
      />
    </div>
  )
}
