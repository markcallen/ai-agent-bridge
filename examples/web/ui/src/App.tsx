import { useState } from 'react'
import SessionList from './components/SessionList'
import Terminal from './components/Terminal'

type ConnectionMode = 'local' | 'remote'

interface ActiveSession {
  id: string
  role: 'observer' | 'writer'
}

const styles: Record<string, React.CSSProperties> = {
  app: {
    minHeight: '100vh',
    background: '#1a1a2e',
    color: '#e0e0e0',
    fontFamily: 'system-ui, -apple-system, sans-serif',
  },
  header: {
    background: '#16213e',
    borderBottom: '1px solid #0f3460',
    padding: '16px 24px',
    display: 'flex',
    alignItems: 'center',
    gap: '24px',
    flexWrap: 'wrap' as const,
  },
  title: {
    fontSize: '20px',
    fontWeight: 700,
    color: '#e94560',
    margin: 0,
  },
  connectionBar: {
    display: 'flex',
    alignItems: 'center',
    gap: '12px',
    flexWrap: 'wrap' as const,
  },
  label: {
    fontSize: '14px',
    color: '#a0a0b0',
  },
  radioGroup: {
    display: 'flex',
    gap: '12px',
  },
  radioLabel: {
    display: 'flex',
    alignItems: 'center',
    gap: '4px',
    cursor: 'pointer',
    fontSize: '14px',
  },
  input: {
    background: '#0f3460',
    border: '1px solid #e94560',
    borderRadius: '4px',
    color: '#e0e0e0',
    padding: '4px 10px',
    fontSize: '14px',
    outline: 'none',
    width: '220px',
  },
  main: {
    padding: '24px',
  },
  terminalPanel: {
    marginTop: '24px',
    border: '1px solid #0f3460',
    borderRadius: '8px',
    overflow: 'hidden',
  },
}

export default function App() {
  const [mode, setMode] = useState<ConnectionMode>('local')
  const [remoteHost, setRemoteHost] = useState('')
  const [activeSession, setActiveSession] = useState<ActiveSession | null>(null)

  const remote = mode === 'remote' && remoteHost ? remoteHost : undefined

  function handleAttach(id: string) {
    setActiveSession({ id, role: 'writer' })
  }

  function handleWatch(id: string) {
    setActiveSession({ id, role: 'observer' })
  }

  function handleCloseTerminal() {
    setActiveSession(null)
  }

  return (
    <div style={styles.app}>
      <header style={styles.header}>
        <h1 style={styles.title}>AI Agent Bridge</h1>
        <div style={styles.connectionBar}>
          <span style={styles.label}>Connect to:</span>
          <div style={styles.radioGroup}>
            <label style={styles.radioLabel}>
              <input
                type="radio"
                name="mode"
                value="local"
                checked={mode === 'local'}
                onChange={() => setMode('local')}
              />
              Local
            </label>
            <label style={styles.radioLabel}>
              <input
                type="radio"
                name="mode"
                value="remote"
                checked={mode === 'remote'}
                onChange={() => setMode('remote')}
              />
              Remote
            </label>
          </div>
          {mode === 'remote' && (
            <input
              style={styles.input}
              type="text"
              placeholder="hostname or host:port"
              value={remoteHost}
              onChange={(e) => setRemoteHost(e.target.value)}
            />
          )}
        </div>
      </header>

      <main style={styles.main}>
        <SessionList
          remote={remote}
          onAttach={handleAttach}
          onWatch={handleWatch}
        />

        {activeSession && (
          <div style={styles.terminalPanel}>
            <Terminal
              sessionId={activeSession.id}
              role={activeSession.role}
              remote={remote}
              onClose={handleCloseTerminal}
            />
          </div>
        )}
      </main>
    </div>
  )
}
