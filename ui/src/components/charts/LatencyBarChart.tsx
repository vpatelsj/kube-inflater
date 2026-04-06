import './chartSetup'
import { Bar } from 'react-chartjs-2'
import type { LatencyMeasurement } from '../../types/benchmark'

interface Props {
  measurements: LatencyMeasurement[]
  topN?: number
}

export default function LatencyBarChart({ measurements, topN = 15 }: Props) {
  const sorted = [...measurements]
    .filter((m) => m.success)
    .sort((a, b) => b.latencyMs - a.latencyMs)
    .slice(0, topN)

  const data = {
    labels: sorted.map((m) => `${m.resource ?? m.endpoint} (${m.verb})`),
    datasets: [
      {
        label: 'Latency (ms)',
        data: sorted.map((m) => m.latencyMs),
        backgroundColor: sorted.map((m) =>
          m.latencyMs > 1000 ? 'rgba(239, 68, 68, 0.7)' :
          m.latencyMs > 500 ? 'rgba(249, 115, 22, 0.7)' :
          'rgba(59, 130, 246, 0.7)'
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
          indexAxis: 'y',
          plugins: {
            legend: { display: false },
            title: { display: true, text: `Top ${topN} Slowest Endpoints` },
          },
          scales: {
            x: { title: { display: true, text: 'ms' } },
          },
        }}
      />
    </div>
  )
}
