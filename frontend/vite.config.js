import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
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
      '/chat': 'http://localhost:8002',
      '/sessions': 'http://localhost:8002',
      '/health': 'http://localhost:8002',
    },
  },
})
