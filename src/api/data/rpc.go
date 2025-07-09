package data

import (
	"context"
	"log"
	"strings"

	"github.com/itering/substrate-api-rpc/client"
	"github.com/itering/substrate-api-rpc/expand"
	"github.com/redis/go-redis/v9"
)

func StartRemarkWatcher(ctx context.Context, rpcURL string, rdb *redis.Client) {
	api, err := client.ConnectSub(rpcURL)
	if err != nil {
		log.Printf("remark watcher connect: %v", err)
		return
	}

	sub, err := api.RPC.Chain.SubscribeNewHeads()
	if err != nil {
		log.Printf("remark watcher head sub: %v", err)
		return
	}

	go func() {
		for {
			select {
			case head := <-sub.Chan():
				hash := head.Hash()
				block, err := api.RPC.Chain.GetBlock(hash)
				if err != nil {
					continue
				}

				for _, ext := range block.Block.Extrinsics {
					// system.remark detection is handled by expand.DecodeRemark
					remarkBytes, err := expand.DecodeRemark(ext.Method.Args)
					if err != nil || len(remarkBytes) == 0 {
						continue
					}
					nonce := strings.TrimSpace(string(remarkBytes))
					if len(nonce) < 8 {
						continue
					}

					signer := ext.Signature.Signer.AsID
					addr := signer.ToHexString()
					_ = ConfirmNonce(ctx, rdb, addr)
				}

			case <-ctx.Done():
				sub.Unsubscribe()
				return
			}
		}
	}()
}
