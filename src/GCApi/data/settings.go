package data

import (
	"sync"

	"github.com/stake-plus/govcomms/src/GCApi/types"
	"gorm.io/gorm"
)

var (
	settingsCache map[string]string
	settingsMu    sync.RWMutex
)

// LoadSettings loads all settings from database into cache
func LoadSettings(db *gorm.DB) error {
	var settings []types.Setting
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

// GetSetting retrieves a setting value by name
func GetSetting(name string) string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return settingsCache[name]
}

// RefreshSettings reloads settings from database
func RefreshSettings(db *gorm.DB) error {
	return LoadSettings(db)
}
