import React from 'react';
import { BrowserRouter as Router, Routes, Route, useParams } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ProposalView } from './components/ProposalView';
import { Home } from './components/Home';
import './App.css';

const queryClient = new QueryClient();

function ProposalWrapper() {
  const { network, id } = useParams<{ network: string; id: string }>();
  return <ProposalView network={network!} referendumId={id!} />;
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Router>
        <div className="app">
          <header>
            <h1>GovComms</h1>
            <span className="tagline">Polkadot Governance Communication Platform</span>
          </header>
          <Routes>
            <Route path="/" element={<Home />} />
            <Route path="/:network/:id" element={<ProposalWrapper />} />
          </Routes>
        </div>
      </Router>
    </QueryClientProvider>
  );
}