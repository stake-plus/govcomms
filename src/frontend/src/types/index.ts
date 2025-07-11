export interface AuthToken {
  token: string;
  address: string;
}

export interface Message {
  id: number;
  author: string;
  body: string;
  created_at: string;
  internal?: boolean;
}

export interface ProposalInfo {
  id: number;
  network: string;
  ref_id: number;
  title?: string;
  submitter: string;
  status: string;
}

export interface ProposalData {
  proposal: ProposalInfo;
  messages: Message[];
}

export type AuthMethod = 'walletconnect' | 'polkadotjs' | 'airgap';