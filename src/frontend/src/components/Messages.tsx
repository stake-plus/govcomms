// src/frontend/src/components/Message.tsx
import { memo } from "react";
import Identicon from "@polkadot/react-identicon";

export interface Message {
  id: number;
  author: string;
  body: string;
  createdAt: string;
}

export default memo(function MessageView({ author, body, createdAt }: Message) {
  const date = new Date(createdAt);
  return (
    <div className="msg">
      <div>
        <Identicon value={author} size={20} theme="polkadot" />
        <span className="msg-author"> {author}</span>
        <span className="msg-time">{date.toLocaleString()}</span>
      </div>
      <pre className="msg-body">{body}</pre>
    </div>
  );
});
