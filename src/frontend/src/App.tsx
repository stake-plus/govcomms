import React, { useState, useEffect } from 'react'
import { useParams, useNavigate, Navigate } from 'react-router-dom'
import { BrowserRouter as Router, Routes, Route } from 'react-router-dom'
import './style.css'

const API_URL = 'http://localhost:8080/v1'

interface Message {
  ID: number
  ProposalID: number
  Author: string
  Body: string
  Internal: boolean
  CreatedAt: string
}

interface ProposalInfo {
  id: number
  network: string
  ref_id: number
  title: string
  submitter: string
  status: string
}

function HomePage() {
  const navigate = useNavigate()
  const [network, setNetwork] = useState('polkadot')
  const [refId, setRefId] = useState('')

  const handleNavigate = () => {
    if (refId && refId.trim()) {
      navigate(`/${network}/${refId}`)
    }
  }

  return (
    <div className="home-container">
      <div className="home-card">
        <h1>GovComms</h1>
        <p className="tagline">Secure communication platform for Polkadot governance</p>
        
        <div className="home-form">
          <div className="form-group">
            <label>Network</label>
            <select value={network} onChange={(e) => setNetwork(e.target.value)}>
              <option value="polkadot">Polkadot</option>
              <option value="kusama">Kusama</option>
            </select>
          </div>
          
          <div className="form-group">
            <label>Referendum ID</label>
            <input
              type="number"
              value={refId}
              onChange={(e) => setRefId(e.target.value)}
              placeholder="Enter referendum number"
              onKeyPress={(e) => e.key === 'Enter' && handleNavigate()}
            />
          </div>
          
          <button 
            className="btn-primary"
            onClick={handleNavigate}
            disabled={!refId || !refId.trim()}
          >
            Go to Referendum
          </button>
        </div>
      </div>
    </div>
  )
}

