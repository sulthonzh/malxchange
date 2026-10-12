package events

// Member identity payloads. api produces these on the member lifecycle
// topics; wallet opens Ledger accounts from member.register (#22/#24
// contract).

// MemberRegistered is the data payload on TopicMemberRegister, keyed by
// the member id.
type MemberRegistered struct {
	MemberID string `json:"memberId"`
	Email    string `json:"email,omitempty"`
}
