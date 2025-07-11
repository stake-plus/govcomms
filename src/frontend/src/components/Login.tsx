import React, { useState } from "react";
import {
  challenge,
  verify,
  setToken,
  clearToken
} from "../api";
import {
  web3Enable,
  web3Accounts,
  web3FromAddress
} from "@polkadot/extension-dapp";
import SignClient from "@walletconnect/sign-client";

type Props = {
  onAuthenticated: () => void;
};

const WC_PROJECT_ID = "CHANGE_ME"; // supply your WalletConnect project id

export default function Login({ onAuthenticated }: Props) {
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [nonce, setNonce] = useState("");
  const [airAddr, setAirAddr] = useState("");
  const [airWaiting, setAirWaiting] = useState(false);

  async function handlePolkadotExt() {
    try {
      setLoading(true);
      setErr(null);
      await web3Enable("GovComms");
      const accts = await web3Accounts();
      if (!accts.length) throw new Error("No extension accounts found");
      const addr = accts[0].address;

      const { nonce } = await challenge(addr, "polkadotjs");

      const injector = await web3FromAddress(addr);
      const sigRes = await injector.signer.signRaw({
        address: addr,
        data: `0x${Buffer.from(nonce, "utf8").toString("hex")}`,
        type: "bytes"
      });

      const { token } = await verify(
        addr,
        "polkadotjs",
        sigRes.signature
      );
      setToken(token);
      onAuthenticated();
    } catch (e) {
      setErr((e as Error).message);
      clearToken();
    } finally {
      setLoading(false);
    }
  }

  async function handleWalletConnect() {
    try {
      setLoading(true);
      setErr(null);

      const signClient = await SignClient.init({
        projectId: WC_PROJECT_ID
      });

      const { uri, approval } = await signClient.connect({
        requiredNamespaces: {
          polkadot: {
            methods: ["polkadot_signMessage"],
            chains: ["polkadot:91b171bb158e2d3848fa23a9f1c25182"],
            events: []
          }
        }
      });

      if (uri) {
        window.open(
          `https://walletconnect.com/qr?uri=${encodeURIComponent(uri)}`,
          "_blank"
        );
      }

      const session = await approval();
      const addr =
        session.namespaces.polkadot.accounts[0].split(":")[2];

      const { nonce } = await challenge(addr, "walletconnect");

      const [{ signature }] = await signClient.request<
        { signature: string }[]
      >({
        topic: session.topic,
        chainId: "polkadot:91b171bb158e2d3848fa23a9f1c25182",
        request: {
          method: "polkadot_signMessage",
          params: { address: addr, message: nonce }
        }
      });

      const { token } = await verify(
        addr,
        "walletconnect",
        signature
      );
      setToken(token);
      onAuthenticated();
    } catch (e) {
      setErr((e as Error).message);
      clearToken();
    } finally {
      setLoading(false);
    }
  }

  async function startAirGap() {
    try {
      setErr(null);
      const addr = prompt("Enter address for AirGap remark") || "";
      if (!addr) return;
      setAirAddr(addr);
      const { nonce } = await challenge(addr, "airgap");
      setNonce(nonce);
      setAirWaiting(true);
    } catch (e) {
      setErr((e as Error).message);
    }
  }

  async function verifyAirGap() {
    try {
      setLoading(true);
      const { token } = await verify(airAddr, "airgap");
      setToken(token);
      onAuthenticated();
    } catch (e) {
      setErr("Remark not yet confirmed on‑chain, try again.");
    } finally {
      setLoading(false);
    }
  }

  return (
    <section>
      <h2>Authenticate</h2>

      <div className="method">
        <button disabled={loading} onClick={handlePolkadotExt}>
          Polkadot extension
        </button>
      </div>

      <div className="method">
        <button disabled={loading} onClick={handleWalletConnect}>
          Wallet Connect
        </button>
      </div>

      <div className="method">
        <button disabled={loading} onClick={startAirGap}>
          AirGap remark
        </button>
      </div>

      {airWaiting && (
        <div style={{ marginTop: "0.75rem" }}>
          <p>
            Send a <code>system.remark</code> with the nonce below from{" "}
            {airAddr}, then click <em>Verify</em>.
          </p>
          <input readOnly value={nonce} style={{ width: "100%" }} />
          <button
            style={{ marginTop: "0.5rem" }}
            disabled={loading}
            onClick={verifyAirGap}
          >
            Verify
          </button>
        </div>
      )}

      {err && (
        <p style={{ color: "crimson", marginTop: "0.75rem" }}>{err}</p>
      )}
    </section>
  );
}
