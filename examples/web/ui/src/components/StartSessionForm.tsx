import { useState } from 'react'
import { startSession } from '../api'

interface Props {
  remote: string | undefined
  onStarted: (sessionId: string) => void
  onCancel: () => void
}

const styles: Record<string, React.CSSProperties> = {
  form: {
    background: '#16213e',
    border: '1px solid #0f3460',
    borderRadius: '8px',
    padding: '20px',
    marginTop: '16px',
    maxWidth: '480px',
  },
  title: {
    fontSize: '16px',
    fontWeight: 600,
    marginBottom: '16px',
    color: '#e94560',
  },
  field: {
    marginBottom: '12px',
  },
  fieldLabel: {
    display: 'block',
    fontSize: '13px',
    color: '#a0a0b0',
    marginBottom: '4px',
  },
  input: {
    width: '100%',
    background: '#0f3460',
    border: '1px solid #1a4a8a',
    borderRadius: '4px',
    color: '#e0e0e0',
    padding: '6px 10px',
    fontSize: '14px',
    outline: 'none',
  },
  select: {
    width: '100%',
    background: '#0f3460',
    border: '1px solid #1a4a8a',
    borderRadius: '4px',
    color: '#e0e0e0',
    padding: '6px 10px',
    fontSize: '14px',
    outline: 'none',
  },
  actions: {
    display: 'flex',
    gap: '10px',
    marginTop: '16px',
  },
  btnPrimary: {
    background: '#e94560',
    border: 'none',
    borderRadius: '4px',
    color: '#fff',
    padding: '7px 18px',
    fontSize: '14px',
    cursor: 'pointer',
  },
  btnSecondary: {
    background: 'transparent',
    border: '1px solid #0f3460',
    borderRadius: '4px',
    color: '#a0a0b0',
    padding: '7px 18px',
    fontSize: '14px',
    cursor: 'pointer',
  },
  error: {
    color: '#e94560',
    fontSize: '13px',
    marginTop: '8px',
  },
}

export default function StartSessionForm({ remote, onStarted, onCancel }: Props) {
  const [repoPath, setRepoPath] = useState('')
  const [provider, setProvider] = useState('claude')
  const [project, setProject] = useState('local')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setLoading(true)
    try {
      const id = await startSession({ remote, project, provider, repoPath })
      onStarted(id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <form style={styles.form} onSubmit={handleSubmit}>
      <div style={styles.title}>Start New Session</div>

      <div style={styles.field}>
        <label style={styles.fieldLabel}>Repo Path</label>
        <input
          style={styles.input}
          type="text"
          placeholder="/path/to/project"
          value={repoPath}
          onChange={(e) => setRepoPath(e.target.value)}
          required
        />
      </div>

      <div style={styles.field}>
        <label style={styles.fieldLabel}>Provider</label>
        <select
          style={styles.select}
          value={provider}
          onChange={(e) => setProvider(e.target.value)}
        >
          <option value="claude">claude</option>
          <option value="codex">codex</option>
          <option value="gemini">gemini</option>
          <option value="opencode">opencode</option>
          <option value="echo">echo (test)</option>
        </select>
      </div>

      <div style={styles.field}>
        <label style={styles.fieldLabel}>Project</label>
        <input
          style={styles.input}
          type="text"
          placeholder="local"
          value={project}
          onChange={(e) => setProject(e.target.value)}
        />
      </div>

      {error && <div style={styles.error}>{error}</div>}

      <div style={styles.actions}>
        <button style={styles.btnPrimary} type="submit" disabled={loading}>
          {loading ? 'Starting…' : 'Start Session'}
        </button>
        <button style={styles.btnSecondary} type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  )
}
