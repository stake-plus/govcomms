import React, { useState } from 'react';
import { web3Enable, web3Accounts, web3FromAddress } from '@polkadot/extension-dapp';
import { ApiPromise, WsProvider } from '@polkadot/api';
import SignClient from '@walletconnect/sign-client';
import { api } from '../services/api';

interface WalletConnectProps {
  onAuth: (token: string, address: string) => void;
}

export function WalletConnect({ onAuth }: WalletConnectProps) {
  const [loading, setLoading] = useState(false);
  const [method, setMethod] = useState<'polkadotjs' | 'walletconnect' | 'airgap'>('polkadotjs');
  const [nonce, setNonce] = useState<string>('');

  const connectPolkadotJs = async () => {
    setLoading(true);
    try {
      const extensions = await web3Enable('GovComms');
      if (extensions.length === 0) {
        alert('No Polkadot extension found. Please install Polkadot.js extension.');
        return;
      }

      const accounts = await web3Accounts();
      if (accounts.length === 0) {
        alert('No accounts found. Please create an account in Polkadot.js extension.');
        return;
      }

      const account = accounts[0];
      const { nonce } = await api.getChallenge(account.address, 'polkadotjs');
      
      const injector = await web3FromAddress(account.address);
      const signRaw = injector?.signer?.signRaw;
      
      if (!signRaw) {
        throw new Error('No signer available');
      }

      const { signature } = await signRaw({
        address: account.address,
        data: nonce,
        type: 'bytes'
      });

      const { token } = await api.verifySignature(account.address, 'polkadotjs', signature);
      onAuth(token, account.address);
    } catch (err) {
      console.error(err);
      alert('Authentication failed');
    } finally {
      setLoading(false);
    }
  };

  const connectWalletConnect = async () => {
    setLoading(true);
    try {
      const client = await SignClient.init({
        projectId: 'YOUR_WALLET_CONNECT_PROJECT_ID', // Replace with your project ID
        metadata: {
          name: 'GovComms',
          description: 'Polkadot Governance Communication',
          url: window.location.origin,
          icons: []
        }
      });

      // Implementation for WalletConnect would go here
      alert('WalletConnect integration coming soon');
    } catch (err) {
      console.error(err);
      alert('WalletConnect failed');
    } finally {
      setLoading(false);
    }
  };

  const connectAirgap = async () => {
    setLoading(true);
    try {
      const address = prompt('Enter your Polkadot/Kusama address:');
      if (!address) return;

      const { nonce } = await api.getChallenge(address, 'airgap');
      setNonce(nonce);

      // Show instructions for airgap
      alert(`Please submit a system.remark extrinsic with the following data:\n\n${nonce}\n\nOnce submitted, click "Verify" to complete authentication.`);
    } catch (err) {
      console.error(err);
      alert('Failed to get nonce');
    } finally {
      setLoading(false);
    }
  };

  const verifyAirgap = async () => {
    const address = prompt('Enter your address again:');
    if (!address) return;

    setLoading(true);
    try {
      const { token } = await api.verifySignature(address, 'airgap', '');
      onAuth(token, address);
    } catch (err) {
      console.error(err);
      alert('Verification failed. Make sure you submitted the remark on-chain.');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="wallet-connect">
      <h3>Connect Your Wallet</h3>
      
      <div className="method-selector">
        <button 
          className={method === 'polkadotjs' ? 'active' : ''}
          onClick={() => setMethod('polkadotjs')}
        >
          Polkadot.js Extension
        </button>
        <button 
          className={method === 'walletconnect' ? 'active' : ''}
          onClick={() => setMethod('walletconnect')}
        >
          WalletConnect
        </button>
        <button 
          className={method === 'airgap' ? 'active' : ''}
          onClick={() => setMethod('airgap')}
        >
          Air-gapped
        </button>
      </div>

      <div className="connect-content">
        {method === 'polkadotjs' && (
          <div>
            <p>Connect using Polkadot.js browser extension</p>
            <button onClick={connectPolkadotJs} disabled={loading}>
              {loading ? 'Connecting...' : 'Connect'}
            </button>
          </div>
        )}

        {method === 'walletconnect' && (
          <div>
            <p>Connect using WalletConnect compatible wallet</p>
            <button onClick={connectWalletConnect} disabled={loading}>
              {loading ? 'Connecting...' : 'Connect'}
            </button>
          </div>
        )}

        {method === 'airgap' && (
          <div>
            <p>For air-gapped wallets, submit a system.remark with the nonce</p>
            {!nonce ? (
              <button onClick={connectAirgap} disabled={loading}>
                {loading ? 'Getting nonce...' : 'Get Nonce'}
              </button>
            ) : (
              <div>
                <p className="nonce">Nonce: <code>{nonce}</code></p>
                <button onClick={verifyAirgap} disabled={loading}>
                  {loading ? 'Verifying...' : 'Verify'}
                </button>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}