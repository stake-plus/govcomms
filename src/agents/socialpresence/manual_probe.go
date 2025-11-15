package socialpresence

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	agentcore "github.com/stake-plus/govcomms/src/agents/core"
)

// ManualProbe inspects mission inputs/artifacts instead of external APIs.
type ManualProbe struct{}

func (ManualProbe) Name() string { return "manual" }

func (ManualProbe) Lookup(ctx context.Context, mission agentcore.Mission) (*Profile, error) {
	subject := mission.Subject
	if strings.TrimSpace(subject.Identifier) == "" {
		return nil, ErrNoData
	}

	profile := &Profile{
		Provider:    "manual",
		Platform:    subject.Platform,
		Handle:      subject.Identifier,
		DisplayName: subject.DisplayName,
		URL:         subject.URL,
	}

	if subject.Metadata != nil {
		if createdAt := parseTime(subject.Metadata["created_at"]); createdAt != nil {
			profile.CreatedAt = createdAt
		}
		if followers, ok := subject.Metadata["followers"]; ok && profile.Followers == nil {
			if v, ok := castInt(followers); ok {
				profile.Followers = &v
			}
		}
	}

	mergeProfileFromMap(profile, mission.Inputs)

	for _, artifact := range mission.Artifacts {
		if artifact.Type != agentcore.ArtifactSocialSnapshot {
			continue
		}
		mergeProfileFromMap(profile, artifact.Data)
	}

	if !profile.hasPayload() {
		return nil, ErrNoData
	}
	return profile, nil
}

func mergeProfileFromMap(profile *Profile, data map[string]any) {
	if len(data) == 0 {
		return
	}
	if followers := readIntFromMap(data, "followers", "follower_count", "stats.followers"); followers != nil {
		profile.Followers = followers
	}
	if following := readIntFromMap(data, "following", "following_count", "stats.following"); following != nil {
		profile.Following = following
	}
	if posts := readIntFromMap(data, "posts", "post_count", "stats.posts"); posts != nil {
		profile.Posts = posts
	}
	if engagement := readFloatFromMap(data, "avg_interactions", "engagement.avg", "stats.avg_interactions"); engagement != nil {
		profile.AvgInteractions = engagement
	}
	if createdAt := readTimeFromMap(data, "created_at", "account.created_at"); createdAt != nil {
		profile.CreatedAt = createdAt
	}
	if urlVal := lookupValue(data, "url"); urlVal != nil {
		if urlStr, ok := urlVal.(string); ok && urlStr != "" {
			profile.URL = urlStr
		}
	}
}

func readIntFromMap(data map[string]any, paths ...string) *int {
	for _, path := range paths {
		if val := lookupValue(data, path); val != nil {
			if parsed, ok := castInt(val); ok {
				return &parsed
			}
		}
	}
	return nil
}

func readFloatFromMap(data map[string]any, paths ...string) *float64 {
	for _, path := range paths {
		if val := lookupValue(data, path); val != nil {
			if parsed, ok := castFloat(val); ok {
				return &parsed
			}
		}
	}
	return nil
}

func readTimeFromMap(data map[string]any, paths ...string) *time.Time {
	for _, path := range paths {
		if val := lookupValue(data, path); val != nil {
			if parsed := parseTime(val); parsed != nil {
				return parsed
			}
		}
	}
	return nil
}

func lookupValue(data map[string]any, path string) any {
	if data == nil {
		return nil
	}
	segments := strings.Split(path, ".")
	var current any = data
	for _, segment := range segments {
		switch typed := current.(type) {
		case map[string]any:
			current = typed[segment]
		default:
			return nil
		}
	}
	return current
}

func castInt(val any) (int, bool) {
	switch v := val.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	case string:
		num, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return num, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func castFloat(val any) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		num, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			return num, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func parseTime(val any) *time.Time {
	switch v := val.(type) {
	case time.Time:
		t := v.UTC()
		return &t
	case *time.Time:
		if v == nil {
			return nil
		}
		t := v.UTC()
		return &t
	case string:
		formats := []string{time.RFC3339, "2006-01-02", "2006/01/02", time.RFC822}
		trimmed := strings.TrimSpace(v)
		for _, layout := range formats {
			if ts, err := time.Parse(layout, trimmed); err == nil {
				t := ts.UTC()
				return &t
			}
		}
		if unix, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			t := time.Unix(unix, 0).UTC()
			return &t
		}
	case float64:
		if v > 0 {
			t := time.Unix(int64(v), 0).UTC()
			return &t
		}
	case int64:
		if v > 0 {
			t := time.Unix(v, 0).UTC()
			return &t
		}
	}
	return nil
}

func (p *Profile) hasPayload() bool {
	return p.Followers != nil ||
		p.Following != nil ||
		p.Posts != nil ||
		p.AvgInteractions != nil ||
		p.CreatedAt != nil ||
		p.URL != ""
}

func (p *Profile) String() string {
	return fmt.Sprintf("%s@%s (%s)", p.Handle, p.Platform, p.Provider)
}
