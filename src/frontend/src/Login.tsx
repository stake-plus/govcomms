import React, { useCallback, useEffect, useState } from 'react';
import {
  web3Accounts,
  web3Enable,
  web3FromAddress,
} from '@polkadot/extension-dapp';
import { stringToHex } from '@polkadot/util';

type Props = { onAuthenticated(jwt: string): void };

export default function Login({ onAuthenticated }: Props) {
  const [accounts, setAccounts] = useState<Awaited<
    ReturnType<typeof web3Accounts>
  >>([]);
  const [selected, setSelected] = useState<string>('');
  const [nonce, setNonce] = useState<string>('');

  //----------------------------------------------------------------------
  // 1.  Load extension accounts once
  //----------------------------------------------------------------------
  useEffect(() => {
    (async () => {
      await web3Enable('GovComms');
      const acc = await web3Accounts();
      setAccounts(acc);
      if (acc.length) setSelected(acc[0].address);
    })();
  }, []);

  //----------------------------------------------------------------------
  // 2.  Ask API for a challenge (‘nonce’) whenever an account is picked
  //----------------------------------------------------------------------
  useEffect(() => {
    if (!selected) return;
    fetch('/v1/auth/challenge', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ address: selected, method: 'polkadotjs' }),
    })
      .then((r) => r.json())
      .then(({ nonce }) => setNonce(nonce))
      .catch(console.error);
  }, [selected]);

  //----------------------------------------------------------------------
  // 3.  Sign the nonce & verify with the API
  //----------------------------------------------------------------------
  const signIn = useCallback(async () => {
    if (!selected || !nonce) return;

    const injector = await web3FromAddress(selected);

    //------------------------------------------------------------------
    // `signRaw` *can* be missing when the user uses a “watch‑only”
    // account.  We guard against it both at run‑time and compile‑time.
    //------------------------------------------------------------------
    const signRaw = injector?.signer?.signRaw;
    if (!signRaw) {
      alert('The selected account cannot sign messages.');
      return;
    }

    const { signature } = await signRaw({
      address: selected,
      data: stringToHex(nonce), // must be hex‑encoded
      type: 'bytes',
    });

    const res = await fetch('/v1/auth/verify', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        address: selected,
        method: 'polkadotjs',
        signature,
      }),
    });

    if (!res.ok) {
      alert('Verification failed.');
      return;
    }

    const { token } = await res.json();
    onAuthenticated(token);
  }, [nonce, onAuthenticated, selected]);

  //----------------------------------------------------------------------
  // 4.  Render
  //----------------------------------------------------------------------
  return (
    <>
      <h2>Login</h2>

      {accounts.length === 0 && <p>No Polkadot extension detected.</p>}

      {accounts.length > 0 && (
        <>
          <select
            value={selected}
            onChange={(e) => setSelected(e.target.value)}
          >
            {accounts.map((a) => (
              <option key={a.address} value={a.address}>
                {a.meta.name ?? a.address}
              </option>
            ))}
          </select>

          <button disabled={!nonce} onClick={signIn}>
            Sign & Login
          </button>
        </>
      )}
    </>
  );
}
