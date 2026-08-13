import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

const proxyTarget = process.env.RUNNING_DEV_PROXY || 'http://127.0.0.1:8000'
const workspaceRoot = fileURLToPath(new URL('..', import.meta.url))

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      '@mail': fileURLToPath(new URL('../modules/mail/frontend', import.meta.url)),
      '@tools': fileURLToPath(new URL('../modules/tools/frontend', import.meta.url)),
      '@vue/test-utils': fileURLToPath(new URL('./node_modules/@vue/test-utils/dist/vue-test-utils.esm-bundler.mjs', import.meta.url)),
      'vitest': fileURLToPath(new URL('./node_modules/vitest/dist/index.js', import.meta.url)),
    },
    dedupe: ['vue', 'vue-router'],
  },
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    fs: {
      allow: [workspaceRoot],
    },
    proxy: {
      '/api': proxyTarget,
      '/v1': proxyTarget,
      '/health': proxyTarget,
    },
  },
  test: {
    include: ['../modules/**/*.spec.ts', 'src/**/*.spec.ts'],
    environment: 'jsdom',
  },
})
