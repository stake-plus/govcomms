const API_BASE = `${location.origin}/v1`;
export type Message = {
  id: number;
  author: string;
  body: string;
  createdAt: string;
};

let jwt = localStorage.getItem("govcomms_jwt") || "";

export function setToken(tok: string) {
  jwt = tok;
  localStorage.setItem("govcomms_jwt", tok);
}

export function clearToken() {
  jwt = "";
  localStorage.removeItem("govcomms_jwt");
}

async function request<T>(
  path: string,
  options: RequestInit = {}
): Promise<T> {
  const opts = { ...options };
  opts.headers = {
    "Content-Type": "application/json",
    ...(jwt ? { Authorization: `Bearer ${jwt}` } : {}),
    ...opts.headers
  };
  const res = await fetch(`${API_BASE}${path}`, opts);
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return (res.status === 204 ? null : await res.json()) as T;
}

export async function challenge(addr: string, method: string) {
  return request<{ nonce: string }>("/auth/challenge", {
    method: "POST",
    body: JSON.stringify({ address: addr, method })
  });
}

export async function verify(
  addr: string,
  method: string,
  signature?: string
) {
  return request<{ token: string }>("/auth/verify", {
    method: "POST",
    body: JSON.stringify({ address: addr, method, signature })
  });
}

export async function fetchMessages(net: string, id: string) {
  return request<Message[]>(`/messages/${net}/${id}`);
}

export async function postMessage(
  net: string,
  id: string,
  body: string
): Promise<void> {
  await request("/messages", {
    method: "POST",
    body: JSON.stringify({ proposalRef: `${net}/${id}`, body })
  });
}
