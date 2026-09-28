import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// In development, run `go run ./cmd/server` on :8080 and `npm run dev` here;
// API and WebSocket calls are proxied to the Go server.
export default defineConfig({
  plugins: [vue()],
  build: { outDir: 'dist', emptyOutDir: true, sourcemap: false },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/ws': { target: 'ws://localhost:8080', ws: true },
    },
  },
})
