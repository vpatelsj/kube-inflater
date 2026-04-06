import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { startRun } from '../api/client'
import type { RunConfig } from '../api/client'

type RunType = 'pod-creation' | 'api-latency' | 'watch-stress'

const runTypeLabels: Record<RunType, string> = {
  'pod-creation': 'Pod Creation / Resource Inflater',
  'api-latency': 'API Latency Report',
  'watch-stress': 'Watch Stress Test',
}

export default function NewRun() {
  const navigate = useNavigate()
  const [runType, setRunType] = useState<RunType>('pod-creation')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Pod creation config
  const [resourceTypes, setResourceTypes] = useState('configmaps')
  const [count, setCount] = useState(100)
  const [workers, setWorkers] = useState(10)
  const [qps, setQps] = useState(50)
  const [burst, setBurst] = useState(100)
  const [spreadNamespaces, setSpreadNamespaces] = useState(5)
  const [dryRun, setDryRun] = useState(true)

  // API latency config
  const [onlyCommon, setOnlyCommon] = useState(false)
  const [noLimits, setNoLimits] = useState(false)

  // Watch stress config
  const [connections, setConnections] = useState(10)
  const [duration, setDuration] = useState(60)
  const [stagger, setStagger] = useState(5)
  const [resourceType, setResourceType] = useState('customresources')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSubmitting(true)
    setError(null)

    const config: RunConfig = {}
    if (runType === 'pod-creation') {
      config.resourceTypes = resourceTypes
      config.count = count
      config.workers = workers
      config.qps = qps
      config.burst = burst
      config.spreadNamespaces = spreadNamespaces
      config.dryRun = dryRun
    } else if (runType === 'api-latency') {
      config.onlyCommon = onlyCommon
      config.noLimits = noLimits
    } else if (runType === 'watch-stress') {
      config.connections = connections
      config.duration = duration
      config.stagger = stagger
      config.resourceType = resourceType
    }

    try {
      const { id } = await startRun(runType, config)
      navigate(`/run/${id}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to start run')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="max-w-2xl mx-auto">
      <h1 className="text-2xl font-bold mb-6">Start New Benchmark Run</h1>

      <form onSubmit={handleSubmit} className="space-y-6">
        {/* Run type selector */}
        <div className="bg-white rounded-lg p-4 shadow-sm border">
          <label className="block text-sm font-medium text-gray-700 mb-2">Benchmark Type</label>
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
            {(Object.keys(runTypeLabels) as RunType[]).map((type) => (
              <button
                key={type}
                type="button"
                onClick={() => setRunType(type)}
                className={`px-3 py-2 rounded-lg text-sm font-medium border transition-colors ${
                  runType === type
                    ? 'bg-gray-900 text-white border-gray-900'
                    : 'bg-white text-gray-700 border-gray-300 hover:bg-gray-50'
                }`}
              >
                {runTypeLabels[type]}
              </button>
            ))}
          </div>
        </div>

        {/* Pod creation config */}
        {runType === 'pod-creation' && (
          <div className="bg-white rounded-lg p-4 shadow-sm border space-y-4">
            <h3 className="font-semibold text-gray-700">Resource Inflater Configuration</h3>
            <div className="grid grid-cols-2 gap-4">
              <Field label="Resource Types" value={resourceTypes} onChange={setResourceTypes}
                help="configmaps, secrets, pods, jobs, services, etc." />
              <NumberField label="Count per Type" value={count} onChange={setCount} />
              <NumberField label="Workers" value={workers} onChange={setWorkers} />
              <NumberField label="QPS" value={qps} onChange={setQps} />
              <NumberField label="Burst" value={burst} onChange={setBurst} />
              <NumberField label="Spread Namespaces" value={spreadNamespaces} onChange={setSpreadNamespaces} />
            </div>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={dryRun} onChange={(e) => setDryRun(e.target.checked)}
                className="rounded border-gray-300" />
              Dry run (log what would be created without actually creating resources)
            </label>
          </div>
        )}

        {/* API latency config */}
        {runType === 'api-latency' && (
          <div className="bg-white rounded-lg p-4 shadow-sm border space-y-4">
            <h3 className="font-semibold text-gray-700">API Latency Configuration</h3>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={onlyCommon} onChange={(e) => setOnlyCommon(e.target.checked)}
                className="rounded border-gray-300" />
              Only test common endpoints (faster)
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={noLimits} onChange={(e) => setNoLimits(e.target.checked)}
                className="rounded border-gray-300" />
              No limits (download full responses — caution on large clusters)
            </label>
          </div>
        )}

        {/* Watch stress config */}
        {runType === 'watch-stress' && (
          <div className="bg-white rounded-lg p-4 shadow-sm border space-y-4">
            <h3 className="font-semibold text-gray-700">Watch Stress Configuration</h3>
            <div className="grid grid-cols-2 gap-4">
              <NumberField label="Connections" value={connections} onChange={setConnections} />
              <NumberField label="Duration (sec)" value={duration} onChange={setDuration} />
              <NumberField label="Stagger (sec)" value={stagger} onChange={setStagger} />
              <Field label="Resource Type" value={resourceType} onChange={setResourceType}
                help="customresources, configmaps, etc." />
            </div>
          </div>
        )}

        {error && (
          <div className="bg-red-50 border border-red-200 rounded-lg p-3 text-red-700 text-sm">{error}</div>
        )}

        <button
          type="submit"
          disabled={submitting}
          className="w-full bg-gray-900 text-white py-3 rounded-lg font-medium hover:bg-gray-700 disabled:opacity-50 transition-colors"
        >
          {submitting ? 'Starting…' : '🚀 Start Run'}
        </button>
      </form>
    </div>
  )
}

function Field({ label, value, onChange, help }: {
  label: string; value: string; onChange: (v: string) => void; help?: string
}) {
  return (
    <div>
      <label className="block text-xs text-gray-500 mb-1">{label}</label>
      <input type="text" value={value} onChange={(e) => onChange(e.target.value)}
        className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm" />
      {help && <p className="text-xs text-gray-400 mt-0.5">{help}</p>}
    </div>
  )
}

function NumberField({ label, value, onChange }: {
  label: string; value: number; onChange: (v: number) => void
}) {
  return (
    <div>
      <label className="block text-xs text-gray-500 mb-1">{label}</label>
      <input type="number" value={value} onChange={(e) => onChange(parseInt(e.target.value) || 0)}
        className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm" />
    </div>
  )
}
