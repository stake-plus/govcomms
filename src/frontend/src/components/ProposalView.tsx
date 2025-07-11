import React, { useState, useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { WalletConnect } from './WalletConnect';
import { MessageList } from './MessageList';
import { MessageInput } from './MessageInput';
import { api } from '../services/api';

interface ProposalViewProps {
  network: string;
  referendumId: string;
}

export function ProposalView({ network, referendumId }: ProposalViewProps) {
  const [address, setAddress] = useState<string | null>(null);
  const [token, setToken] = useState<string | null>(null);
  const queryClient = useQueryClient();

  useEffect(() => {
    const savedToken = localStorage.getItem('auth_token');
    const savedAddress = localStorage.getItem('auth_address');
    if (savedToken && savedAddress) {
      setToken(savedToken);
      setAddress(savedAddress);
      api.setAuthToken(savedToken);
    }
  }, []);

  const { data, isLoading, error } = useQuery({
    queryKey: ['messages', network, referendumId],
    queryFn: () => api.getMessages(network, referendumId),
    enabled: !!token,
  });

  const sendMessage = useMutation({
    mutationFn: (body: string) => 
      api.sendMessage(`${network}/${referendumId}`, body, data?.proposal?.title),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['messages', network, referendumId] });
    },
  });

  const handleAuth = (authToken: string, authAddress: string) => {
    setToken(authToken);
    setAddress(authAddress);
    localStorage.setItem('auth_token', authToken);
    localStorage.setItem('auth_address', authAddress);
    api.setAuthToken(authToken);
  };

  const handleLogout = () => {
    setToken(null);
    setAddress(null);
    localStorage.removeItem('auth_token');
    localStorage.removeItem('auth_address');
    api.setAuthToken(null);
  };

  if (!token) {
    return (
      <div className="container">
        <div className="proposal-header">
          <h2>{network.charAt(0).toUpperCase() + network.slice(1)} Referendum #{referendumId}</h2>
        </div>
        <WalletConnect onAuth={handleAuth} />
      </div>
    );
  }

  if (isLoading) {
    return <div className="container">Loading...</div>;
  }

  if (error) {
    return <div className="container error">Error loading messages</div>;
  }

  return (
    <div className="container">
      <div className="proposal-header">
        <h2>{network.charAt(0).toUpperCase() + network.slice(1)} Referendum #{referendumId}</h2>
        {data?.proposal?.title && <h3>{data.proposal.title}</h3>}
        <div className="auth-info">
          <span>Connected as: {address}</span>
          <button onClick={handleLogout} className="logout-btn">Logout</button>
        </div>
      </div>

      <MessageList messages={data?.messages || []} />
      
      <MessageInput
        onSend={(message) => sendMessage.mutate(message)}
        disabled={sendMessage.isPending}
      />
    </div>
  );
}