import React, { createContext, useContext, useState } from 'react';

export interface Session {
  address: string;
  token: string;
}

const AuthCtx = createContext<{
  session?: Session;
  setSession: (s?: Session) => void;
}>({ setSession: () => {} });

export const AuthProvider: React.FC<React.PropsWithChildren> = ({ children }) => {
  const [session, setSession] = useState<Session>();
  return (
    <AuthCtx.Provider value={{ session, setSession }}>
      {children}
    </AuthCtx.Provider>
  );
};

export const useAuth = () => useContext(AuthCtx);
