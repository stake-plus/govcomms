// src/frontend/src/Router.tsx
import { BrowserRouter, Routes, Route } from "react-router-dom";
import Login from "./auth/Login";
import ProposalPage from "./pages/ProposalPage";

export default function AppRouter() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/:net/:id" element={<ProposalPage />} />
      </Routes>
    </BrowserRouter>
  );
}
