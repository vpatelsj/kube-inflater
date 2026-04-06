// Types mirroring Go benchmarkio JSON structs

export type ReportType = 'pod-creation' | 'watch-stress' | 'api-latency'

export interface ReportListItem {
  id: string
  type: ReportType
  runID: string
  timestamp: string
  filename: string
}

// --- Pod Creation ---

export interface PodCreationReport {
  type: 'pod-creation'
  runID: string
  timestamp: string
  config: PodCreationConfig
  results: InflationResult[]
  clusterInfo?: ClusterInfo
  apiLatency?: LatencyMeasurement[]
}

export interface PodCreationConfig {
  resourceTypes: string[]
  countPerType: number
  workers: number
  qps: number
  burst: number
  batchInitial: number
  batchFactor: number
  maxBatches: number
  spreadNamespaces: number
  dataSizeBytes: number
  kwokNodes: number
}

export interface InflationResult {
  resourceType: string
  totalCreated: number
  totalFailed: number
  totalDurationMs: number
  throughput: number
  batches: BatchResult[]
}

export interface BatchResult {
  batchNum: number
  size: number
  created: number
  failed: number
  durationMs: number
  throughput: number
}

// --- Watch Stress ---

export interface WatchStressReport {
  type: 'watch-stress'
  runID: string
  timestamp: string
  config: WatchStressConfig
  watchMetrics: WatchMetrics
  mutatorMetrics?: MutatorMetrics
  scalingData?: ScalingDataPoint[]
}

export interface WatchStressConfig {
  connections: number
  durationSec: number
  staggerSec: number
  resourceTypes: string[]
  agentNodes?: number
  podsPerNode?: number
  namespace: string
  spreadCount: number
}

export interface WatchMetrics {
  totalEvents: number
  eventsPerSecond: number
  reconnects: number
  errors: number
  peakAliveWatches: number
  avgConnectLatencyMs: number
  maxConnectLatencyMs: number
  avgDeliveryLatencyMs: number
  maxDeliveryLatencyMs: number
  p99DeliveryLatencyMs: number
  minEventsPerConn: number
  maxEventsPerConn: number
  durationSec: number
}

export interface MutatorMetrics {
  creates: number
  updates: number
  deletes: number
  errors: number
  actualRate: number
  durationSec: number
}

export interface ScalingDataPoint {
  connections: number
  eventsPerSec: number
  avgConnectMs: number
  maxConnectMs: number
  errorRate: number
  peakAlive: number
}

// --- API Latency ---

export interface APILatencyReport {
  type: 'api-latency'
  runID: string
  timestamp: string
  clusterInfo: ClusterInfo
  measurements: LatencyMeasurement[]
}

export interface LatencyMeasurement {
  endpoint: string
  groupVersion?: string
  kind?: string
  resource?: string
  verb: string
  latencyMs: number
  success: boolean
  error?: string
  responseCode: number
  responseSizeBytes: number
}

// --- Shared ---

export interface ClusterInfo {
  nodeCount: number
  podCount: number
  namespaceCount: number
  serviceCount: number
  deploymentCount: number
  configMapCount: number
  secretCount: number
  persistentVolumeCount: number
  storageClassCount: number
  ingressCount: number
}

export type AnyReport = PodCreationReport | WatchStressReport | APILatencyReport
