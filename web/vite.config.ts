import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../internal/webassets/dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'happy-dom',
  },
})
