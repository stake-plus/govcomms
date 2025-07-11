// src/frontend/src/auth/Login.tsx
import { useCallback, useState } from "react";
import { web3Enable, web3FromAddress, isWeb3Injected } from "@polkadot/extension-dapp";
import { initWalletConnect, signWithWalletConnect } from "../walletconnect";

export default function Login() {
  const [method, setMethod] = useState<"polkadotjs" | "walletconnect" | "airgap">("polkadotjs");
  const [addr, setAddr] = useState("");
  const [nonce, setNonce] = useState("");

  /** WalletConnect client – initialised lazily */
  const [wc, setWc] = useState<Awaited<ReturnType<typeof initWalletConnect>>>();

  /** ---------------------------------------------------------------- */
  const fetchNonce = useCallback(async () => {
    const r = await fetch("/v1/auth/challenge", {
      method: "POST",
      body: JSON.stringify({ address: addr, method }),
      headers: { "Content-Type": "application/json" },
    });
    const { nonce } = await r.json();
    setNonce(nonce);
  }, [addr, method]);

  /** ---------------------------------------------------------------- */
  const sign = useCallback(async () => {
    if (!nonce) throw new Error("Challenge nonce not fetched");

    if (method === "walletconnect") {
      if (!wc) setWc(await initWalletConnect(import.meta.env.VITE_WC_PROJECT!));
      const session = wc!.session.getAll()[0];
      const sig = await signWithWalletConnect(wc!, session, addr, nonce);
      return sig;
    }

    /* ---------- Polkadot {.js} extension ---------- */
    if (!isWeb3Injected) throw new Error("Polkadot extension not found");
    await web3Enable("GovComms");
    const injector = await web3FromAddress(addr);
    const signRaw = injector.signer?.signRaw;
    if (!signRaw) throw new Error("signRaw not supported by this account");

    const { signature } = await signRaw({
      address: addr,
      data: nonce,
      type: "bytes",
    });
    return signature;
  }, [addr, nonce, method, wc]);

  /** ---------------------------------------------------------------- */
  const verify = useCallback(async () => {
    const signature = await sign();
    const r = await fetch("/v1/auth/verify", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ address: addr, method, signature }),
    });
    if (!r.ok) throw new Error("Verification failed");
    const { token } = await r.json();
    localStorage.setItem("jwt", token);
  }, [addr, method, sign]);

  return (
    <section>
      {/*  UI omitted for brevity */}
      <button onClick={fetchNonce}>Get Challenge</button>
      <button onClick={verify} disabled={!nonce}>
        Sign & Login
      </button>
    </section>
  );
}

