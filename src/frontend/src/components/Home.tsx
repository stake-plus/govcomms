import React from 'react';
import { Link } from 'react-router-dom';

export function Home() {
  return (
    <div className="home">
      <section className="hero">
        <h2>Welcome to GovComms</h2>
        <p>
          A communication bridge between Polkadot/Kusama governance proposers and the Chaos DAO voting community.
        </p>
      </section>
      
      <section className="instructions">
        <h3>How it works</h3>
        <ol>
          <li>DAO members provide feedback on proposals using the !feedback command in Discord</li>
          <li>Proposers receive a link to continue the discussion here</li>
          <li>Messages are relayed between this platform and the DAO's Discord</li>
          <li>The first feedback is automatically posted to Polkassembly</li>
        </ol>
      </section>

      <section className="links">
        <h3>Quick Links</h3>
        <div className="link-grid">
          <Link to="/polkadot/1" className="example-link">Example: Polkadot Ref #1</Link>
          <Link to="/kusama/1" className="example-link">Example: Kusama Ref #1</Link>
        </div>
      </section>
    </div>
  );
}