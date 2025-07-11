// src/api/data/indexer.go
package data

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"

	"github.com/OneOfOne/xxhash"
	"github.com/gorilla/websocket"
)

const polkadotRPC = "wss://rpc.polkadot.io"

// ---------- tiny JSON-RPC helpers ----------

type rpcReq struct {
	Jsonrpc string        `json:"jsonrpc"`
	ID      uint64        `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResp struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ---------- TwoX-128 (Substrate) ----------

func twox128(data []byte) []byte {
	hash1 := xxhash.NewS64(0)
	hash1.Write(data)
	hash2 := xxhash.NewS64(1)
	hash2.Write(data)

	out := make([]byte, 16)
	binary.LittleEndian.PutUint64(out[0:], hash1.Sum64())
	binary.LittleEndian.PutUint64(out[8:], hash2.Sum64())
	return out
}

func storageKey(pallet, item string) string {
	key := append(twox128([]byte(pallet)), twox128([]byte(item))...)
	return "0x" + hex.EncodeToString(key)
}

// Referenda.ReferendumCount
var refCountKey = storageKey("Referenda", "ReferendumCount")

// ---------- core fetcher ----------

func getReferendumCount(ws *websocket.Conn) (uint32, error) {
	req := rpcReq{
		Jsonrpc: "2.0",
		ID:      1,
		Method:  "state_getStorage",
		Params:  []interface{}{refCountKey, nil}, // nil → latest
	}
	if err := ws.WriteJSON(req); err != nil {
		return 0, err
	}

	var rsp rpcResp
	if err := ws.ReadJSON(&rsp); err != nil {
		return 0, err
	}
	if rsp.Error != nil {
		return 0, fmt.Errorf("RPC %d: %s", rsp.Error.Code, rsp.Error.Message)
	}

	var hexVal string
	if err := json.Unmarshal(rsp.Result, &hexVal); err != nil {
		return 0, err
	}
	if len(hexVal) < 3 { // "0x"
		return 0, nil
	}

	raw, err := hex.DecodeString(hexVal[2:])
	if err != nil {
		return 0, err
	}
	if len(raw) < 4 {
		return 0, fmt.Errorf("unexpected storage length: %d", len(raw))
	}

	return binary.LittleEndian.Uint32(raw[:4]), nil
}

// ---------- public entry-point ----------

// Call this from whatever scheduler you already have.
func RunPolkadotIndexer(ctx context.Context) {
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, polkadotRPC, nil)
	if err != nil {
		log.Printf("indexer polkadot: dial error: %v", err)
		return
	}
	defer ws.Close()

	cnt, err := getReferendumCount(ws)
	if err != nil {
		log.Printf("indexer polkadot: failed to fetch count: %v", err)
		return
	}
	log.Printf("indexer polkadot: chain reports %d referenda", cnt)
}
