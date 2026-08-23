import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const serverTarget = process.env.VITE_SERVER_TARGET ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': serverTarget,
    },
  },
  build: {
    outDir: 'dist',
  },
})
