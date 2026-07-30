import { Routes, Route } from 'react-router-dom'

function App() {
  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-100">
      <Routes>
        <Route path="*" element={<div className="flex items-center justify-center h-screen text-zinc-500">InstaEdit</div>} />
      </Routes>
    </div>
  )
}

export default App
