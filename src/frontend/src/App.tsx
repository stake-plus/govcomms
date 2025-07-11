import React, { useState, useEffect } from 'react'
import { useParams } from 'react-router-dom'
import { BrowserRouter as Router, Routes, Route } from 'react-router-dom'
import './App.css'

const API_URL = 'http://localhost:443/v1'

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

function ProposalPage() {
  const { network, id } = useParams<{ network: string; id: string }>()
  const [token, setToken] = useState<string | null>(localStorage.getItem('govcomms_token'))
  const [address, setAddress] = useState<string>('')
  const [authMethod, setAuthMethod] = useState<'walletconnect' | 'polkadotjs' | 'airgap'>('polkadotjs')
  const [nonce, setNonce] = useState<string>('')
  const [messages, setMessages] = useState<Message[]>([])
  const [proposal, setProposal] = useState<ProposalInfo | null>(null)
  const [messageBody, setMessageBody] = useState<string>('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>('')

  useEffect(() => {
    if (token) {
      fetchMessages()
    }
  }, [token, network, id])

  const fetchMessages = async () => {
    try {
      const response = await fetch(`${API_URL}/messages/${network}/${id}`, {
        headers: { 'Authorization': `Bearer ${token}` }
      })
      if (response.ok) {
        const data = await response.json()
        setProposal(data.proposal)
        setMessages(data.messages)
      }
    } catch (err) {
      setError('Failed to load messages')
    }
  }

  const handleChallenge = async () => {
    setLoading(true)
    setError('')
    try {
      const response = await fetch(`${API_URL}/auth/challenge`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ address, method: authMethod })
      })
      const data = await response.json()
      setNonce(data.nonce)
      
      if (authMethod === 'polkadotjs') {
        const { web3Enable, web3Accounts, web3FromAddress } = await import('@polkadot/extension-dapp')
        await web3Enable('GovComms')
        const accounts = await web3Accounts()
        if (accounts.length === 0) throw new Error('No accounts found')
        
        const account = accounts.find(a => a.address === address) || accounts[0]
        setAddress(account.address)
        
        const injector = await web3FromAddress(account.address)
        const signRaw = injector?.signer?.signRaw
        if (!signRaw) throw new Error('No signer found')
        
        const { signature } = await signRaw({
          address: account.address,
          data: data.nonce,
          type: 'bytes'
        })
        
        handleVerify(signature)
      }
    } catch (err: any) {
      setError(err.message || 'Authentication failed')
    } finally {
      setLoading(false)
    }
  }

  const handleVerify = async (signature: string = '') => {
    setLoading(true)
    try {
      const response = await fetch(`${API_URL}/auth/verify`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ address, method: authMethod, signature })
      })
      const data = await response.json()
      if (data.token) {
        localStorage.setItem('govcomms_token', data.token)
        setToken(data.token)
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
      <div className="container">
        <header>
          <h1>GovComms</h1>
          <span id="ref">{network}/{id}</span>
        </header>
        <section>
          <h2>Authenticate to Continue</h2>
          {error && <div className="error">{error}</div>}
          
          <div className="method">
            <label>
              <input
                type="radio"
                value="polkadotjs"
                checked={authMethod === 'polkadotjs'}
                onChange={(e) => setAuthMethod(e.target.value as any)}
              />
              Polkadot.js Extension
            </label>
            <label>
              <input
                type="radio"
                value="walletconnect"
                checked={authMethod === 'walletconnect'}
                onChange={(e) => setAuthMethod(e.target.value as any)}
              />
              WalletConnect
            </label>
            <label>
              <input
                type="radio"
                value="airgap"
                checked={authMethod === 'airgap'}
                onChange={(e) => setAuthMethod(e.target.value as any)}
              />
              Airgap (System Remark)
            </label>
          </div>

          <input
            type="text"
            placeholder="Enter your address"
            value={address}
            onChange={(e) => setAddress(e.target.value)}
          />

          <button onClick={handleChallenge} disabled={loading || !address}>
            {loading ? 'Authenticating...' : 'Authenticate'}
          </button>

          {nonce && authMethod === 'airgap' && (
            <div className="nonce">
              <p>Submit this nonce as a system remark:</p>
              <code>{nonce}</code>
              <button onClick={() => handleVerify()} disabled={loading}>
                I've submitted the remark
              </button>
            </div>
          )}
        </section>
      </div>
    )
  }

  return (
    <div className="container">
      <header>
        <h1>GovComms</h1>
        <span id="ref">{network}/{id}</span>
        <button onClick={logout}>Logout</button>
      </header>
      
      <section>
        {proposal && (
          <div>
            <h2>Referendum #{proposal.ref_id}</h2>
            {proposal.title && <h3>{proposal.title}</h3>}
          </div>
        )}
        
        <div id="messages">
          {messages.length === 0 ? (
            <p>No messages yet</p>
          ) : (
            messages.map((msg) => (
              <div key={msg.ID} className="msg">
                <div>
                  <span className="msg-author">{msg.Author}</span>
                  <span className="msg-time">{new Date(msg.CreatedAt).toLocaleString()}</span>
                </div>
                <div className="msg-body">{msg.Body}</div>
              </div>
            ))
          )}
        </div>

        <div>
          <textarea
            value={messageBody}
            onChange={(e) => setMessageBody(e.target.value)}
            placeholder="Type your message..."
            rows={4}
          />
          <button onClick={sendMessage} disabled={loading || !messageBody.trim()}>
            {loading ? 'Sending...' : 'Send Message'}
          </button>
        </div>

        {error && <div className="error">{error}</div>}
      </section>
    </div>
  )
}

function App() {
  return (
    <Router>
      <Routes>
        <Route path="/:network/:id" element={<ProposalPage />} />
        <Route path="/" element={
          <div className="container">
            <header>
              <h1>GovComms</h1>
            </header>
            <section>
              <p>Navigate to a specific proposal using the URL format: /{`{network}`}/{`{id}`}</p>
              <p>Example: /polkadot/52 or /kusama/403</p>
            </section>
          </div>
        } />
      </Routes>
    </Router>
  )
}

export default App