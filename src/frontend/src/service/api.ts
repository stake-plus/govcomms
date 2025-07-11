const API_BASE_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080/v1';

class ApiService {
  private token: string | null = null;

  setAuthToken(token: string | null) {
    this.token = token;
  }

  private async request(endpoint: string, options: RequestInit = {}) {
    const headers: HeadersInit = {
      'Content-Type': 'application/json',
      ...options.headers,
    };

    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`;
    }

    const response = await fetch(`${API_BASE_URL}${endpoint}`, {
      ...options,
      headers,
    });

    if (!response.ok) {
      const error = await response.json().catch(() => ({ err: 'Request failed' }));
      throw new Error(error.err || 'Request failed');
    }

    return response.json();
  }

  async getChallenge(address: string, method: string) {
    return this.request('/auth/challenge', {
      method: 'POST',
      body: JSON.stringify({ address, method }),
    });
  }

  async verifySignature(address: string, method: string, signature: string) {
    return this.request('/auth/verify', {
      method: 'POST',
      body: JSON.stringify({ address, method, signature }),
    });
  }

  async getMessages(network: string, refId: string) {
    return this.request(`/messages/${network}/${refId}`);
  }

  async sendMessage(proposalRef: string, body: string, title?: string) {
    return this.request('/messages', {
      method: 'POST',
      body: JSON.stringify({ proposalRef, body, title }),
    });
  }
}

export const api = new ApiService();