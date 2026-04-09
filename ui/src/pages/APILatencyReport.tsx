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
  const latencies = successful.map((m) => m.latencyMs).sort((a, b) => a - b)
  const avgLatency = latencies.length > 0 ? latencies.reduce((a, b) => a + b, 0) / latencies.length : 0
  const pct = (p: number) => latencies.length > 0 ? latencies[Math.floor(latencies.length * p / 100)] ?? 0 : 0
  const p50 = pct(50), p75 = pct(75), p95 = pct(95), p99 = pct(99)
  const maxLatency = latencies.length > 0 ? latencies[latencies.length - 1]! : 0

  // Verb analysis
  const verbMap = new Map<string, { count: number; totalMs: number; errors: number }>()
  r.measurements.forEach((m) => {
    const v = verbMap.get(m.verb) ?? { count: 0, totalMs: 0, errors: 0 }
    v.count++
    v.totalMs += m.latencyMs
    if (!m.success) v.errors++
    verbMap.set(m.verb, v)
  })

  // Response code distribution
  const codeMap = new Map<number, number>()
  r.measurements.forEach((m) => {
    codeMap.set(m.responseCode, (codeMap.get(m.responseCode) ?? 0) + 1)
  })

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
            { label: 'P50', value: `${p50.toFixed(1)}ms` },
            { label: 'P95', value: `${p95.toFixed(1)}ms` },
            { label: 'P99', value: `${p99.toFixed(1)}ms` },
            { label: 'Max', value: `${maxLatency.toFixed(1)}ms` },
          ]}
        />

        <ClusterInfoCard info={r.clusterInfo} />

        {/* Percentile + Verb + Status tables */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4 mt-4">
          {/* Percentile breakdown */}
          <div className="bg-white rounded-lg p-4 shadow-sm border overflow-x-auto">
            <h4 className="text-sm font-semibold text-gray-500 mb-2">Latency Percentiles</h4>
            <table className="w-full text-sm">
              <tbody>
                {[
                  ['P50', p50], ['P75', p75], ['P95', p95], ['P99', p99], ['Max', maxLatency]
                ].map(([label, val]) => (
                  <tr key={label as string} className="border-b border-gray-100">
                    <td className="px-3 py-1.5 text-gray-600">{label}</td>
                    <td className={`px-3 py-1.5 text-right font-mono ${(val as number) > 1000 ? 'text-red-600' : ''}`}>{(val as number).toFixed(1)}ms</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* Verb distribution */}
          <div className="bg-white rounded-lg p-4 shadow-sm border overflow-x-auto">
            <h4 className="text-sm font-semibold text-gray-500 mb-2">By Verb</h4>
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-gray-500 border-b bg-gray-50">
                  <th className="px-3 py-1.5">Verb</th>
                  <th className="px-3 py-1.5 text-right">Count</th>
                  <th className="px-3 py-1.5 text-right">Avg ms</th>
                  <th className="px-3 py-1.5 text-right">Errors</th>
                </tr>
              </thead>
              <tbody>
                {[...verbMap.entries()].sort((a, b) => b[1].count - a[1].count).map(([verb, stats]) => (
                  <tr key={verb} className="border-b border-gray-100">
                    <td className="px-3 py-1.5 font-medium">{verb}</td>
                    <td className="px-3 py-1.5 text-right font-mono">{stats.count}</td>
                    <td className="px-3 py-1.5 text-right font-mono">{(stats.totalMs / stats.count).toFixed(1)}</td>
                    <td className={`px-3 py-1.5 text-right font-mono ${stats.errors > 0 ? 'text-red-600' : 'text-gray-400'}`}>{stats.errors}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* Response code distribution */}
          <div className="bg-white rounded-lg p-4 shadow-sm border overflow-x-auto">
            <h4 className="text-sm font-semibold text-gray-500 mb-2">Response Codes</h4>
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-gray-500 border-b bg-gray-50">
                  <th className="px-3 py-1.5">Code</th>
                  <th className="px-3 py-1.5 text-right">Count</th>
                  <th className="px-3 py-1.5 text-right">%</th>
                </tr>
              </thead>
              <tbody>
                {[...codeMap.entries()].sort((a, b) => a[0] - b[0]).map(([code, count]) => (
                  <tr key={code} className="border-b border-gray-100">
                    <td className={`px-3 py-1.5 font-mono font-medium ${code >= 400 ? 'text-red-600' : code >= 300 ? 'text-yellow-600' : 'text-green-600'}`}>{code}</td>
                    <td className="px-3 py-1.5 text-right font-mono">{count}</td>
                    <td className="px-3 py-1.5 text-right font-mono">{((count / r.measurements.length) * 100).toFixed(1)}%</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

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
              <tr className="text-left text-xs text-gray-500 border-b">
                <th className="pb-2">Endpoint</th>
                <th className="pb-2">Kind</th>
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
                    <td className="py-1 text-xs text-gray-500">{m.kind ?? '—'}</td>
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

        {/* Failed endpoints detail */}
        {failed.length > 0 && (
          <div className="bg-white rounded-lg p-4 shadow-sm border mt-4 overflow-x-auto">
            <h3 className="text-lg font-semibold mb-3 text-red-600">Failed Endpoints ({failed.length})</h3>
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-gray-500 border-b bg-red-50">
                  <th className="px-3 py-1.5">Endpoint</th>
                  <th className="px-3 py-1.5">Verb</th>
                  <th className="px-3 py-1.5 text-right">Code</th>
                  <th className="px-3 py-1.5">Error</th>
                </tr>
              </thead>
              <tbody>
                {failed.map((m, i) => (
                  <tr key={i} className="border-b border-gray-100">
                    <td className="px-3 py-1 font-mono text-xs">{m.resource ?? m.endpoint}</td>
                    <td className="px-3 py-1">{m.verb}</td>
                    <td className="px-3 py-1 text-right font-mono text-red-600">{m.responseCode}</td>
                    <td className="px-3 py-1 text-xs text-red-600">{m.error ?? '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
