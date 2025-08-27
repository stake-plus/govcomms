package claims

type Claim struct {
	Claim    string   `json:"claim"`
	Category string   `json:"category"`
	URLs     []string `json:"urls,omitempty"`
	Context  string   `json:"context,omitempty"`
}

type VerificationStatus string

const (
	StatusValid    VerificationStatus = "Valid"
	StatusRejected VerificationStatus = "Rejected"
	StatusUnknown  VerificationStatus = "Unknown"
)

type VerificationResult struct {
	Claim      string
	Status     VerificationStatus
	Evidence   string
	SourceURLs []string
}

type ClaimsResponse struct {
	TotalClaims int     `json:"total_claims"`
	TopClaims   []Claim `json:"top_claims"`
}
