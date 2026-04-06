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
              <div className="text-2xl font-bold text-red-600">{m.errors.toLocaleString()}</div>
              <div className="text-xs text-gray-500">Errors</div>
            </div>
            <div>
              <div className="text-2xl font-bold">{m.reconnects.toLocaleString()}</div>
              <div className="text-xs text-gray-500">Reconnects</div>
            </div>
            <div>
              <div className="text-2xl font-bold">{m.avgConnectLatencyMs.toFixed(0)}ms</div>
              <div className="text-xs text-gray-500">Avg Connect</div>
            </div>
            <div>
              <div className="text-2xl font-bold">{m.p99DeliveryLatencyMs.toFixed(1)}ms</div>
              <div className="text-xs text-gray-500">P99 Delivery</div>
            </div>
            <div>
              <div className="text-2xl font-bold">{m.durationSec.toFixed(0)}s</div>
              <div className="text-xs text-gray-500">Duration</div>
            </div>
          </div>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <ConnectLatencyChart metrics={m} />
          {r.scalingData && r.scalingData.length > 0 && (
            <ScalingChart data={r.scalingData} />
          )}
        </div>

        {r.mutatorMetrics && (
          <div className="bg-white rounded-lg p-4 shadow-sm border mt-4">
            <h3 className="text-lg font-semibold mb-3">Mutator Performance</h3>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 text-center">
              <div>
                <div className="text-xl font-bold">{r.mutatorMetrics.creates.toLocaleString()}</div>
                <div className="text-xs text-gray-500">Creates</div>
              </div>
              <div>
                <div className="text-xl font-bold">{r.mutatorMetrics.updates.toLocaleString()}</div>
                <div className="text-xs text-gray-500">Updates</div>
              </div>
              <div>
                <div className="text-xl font-bold">{r.mutatorMetrics.deletes.toLocaleString()}</div>
                <div className="text-xs text-gray-500">Deletes</div>
              </div>
              <div>
                <div className="text-xl font-bold text-green-600">{r.mutatorMetrics.actualRate.toFixed(1)}/s</div>
                <div className="text-xs text-gray-500">Actual Rate</div>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
