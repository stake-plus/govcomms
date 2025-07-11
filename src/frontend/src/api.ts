import { ofetch } from 'ofetch';

export const api = ofetch.create({
  baseURL: import.meta.env.VITE_API ?? 'https://api.gcs.example.com/v1',
  retry: 0
});

export async function challenge(address: string, method: string) {
  return api('/auth/challenge', { method: 'POST', body: { address, method } });
}

export async function verify(
  address: string,
  method: string,
  signature?: string
) {
  return api<{ token: string }>('/auth/verify', {
    method: 'POST',
    body: { address, method, signature }
  });
}

export async function listMessages(net: string, ref: number, jwt?: string) {
  return api(`/messages/${net}/${ref}`, {
    headers: jwt ? { Authorization: `Bearer ${jwt}` } : undefined
  });
}

export async function postMessage(
  jwt: string,
  proposalRef: string,
  body: string,
  emails: string[] = []
) {
  return api('/messages', {
    method: 'POST',
    body: { proposalRef, body, emails },
    headers: { Authorization: `Bearer ${jwt}` }
  });
}
