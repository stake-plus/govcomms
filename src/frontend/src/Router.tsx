// src/frontend/src/Router.tsx
import { BrowserRouter, Routes, Route } from "react-router-dom";
import Login from "./auth/Login";

export default function AppRouter() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<Login />} />
      </Routes>
    </BrowserRouter>
  );
}
