import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  // `pnpm dev` talks to a Box Office started with `make boxoffice-run`.
  server: { proxy: { '/api': 'http://localhost:7272' } },
  build: { outDir: 'dist', emptyOutDir: false },
  test: { environment: 'jsdom' },
})
