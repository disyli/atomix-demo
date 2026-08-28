import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:51720',
        changeOrigin: true
      }
    }
  },
  build: {
    outDir: 'dist'
  }
})
