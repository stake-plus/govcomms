// src/frontend/src/config.ts
interface Config {
  apiUrl: string;
  gcUrl: string;
}

const config: Config = {
  apiUrl: import.meta.env.VITE_API_URL || window.location.origin + '/v1',
  gcUrl: import.meta.env.VITE_GC_URL || window.location.origin
};

export default config;