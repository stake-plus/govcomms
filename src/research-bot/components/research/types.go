package research

type VerificationStatus string

const (
	StatusValid    VerificationStatus = "Valid"
	StatusRejected VerificationStatus = "Rejected"
	StatusUnknown  VerificationStatus = "Unknown"
)

type Claim struct {
	Claim    string `json:"claim"`
	Category string `json:"category"`
}

type VerificationResult struct {
	Claim    string
	Status   VerificationStatus
	Evidence string
}

type TeamMember struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	GitHub   string `json:"github"`
	Twitter  string `json:"twitter"`
	LinkedIn string `json:"linkedin"`
}

type TeamAnalysisResult struct {
	Name            string
	Role            string
	IsReal          bool
	HasStatedSkills bool
	Capability      string
}
