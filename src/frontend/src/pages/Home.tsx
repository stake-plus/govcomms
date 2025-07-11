import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';

function Home() {
  const navigate = useNavigate();
  const [network, setNetwork] = useState('polkadot');
  const [refId, setRefId] = useState('');

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (refId) {
      navigate(`/auth/${network}/${refId}`);
    }
  };

  return (
    <div className="home-container">
      <div className="home-card">
        <h1>GovComms</h1>
        <p className="tagline">Connect with ChaosDAO on governance proposals</p>
        
        <form className="home-form" onSubmit={handleSubmit}>
          <div className="form-group">
            <label htmlFor="network">Network</label>
            <select 
              id="network" 
              value={network} 
              onChange={(e) => setNetwork(e.target.value)}
            >
              <option value="polkadot">Polkadot</option>
              <option value="kusama">Kusama</option>
            </select>
          </div>
          
          <div className="form-group">
            <label htmlFor="refId">Referendum Number</label>
            <input
              id="refId"
              type="number"
              placeholder="Enter referendum number"
              value={refId}
              onChange={(e) => setRefId(e.target.value)}
              required
            />
          </div>
          
          <button type="submit" className="btn-primary">
            Continue
          </button>
        </form>
      </div>
    </div>
  );
}

export default Home;