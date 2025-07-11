import React, { useEffect, useState } from 'react';

interface Message {
  id: string;
  author: string;
  body: string;
  created_at: string; // ISO string
}

interface Props {
  token: string;
}

export default function Messages({ token }: Props) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [loading, setLoading] = useState(true);

  // Fetch once, then poll every 5 s
  useEffect(() => {
    let timer: ReturnType<typeof setInterval>;

    async function load() {
      try {
        const res = await fetch('/v1/messages', {
          headers: { Authorization: `Bearer ${token}` },
        });
        const data: Message[] = await res.json();
        setMessages(data);
      } catch (err) {
        console.error(err);
      } finally {
        setLoading(false);
      }
    }

    load();
    timer = setInterval(load, 5_000);
    return () => clearInterval(timer);
  }, [token]);

  //--------------------------------------------------------------------
  // Render
  //--------------------------------------------------------------------
  if (loading) return <p>Loading …</p>;
  if (messages.length === 0) return <p>No messages yet.</p>;

  return (
    <ul className="messages">
      {messages.map((m) => (
        <li key={m.id}>
          <header>
            <strong>{m.author}</strong>{' '}
            <time dateTime={m.created_at}>
              {new Date(m.created_at).toLocaleString()}
            </time>
          </header>
          <p>{m.body}</p>
        </li>
      ))}
    </ul>
  );
}
