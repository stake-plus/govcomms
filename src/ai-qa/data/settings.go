package data

import (
	"sync"

	"github.com/stake-plus/govcomms/src/ai-qa/types"
	"gorm.io/gorm"
)

var (
	settingsCache map[string]string
	settingsMu    sync.RWMutex
)

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

func GetSetting(name string) string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return settingsCache[name]
}
