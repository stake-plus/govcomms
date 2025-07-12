// src/frontend/src/config.ts
interface Config {
  apiUrl: string;
  gcUrl: string;
}

const isDevelopment = import.meta.env.MODE === 'development';

const config: Config = {
  apiUrl: import.meta.env.VITE_API_URL || (isDevelopment ? 'http://localhost:443/v1' : window.location.origin + '/v1'),
  gcUrl: import.meta.env.VITE_GC_URL || (isDevelopment ? 'http://localhost:3000' : window.location.origin)
};

export default config;