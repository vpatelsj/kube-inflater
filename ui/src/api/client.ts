import type { ReportListItem, ReportType, AnyReport } from '../types/benchmark'

const API_BASE = '/api'

// --- Presets ---

export interface PresetInfo {
  name: string
  label: string
  resourceTypes: string[]
  countPerType: number
  workers: number
  qps: number
  burst: number
  spreadNamespaces: number
  watchConnections: number
  mutatorRate: number
}

export async function fetchPresets(): Promise<PresetInfo[]> {
  const res = await fetch(`${API_BASE}/presets`)
  if (!res.ok) throw new Error(`Failed to fetch presets: ${res.statusText}`)
  return res.json()
}

// --- Reports ---

export async function fetchReports(type?: ReportType): Promise<ReportListItem[]> {
  const params = type ? `?type=${type}` : ''
  const res = await fetch(`${API_BASE}/reports${params}`)
  if (!res.ok) throw new Error(`Failed to fetch reports: ${res.statusText}`)
  const data: ReportListItem[] | null = await res.json()
  return data ?? []
}

export async function fetchReport(id: string): Promise<AnyReport> {
  const res = await fetch(`${API_BASE}/reports/${id}`)
  if (!res.ok) throw new Error(`Failed to fetch report: ${res.statusText}`)
  return res.json()
}

// --- Run Management ---

export interface RunSummary {
  id: string
  type: string
  status: 'pending' | 'running' | 'completed' | 'failed'
  startedAt: string
  endedAt?: string
  reportID?: string
  error?: string
}

export interface RunDetail extends RunSummary {
  logLines?: string[]
  config: Record<string, unknown>
}

export interface RunConfig {
  preset?: string
  resourceTypes?: string
  count?: number
  workers?: number
  qps?: number
  burst?: number
  spreadNamespaces?: number
  dryRun?: boolean
  onlyCommon?: boolean
  noLimits?: boolean
  connections?: number
  duration?: number
  stagger?: number
  resourceType?: string
}

export async function fetchRuns(): Promise<RunSummary[]> {
  const res = await fetch(`${API_BASE}/runs`)
  if (!res.ok) throw new Error(`Failed to fetch runs: ${res.statusText}`)
  const data: RunSummary[] | null = await res.json()
  return data ?? []
}

export async function fetchRun(id: string): Promise<RunDetail> {
  const res = await fetch(`${API_BASE}/runs/${id}`)
  if (!res.ok) throw new Error(`Failed to fetch run: ${res.statusText}`)
  return res.json()
}

export async function startRun(type: string, config: RunConfig): Promise<{ id: string }> {
  const res = await fetch(`${API_BASE}/runs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ type, config }),
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || res.statusText)
  }
  return res.json()
}

export async function deleteAllRuns(): Promise<{ deleted: number }> {
  const res = await fetch(`${API_BASE}/runs`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`Failed to delete runs: ${res.statusText}`)
  return res.json()
}

export async function deleteRun(id: string): Promise<void> {
  const res = await fetch(`${API_BASE}/runs/${id}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`Failed to delete run: ${res.statusText}`)
}

export async function stopRun(id: string): Promise<void> {
  const res = await fetch(`${API_BASE}/runs/stop/${id}`, { method: 'POST' })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || res.statusText)
  }
}

export function subscribeToRun(
  runId: string,
  onLog: (line: string) => void,
  onStatus: (status: string, reportID: string) => void,
): EventSource {
  const es = new EventSource(`${API_BASE}/runs/events/${runId}`)
  es.onmessage = (e) => onLog(e.data)
  es.addEventListener('status', (e) => {
    const data = JSON.parse((e as MessageEvent).data) as { status: string; reportID: string }
    onStatus(data.status, data.reportID)
  })
  return es
}

// --- Live Cluster Monitoring ---

export interface ClusterSnapshot {
  timestamp: string
  elapsedSec: number
  totalPods: number
  runningPods: number
  pendingPods: number
  failedPods: number
  totalNodes: number
  readyNodes: number
  configmaps: number
  secrets: number
  services: number
  jobs: number
  namespaces: number
  serviceAccounts: number
  statefulsets: number
  customResources: number
  resourceCounts: Record<string, number>
  apiHealthMs: number
  // Cluster-wide totals (unfiltered)
  clusterPods: number
  clusterConfigmaps: number
  clusterSecrets: number
  clusterServices: number
  clusterJobs: number
  clusterNamespaces: number
  clusterServiceAccounts: number
  clusterStatefulsets: number
  clusterCustomResources: number
  // Watch connections (from apiserver metrics)
  watchConnections: number
}

export function subscribeToCluster(
  runId: string,
  onSnapshot: (snap: ClusterSnapshot) => void,
  onError: (err: string) => void,
): EventSource {
  const es = new EventSource(`${API_BASE}/cluster/live/${runId}`)
  es.addEventListener('snapshot', (e) => {
    const snap = JSON.parse((e as MessageEvent).data) as ClusterSnapshot
    onSnapshot(snap)
  })
  es.addEventListener('error', (e) => {
    if ((e as MessageEvent).data) {
      const data = JSON.parse((e as MessageEvent).data) as { error: string }
      onError(data.error)
    }
  })
  return es
}

export function subscribeToClusterOverview(
  onSnapshot: (snap: ClusterSnapshot) => void,
  onError: (err: string) => void,
): EventSource {
  const es = new EventSource(`${API_BASE}/cluster/overview`)
  es.addEventListener('snapshot', (e) => {
    const snap = JSON.parse((e as MessageEvent).data) as ClusterSnapshot
    onSnapshot(snap)
  })
  es.addEventListener('error', (e) => {
    if ((e as MessageEvent).data) {
      const data = JSON.parse((e as MessageEvent).data) as { error: string }
      onError(data.error)
    }
  })
  return es
}
