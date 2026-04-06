import { useRef } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useReport } from '../hooks/useBenchmarkData'
import type { APILatencyReport as APILatencyReportType } from '../types/benchmark'
import ReportHeader from '../components/ReportHeader'
import ClusterInfoCard from '../components/ClusterInfoCard'
import PDFExportButton from '../components/PDFExportButton'
import LatencyBarChart from '../components/charts/LatencyBarChart'
import LatencyScatterChart from '../components/charts/LatencyScatterChart'
import LatencyHistogram from '../components/charts/LatencyHistogram'

export default function APILatencyReport() {
  const { id } = useParams<{ id: string }>()
  const { report, loading, error } = useReport(id)
  const printRef = useRef<HTMLDivElement>(null)

  if (loading) return <p className="text-gray-500">Loading…</p>
  if (error) return <p className="text-red-600">Error: {error}</p>
  if (!report || report.type !== 'api-latency') return <p>Report not found</p>

  const r = report as APILatencyReportType
  const successful = r.measurements.filter((m) => m.success)
  const failed = r.measurements.filter((m) => !m.success)
  const latencies = successful.map((m) => m.latencyMs)
  const avgLatency = latencies.length > 0 ? latencies.reduce((a, b) => a + b, 0) / latencies.length : 0

  return (
    <div>
      <div className="flex items-center gap-4 mb-4">
        <Link to="/" className="text-sm text-blue-600 hover:underline">← Dashboard</Link>
        <PDFExportButton targetRef={printRef} filename={`api-latency-${r.runID}`} />
      </div>

      <div ref={printRef}>
        <ReportHeader
          title={`API Latency — ${r.runID}`}
          items={[
            { label: 'Timestamp', value: new Date(r.timestamp).toLocaleString() },
            { label: 'Endpoints Tested', value: r.measurements.length },
            { label: 'Successful', value: successful.length },
            { label: 'Failed', value: failed.length },
            { label: 'Avg Latency', value: `${avgLatency.toFixed(1)}ms` },
          ]}
        />

        <ClusterInfoCard info={r.clusterInfo} />

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mt-4">
          <LatencyBarChart measurements={r.measurements} />
          <LatencyHistogram measurements={r.measurements} />
          <LatencyScatterChart measurements={r.measurements} />
        </div>

        {/* Full results table */}
        <div className="bg-white rounded-lg p-4 shadow-sm border mt-4 overflow-x-auto">
          <h3 className="text-lg font-semibold mb-3">All Measurements</h3>
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-gray-500 border-b">
                <th className="pb-2">Endpoint</th>
                <th className="pb-2">Verb</th>
                <th className="pb-2 text-right">Latency (ms)</th>
                <th className="pb-2 text-right">Response Size</th>
                <th className="pb-2 text-right">Status</th>
              </tr>
            </thead>
            <tbody>
              {r.measurements
                .sort((a, b) => b.latencyMs - a.latencyMs)
                .map((m, i) => (
                  <tr key={i} className="border-b border-gray-100">
                    <td className="py-1 font-mono text-xs">{m.resource ?? m.endpoint}</td>
                    <td className="py-1">{m.verb}</td>
                    <td className={`py-1 text-right font-mono ${m.latencyMs > 1000 ? 'text-red-600' : ''}`}>
                      {m.latencyMs.toFixed(1)}
                    </td>
                    <td className="py-1 text-right font-mono">
                      {m.responseSizeBytes > 1024 * 1024
                        ? `${(m.responseSizeBytes / 1024 / 1024).toFixed(1)} MB`
                        : m.responseSizeBytes > 1024
                        ? `${(m.responseSizeBytes / 1024).toFixed(1)} KB`
                        : `${m.responseSizeBytes} B`}
                    </td>
                    <td className="py-1 text-right">
                      {m.success ? (
                        <span className="text-green-600">{m.responseCode}</span>
                      ) : (
                        <span className="text-red-600">{m.error ?? m.responseCode}</span>
                      )}
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
