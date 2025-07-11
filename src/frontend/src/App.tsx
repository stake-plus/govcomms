import React, { useState } from 'react';
import Login from './Login';
import Messages from './Messages';

export default function App() {
  const [jwt, setJwt] = useState<string | null>(null);

  return (
    <>
      <header>
        <h1>GovComms Demo</h1>
        {jwt && <span id="ref">✔&nbsp;Authenticated</span>}
      </header>

      <main>
        {!jwt ? (
          <Login onAuthenticated={setJwt} />
        ) : (
          <Messages token={jwt} />
        )}
      </main>
    </>
  );
}
