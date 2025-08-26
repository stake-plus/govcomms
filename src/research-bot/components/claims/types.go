package claims

type Claim struct {
	Claim    string `json:"claim"`
	Category string `json:"category"`
	URL      string `json:"url,omitempty"`     // URL from proposal to verify claim
	Context  string `json:"context,omitempty"` // Additional context about the claim
}

type VerificationStatus string

const (
	StatusValid    VerificationStatus = "Valid"
	StatusRejected VerificationStatus = "Rejected"
	StatusUnknown  VerificationStatus = "Unknown"
)

type VerificationResult struct {
	Claim     string
	Status    VerificationStatus
	Evidence  string
	SourceURL string // URL where evidence was found
}

type ClaimsResponse struct {
	TotalClaims int     `json:"total_claims"`
	TopClaims   []Claim `json:"top_claims"`
}
