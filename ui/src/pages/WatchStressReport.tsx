import { useRef } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useReport } from '../hooks/useBenchmarkData'
import type { WatchStressReport as WatchStressReportType } from '../types/benchmark'
import ReportHeader from '../components/ReportHeader'
import PDFExportButton from '../components/PDFExportButton'
import ConnectLatencyChart from '../components/charts/ConnectLatencyChart'
import ScalingChart from '../components/charts/ScalingChart'

export default function WatchStressReport() {
  const { id } = useParams<{ id: string }>()
  const { report, loading, error } = useReport(id)
  const printRef = useRef<HTMLDivElement>(null)

  if (loading) return <p className="text-gray-500">Loading…</p>
  if (error) return <p className="text-red-600">Error: {error}</p>
  if (!report || report.type !== 'watch-stress') return <p>Report not found</p>

  const r = report as WatchStressReportType
  const m = r.watchMetrics

  return (
    <div>
      <div className="flex items-center gap-4 mb-4">
        <Link to="/" className="text-sm text-blue-600 hover:underline">← Dashboard</Link>
        <PDFExportButton targetRef={printRef} filename={`watch-stress-${r.runID}`} />
      </div>

      <div ref={printRef}>
        <ReportHeader
          title={`Watch Stress — ${r.runID}`}
          items={[
            { label: 'Timestamp', value: new Date(r.timestamp).toLocaleString() },
            { label: 'Connections', value: r.config.connections },
            { label: 'Duration', value: `${r.config.durationSec}s` },
            { label: 'Stagger', value: `${r.config.staggerSec}s` },
            { label: 'Resource Types', value: r.config.resourceTypes.join(', ') },
            ...(r.config.agentNodes ? [{ label: 'Agent Nodes', value: r.config.agentNodes }] : []),
          ]}
        />

        {/* Key metrics */}
        <div className="bg-white rounded-lg p-4 shadow-sm border mb-4">
          <h3 className="text-lg font-semibold mb-3">Watch Performance</h3>
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 text-center">
            <div>
              <div className="text-2xl font-bold text-blue-600">{m.totalEvents.toLocaleString()}</div>
              <div className="text-xs text-gray-500">Total Events</div>
            </div>
            <div>
              <div className="text-2xl font-bold text-green-600">{m.eventsPerSecond.toLocaleString(undefined, { maximumFractionDigits: 0 })}</div>
              <div className="text-xs text-gray-500">Events/sec</div>
            </div>
            <div>
              <div className="text-2xl font-bold">{m.peakAliveWatches.toLocaleString()}</div>
              <div className="text-xs text-gray-500">Peak Watches</div>
            </div>
            <div>
              <div className="text-2xl font-bold">{m.durationSec.toFixed(0)}s</div>
              <div className="text-xs text-gray-500">Duration</div>
            </div>
          </div>
        </div>

        {/* Detailed metrics tables */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-4">
          {/* Latency breakdown */}
          <div className="bg-white rounded-lg p-4 shadow-sm border overflow-x-auto">
            <h4 className="text-sm font-semibold text-gray-500 mb-2">Latency Breakdown</h4>
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-gray-500 border-b bg-gray-50">
                  <th className="px-3 py-1.5">Metric</th>
                  <th className="px-3 py-1.5 text-right">Connect</th>
                  <th className="px-3 py-1.5 text-right">Delivery</th>
                </tr>
              </thead>
              <tbody>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Average</td>
                  <td className="px-3 py-1.5 text-right font-mono">{m.avgConnectLatencyMs.toFixed(1)}ms</td>
                  <td className="px-3 py-1.5 text-right font-mono">{m.avgDeliveryLatencyMs.toFixed(1)}ms</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">P99</td>
                  <td className="px-3 py-1.5 text-right font-mono text-gray-400">—</td>
                  <td className="px-3 py-1.5 text-right font-mono">{m.p99DeliveryLatencyMs.toFixed(1)}ms</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Max</td>
                  <td className={`px-3 py-1.5 text-right font-mono ${m.maxConnectLatencyMs > 1000 ? 'text-red-600' : ''}`}>{m.maxConnectLatencyMs.toFixed(1)}ms</td>
                  <td className={`px-3 py-1.5 text-right font-mono ${m.maxDeliveryLatencyMs > 1000 ? 'text-red-600' : ''}`}>{m.maxDeliveryLatencyMs.toFixed(1)}ms</td>
                </tr>
              </tbody>
            </table>
          </div>

          {/* Connection health */}
          <div className="bg-white rounded-lg p-4 shadow-sm border overflow-x-auto">
            <h4 className="text-sm font-semibold text-gray-500 mb-2">Connection Health</h4>
            <table className="w-full text-sm">
              <tbody>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Peak Alive Watches</td>
                  <td className="px-3 py-1.5 text-right font-mono font-semibold">{m.peakAliveWatches.toLocaleString()}</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Reconnects</td>
                  <td className={`px-3 py-1.5 text-right font-mono ${m.reconnects > 0 ? 'text-yellow-600' : ''}`}>{m.reconnects.toLocaleString()}</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Errors</td>
                  <td className={`px-3 py-1.5 text-right font-mono ${m.errors > 0 ? 'text-red-600 font-semibold' : ''}`}>{m.errors.toLocaleString()}</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Min Events/Conn</td>
                  <td className="px-3 py-1.5 text-right font-mono">{m.minEventsPerConn.toLocaleString()}</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Max Events/Conn</td>
                  <td className="px-3 py-1.5 text-right font-mono">{m.maxEventsPerConn.toLocaleString()}</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Event Spread</td>
                  <td className="px-3 py-1.5 text-right font-mono">
                    {m.minEventsPerConn > 0 ? `${(m.maxEventsPerConn / m.minEventsPerConn).toFixed(1)}x` : '—'}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <ConnectLatencyChart metrics={m} />
          {r.scalingData && r.scalingData.length > 0 && (
            <ScalingChart data={r.scalingData} />
          )}
        </div>

        {/* Scaling data table */}
        {r.scalingData && r.scalingData.length > 0 && (
          <div className="bg-white rounded-lg p-4 shadow-sm border mt-4 overflow-x-auto">
            <h4 className="text-sm font-semibold text-gray-500 mb-2">Scaling Data</h4>
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-gray-500 border-b bg-gray-50">
                  <th className="px-3 py-1.5 text-right">Connections</th>
                  <th className="px-3 py-1.5 text-right">Events/sec</th>
                  <th className="px-3 py-1.5 text-right">Avg Connect</th>
                  <th className="px-3 py-1.5 text-right">Max Connect</th>
                  <th className="px-3 py-1.5 text-right">Error Rate</th>
                  <th className="px-3 py-1.5 text-right">Peak Alive</th>
                </tr>
              </thead>
              <tbody>
                {r.scalingData.map((d, i) => (
                  <tr key={i} className="border-b border-gray-100">
                    <td className="px-3 py-1 text-right font-mono">{d.connections.toLocaleString()}</td>
                    <td className="px-3 py-1 text-right font-mono text-blue-600">{d.eventsPerSec.toFixed(0)}</td>
                    <td className="px-3 py-1 text-right font-mono">{d.avgConnectMs.toFixed(1)}ms</td>
                    <td className={`px-3 py-1 text-right font-mono ${d.maxConnectMs > 1000 ? 'text-red-600' : ''}`}>{d.maxConnectMs.toFixed(1)}ms</td>
                    <td className={`px-3 py-1 text-right font-mono ${d.errorRate > 0 ? 'text-red-600' : ''}`}>{(d.errorRate * 100).toFixed(2)}%</td>
                    <td className="px-3 py-1 text-right font-mono">{d.peakAlive.toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {r.mutatorMetrics && (
          <div className="bg-white rounded-lg p-4 shadow-sm border mt-4 overflow-x-auto">
            <h3 className="text-lg font-semibold mb-3">Mutator Performance</h3>
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-gray-500 border-b bg-gray-50">
                  <th className="px-3 py-1.5">Metric</th>
                  <th className="px-3 py-1.5 text-right">Value</th>
                </tr>
              </thead>
              <tbody>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Creates</td>
                  <td className="px-3 py-1.5 text-right font-mono">{r.mutatorMetrics.creates.toLocaleString()}</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Updates</td>
                  <td className="px-3 py-1.5 text-right font-mono">{r.mutatorMetrics.updates.toLocaleString()}</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Deletes</td>
                  <td className="px-3 py-1.5 text-right font-mono">{r.mutatorMetrics.deletes.toLocaleString()}</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Total Mutations</td>
                  <td className="px-3 py-1.5 text-right font-mono font-semibold">{(r.mutatorMetrics.creates + r.mutatorMetrics.updates + r.mutatorMetrics.deletes).toLocaleString()}</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Errors</td>
                  <td className={`px-3 py-1.5 text-right font-mono ${r.mutatorMetrics.errors > 0 ? 'text-red-600 font-semibold' : ''}`}>{r.mutatorMetrics.errors.toLocaleString()}</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Actual Rate</td>
                  <td className="px-3 py-1.5 text-right font-mono text-green-600">{r.mutatorMetrics.actualRate.toFixed(1)}/s</td>
                </tr>
                <tr className="border-b border-gray-100">
                  <td className="px-3 py-1.5 text-gray-600">Duration</td>
                  <td className="px-3 py-1.5 text-right font-mono">{r.mutatorMetrics.durationSec.toFixed(1)}s</td>
                </tr>
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
