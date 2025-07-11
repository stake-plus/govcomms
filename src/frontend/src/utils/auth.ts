import { AuthToken } from '../types';

const AUTH_KEY = 'govcomms_auth';

export const saveAuth = (auth: AuthToken) => {
  sessionStorage.setItem(AUTH_KEY, JSON.stringify(auth));
};

export const getAuth = (): AuthToken | null => {
  const stored = sessionStorage.getItem(AUTH_KEY);
  return stored ? JSON.parse(stored) : null;
};

export const clearAuth = () => {
  sessionStorage.removeItem(AUTH_KEY);
};

export const formatAddress = (address: string): string => {
  if (!address || address.length <= 16) return address || '';
  return `${address.slice(0, 8)}...${address.slice(-8)}`;
};