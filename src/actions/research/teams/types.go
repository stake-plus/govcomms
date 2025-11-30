package teams

type TeamMember struct {
	Name     string   `json:"name"`
	Role     string   `json:"role"`
	GitHub   []string `json:"github"`
	Twitter  []string `json:"twitter"`
	LinkedIn []string `json:"linkedin"`
	Other    []string `json:"other"`
}

type TeamAnalysisResult struct {
	Name            string
	Role            string
	IsReal          bool
	HasStatedSkills bool
	Capability      string
	VerifiedURLs    []string // Added to track verified URLs
}
