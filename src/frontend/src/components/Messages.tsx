import React, { useEffect, useState, useRef } from "react";
import {
  fetchMessages,
  postMessage,
  Message
} from "../api";
import Identicon from "@polkadot/react-identicon";

type Props = {
  net: string;
  id: string;
};

export default function Messages({ net, id }: Props) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [text, setText] = useState("");
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function load() {
    const list = await fetchMessages(net, id);
    setMessages(list);
    // scroll
    setTimeout(() => bottomRef.current?.scrollIntoView(), 0);
  }

  async function send(e: React.FormEvent) {
    e.preventDefault();
    const body = text.trim();
    if (!body) return;
    await postMessage(net, id, body);
    setText("");
    await load();
  }

  return (
    <section>
      <div id="messages">
        {messages.map((m) => (
          <div key={m.id} className="msg">
            <div style={{ display: "flex", alignItems: "center" }}>
              <Identicon
                value={m.author}
                size={20}
                style={{ marginRight: "0.5rem" }}
              />
              <span className="msg-author">{m.author}</span>
              <span className="msg-time">
                {new Date(m.createdAt).toLocaleString()}
              </span>
            </div>
            <div className="msg-body">{m.body}</div>
          </div>
        ))}
        <div ref={bottomRef} />
      </div>

      <form id="msg-form" onSubmit={send}>
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="Write a message"
        />
        <button type="submit">Send</button>
      </form>
    </section>
  );
}
