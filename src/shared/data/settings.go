package data

import (
	"sync"

	"github.com/stake-plus/govcomms/src/shared/gov"
	"gorm.io/gorm"
)

var (
	settingsCache map[string]string
	settingsMu    sync.RWMutex
)

// LoadSettings loads all settings from the database into cache
func LoadSettings(db *gorm.DB) error {
	var settings []gov.Setting
	if err := db.Find(&settings).Error; err != nil {
		return err
	}

	settingsMu.Lock()
	defer settingsMu.Unlock()

	settingsCache = make(map[string]string)
	for _, s := range settings {
		settingsCache[s.Name] = s.Value
	}

	return nil
}

// GetSetting retrieves a setting value from cache (call LoadSettings first)
func GetSetting(name string) string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return settingsCache[name]
}

