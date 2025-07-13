import React, { useState, useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import Identicon from '@polkadot/react-identicon';
import ReactMarkdown from 'react-markdown';
import { api } from '../utils/api';
import { getAuth, clearAuth, formatAddress } from '../utils/auth';
import { ProposalData } from '../types';

function Proposal() {
  const { network, refId } = useParams<{ network: string; refId: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const auth = getAuth();
  const [messageBody, setMessageBody] = useState('');

  useEffect(() => {
    if (!auth) {
      navigate(`/auth/${network}/${refId}`);
    }
  }, [auth, navigate, network, refId]);

  const { data, isLoading, error } = useQuery<ProposalData>({
    queryKey: ['messages', network, refId],
    queryFn: () => api.getMessages(network!, refId!, auth!.token),
    enabled: !!auth,
    refetchInterval: 5000
  });

  const sendMessageMutation = useMutation({
    mutationFn: (body: string) => 
      api.sendMessage(`${network}/${refId}`, body, auth!.token),
    onSuccess: () => {
      setMessageBody('');
      queryClient.invalidateQueries({ queryKey: ['messages', network, refId] });
    }
  });

  const handleSendMessage = (e: React.FormEvent) => {
    e.preventDefault();
    if (messageBody.trim()) {
      sendMessageMutation.mutate(messageBody);
    }
  };

  const handleLogout = () => {
    clearAuth();
    navigate('/');
  };

  const formatTime = (timestamp: string | null | undefined) => {
    if (!timestamp) {
      return 'Unknown time';
    }
    try {
      const date = new Date(timestamp);
      
      if (isNaN(date.getTime())) {
        console.error('Invalid date format:', timestamp);
        return 'Unknown time';
      }
      
      return date.toLocaleString();
    } catch (e) {
      console.error('Date parsing error:', e, timestamp);
      return 'Unknown time';
    }
  };

  if (!auth) return null;
  
  if (isLoading) {
    return (
      <div className="proposal-container">
        <div className="proposal-content">
          <p>Loading...</p>
        </div>
      </div>
    );
  }

  if (error || !data) {
    return (
      <div className="proposal-container">
        <div className="proposal-content">
          <p className="error-message">Failed to load proposal data</p>
        </div>
      </div>
    );
  }

  return (
    <div className="proposal-container">
      <header className="proposal-header">
        <div className="header-left">
          <h1>GovComms</h1>
          <span className="ref-badge">{network?.toUpperCase()} #{refId}</span>
        </div>
        <div className="header-right">
          <Identicon value={auth.address} size={32} theme="polkadot" />
          <span className="user-address">{formatAddress(auth.address)}</span>
          <button className="btn-logout" onClick={handleLogout}>
            Logout
          </button>
        </div>
      </header>

      <div className="proposal-content-wide">
        <section className="proposal-info">
          <h2>Referendum #{data.proposal.ref_id}</h2>
          {data.proposal.title && <h3>{data.proposal.title}</h3>}
          <div className="proposal-meta">
            <span>Network: {data.proposal.network}</span>
            <span>Status: {data.proposal.status}</span>
            <span>Proposer: {formatAddress(data.proposal.submitter)}</span>
          </div>
        </section>

        <section className="messages-section">
          <h3>Discussion</h3>
          <div className="messages-list">
            {data.messages.length === 0 ? (
              <p className="no-messages">No messages yet. Be the first to start the discussion!</p>
            ) : (
              data.messages.map((message) => (
                <div key={message.ID} className={`message ${message.Internal ? 'internal' : ''}`}>
                  <div className="message-header">
                    <span className="message-author">{formatAddress(message.Author)}</span>
                    <span className="message-time">{formatTime(message.CreatedAt)}</span>
                  </div>
                  <div className="message-body markdown-content">
                    <ReactMarkdown>{message.Body}</ReactMarkdown>
                  </div>
                </div>
              ))
            )}
          </div>
        </section>

        <section className="message-input-section">
          <h3>Send Message</h3>
          <div className="markdown-hint">
            <span>You can use **bold**, *italic*, `code`, [links](url), and more markdown formatting.</span>
          </div>
          <form onSubmit={handleSendMessage}>
            <textarea
              className="message-textarea"
              placeholder="Type your message here... (Markdown supported)"
              value={messageBody}
              onChange={(e) => setMessageBody(e.target.value)}
              rows={10}
              disabled={sendMessageMutation.isPending}
            />
            <button 
              type="submit" 
              className="btn-send"
              disabled={!messageBody.trim() || sendMessageMutation.isPending}
            >
              {sendMessageMutation.isPending ? 'Sending...' : 'Send Message'}
            </button>
          </form>
          {sendMessageMutation.isError && (
            <p className="error-message">Failed to send message. Please try again.</p>
          )}
        </section>
      </div>
    </div>
  );
}

export default Proposal;