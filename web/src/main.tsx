import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { installChunkLoadRecovery } from './lib/chunkLoadRecovery'

// Self-heal stale-bundle chunk loads after a deploy (see
// lib/chunkLoadRecovery.ts): a browser holding the previous index.html
// will reload once instead of landing on the error boundary.
installChunkLoadRecovery()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)

