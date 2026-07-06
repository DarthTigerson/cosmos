import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      // SSE stream needs special handling to disable compression
      '/chat/stream': {
        target: 'http://localhost:8002',
        changeOrigin: true,
        selfHandleResponse: false,
        configure: (proxy) => {
          proxy.on('proxyReq', (proxyReq) => {
            proxyReq.setHeader('accept-encoding', 'identity')
          })
        },
      },
      // Everything else that isn't a static asset goes to the backend
      '^/(chat|sessions|podcasts|episodes|questions|health|memory|battery)': {
        target: 'http://localhost:8002',
        changeOrigin: true,
      },
    },
  },
})
