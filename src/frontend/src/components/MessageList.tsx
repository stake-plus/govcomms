import React from 'react';

interface Message {
  ID: number;
  Author: string;
  Body: string;
  CreatedAt: string;
  Internal: boolean;
}

interface MessageListProps {
  messages: Message[];
}

export function MessageList({ messages }: MessageListProps) {
  return (
    <div className="message-list">
      {messages.length === 0 ? (
        <p className="no-messages">No messages yet. Be the first to start the conversation!</p>
      ) : (
        messages.map((msg) => (
          <div key={msg.ID} className={`message ${msg.Internal ? 'internal' : ''}`}>
            <div className="message-header">
              <span className="author">{msg.Author}</span>
              <span className="time">{new Date(msg.CreatedAt).toLocaleString()}</span>
            </div>
            <div className="message-body">{msg.Body}</div>
          </div>
        ))
      )}
    </div>
  );
}