import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    proxy: {
      '/v1': {
        target: 'https://localhost:443',
        changeOrigin: true,
      }
    }
  }
});