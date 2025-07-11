package data

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"strings"

	substrate "github.com/itering/substrate-api-rpc"
	"github.com/itering/substrate-api-rpc/metadata"
	"github.com/itering/substrate-api-rpc/model"
	"github.com/itering/substrate-api-rpc/rpc"
	"github.com/itering/substrate-api-rpc/websocket"
	"github.com/redis/go-redis/v9"
)

// StartRemarkWatcher subscribes to new blocks and confirms login nonces
// posted via `system.remark`.
func StartRemarkWatcher(ctx context.Context, rpcURL string, rdb *redis.Client) {
	websocket.SetEndpoint(rpcURL)
	pooled, err := websocket.Init()
	if err != nil {
		log.Printf("remark watcher: connect: %v", err)
		return
	}
	conn := pooled.Conn
	defer pooled.Close()

	meta, spec, err := loadMetadata(conn)
	if err != nil {
		log.Printf("remark watcher: metadata: %v", err)
		return
	}

	// Subscribe to new-head events.
	subReq := rpc.ChainSubscribeNewHead(rand.Int())
	var subResp model.JsonRpcResult
	if err = websocket.SendWsRequest(conn, &subResp, subReq); err != nil || subResp.Error != nil {
		log.Printf("remark watcher: subscribe: %v", err)
		return
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			var msg model.JsonRpcResult
			if err := conn.ReadJSON(&msg); err != nil {
				log.Printf("remark watcher: read: %v", err)
				return
			}

			head := msg.ToNewHead()
			if head == nil {
				continue
			}

			num, _ := strconv.ParseUint(strings.TrimPrefix(head.Number, "0x"), 16, 64)
			var hashRes model.JsonRpcResult
			if err := websocket.SendWsRequest(conn, &hashRes, rpc.ChainGetBlockHash(rand.Int(), int(num))); err != nil {
				continue
			}

			blockHash, _ := hashRes.ToString()
			if blockHash == "" {
				continue
			}

			var blkRes model.JsonRpcResult
			if err := websocket.SendWsRequest(conn, &blkRes, rpc.ChainGetBlock(rand.Int(), blockHash)); err != nil {
				continue
			}

			blk := blkRes.ToBlock()
			if blk == nil {
				continue
			}

			for _, extHex := range blk.Block.Extrinsics {
				if nonce := parseRemark(extHex, meta, spec); nonce != "" {
					if signer, err := extractSigner(extHex); err == nil {
						_ = ConfirmNonce(ctx, rdb, signer)
						log.Printf("remark watcher: confirmed nonce for %s", signer)
					}
				}
			}
		}
	}()
}

// loadMetadata fetches metadata + runtime spec version.
func loadMetadata(c websocket.WsConn) (*metadata.Instant, int, error) {
	var ver model.JsonRpcResult
	if err := websocket.SendWsRequest(c, &ver, rpc.ChainGetRuntimeVersion(rand.Int())); err != nil {
		return nil, 0, err
	}

	rv := ver.ToRuntimeVersion()
	if rv == nil {
		return nil, 0, fmt.Errorf("nil runtime version")
	}

	var metaRes model.JsonRpcResult
	if err := websocket.SendWsRequest(c, &metaRes, rpc.StateGetMetadata(rand.Int())); err != nil {
		return nil, 0, err
	}

	metaHex, err := metaRes.ToString()
	if err != nil {
		return nil, 0, err
	}

	raw := metadata.RuntimeRaw{Spec: rv.SpecVersion, Raw: metaHex}
	return metadata.Process(&raw), rv.SpecVersion, nil
}

// parseRemark returns the string payload of a `system.remark` call.
func parseRemark(extHex string, meta *metadata.Instant, spec int) string {
	decoded, err := substrate.DecodeExtrinsic([]string{extHex}, meta, spec)
	if err != nil || len(decoded) == 0 {
		return ""
	}

	ext := decoded[0]
	mod, _ := ext["module"].(string)
	if !strings.EqualFold(mod, "System") {
		return ""
	}

	call, _ := ext["call"].(string)
	if !strings.EqualFold(call, "remark") {
		return ""
	}

	params, ok := ext["params"].([]interface{})
	if !ok || len(params) == 0 {
		return ""
	}

	first := params[0].(map[string]interface{})
	valHex, _ := first["value"].(string)
	if valHex == "" {
		return ""
	}

	b, _ := hex.DecodeString(strings.TrimPrefix(valHex, "0x"))
	if len(b) < 8 {
		return ""
	}
	return string(b)
}

// extractSigner does a minimal SS58 extraction from a raw extrinsic hex.
func extractSigner(extHex string) (string, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(extHex, "0x"))
	if err != nil || len(raw) < 38 {
		return "", fmt.Errorf("bad extrinsic")
	}

	i := 1
	if raw[0] >= 0x80 {
		i = 2 // compact-u32 len > 1 byte
	}

	addr := raw[i+1 : i+35] // 32-byte pubkey + prefix/checksum
	return "0x" + hex.EncodeToString(addr), nil
}
