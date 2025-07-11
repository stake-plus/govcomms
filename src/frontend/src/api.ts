import { ApiPromise, WsProvider } from '@polkadot/api';

/**
 * Load a Polkadot‐JS API instance once, cache & reuse.
 */
export const getPolkadotApi = (() => {
  let api: ApiPromise | null = null;
  return async function (endpoint = 'wss://rpc.polkadot.io') {
    if (!api) {
      api = await ApiPromise.create({ provider: new WsProvider(endpoint) });
      await api.isReady;
    }
    return api;
  };
})();
