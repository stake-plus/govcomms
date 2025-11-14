package gov

import (
	"strings"
	"sync"

	"gorm.io/gorm"
)

// Manager manages network lookups with caching
type NetworkManager struct {
	db       *gorm.DB
	networks map[uint8]*Network
	byName   map[string]*Network
	mu       sync.RWMutex
}

// NewNetworkManager creates a new network manager and loads networks from DB
func NewNetworkManager(db *gorm.DB) (*NetworkManager, error) {
	m := &NetworkManager{
		db:       db,
		networks: make(map[uint8]*Network),
		byName:   make(map[string]*Network),
	}

	if err := m.loadNetworks(); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *NetworkManager) loadNetworks() error {
	var networks []Network
	if err := m.db.Find(&networks).Error; err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.networks = make(map[uint8]*Network)
	m.byName = make(map[string]*Network)

	for i := range networks {
		net := &networks[i]
		m.networks[net.ID] = net
		m.byName[strings.ToLower(net.Name)] = net
	}

	return nil
}

// GetByID returns network by ID
func (m *NetworkManager) GetByID(id uint8) *Network {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.networks[id]
}

// GetByName returns network by name (case-insensitive)
func (m *NetworkManager) GetByName(name string) *Network {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byName[strings.ToLower(name)]
}

// GetAll returns all networks
func (m *NetworkManager) GetAll() map[uint8]*Network {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[uint8]*Network)
	for k, v := range m.networks {
		result[k] = v
	}
	return result
}

// FindByChannelID finds network by Discord channel ID
func (m *NetworkManager) FindByChannelID(channelID string) *Network {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, net := range m.networks {
		if net.DiscordChannelID == channelID {
			return net
		}
	}
	return nil
}

