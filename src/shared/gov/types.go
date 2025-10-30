package gov

import "time"

// Network represents a blockchain network (Polkadot, Kusama, etc.)
type Network struct {
	ID               uint8  `gorm:"primaryKey"`
	Name             string `gorm:"size:32;unique;not null"`
	Symbol           string `gorm:"size:8;not null"`
	URL              string `gorm:"size:256;not null"`
	DiscordChannelID string `gorm:"size:64"`
}

// RefThread maps Discord thread IDs to referendum information
type RefThread struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	ThreadID  string `gorm:"uniqueIndex"`
	RefDBID   uint64 `gorm:"index"`
	NetworkID uint8  `gorm:"index"`
	RefID     uint64 `gorm:"index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

