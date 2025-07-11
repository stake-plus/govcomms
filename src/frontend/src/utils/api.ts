const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:443/v1';

export class ApiError extends Error {
  constructor(public status: number, message: string, public details?: any) {
    super(message);
    this.name = 'ApiError';
  }
}

export const api = {
  challenge: async (address: string, method: string) => {
    const res = await fetch(`${API_URL}/auth/challenge`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ address, method })
    });
    if (!res.ok) {
      const error = await res.json().catch(() => ({ err: 'Failed to get challenge' }));
      throw new ApiError(res.status, error.err || 'Failed to get challenge');
    }
    return res.json();
  },

  verify: async (address: string, method: string, signature?: string) => {
    const res = await fetch(`${API_URL}/auth/verify`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ address, method, signature })
    });
    if (!res.ok) {
      const error = await res.json().catch(() => ({ err: 'Failed to verify' }));
      if (res.status === 401) {
        throw new ApiError(res.status, 'Not authorized for this referendum', error);
      }
      throw new ApiError(res.status, error.err || 'Failed to verify');
    }
    return res.json();
  },

  getMessages: async (network: string, refId: string, token: string) => {
    const res = await fetch(`${API_URL}/messages/${network}/${refId}`, {
      headers: { 'Authorization': `Bearer ${token}` }
    });
    if (!res.ok) {
      const error = await res.json().catch(() => ({ err: 'Failed to fetch messages' }));
      throw new ApiError(res.status, error.err || 'Failed to fetch messages');
    }
    return res.json();
  },

  sendMessage: async (proposalRef: string, body: string, token: string) => {
    const res = await fetch(`${API_URL}/messages`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`
      },
      body: JSON.stringify({ proposalRef, body, emails: [] })
    });
    if (!res.ok) {
      const error = await res.json().catch(() => ({ err: 'Failed to send message' }));
      throw new ApiError(res.status, error.err || 'Failed to send message');
    }
    return res.json();
  }
};