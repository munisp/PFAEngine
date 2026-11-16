import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    // Dev proxy to the member-svc binary (default PORT=8096).
    proxy: { '/api': { target: 'http://localhost:8096', changeOrigin: true } },
  },
})
