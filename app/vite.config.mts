import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// base './' so the built page loads from a file:// URL inside Electron.
export default defineConfig({
  base: './',
  plugins: [react()],
  build: { outDir: 'dist', emptyOutDir: true },
})
