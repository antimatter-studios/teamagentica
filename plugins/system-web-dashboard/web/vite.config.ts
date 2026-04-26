/// <reference types="vitest" />
import path from 'node:path'
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [tailwindcss(), react()],
  resolve: {
    preserveSymlinks: true,
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  optimizeDeps: {
    exclude: ['@teamagentica/api-client'],
  },
  server: {
    // Bind externally so the dev server is reachable from outside the container.
    host: '0.0.0.0',
    port: 3000,
    // The browser hits the dev server via the DDT reverse proxy at
    // ui.teamagentica.localhost:3000. Vite's default dev host check would
    // reject that Host header, so allow it (and plain localhost for direct
    // container hits during debugging).
    allowedHosts: ['ui.teamagentica.localhost', 'localhost'],
    hmr: {
      // The HMR websocket URL the browser builds must point at the external
      // port (3000 via DDT proxy), not Vite's internal default of 5173.
      clientPort: 3000,
    },
  },
  test: {
    environment: 'node',
    globals: true,
  },
})
