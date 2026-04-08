import { useRef } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useReport } from '../hooks/useBenchmarkData'
import type { ResourceCreationReport as ResourceCreationReportType } from '../types/benchmark'
import ReportHeader from '../components/ReportHeader'
import ClusterInfoCard from '../components/ClusterInfoCard'
import PDFExportButton from '../components/PDFExportButton'
import ThroughputChart from '../components/charts/ThroughputChart'
import CumulativeChart from '../components/charts/CumulativeChart'
import FailureRateChart from '../components/charts/FailureRateChart'
import LatencyBarChart from '../components/charts/LatencyBarChart'

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

        {r.results.map((result) => (
          <div key={result.resourceType} className="mb-8">
            <div className="bg-white rounded-lg p-4 shadow-sm border mb-4">
              <h3 className="text-lg font-semibold mb-2">{result.resourceType}</h3>
              <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 text-center">
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
              </div>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              <ThroughputChart batches={result.batches} title={`${result.resourceType} — Batch Throughput`} />
              <CumulativeChart batches={result.batches} />
              <FailureRateChart batches={result.batches} />
            </div>
          </div>
        ))}

        {r.clusterInfo && <ClusterInfoCard info={r.clusterInfo} />}

        {r.apiLatency && r.apiLatency.length > 0 && (
          <div className="mt-6">
            <h3 className="text-lg font-semibold mb-3">API Latency Under Load</h3>
            <LatencyBarChart measurements={r.apiLatency} />
          </div>
        )}
      </div>
    </div>
  )
}