function ProposalPage() {
  const { network, id } = useParams<{ network: string; id: string }>()
  const navigate = useNavigate()
  const [token, setToken] = useState<string | null>(localStorage.getItem('govcomms_token'))
  const [address, setAddress] = useState<string>('')
  const [authMethod, setAuthMethod] = useState<'walletconnect' | 'polkadotjs' | 'airgap' | null>(null)
  const [nonce, setNonce] = useState<string>('')
  const [messages, setMessages] = useState<Message[]>([])
  const [proposal, setProposal] = useState<ProposalInfo | null>(null)
  const [messageBody, setMessageBody] = useState<string>('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>('')
  const [wcUri, setWcUri] = useState<string>('')

  useEffect(() => {
    if (!network || !id || !['polkadot', 'kusama'].includes(network.toLowerCase())) {
      navigate('/')
      return
    }
    
    if (token) {
      fetchMessages()
    }
  }, [token, network, id, navigate])

  const fetchMessages = async () => {
    try {
      const response = await fetch(`${API_URL}/messages/${network}/${id}`, {
        headers: { 'Authorization': `Bearer ${token}` }
      })
      if (response.ok) {
        const data = await response.json()
        setProposal(data.proposal)
        setMessages(data.messages || [])
      }
    } catch (err) {
      setError('Failed to load messages')
    }
  }

  const connectPolkadotJS = async () => {
    setLoading(true)
    setError('')
    try {
      const { web3Enable, web3Accounts } = await import('@polkadot/extension-dapp')
      const extensions = await web3Enable('GovComms')
      if (extensions.length === 0) {
        throw new Error('No Polkadot.js extension found. Please install it from polkadot.js.org/extension')
      }

      const accounts = await web3Accounts()
      if (accounts.length === 0) {
        throw new Error('No accounts found in Polkadot.js extension')
      }

      const selectedAccount = accounts[0]
      setAddress(selectedAccount.address)

      const response = await fetch(`${API_URL}/auth/challenge`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ address: selectedAccount.address, method: 'polkadotjs' })
      })
      const data = await response.json()

      const { web3FromAddress } = await import('@polkadot/extension-dapp')
      const injector = await web3FromAddress(selectedAccount.address)
      const signRaw = injector?.signer?.signRaw
      if (!signRaw) throw new Error('Unable to sign message')

      const { signature } = await signRaw({
        address: selectedAccount.address,
        data: data.nonce,
        type: 'bytes'
      })

      await verifySignature(selectedAccount.address, 'polkadotjs', signature)
    } catch (err: any) {
      setError(err.message || 'Failed to connect with Polkadot.js')
    } finally {
      setLoading(false)
    }
  }

  const connectWalletConnect = async () => {
    setLoading(true)
    setError('')
    try {
      const SignClient = (await import('@walletconnect/sign-client')).default
      const client = await SignClient.init({
        projectId: 'YOUR_PROJECT_ID', // Replace with actual project ID
        metadata: {
          name: 'GovComms',
          description: 'Governance Communication Platform',
          url: window.location.origin,
          icons: []
        }
      })

      const { uri, approval } = await client.connect({
        requiredNamespaces: {
          polkadot: {
            methods: ['polkadot_signMessage'],
            chains: ['polkadot:91b171bb158e2d3848fa23a9f1c25182'],
            events: []
          }
        }
      })

      if (uri) {
        setWcUri(uri)
      }

      const session = await approval()
      const account = session.namespaces.polkadot.accounts[0]
      const address = account.split(':')[2]
      setAddress(address)

      // Continue with challenge/verify flow
      const response = await fetch(`${API_URL}/auth/challenge`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ address, method: 'walletconnect' })
      })
      const data = await response.json()

      const result = await client.request({
        topic: session.topic,
        chainId: 'polkadot:91b171bb158e2d3848fa23a9f1c25182',
        request: {
          method: 'polkadot_signMessage',
          params: {
            address,
            message: data.nonce
          }
        }
      })

      // Type guard to ensure result is a string
      const signature = typeof result === 'string' ? result : String(result)
      await verifySignature(address, 'walletconnect', signature)
    } catch (err: any) {
      setError(err.message || 'Failed to connect with WalletConnect')
    } finally {
      setLoading(false)
    }
  }

  const connectAirgap = async () => {
    if (!address || !address.trim()) {
      setError('Please enter your address')
      return
    }

    setLoading(true)
    setError('')
    try {
      const response = await fetch(`${API_URL}/auth/challenge`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ address, method: 'airgap' })
      })
      const data = await response.json()
      setNonce(data.nonce)
    } catch (err: any) {
      setError(err.message || 'Failed to generate nonce')
    } finally {
      setLoading(false)
    }
  }

  const verifySignature = async (addr: string, method: string, signature: string = '') => {
    setLoading(true)
    try {
      const response = await fetch(`${API_URL}/auth/verify`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ address: addr, method, signature })
      })
      const data = await response.json()
      if (data.token) {
        localStorage.setItem('govcomms_token', data.token)
        setToken(data.token)
        setAuthMethod(null)
        setWcUri('')
        setNonce('')
      }
    } catch (err) {
      setError('Verification failed')
    } finally {
      setLoading(false)
    }
  }

  const sendMessage = async () => {
    if (!messageBody.trim()) return
    
    setLoading(true)
    try {
      const response = await fetch(`${API_URL}/messages`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${token}`
        },
        body: JSON.stringify({
          proposalRef: `${network}/${id}`,
          body: messageBody,
          emails: []
        })
      })
      
      if (response.ok) {
        setMessageBody('')
        fetchMessages()
      } else {
        setError('Failed to send message')
      }
    } catch (err) {
      setError('Failed to send message')
    } finally {
      setLoading(false)
    }
  }

  const logout = () => {
    localStorage.removeItem('govcomms_token')
    setToken(null)
    setAddress('')
    setMessages([])
  }

  if (!token) {
    return (
      <div className="auth-container">
        <div className="auth-card">
          <div className="auth-header">
            <h1>GovComms</h1>
            <span className="ref-badge">{network?.toUpperCase()} / #{id}</span>
          </div>

          {!authMethod && (
            <div className="auth-methods">
              <h2>Choose Authentication Method</h2>
              <div className="method-grid">
                <button 
                  className="method-button"
                  onClick={() => setAuthMethod('polkadotjs')}
                >
                  <div className="method-icon">🔐</div>
                  <h3>Polkadot.js</h3>
                  <p>Use browser extension</p>
                </button>
                
                <button 
                  className="method-button"
                  onClick={() => setAuthMethod('walletconnect')}
                >
                  <div className="method-icon">📱</div>
                  <h3>WalletConnect</h3>
                  <p>Connect mobile wallet</p>
                </button>
                
                <button 
                  className="method-button"
                  onClick={() => setAuthMethod('airgap')}
                >
                  <div className="method-icon">🔒</div>
                  <h3>Airgap</h3>
                  <p>Submit system remark</p>
                </button>
              </div>
            </div>
          )}

          {authMethod === 'polkadotjs' && (
            <div className="auth-content">
              <h2>Connect with Polkadot.js</h2>
              <p>Make sure you have the Polkadot.js extension installed and your account is unlocked.</p>
              <button 
                className="btn-primary"
                onClick={connectPolkadotJS}
                disabled={loading}
              >
                {loading ? 'Connecting...' : 'Connect Extension'}
              </button>
              <button 
                className="btn-secondary"
                onClick={() => setAuthMethod(null)}
              >
                Back
              </button>
            </div>
          )}

          {authMethod === 'walletconnect' && (
            <div className="auth-content">
              <h2>Connect with WalletConnect</h2>
              {wcUri ? (
                <div className="qr-container">
                  <p>Scan this QR code with your wallet</p>
                  <div className="qr-placeholder">
                    <pre>{wcUri}</pre>
                  </div>
                </div>
              ) : (
                <button 
                  className="btn-primary"
                  onClick={connectWalletConnect}
                  disabled={loading}
                >
                  {loading ? 'Generating QR...' : 'Generate QR Code'}
                </button>
              )}
              <button 
                className="btn-secondary"
                onClick={() => {
                  setAuthMethod(null)
                  setWcUri('')
                }}
              >
                Back
              </button>
            </div>
          )}

          {authMethod === 'airgap' && (
            <div className="auth-content">
              <h2>Airgap Authentication</h2>
              {!nonce ? (
                <>
                  <p>Enter your Polkadot/Kusama address:</p>
                  <input
                    type="text"
                    className="address-input"
                    placeholder="5GrwvaEF5zXb26Fz9rcQpDWS57CtERHpNehXCPcNoHGKutQY"
                    value={address}
                    onChange={(e) => setAddress(e.target.value)}
                  />
                  <button 
                    className="btn-primary"
                    onClick={connectAirgap}
                    disabled={loading || !address}
                  >
                    {loading ? 'Generating...' : 'Generate Nonce'}
                  </button>
                </>
              ) : (
                <>
                  <div className="nonce-display">
                    <p>Submit this as a system remark from address:</p>
                    <code className="address-code">{address}</code>
                    <p>Remark content:</p>
                    <code className="nonce-code">{nonce}</code>
                  </div>
                  <button 
                    className="btn-primary"
                    onClick={() => verifySignature(address, 'airgap')}
                    disabled={loading}
                  >
                    {loading ? 'Verifying...' : "I've submitted the remark"}
                  </button>
                </>
              )}
              <button 
                className="btn-secondary"
                onClick={() => {
                  setAuthMethod(null)
                  setNonce('')
                }}
              >
                Back
              </button>
            </div>
          )}

          {error && <div className="error-message">{error}</div>}
        </div>
      </div>
    )
  }

  return (
    <div className="proposal-container">
      <header className="proposal-header">
        <div className="header-left">
          <h1>GovComms</h1>
          <span className="ref-badge">{network?.toUpperCase()} / #{id}</span>
        </div>
        <div className="header-right">
          <span className="user-address">{address}</span>
          <button className="btn-logout" onClick={logout}>Logout</button>
        </div>
      </header>
      
      <div className="proposal-content">
        {proposal && (
          <div className="proposal-info">
            <h2>Referendum #{proposal.ref_id}</h2>
            {proposal.title && <h3>{proposal.title}</h3>}
            <div className="proposal-meta">
              <span>Status: {proposal.status}</span>
              <span>Submitted by: {proposal.submitter}</span>
            </div>
          </div>
        )}
        
        <div className="messages-section">
          <h3>Discussion</h3>
          <div className="messages-list">
            {messages.length === 0 ? (
              <div className="no-messages">No messages yet. Be the first to contribute!</div>
            ) : (
              messages.map((msg) => (
                <div key={msg.ID} className={`message ${msg.Internal ? 'internal' : ''}`}>
                  <div className="message-header">
                    <span className="message-author">{msg.Author}</span>
                    <span className="message-time">{new Date(msg.CreatedAt).toLocaleString()}</span>
                  </div>
                  <div className="message-body">{msg.Body}</div>
                </div>
              ))
            )}
          </div>
        </div>

        <div className="message-input-section">
          <textarea
            className="message-textarea"
            value={messageBody}
            onChange={(e) => setMessageBody(e.target.value)}
            placeholder="Share your thoughts on this proposal..."
            rows={4}
          />
          <button 
            className="btn-send"
            onClick={sendMessage} 
            disabled={loading || !messageBody.trim()}
          >
            {loading ? 'Sending...' : 'Send Message'}
          </button>
        </div>

        {error && <div className="error-message">{error}</div>}
      </div>
    </div>
  )
}

function App() {
  return (
    <Router>
      <Routes>
        <Route path="/" element={<HomePage />} />
        <Route path="/:network/:id" element={<ProposalPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Router>
  )
}

export default App