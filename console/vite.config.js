import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    host: '0.0.0.0',
    // Dev-only equivalent of nginx.conf's /_proxy/* rules, so `npm run dev`
    // can hit the same host-exposed container ports without nginx in front.
    proxy: {
      '/_proxy/auth': { target: 'http://localhost:8001', changeOrigin: true, rewrite: (p) => p.replace(/^\/_proxy\/auth/, '') },
      '/_proxy/management': { target: 'http://localhost:8003', changeOrigin: true, rewrite: (p) => p.replace(/^\/_proxy\/management/, '') },
    },
  },
})
