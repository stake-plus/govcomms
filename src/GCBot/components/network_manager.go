package components

import (
	"strings"
	"sync"

	"github.com/stake-plus/govcomms/src/GCApi/types"
	"gorm.io/gorm"
)

type Manager struct {
	db       *gorm.DB
	networks map[uint8]*types.Network
	byName   map[string]*types.Network
	mu       sync.RWMutex
}

func NewManager(db *gorm.DB) (*Manager, error) {
	m := &Manager{
		db:       db,
		networks: make(map[uint8]*types.Network),
		byName:   make(map[string]*types.Network),
	}

	if err := m.loadNetworks(); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *Manager) loadNetworks() error {
	var networks []types.Network
	if err := m.db.Find(&networks).Error; err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range networks {
		net := &networks[i]
		m.networks[net.ID] = net
		m.byName[strings.ToLower(net.Name)] = net
		if net.PolkassemblyPrefix != "" {
			m.byName[strings.ToLower(net.PolkassemblyPrefix)] = net
		}
	}

	return nil
}

func (m *Manager) GetByID(id uint8) *types.Network {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.networks[id]
}

func (m *Manager) GetByName(name string) *types.Network {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byName[strings.ToLower(name)]
}

func (m *Manager) GetAll() map[uint8]*types.Network {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[uint8]*types.Network)
	for k, v := range m.networks {
		result[k] = v
	}
	return result
}

func (m *Manager) FindByChannelID(channelID string) *types.Network {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, net := range m.networks {
		if net.DiscordChannelID == channelID {
			return net
		}
	}
	return nil
}

func (m *Manager) Reload() error {
	return m.loadNetworks()
}
