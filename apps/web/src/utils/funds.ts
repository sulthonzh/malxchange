// Shared funds shapes for the member center (spec #24 stories 13–15):
// the api funds routes' bodies named once so the assets, deposit,
// withdraw, and records pages cannot drift from each other.
export interface BalanceRow {
  coin: string
  available: string
  frozen: string
}

export interface WalletCoin {
  name: string
  scale: number
  withdrawFee: string
  minWithdraw: string
  withdrawable: boolean
}

export interface DepositAddress {
  coin: string
  address: string
}

export interface MemberDeposit {
  coin: string
  txid: string
  vout: number
  amount: string
  confirmations: number
  needed: number
  status: string
  detectedAt: string
}

export interface JournalEntry {
  coin: string
  kind: string
  amount: string
  availDelta: string
  frozenDelta: string
  reason: string
  ref: string
  at: string
}

export interface WithdrawPreview {
  coin: string
  amount: string
  fee: string
  net: string
}

export interface WithdrawAddress {
  id: string
  coin: string
  address: string
  label: string
}
