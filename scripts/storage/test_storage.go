package main

import (
	"log"

	polkadot "github.com/stake-plus/govcomms/src/polkadot-go"
)

func main() {
	// Connect to Polkadot
	client, err := polkadot.NewClient("wss://rpc.polkadot.io")
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()

	// Test referendum 1670
	refID := uint32(1670)

	info, err := client.GetReferendumInfo(refID)
	if err != nil {
		log.Fatalf("Error getting referendum info: %v", err)
	}

	log.Printf("Referendum %d info:", refID)
	log.Printf("  Status: %s", info.Status)
	log.Printf("  Track: %d", info.Track)
	log.Printf("  Origin: %s", info.Origin)
	log.Printf("  Proposal: %s", info.Proposal)
	log.Printf("  Enactment: %s", info.Enactment)
	log.Printf("  Submitted: block %d", info.Submitted)
	log.Printf("  Submitter: %s", info.Submission.Who)
	log.Printf("  Amount: %s", info.Submission.Amount)

	if info.Decision != nil {
		log.Printf("  Decision Since: block %d", info.Decision.Since)
		if info.Decision.Confirming != nil {
			log.Printf("  Confirming: block %d", *info.Decision.Confirming)
		}
	}

	log.Printf("  Tally:")
	log.Printf("    Ayes: %s", info.Tally.Ayes)
	log.Printf("    Nays: %s", info.Tally.Nays)
	log.Printf("    Support: %s", info.Tally.Support)
}
