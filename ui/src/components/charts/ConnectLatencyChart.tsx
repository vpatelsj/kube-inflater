import './chartSetup'
import { Bar } from 'react-chartjs-2'
import type { WatchMetrics } from '../../types/benchmark'

interface Props {
  metrics: WatchMetrics
}

export default function ConnectLatencyChart({ metrics }: Props) {
  const data = {
    labels: ['Avg Connect', 'Max Connect', 'Avg Delivery', 'P99 Delivery', 'Max Delivery'],
    datasets: [
      {
        label: 'Latency (ms)',
        data: [
          metrics.avgConnectLatencyMs,
          metrics.maxConnectLatencyMs,
          metrics.avgDeliveryLatencyMs,
          metrics.p99DeliveryLatencyMs,
          metrics.maxDeliveryLatencyMs,
        ],
        backgroundColor: [
          'rgba(59, 130, 246, 0.7)',
          'rgba(59, 130, 246, 0.4)',
          'rgba(34, 197, 94, 0.7)',
          'rgba(249, 115, 22, 0.7)',
          'rgba(239, 68, 68, 0.7)',
        ],
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
            title: { display: true, text: 'Latency Breakdown (ms)' },
          },
          scales: {
            x: { title: { display: true, text: 'ms' } },
          },
        }}
      />
    </div>
  )
}
