// src/frontend/src/walletconnect.ts
import SignClient from "@walletconnect/sign-client";
import type { SessionTypes } from "@walletconnect/types";

/**
 * Initialise a WalletConnect Sign v2 client.
 * Keep the returned instance in React state and re‑use it for every call.
 */
export async function initWalletConnect(projectId: string) {
  return SignClient.init({ projectId });
}

/**
 * Sign an arbitrary string with the account that is in the active WC session.
 */
export async function signWithWalletConnect(
  client: SignClient,
  session: SessionTypes.Struct,
  address: string,
  message: string
) {
  if (!client) throw new Error("WalletConnect client not initialised");

  const polkadotNs = session.namespaces?.polkadot;
  const chains = polkadotNs?.chains;

  if (!chains || chains.length === 0) {
    throw new Error("Polkadot namespace missing or has no chains in WalletConnect session");
  }

  const chainId = chains[0]; // e.g. "polkadot:91b171bb158e2d3848fa23a9f1c25182"

  const request = {
    topic: session.topic,
    chainId,
    request: {
      method: "polkadot_signMessage",
      params: {
        address,
        message, // already a hex string or UTF‑8 string
      },
    },
  } as const;

  const { signature } = await client.request<{ signature: string }>(request);
  return signature;
}
