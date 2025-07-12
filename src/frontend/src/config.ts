// src/frontend/src/config.ts
interface Config {
  apiUrl: string;
  gcUrl: string;
  walletConnectProjectId: string;
  walletConnectName: string;
  walletConnectDescription: string;
}

const isDevelopment = import.meta.env.MODE === 'development';

const config: Config = {
  apiUrl: import.meta.env.VITE_API_URL || (isDevelopment ? 'http://localhost:443/v1' : window.location.origin + '/v1'),
  gcUrl: import.meta.env.VITE_GC_URL || (isDevelopment ? 'http://localhost:3000' : window.location.origin),
  walletConnectProjectId: import.meta.env.VITE_WC_PROJECT_ID || '60579b65953a7b91dbe19366e383d8bb',
  walletConnectName: 'REEEEEEEEEE DAO - Opengov Communications Platform',
  walletConnectDescription: 'Connect with REEEEEEEEEE DAO to discuss your polkadot oepngov proposal.'
};

export default config;