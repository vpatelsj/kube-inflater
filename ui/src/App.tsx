import { Routes, Route, Link } from 'react-router-dom'
import Dashboard from './pages/Dashboard'
import PodCreationReport from './pages/PodCreationReport'
import WatchStressReport from './pages/WatchStressReport'
import APILatencyReport from './pages/APILatencyReport'
import NewRun from './pages/NewRun'
import RunMonitor from './pages/RunMonitor'

export default function App() {
  return (
    <div className="min-h-screen">
      <nav className="bg-gray-900 text-white px-6 py-3 flex items-center gap-6">
        <Link to="/" className="text-lg font-bold tracking-tight">📊 Benchmark UI</Link>
        <div className="flex gap-4 text-sm text-gray-300">
          <Link to="/" className="hover:text-white">Dashboard</Link>
          <Link to="/new-run" className="hover:text-white">New Run</Link>
        </div>
      </nav>
      <main className="max-w-7xl mx-auto px-4 py-6">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/new-run" element={<NewRun />} />
          <Route path="/run/:id" element={<RunMonitor />} />
          <Route path="/report/pod-creation/:id" element={<PodCreationReport />} />
          <Route path="/report/watch-stress/:id" element={<WatchStressReport />} />
          <Route path="/report/api-latency/:id" element={<APILatencyReport />} />
        </Routes>
      </main>
    </div>
  )
}
