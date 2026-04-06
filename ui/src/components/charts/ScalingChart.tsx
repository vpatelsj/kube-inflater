import './chartSetup'
import { Line } from 'react-chartjs-2'
import type { ScalingDataPoint } from '../../types/benchmark'

interface Props {
  data: ScalingDataPoint[]
}

export default function ScalingChart({ data: points }: Props) {
  const labels = points.map((p) => `${(p.connections / 1000).toFixed(0)}k`)

  const chartData = {
    labels,
    datasets: [
      {
        label: 'Events/sec',
        data: points.map((p) => p.eventsPerSec),
        borderColor: 'rgb(59, 130, 246)',
        backgroundColor: 'rgba(59, 130, 246, 0.1)',
        yAxisID: 'y',
        tension: 0.3,
      },
      {
        label: 'Avg Connect (ms)',
        data: points.map((p) => p.avgConnectMs),
        borderColor: 'rgb(249, 115, 22)',
        backgroundColor: 'rgba(249, 115, 22, 0.1)',
        yAxisID: 'y1',
        tension: 0.3,
      },
    ],
  }

  return (
    <div className="bg-white rounded-lg p-4 shadow-sm border">
      <Line
        data={chartData}
        options={{
          responsive: true,
          interaction: { mode: 'index', intersect: false },
          plugins: {
            title: { display: true, text: 'Watch Connection Scaling' },
          },
          scales: {
            x: { title: { display: true, text: 'Connections' } },
            y: {
              type: 'linear',
              display: true,
              position: 'left',
              title: { display: true, text: 'Events/sec' },
            },
            y1: {
              type: 'linear',
              display: true,
              position: 'right',
              title: { display: true, text: 'Connect Latency (ms)' },
              grid: { drawOnChartArea: false },
            },
          },
        }}
      />
    </div>
  )
}
