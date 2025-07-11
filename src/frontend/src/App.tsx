import React from 'react';
import { Routes, Route, Navigate } from 'react-router-dom';
import Home from 'pages/Home';
import Auth from 'pages/Auth';
import Proposal from 'pages/Proposal';

function App() {
  return (
    <Routes>
      <Route path="/" element={<Home />} />
      <Route path="/auth/:network/:refId" element={<Auth />} />
      <Route path="/:network/:refId" element={<Proposal />} />
      <Route path="*" element={<Navigate to="/" />} />
    </Routes>
  );
}

export default App;