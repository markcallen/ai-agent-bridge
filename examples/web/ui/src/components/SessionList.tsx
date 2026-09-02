import { useState, useEffect, useCallback } from 'react'
import { listSessions, stopSession, type Session } from '../api'

interface Props {
  remote: string | undefined
  onAttach: (id: string) => void
  onWatch: (id: string) => void
  onNewSession: () => void
}

const styles: Record<string, React.CSSProperties> = {
  container: {
    background: '#16213e',
    border: '1px solid #0f3460',
    borderRadius: '8px',
    padding: '20px',
    flexShrink: 0,
  },
  header: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: '16px',
  },
  title: {
    fontSize: '18px',
    fontWeight: 600,
  },
  headerActions: {
    display: 'flex',
    gap: '8px',
  },
  btn: {
    background: '#0f3460',
    border: '1px solid #1a4a8a',
    borderRadius: '4px',
    color: '#e0e0e0',
    padding: '6px 14px',
    fontSize: '13px',
    cursor: 'pointer',
  },
  btnPrimary: {
    background: '#e94560',
    border: 'none',
    borderRadius: '4px',
    color: '#fff',
    padding: '6px 14px',
    fontSize: '13px',
    cursor: 'pointer',
  },
  btnSmall: {
    background: '#0f3460',
    border: '1px solid #1a4a8a',
    borderRadius: '3px',
    color: '#e0e0e0',
    padding: '3px 8px',
    fontSize: '12px',
    cursor: 'pointer',
    marginRight: '4px',
  },
  btnDanger: {
    background: 'transparent',
    border: '1px solid #e94560',
    borderRadius: '3px',
    color: '#e94560',
    padding: '3px 8px',
    fontSize: '12px',
    cursor: 'pointer',
  },
  table: {
    width: '100%',
    borderCollapse: 'collapse' as const,
    fontSize: '14px',
  },
  tableWrapper: {
    maxHeight: '115px',
    overflowY: 'auto' as const,
  },
  th: {
    textAlign: 'left' as const,
    padding: '8px 12px',
    borderBottom: '1px solid #0f3460',
    color: '#a0a0b0',
    fontWeight: 500,
    fontSize: '13px',
    position: 'sticky' as const,
    top: 0,
    background: '#16213e',
    zIndex: 1,
  },
  td: {
    padding: '8px 12px',
    borderBottom: '1px solid #0f3460',
    verticalAlign: 'middle' as const,
  },
  mono: {
    fontFamily: 'monospace',
    fontSize: '12px',
    color: '#a0d8ef',
  },
  statusRunning: {
    color: '#4caf50',
    fontSize: '13px',
  },
  statusStopped: {
    color: '#a0a0b0',
    fontSize: '13px',
  },
  statusOther: {
    color: '#e94560',
    fontSize: '13px',
  },
  empty: {
    color: '#a0a0b0',
    textAlign: 'center' as const,
    padding: '32px',
    fontSize: '14px',
  },
  error: {
    color: '#e94560',
    fontSize: '13px',
    padding: '8px',
  },
}

function statusStyle(status: string): React.CSSProperties {
  const s = status.toLowerCase()
  if (s.includes('running') || s.includes('attached')) return styles.statusRunning
  if (s.includes('stopped')) return styles.statusStopped
  return styles.statusOther
}

function shortID(id: string): string {
  return id.length > 12 ? id.slice(0, 8) + '…' : id
}

function formatDate(iso: string): string {
  if (!iso) return ''
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}

function shortDir(repoPath: string): string {
  if (!repoPath) return ''
  // Replace common home directory prefixes with ~/
  const homePatterns = [
    /^\/Users\/[^/]+\//,   // macOS: /Users/<user>/
    /^\/home\/[^/]+\//,    // Linux: /home/<user>/
    /^\/root\//,           // Linux root
  ]
  for (const pattern of homePatterns) {
    if (pattern.test(repoPath)) {
      return repoPath.replace(pattern, '~/')
    }
  }
  return repoPath
}

export default function SessionList({ remote, onAttach, onWatch, onNewSession }: Props) {
  const [sessions, setSessions] = useState<Session[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const list = await listSessions(remote)
      setSessions(list)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [remote])

  useEffect(() => {
    void refresh()
  }, [refresh])

  async function handleStop(id: string) {
    try {
      await stopSession(id, remote)
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div style={styles.container}>
      <div style={styles.header}>
        <span style={styles.title}>Sessions</span>
        <div style={styles.headerActions}>
          <button style={styles.btn} onClick={() => void refresh()} disabled={loading}>
            {loading ? 'Refreshing…' : 'Refresh'}
          </button>
          <button style={styles.btnPrimary} onClick={onNewSession}>
            + New Session
          </button>
        </div>
      </div>

      {error && <div style={styles.error}>{error}</div>}

      {sessions.length === 0 && !loading ? (
        <div style={styles.empty}>No sessions found. Start a new one above.</div>
      ) : (
        <div style={styles.tableWrapper}>
          <table style={styles.table}>
            <thead>
              <tr>
                <th style={styles.th}>Session ID</th>
                <th style={styles.th}>Project</th>
                <th style={styles.th}>Provider</th>
                <th style={styles.th}>Directory</th>
                <th style={styles.th}>Status</th>
                <th style={styles.th}>Created</th>
                <th style={styles.th}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sessions.map((s) => (
                <tr key={s.sessionId}>
                  <td style={styles.td}>
                    <span style={styles.mono} title={s.sessionId}>
                      {shortID(s.sessionId)}
                    </span>
                  </td>
                  <td style={styles.td}>{s.projectId}</td>
                  <td style={styles.td}>{s.provider}</td>
                  <td style={styles.td}>
                    <span style={styles.mono} title={s.repoPath}>
                      {shortDir(s.repoPath)}
                    </span>
                  </td>
                  <td style={styles.td}>
                    <span style={statusStyle(s.status)}>{s.status}</span>
                  </td>
                  <td style={styles.td}>{formatDate(s.createdAt)}</td>
                  <td style={styles.td}>
                    <button
                      style={{
                        ...styles.btnSmall,
                        ...(s.status === 'SESSION_STATUS_ATTACHED'
                          ? { opacity: 0.4, cursor: 'not-allowed' }
                          : {}),
                      }}
                      disabled={s.status === 'SESSION_STATUS_ATTACHED'}
                      onClick={() => onAttach(s.sessionId)}
                    >
                      Attach
                    </button>
                    <button style={styles.btnSmall} onClick={() => onWatch(s.sessionId)}>
                      Watch
                    </button>
                    <button style={styles.btnDanger} onClick={() => void handleStop(s.sessionId)}>
                      Stop
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
