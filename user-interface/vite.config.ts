/// <reference types="vitest" />
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  resolve: {
    preserveSymlinks: true,
  },
  optimizeDeps: {
    exclude: ['@teamagentica/api-client'],
  },
  test: {
    environment: 'node',
    globals: true,
  },
})
