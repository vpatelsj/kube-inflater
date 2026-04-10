import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { startRun, fetchPresets } from '../api/client'
import type { RunConfig, PresetInfo } from '../api/client'

export default function NewRun() {
  const navigate = useNavigate()
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [presets, setPresets] = useState<PresetInfo[]>([])
  const [selectedPreset, setSelectedPreset] = useState<string | null>(null)

  // Resource creation config
  const [resourceTypes, setResourceTypes] = useState('configmaps')
  const [count, setCount] = useState(100)
  const [workers, setWorkers] = useState(10)
  const [qps, setQps] = useState(50)
  const [burst, setBurst] = useState(100)
  const [spreadNamespaces, setSpreadNamespaces] = useState(5)
  const [dryRun, setDryRun] = useState(true)

  // Fetch presets on mount
  useEffect(() => {
    fetchPresets().then(setPresets).catch(() => {})
  }, [])

  const applyPreset = (preset: PresetInfo) => {
    setSelectedPreset(preset.name)
    setResourceTypes(preset.resourceTypes.join(','))
    setCount(preset.countPerType)
    setWorkers(preset.workers)
    setQps(preset.qps)
    setBurst(preset.burst)
    setSpreadNamespaces(preset.spreadNamespaces)
    setDryRun(false)
  }

  const clearPreset = () => {
    setSelectedPreset(null)
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSubmitting(true)
    setError(null)

    const config: RunConfig = {
      resourceTypes,
      count,
      workers,
      qps,
      burst,
      spreadNamespaces,
      dryRun,
    }
    if (selectedPreset) {
      config.preset = selectedPreset
    }

    try {
      const { id } = await startRun('resource-creation', config)
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
        {/* Preset picker */}
        {presets.length > 0 && (
              <div className="bg-white rounded-lg p-4 shadow-sm border">
                <label className="block text-sm font-medium text-gray-700 mb-2">Quick Preset</label>
                <div className="grid grid-cols-3 gap-3">
                  {presets.map((p) => (
                    <button
                      key={p.name}
                      type="button"
                      onClick={() => applyPreset(p)}
                      className={`relative px-4 py-3 rounded-lg text-sm font-medium border-2 transition-all ${
                        selectedPreset === p.name
                          ? 'bg-gray-900 text-white border-gray-900 shadow-md'
                          : 'bg-white text-gray-700 border-gray-200 hover:border-gray-400 hover:shadow-sm'
                      }`}
                    >
                      <div className="text-base font-bold uppercase tracking-wide">
                        {p.name === 'small' ? 'S' : p.name === 'medium' ? 'M' : 'L'}
                      </div>
                      <div className={`text-xs mt-0.5 ${selectedPreset === p.name ? 'text-gray-300' : 'text-gray-500'}`}>
                        {(p.countPerType / 1000)}k &times; {p.resourceTypes.length} types + {p.watchConnections} watches
                      </div>
                    </button>
                  ))}
                </div>
                {selectedPreset && (
                  <button type="button" onClick={clearPreset}
                    className="mt-2 text-xs text-gray-400 hover:text-gray-600 underline">
                    Clear preset
                  </button>
                )}
              </div>
            )}

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
