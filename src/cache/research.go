package cache

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// RefClaim stores claim verification results for a referendum.
type RefClaim struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement"`
	RefDBID         uint64    `gorm:"column:ref_db_id;index:idx_claims_ref"`
	NetworkID       uint8     `gorm:"column:network_id;index:idx_claims_network_ref"`
	RefID           uint32    `gorm:"column:ref_id;index:idx_claims_network_ref"`
	ClaimText       string    `gorm:"column:claim_text;type:text"`
	Category        string    `gorm:"column:category;size:64"`
	ClaimURLs       string    `gorm:"column:claim_urls;type:text"` // JSON array
	Context         string    `gorm:"column:context;type:text"`
	Status          string    `gorm:"column:status;size:32;index:idx_claims_status"`
	Evidence        string    `gorm:"column:evidence;type:text"`
	SourceURLs      string    `gorm:"column:source_urls;type:text"` // JSON array
	ProviderCompany string    `gorm:"column:provider_company;size:128"`
	AIModel         string    `gorm:"column:ai_model;size:128"`
	TotalClaimsFound *uint32  `gorm:"column:total_claims_found"`
	CreatedAt       time.Time `gorm:"index:idx_claims_created"`
	UpdatedAt       time.Time
}

// TableName implements gorm's tabler interface.
func (RefClaim) TableName() string {
	return "ref_claims"
}

// RefTeamMember stores team member analysis results for a referendum.
type RefTeamMember struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement"`
	RefDBID         uint64    `gorm:"column:ref_db_id;index:idx_team_ref"`
	NetworkID       uint8     `gorm:"column:network_id;index:idx_team_network_ref"`
	RefID           uint32    `gorm:"column:ref_id;index:idx_team_network_ref"`
	Name            string    `gorm:"column:name;size:255;index:idx_team_name"`
	Role            string    `gorm:"column:role;size:255"`
	IsReal          *bool     `gorm:"column:is_real"`
	HasStatedSkills *bool     `gorm:"column:has_stated_skills"`
	Capability      string    `gorm:"column:capability;type:text"`
	GitHubURLs      string    `gorm:"column:github_urls;type:text"` // JSON array
	TwitterURLs     string    `gorm:"column:twitter_urls;type:text"` // JSON array
	LinkedInURLs     string    `gorm:"column:linkedin_urls;type:text"` // JSON array
	OtherURLs       string    `gorm:"column:other_urls;type:text"` // JSON array
	VerifiedURLs    string    `gorm:"column:verified_urls;type:text"` // JSON array
	ProviderCompany string    `gorm:"column:provider_company;size:128"`
	AIModel         string    `gorm:"column:ai_model;size:128"`
	CreatedAt       time.Time `gorm:"index:idx_team_created"`
	UpdatedAt       time.Time
}

// TableName implements gorm's tabler interface.
func (RefTeamMember) TableName() string {
	return "ref_team_members"
}

// ResearchStore provides persistence helpers for research data (claims and team analysis).
type ResearchStore struct {
	db *gorm.DB
}

// NewResearchStore returns a new research store instance.
func NewResearchStore(db *gorm.DB) *ResearchStore {
	return &ResearchStore{db: db}
}

// SaveClaim persists a claim verification result.
func (rs *ResearchStore) SaveClaim(refDBID uint64, networkID uint8, refID uint32, claimText, category string, claimURLs []string, context string, status, evidence string, sourceURLs []string, providerCompany, aiModel string, totalClaimsFound *uint32) error {
	if rs == nil || rs.db == nil {
		return fmt.Errorf("research store not initialized")
	}

	claimURLsJSON, _ := json.Marshal(claimURLs)
	sourceURLsJSON, _ := json.Marshal(sourceURLs)

	claim := RefClaim{
		RefDBID:         refDBID,
		NetworkID:       networkID,
		RefID:           refID,
		ClaimText:       claimText,
		Category:        category,
		ClaimURLs:       string(claimURLsJSON),
		Context:         context,
		Status:          status,
		Evidence:        evidence,
		SourceURLs:      string(sourceURLsJSON),
		ProviderCompany: providerCompany,
		AIModel:         aiModel,
		TotalClaimsFound: totalClaimsFound,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	return rs.db.Create(&claim).Error
}

// SaveTeamMember persists a team member analysis result.
func (rs *ResearchStore) SaveTeamMember(refDBID uint64, networkID uint8, refID uint32, name, role string, isReal, hasStatedSkills *bool, capability string, githubURLs, twitterURLs, linkedinURLs, otherURLs, verifiedURLs []string, providerCompany, aiModel string) error {
	if rs == nil || rs.db == nil {
		return fmt.Errorf("research store not initialized")
	}

	githubJSON, _ := json.Marshal(githubURLs)
	twitterJSON, _ := json.Marshal(twitterURLs)
	linkedinJSON, _ := json.Marshal(linkedinURLs)
	otherJSON, _ := json.Marshal(otherURLs)
	verifiedJSON, _ := json.Marshal(verifiedURLs)

	member := RefTeamMember{
		RefDBID:         refDBID,
		NetworkID:       networkID,
		RefID:           refID,
		Name:            name,
		Role:            role,
		IsReal:          isReal,
		HasStatedSkills: hasStatedSkills,
		Capability:      capability,
		GitHubURLs:      string(githubJSON),
		TwitterURLs:     string(twitterJSON),
		LinkedInURLs:    string(linkedinJSON),
		OtherURLs:       string(otherJSON),
		VerifiedURLs:    string(verifiedJSON),
		ProviderCompany: providerCompany,
		AIModel:         aiModel,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	return rs.db.Create(&member).Error
}

// GetClaimsForRef returns all claims for a specific referendum.
func (rs *ResearchStore) GetClaimsForRef(refDBID uint64) ([]RefClaim, error) {
	if rs == nil || rs.db == nil {
		return nil, fmt.Errorf("research store not initialized")
	}

	var claims []RefClaim
	err := rs.db.Where("ref_db_id = ?", refDBID).
		Order("created_at DESC").
		Find(&claims).Error
	return claims, err
}

// GetTeamMembersForRef returns all team members for a specific referendum.
func (rs *ResearchStore) GetTeamMembersForRef(refDBID uint64) ([]RefTeamMember, error) {
	if rs == nil || rs.db == nil {
		return nil, fmt.Errorf("research store not initialized")
	}

	var members []RefTeamMember
	err := rs.db.Where("ref_db_id = ?", refDBID).
		Order("created_at DESC").
		Find(&members).Error
	return members, err
}

// DeleteClaimsForRef deletes all claims for a specific referendum (useful before refreshing).
func (rs *ResearchStore) DeleteClaimsForRef(refDBID uint64) error {
	if rs == nil || rs.db == nil {
		return fmt.Errorf("research store not initialized")
	}
	return rs.db.Where("ref_db_id = ?", refDBID).Delete(&RefClaim{}).Error
}

// DeleteTeamMembersForRef deletes all team members for a specific referendum (useful before refreshing).
func (rs *ResearchStore) DeleteTeamMembersForRef(refDBID uint64) error {
	if rs == nil || rs.db == nil {
		return fmt.Errorf("research store not initialized")
	}
	return rs.db.Where("ref_db_id = ?", refDBID).Delete(&RefTeamMember{}).Error
}

