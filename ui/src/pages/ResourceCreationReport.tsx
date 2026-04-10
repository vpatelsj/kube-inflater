import { useRef } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useReport } from '../hooks/useBenchmarkData'
import type { ResourceCreationReport as ResourceCreationReportType } from '../types/benchmark'
import ReportHeader from '../components/ReportHeader'
import ClusterInfoCard from '../components/ClusterInfoCard'
import PDFExportButton from '../components/PDFExportButton'

export default function ResourceCreationReport() {
  const { id } = useParams<{ id: string }>()
  const { report, loading, error } = useReport(id)
  const printRef = useRef<HTMLDivElement>(null)

  if (loading) return <p className="text-gray-500">Loading…</p>
  if (error) return <p className="text-red-600">Error: {error}</p>
  if (!report || report.type !== 'resource-creation') return <p>Report not found</p>

  const r = report as ResourceCreationReportType
  const cfg = r.config

  return (
    <div>
      <div className="flex items-center gap-4 mb-4">
        <Link to="/" className="text-sm text-blue-600 hover:underline">← Dashboard</Link>
        <PDFExportButton targetRef={printRef} filename={`${cfg.resourceTypes.join('-')}-${r.runID}`} />
      </div>

      <div ref={printRef}>
        <ReportHeader
          title={`${cfg.resourceTypes.join(', ')} — ${r.runID}`}
          items={[
            { label: 'Timestamp', value: new Date(r.timestamp).toLocaleString() },
            { label: 'Resource Types', value: cfg.resourceTypes.join(', ') },
            { label: 'Count/Type', value: cfg.countPerType },
            { label: 'Workers', value: cfg.workers },
            { label: 'QPS', value: cfg.qps },
            { label: 'Burst', value: cfg.burst },
            { label: 'Batch Initial', value: cfg.batchInitial },
            { label: 'Batch Factor', value: `${cfg.batchFactor}x` },
            { label: 'Spread NS', value: cfg.spreadNamespaces },
            { label: 'KWOK Nodes', value: cfg.kwokNodes },
          ]}
        />

        {/* Summary table across all resource types */}
        {r.results.length > 1 && (
          <div className="bg-white rounded-lg p-4 shadow-sm border mb-6 overflow-x-auto">
            <h3 className="text-sm font-semibold text-gray-500 mb-2">Summary Across All Types</h3>
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-gray-500 border-b bg-gray-50">
                  <th className="px-3 py-1.5">Resource Type</th>
                  <th className="px-3 py-1.5 text-right">Created</th>
                  <th className="px-3 py-1.5 text-right">Failed</th>
                  <th className="px-3 py-1.5 text-right">Fail %</th>
                  <th className="px-3 py-1.5 text-right">Duration</th>
                  <th className="px-3 py-1.5 text-right">Throughput</th>
                  <th className="px-3 py-1.5 text-right">Batches</th>
                </tr>
              </thead>
              <tbody>
                {r.results.map((res) => {
                  const failPct = (res.totalCreated + res.totalFailed) > 0
                    ? (res.totalFailed / (res.totalCreated + res.totalFailed)) * 100 : 0
                  return (
                    <tr key={res.resourceType} className="border-b border-gray-100">
                      <td className="px-3 py-1.5 font-medium">{res.resourceType}</td>
                      <td className="px-3 py-1.5 text-right font-mono text-green-600">{res.totalCreated.toLocaleString()}</td>
                      <td className={`px-3 py-1.5 text-right font-mono ${res.totalFailed > 0 ? 'text-red-600' : 'text-gray-400'}`}>{res.totalFailed.toLocaleString()}</td>
                      <td className={`px-3 py-1.5 text-right font-mono ${failPct > 0 ? 'text-red-600' : 'text-gray-400'}`}>{failPct.toFixed(2)}%</td>
                      <td className="px-3 py-1.5 text-right font-mono">{(res.totalDurationMs / 1000).toFixed(1)}s</td>
                      <td className="px-3 py-1.5 text-right font-mono text-blue-600">{res.throughput.toFixed(1)}/s</td>
                      <td className="px-3 py-1.5 text-right font-mono">{(res.batches ?? []).length}</td>
                    </tr>
                  )
                })}
                <tr className="font-semibold bg-gray-50">
                  <td className="px-3 py-1.5">Total</td>
                  <td className="px-3 py-1.5 text-right font-mono text-green-600">{r.results.reduce((s, x) => s + x.totalCreated, 0).toLocaleString()}</td>
                  <td className="px-3 py-1.5 text-right font-mono text-red-600">{r.results.reduce((s, x) => s + x.totalFailed, 0).toLocaleString()}</td>
                  <td className="px-3 py-1.5 text-right font-mono">—</td>
                  <td className="px-3 py-1.5 text-right font-mono">{(r.results.reduce((s, x) => s + x.totalDurationMs, 0) / 1000).toFixed(1)}s</td>
                  <td className="px-3 py-1.5 text-right font-mono">—</td>
                  <td className="px-3 py-1.5 text-right font-mono">{r.results.reduce((s, x) => s + (x.batches ?? []).length, 0)}</td>
                </tr>
              </tbody>
            </table>
          </div>
        )}

        {r.results.map((result) => (
          <div key={result.resourceType} className="mb-8">
            <div className="bg-white rounded-lg p-4 shadow-sm border mb-4">
              <h3 className="text-lg font-semibold mb-2">{result.resourceType}</h3>
              <div className="grid grid-cols-2 sm:grid-cols-5 gap-3 text-center">
                <div>
                  <div className="text-2xl font-bold text-green-600">{result.totalCreated.toLocaleString()}</div>
                  <div className="text-xs text-gray-500">Created</div>
                </div>
                <div>
                  <div className="text-2xl font-bold text-red-600">{result.totalFailed.toLocaleString()}</div>
                  <div className="text-xs text-gray-500">Failed</div>
                </div>
                <div>
                  <div className="text-2xl font-bold">{(result.totalDurationMs / 1000).toFixed(1)}s</div>
                  <div className="text-xs text-gray-500">Duration</div>
                </div>
                <div>
                  <div className="text-2xl font-bold text-blue-600">{result.throughput.toFixed(1)}</div>
                  <div className="text-xs text-gray-500">items/sec</div>
                </div>
                <div>
                  <div className="text-2xl font-bold">{result.totalFailed > 0 ? ((result.totalFailed / (result.totalCreated + result.totalFailed)) * 100).toFixed(2) : '0'}%</div>
                  <div className="text-xs text-gray-500">Failure Rate</div>
                </div>
              </div>
            </div>

            {/* Batch detail table — only for batch-based types */}
            {result.batches && result.batches.length > 0 ? (
              <>
              <div className="bg-white rounded-lg p-4 shadow-sm border mb-4 overflow-x-auto">
                <h4 className="text-sm font-semibold text-gray-500 mb-2">Batch Details</h4>
                <table className="w-full text-sm">
                  <thead>
                    <tr className="text-left text-xs text-gray-500 border-b bg-gray-50">
                      <th className="px-3 py-1.5 text-right">#</th>
                      <th className="px-3 py-1.5 text-right">Size</th>
                      <th className="px-3 py-1.5 text-right">Created</th>
                      <th className="px-3 py-1.5 text-right">Failed</th>
                      <th className="px-3 py-1.5 text-right">Fail %</th>
                      <th className="px-3 py-1.5 text-right">Duration</th>
                      <th className="px-3 py-1.5 text-right">Throughput</th>
                      <th className="px-3 py-1.5">Progress</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.batches.map((b) => {
                      const failPct = b.size > 0 ? (b.failed / b.size) * 100 : 0
                      const cumCreated = result.batches.slice(0, b.batchNum).reduce((s, x) => s + x.created, 0)
                      const pct = result.totalCreated > 0 ? (cumCreated / result.totalCreated) * 100 : 0
                      return (
                        <tr key={b.batchNum} className="border-b border-gray-100">
                          <td className="px-3 py-1 text-right font-mono">{b.batchNum}</td>
                          <td className="px-3 py-1 text-right font-mono">{b.size.toLocaleString()}</td>
                          <td className="px-3 py-1 text-right font-mono text-green-600">{b.created.toLocaleString()}</td>
                          <td className={`px-3 py-1 text-right font-mono ${b.failed > 0 ? 'text-red-600' : 'text-gray-400'}`}>{b.failed}</td>
                          <td className={`px-3 py-1 text-right font-mono ${failPct > 0 ? 'text-red-600' : 'text-gray-400'}`}>{failPct.toFixed(1)}%</td>
                          <td className="px-3 py-1 text-right font-mono">{b.durationMs >= 1000 ? `${(b.durationMs / 1000).toFixed(1)}s` : `${b.durationMs}ms`}</td>
                          <td className="px-3 py-1 text-right font-mono text-blue-600">{b.throughput.toFixed(0)}/s</td>
                          <td className="px-3 py-1 w-32">
                            <div className="bg-gray-100 rounded-full h-2">
                              <div className="bg-blue-500 rounded-full h-2" style={{ width: `${Math.min(pct, 100)}%` }} />
                            </div>
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>

              </>
            ) : (
              <div className="bg-gray-50 rounded-lg p-3 border text-sm text-gray-500 italic">
                Setup-based resource — created via DaemonSet, no per-batch data available
              </div>
            )}
          </div>
        ))}

        {r.clusterInfo && <ClusterInfoCard info={r.clusterInfo} />}


      </div>
    </div>
  )
}
