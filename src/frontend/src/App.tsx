import React from 'react';
import { Routes, Route, Navigate } from 'react-router-dom';
import { useLocation } from 'react-router-dom';
import Home from './pages/Home';
import Auth from './pages/Auth';
import Proposal from './pages/Proposal';
import { getAuth } from './utils/auth';

function RequireAuth({ children }: { children: React.ReactElement }) {
  const auth = getAuth();
  const location = useLocation();
  
  if (!auth) {
    // Extract network and refId from the current path
    const pathParts = location.pathname.split('/');
    if (pathParts.length === 3 && pathParts[1] && pathParts[2]) {
      return <Navigate to={`/auth/${pathParts[1]}/${pathParts[2]}`} replace />;
    }
    return <Navigate to="/" replace />;
  }
  
  return children;
}

function App() {
  return (
    <Routes>
      <Route path="/" element={<Home />} />
      <Route path="/auth/:network/:refId" element={<Auth />} />
      <Route path="/:network/:refId" element={
        <RequireAuth>
          <Proposal />
        </RequireAuth>
      } />
      <Route path="*" element={<Navigate to="/" />} />
    </Routes>
  );
}

export default App;