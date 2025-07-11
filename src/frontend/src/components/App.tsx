import React, { useState } from "react";
import Login from "./components/Login";
import Messages from "./components/Messages";
import { clearToken } from "./api";

const path = location.pathname.split("/").filter(Boolean);
const net = path[0] || "polkadot";
const refId = path[1] || "0";

export default function App() {
  const [authed, setAuthed] = useState(
    !!localStorage.getItem("govcomms_jwt")
  );

  function logout() {
    clearToken();
    setAuthed(false);
  }

  return (
    <>
      <header>
        <h1>GovComms</h1>
        <div style={{ display: "flex", alignItems: "center" }}>
          <span id="ref" style={{ marginRight: "1rem" }}>
            {net} / #{refId}
          </span>
          {authed && (
            <button onClick={logout} style={{ fontSize: "0.8rem" }}>
              Log out
            </button>
          )}
        </div>
      </header>

      {authed ? (
        <Messages net={net} id={refId} />
      ) : (
        <Login onAuthenticated={() => setAuthed(true)} />
      )}
    </>
  );
}
