import './chartSetup'
import { Scatter } from 'react-chartjs-2'
import type { LatencyMeasurement } from '../../types/benchmark'

interface Props {
  measurements: LatencyMeasurement[]
}

export default function LatencyScatterChart({ measurements }: Props) {
  const successful = measurements.filter((m) => m.success && m.responseSizeBytes > 0)

  const data = {
    datasets: [
      {
        label: 'Endpoints',
        data: successful.map((m) => ({
          x: m.responseSizeBytes / 1024,
          y: m.latencyMs,
        })),
        backgroundColor: 'rgba(59, 130, 246, 0.6)',
        pointRadius: 5,
      },
    ],
  }

  return (
    <div className="bg-white rounded-lg p-4 shadow-sm border">
      <Scatter
        data={data}
        options={{
          responsive: true,
          plugins: {
            title: { display: true, text: 'Response Size vs Latency' },
          },
          scales: {
            x: { title: { display: true, text: 'Response Size (KB)' }, type: 'logarithmic' },
            y: { title: { display: true, text: 'Latency (ms)' } },
          },
        }}
      />
    </div>
  )
}
