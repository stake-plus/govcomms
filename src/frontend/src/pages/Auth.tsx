import React, { useState, useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { web3Enable, web3Accounts } from '@polkadot/extension-dapp';
import { Keyring } from '@polkadot/api';
import WalletConnect from '@walletconnect/sign-client';
import { api, ApiError } from '../utils/api';
import { saveAuth } from '../utils/auth';
import { AuthMethod } from '../types';

function Auth() {
  const { network, refId } = useParams<{ network: string; refId: string }>();
  const navigate = useNavigate();
  const [selectedMethod, setSelectedMethod] = useState<AuthMethod | null>(null);
  const [address, setAddress] = useState('');
  const [nonce, setNonce] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleMethodSelect = (method: AuthMethod) => {
    setSelectedMethod(method);
    setError('');
  };

  const handleAddressSubmit = async () => {
    if (!address || !selectedMethod) return;
    
    setLoading(true);
    setError('');
    
    try {
      const { nonce } = await api.challenge(address, selectedMethod);
      setNonce(nonce);
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
      } else {
        setError('Failed to get challenge');
      }
    } finally {
      setLoading(false);
    }
  };

  const handleAuthError = (err: unknown) => {
    if (err instanceof ApiError) {
      if (err.status === 401) {
        return (
          <div>
            <p>Your address is not authorized for this referendum.</p>
            <p>Only addresses that are authorized participants (proposer, council members, or those who have voted) can send messages.</p>
            <p>Network: {network?.toUpperCase()} Referendum #{refId}</p>
          </div>
        );
      }
      return err.message;
    }
    return err instanceof Error ? err.message : 'Authentication failed';
  };

  const handlePolkadotJsAuth = async () => {
    try {
      setLoading(true);
      setError('');
      const extensions = await web3Enable('GovComms');
      
      if (extensions.length === 0) {
        setError('No Polkadot extension found');
        return;
      }
      
      const accounts = await web3Accounts();
      if (accounts.length === 0) {
        setError('No accounts found');
        return;
      }
      
      const account = accounts[0];
      setAddress(account.address);
      
      const { nonce } = await api.challenge(account.address, 'polkadotjs');
      
      const keyring = new Keyring({ type: 'sr25519' });
      const pair = keyring.addFromAddress(account.address);
      
      const { web3FromAddress } = await import('@polkadot/extension-dapp');
      const injector = await web3FromAddress(account.address);
      
      if (!injector.signer.signRaw) {
        throw new Error('Signer not available');
      }
      
      const { signature } = await injector.signer.signRaw({
        address: account.address,
        data: nonce,
        type: 'bytes'
      });
      
      const { token } = await api.verify(account.address, 'polkadotjs', signature);
      
      saveAuth({ token, address: account.address });
      navigate(`/${network}/${refId}`);
    } catch (err) {
      const errorMessage = handleAuthError(err);
      setError(typeof errorMessage === 'string' ? errorMessage : '');
      if (typeof errorMessage !== 'string') {
        // If it's a JSX element (authorization error), we'll display it in the error section
        setError('AUTHORIZATION_ERROR');
      }
    } finally {
      setLoading(false);
    }
  };

  const handleWalletConnectAuth = async () => {
    try {
      setLoading(true);
      setError('');
      
      const client = await WalletConnect.init({
        projectId: import.meta.env.VITE_WC_PROJECT_ID || 'YOUR_PROJECT_ID',
        metadata: {
          name: 'GovComms',
          description: 'Connect with ChaosDAO',
          url: window.location.origin,
          icons: []
        }
      });
      
      const { uri, approval } = await client.connect({
        requiredNamespaces: {
          polkadot: {
            methods: ['polkadot_signMessage'],
            chains: ['polkadot:91b171bb158e2d3848fa23a9f1c25182'],
            events: []
          }
        }
      });
      
      if (uri) {
        window.open(`https://wallet.walletconnect.com/wc?uri=${encodeURIComponent(uri)}`, '_blank');
      }
      
      const session = await approval();
      const account = session.namespaces.polkadot.accounts[0];
      const address = account.split(':')[2];
      
      setAddress(address);
      
      const { nonce } = await api.challenge(address, 'walletconnect');
      
      const result = await client.request({
        topic: session.topic,
        chainId: 'polkadot:91b171bb158e2d3848fa23a9f1c25182',
        request: {
          method: 'polkadot_signMessage',
          params: {
            address,
            message: nonce
          }
        }
      });
      
      const signatureResult = result as { signature: string };
      const { token } = await api.verify(address, 'walletconnect', signatureResult.signature);
      
      saveAuth({ token, address });
      navigate(`/${network}/${refId}`);
    } catch (err) {
      const errorMessage = handleAuthError(err);
      setError(typeof errorMessage === 'string' ? errorMessage : '');
      if (typeof errorMessage !== 'string') {
        setError('AUTHORIZATION_ERROR');
      }
    } finally {
      setLoading(false);
    }
  };

  const checkAirgapStatus = async () => {
    if (!address) return;
    
    try {
      const { token } = await api.verify(address, 'airgap');
      saveAuth({ token, address });
      navigate(`/${network}/${refId}`);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setError('AUTHORIZATION_ERROR');
        return;
      }
      // Continue polling if it's not an authorization error
      setTimeout(checkAirgapStatus, 2000);
    }
  };

  useEffect(() => {
    if (selectedMethod === 'airgap' && nonce) {
      checkAirgapStatus();
    }
  }, [nonce]);

  return (
    <div className="auth-container">
      <div className="auth-card">
        <div className="auth-header">
          <h1>Authenticate</h1>
          <span className="ref-badge">{network?.toUpperCase()} #{refId}</span>
        </div>

        {!selectedMethod && (
          <div className="auth-methods">
            <h2>Choose Authentication Method</h2>
            <div className="method-grid">
              <button
                className="method-button"
                onClick={() => handleMethodSelect('walletconnect')}
              >
                <div className="method-icon">🔗</div>
                <h3>WalletConnect</h3>
                <p>Connect with mobile wallet</p>
              </button>
              
              <button
                className="method-button"
                onClick={() => handleMethodSelect('polkadotjs')}
              >
                <div className="method-icon">🔐</div>
                <h3>Polkadot.js</h3>
                <p>Browser extension</p>
              </button>
              
              <button
                className="method-button"
                onClick={() => handleMethodSelect('airgap')}
              >
                <div className="method-icon">📱</div>
                <h3>Air-gapped</h3>
                <p>Sign with offline device</p>
              </button>
            </div>
          </div>
        )}

        {selectedMethod === 'polkadotjs' && (
          <div className="auth-content">
            <h2>Connect with Polkadot.js</h2>
            <p>Click below to connect your Polkadot.js extension</p>
            <button 
              className="btn-primary" 
              onClick={handlePolkadotJsAuth}
              disabled={loading}
            >
              {loading ? 'Connecting...' : 'Connect Extension'}
            </button>
            <button 
              className="btn-secondary" 
              onClick={() => setSelectedMethod(null)}
            >
              Back
            </button>
          </div>
        )}

        {selectedMethod === 'walletconnect' && (
          <div className="auth-content">
            <h2>Connect with WalletConnect</h2>
            <p>Click below to connect your mobile wallet</p>
            <button 
              className="btn-primary" 
              onClick={handleWalletConnectAuth}
              disabled={loading}
            >
              {loading ? 'Connecting...' : 'Connect Wallet'}
            </button>
            <button 
              className="btn-secondary" 
              onClick={() => setSelectedMethod(null)}
            >
              Back
            </button>
          </div>
        )}

        {selectedMethod === 'airgap' && !nonce && (
          <div className="auth-content">
            <h2>Air-gapped Authentication</h2>
            <p>Enter your Polkadot address to continue</p>
            <input
              type="text"
              className="address-input"
              placeholder="Enter your Polkadot address"
              value={address}
              onChange={(e) => setAddress(e.target.value)}
            />
            <button 
              className="btn-primary" 
              onClick={handleAddressSubmit}
              disabled={!address || loading}
            >
              {loading ? 'Loading...' : 'Continue'}
            </button>
            <button 
              className="btn-secondary" 
              onClick={() => setSelectedMethod(null)}
            >
              Back
            </button>
          </div>
        )}

        {selectedMethod === 'airgap' && nonce && (
          <div className="auth-content">
            <h2>Submit Remark On-chain</h2>
            <div className="nonce-display">
              <p>Your address:</p>
              <code className="address-code">{address}</code>
              <p>Submit a system.remark with this exact text:</p>
              <code className="nonce-code">{nonce}</code>
            </div>
            <p>Waiting for on-chain confirmation...</p>
            <button 
              className="btn-secondary" 
              onClick={() => {
                setSelectedMethod(null);
                setNonce('');
                setAddress('');
              }}
            >
              Cancel
            </button>
          </div>
        )}

        {error && (
          <div className="error-message">
            {error === 'AUTHORIZATION_ERROR' ? (
              <>
                <p><strong>Authorization Failed</strong></p>
                <p>Your address is not authorized for this referendum.</p>
                <p>Only addresses that are authorized participants (proposer, council members, or those who have voted) can send messages.</p>
                <p>Network: {network?.toUpperCase()} Referendum #{refId}</p>
              </>
            ) : (
              error
            )}
          </div>
        )}
      </div>
    </div>
  );
}

export default Auth;