package fsx

import (
    "crypto/md5"
    "encoding/hex"
    "fmt"
    "path/filepath"
)

func ProposalCacheFilename(network string, refID uint32) string {
    hash := md5.Sum([]byte(fmt.Sprintf("%s-%d", network, refID)))
    return fmt.Sprintf("%s-%d-%s.txt", network, refID, hex.EncodeToString(hash[:8]))
}

func ProposalCachePath(tempDir, network string, refID uint32) string {
    return filepath.Join(tempDir, ProposalCacheFilename(network, refID))
}


