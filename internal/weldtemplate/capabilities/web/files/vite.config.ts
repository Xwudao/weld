import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Build output is embedded by internal/web/assets.go. emptyOutDir is off so the
// tracked dist/.gitkeep placeholder survives a build.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: false,
  },
  server: {
    proxy: {
      // /api is reserved for the api capability (weld add api).
      '/api': 'http://localhost:8080',
    },
  },
})
