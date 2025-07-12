// Minimal end‑to‑end integration test for the GovComms API.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	baseURL  = getenv("API_URL", "http://localhost:443/v1")
	redisURL = getenv("REDIS_URL", "redis://172.16.254.7:6379/0")
	addr     = "5GrwvaEF5zXb26Fz9rcQpDWSnT5JEmbdr5" // dev Alice
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	ctx := context.Background()
	rdb := mustRedis()
	defer rdb.Close()

	_ = challenge()        // obtain nonce but we don't need the value after confirming
	confirmNonce(ctx, rdb) // mark as CONFIRMED in Redis
	token := verify()      // get JWT

	proposal := "polkadot/1"
	msgID := createMessage(token, proposal)
	checkMessages(token, proposal, msgID)

	castVote(token, proposal)
	checkVotes(token, proposal)

	fmt.Println("✓ all endpoints passed")
}

// ----------------------------- auth

func challenge() string {
	var resp struct{ Nonce string }
	doJSON("POST", "/auth/challenge", map[string]any{
		"address": addr,
		"method":  "airgap",
	}, &resp, http.StatusOK)
	if resp.Nonce == "" {
		log.Fatal("challenge: empty nonce")
	}
	return resp.Nonce
}

func confirmNonce(ctx context.Context, rdb *redis.Client) {
	if err := rdb.Set(ctx, "nonce:"+addr, "CONFIRMED", 5*time.Minute).Err(); err != nil {
		log.Fatalf("redis set: %v", err)
	}
}

func verify() string {
	var resp struct{ Token string }
	doJSON("POST", "/auth/verify", map[string]any{
		"address": addr,
		"method":  "airgap",
	}, &resp, http.StatusOK)
	if resp.Token == "" {
		log.Fatal("verify: empty token")
	}
	return resp.Token
}

// ----------------------------- messages

func createMessage(tok, prop string) uint64 {
	var resp struct{ ID uint64 }
	doAuth(tok, "POST", "/messages", map[string]any{
		"proposalRef": prop,
		"body":        "integration-test " + uuid.NewString(),
		"emails":      []string{},
	}, &resp, http.StatusCreated)
	return resp.ID
}

func checkMessages(tok, prop string, want uint64) {
	var msgs []struct{ ID uint64 }
	doAuth(tok, "GET", "/messages/"+prop, nil, &msgs, http.StatusOK)
	for _, m := range msgs {
		if m.ID == want {
			return
		}
	}
	log.Fatal("messages: created message not found")
}

// ----------------------------- votes

func castVote(tok, prop string) {
	doAuth(tok, "POST", "/votes", map[string]any{
		"proposalRef": prop,
		"choice":      "aye",
	}, nil, http.StatusCreated)
}

func checkVotes(tok, prop string) {
	var sum map[string]uint64
	doAuth(tok, "GET", "/votes/"+prop, nil, &sum, http.StatusOK)
	if sum["aye"] == 0 {
		log.Fatal("votes: tally missing aye")
	}
}

// ----------------------------- helpers

func mustRedis() *redis.Client {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("redis url: %v", err)
	}
	return redis.NewClient(opt)
}

func doAuth(token, method, path string, body, out any, want int) {
	doReq(method, path, token, body, out, want)
}

func doJSON(method, path string, body, out any, want int) {
	doReq(method, path, "", body, out, want)
}

func doReq(method, path, token string, body, out any, want int) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			log.Fatalf("%s %s encode: %v", method, path, err)
		}
	}
	req, _ := http.NewRequest(method, baseURL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		log.Fatalf("%s %s: want %d got %d", method, path, want, res.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			log.Fatalf("%s %s decode: %v", method, path, err)
		}
	}
}
