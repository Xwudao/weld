import { constants } from 'node:zlib'

import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { compression, defineAlgorithm } from 'vite-plugin-compression2'

// Build output is embedded by internal/web/assets.go. emptyOutDir is off so the
// tracked dist/.gitkeep placeholder survives a build. The compression plugin
// writes a precompressed ".gz" and ".br" sidecar next to every compressible
// asset, so the Go handler serves the sidecar (with Content-Encoding: gzip or
// br) instead of compressing on every request; all files are embedded in the
// binary. Brotli uses quality 9 (its level scale runs to 11) to stay off the
// plugin's high-memory path while still beating gzip.
export default defineConfig({
  // React 19 provides react/compiler-runtime; no separate runtime is needed.
  plugins: [
    react({ compiler: { target: '19', logDiagnostics: false } }),
    compression({
      include: /\.(html|xml|css|json|js|mjs|svg|yaml|yml)$/,
      algorithms: [
        defineAlgorithm('brotliCompress', {
          params: { [constants.BROTLI_PARAM_QUALITY]: 9 },
        }),
        defineAlgorithm('gzip', { level: 9 }),
      ],
    }),
  ],
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
