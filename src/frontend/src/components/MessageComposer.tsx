// src/frontend/src/components/MessageComposer.tsx
import { FormEvent, useState } from "react";

interface Props {
  proposalRef: string; // e.g. "polkadot/42"
  onSent: () => void;
}

export default function MessageComposer({ proposalRef, onSent }: Props) {
  const [body, setBody] = useState("");
  const [emails, setEmails] = useState<string>(""); // CSV in a single input

  async function send(e: FormEvent) {
    e.preventDefault();
    const jwt = localStorage.getItem("jwt");
    const r = await fetch("/v1/messages", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${jwt}`,
      },
      body: JSON.stringify({
        proposalRef,
        body,
        emails: emails
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
      }),
    });
    if (!r.ok) {
      alert("failed: " + (await r.text()));
      return;
    }
    setBody("");
    setEmails("");
    onSent();
  }

  return (
    <form onSubmit={send}>
      <textarea
        value={body}
        onChange={(e) => setBody(e.target.value)}
        placeholder="Enter your message"
        required
      />
      <input
        type="text"
        value={emails}
        onChange={(e) => setEmails(e.target.value)}
        placeholder="Optional notification emails (comma separated)"
      />
      <button type="submit">Post message</button>
    </form>
  );
}
