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
      // Proxies the API mount in development; it mirrors httpserver.APIPrefix
      // (the built app is served by the Go server, so this only affects `vite dev`).
      '/api': 'http://localhost:8080',
    },
  },
})
