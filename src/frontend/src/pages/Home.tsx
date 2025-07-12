import React, { useState, useEffect } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';

function Home() {
	const navigate = useNavigate();
	const location = useLocation();
	const [network, setNetwork] = useState('polkadot');
	const [refId, setRefId] = useState('');
	const [error, setError] = useState('');

	useEffect(() => {
		// Check if we have an error from navigation state
		if (location.state?.error) {
			setError(location.state.error);
			// Clear the state
			window.history.replaceState({}, document.title);
		}
	}, [location]);

	const handleSubmit = (e: React.FormEvent) => {
		e.preventDefault();
		setError(''); // Clear any existing error
		if (refId) {
			navigate(`/auth/${network}/${refId}`);
		}
	};

	return (
		<div className="home-container">
			<div className="home-card">
				<h1>GovComms</h1>
				<p className="tagline">Connect with REEEEEEEEEE DAO</p>
				
				{error && (
					<div className="error-message">
						{error}
					</div>
				)}
				
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