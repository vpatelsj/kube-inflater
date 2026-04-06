import { useState, useEffect } from 'react'
import type { ReportListItem, ReportType, AnyReport } from '../types/benchmark'
import { fetchReports, fetchReport } from '../api/client'

export function useReports(type?: ReportType) {
  const [reports, setReports] = useState<ReportListItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setLoading(true)
    fetchReports(type)
      .then(setReports)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }, [type])

  return { reports, loading, error }
}

export function useReport(id: string | undefined) {
  const [report, setReport] = useState<AnyReport | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!id) return
    setLoading(true)
    fetchReport(id)
      .then(setReport)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }, [id])

  return { report, loading, error }
}
