package core

import (
	"log"
	"net/http"

	aicore "github.com/stake-plus/govcomms/src/ai/core"
	"gorm.io/gorm"
)

// RuntimeDeps captures shared resources that agents can opt into.
type RuntimeDeps struct {
	DB     *gorm.DB
	HTTP   *http.Client
	AI     aicore.Client
	Logger *log.Logger
}
