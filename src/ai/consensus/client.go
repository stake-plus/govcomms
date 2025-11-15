package consensus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/stake-plus/govcomms/src/ai/core"
)

func init() {
	core.RegisterProvider("consensus", newClient)
}

type client struct {
	researchers []participant
	reviewers   []participant
	voters      []participant

	agreementThreshold float64
	rounds             int
	roundDelay         time.Duration

	baseOptions core.Options
}

type participant struct {
	name     string
	provider string
	model    string
	client   core.Client
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	specs := participantSpecs{
		Researchers: parseParticipantSpecs(cfg.Extra["consensus_researchers"]),
		Reviewers:   parseParticipantSpecs(cfg.Extra["consensus_reviewers"]),
		Voters:      parseParticipantSpecs(cfg.Extra["consensus_voters"]),
	}

	if len(specs.Researchers) == 0 {
		specs.Researchers = defaultSpecsFromKeys(cfg)
	}
	if len(specs.Reviewers) == 0 {
		specs.Reviewers = append([]participantSpec{}, specs.Researchers...)
	}
	if len(specs.Voters) == 0 {
		specs.Voters = append([]participantSpec{}, specs.Reviewers...)
	}

	if len(specs.Researchers) == 0 {
		return nil, errors.New("consensus: no researchers configured")
	}

	researchers, err := buildParticipants(cfg, specs.Researchers)
	if err != nil {
		return nil, err
	}
	reviewers, err := buildParticipants(cfg, specs.Reviewers)
	if err != nil {
		return nil, err
	}
	voters, err := buildParticipants(cfg, specs.Voters)
	if err != nil {
		return nil, err
	}

	agreement := clampFloat(parseFloat(cfg.Extra["consensus_agreement"], 0.67), 0.5, 0.99)
	rounds := parseInt(cfg.Extra["consensus_rounds"], 1)
	if rounds < 1 {
		rounds = 1
	}
	delaySeconds := parseInt(cfg.Extra["consensus_round_delay"], 120)
	if delaySeconds < 30 {
		delaySeconds = 120
	}

	return &client{
		researchers:        researchers,
		reviewers:          reviewers,
		voters:             voters,
		agreementThreshold: agreement,
		rounds:             rounds,
		roundDelay:         time.Duration(delaySeconds) * time.Second,
		baseOptions: core.Options{
			Model:               cfg.Model,
			Temperature:         cfg.Temperature,
			MaxCompletionTokens: cfg.MaxCompletionTokens,
			SystemPrompt:        cfg.SystemPrompt,
		},
	}, nil
}

func (c *client) AnswerQuestion(ctx context.Context, content string, question string, opts core.Options) (string, error) {
	prompt := fmt.Sprintf("Context:\n%s\n\nQuestion:\n%s\n\nProvide the best possible answer grounded in the provided context.", content, question)
	return c.Respond(ctx, prompt, nil, opts)
}

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	if len(c.researchers) == 0 {
		return "", errors.New("consensus: no researchers available")
	}

	analysis := c.runResearch(ctx, input, tools, opts)
	if len(analysis) == 0 {
		return "", errors.New("consensus: all researchers failed")
	}

	var (
		ballots []ballot
		report  consensusReport
	)

	for round := 0; round < c.rounds; round++ {
		ballots = c.runReview(ctx, input, analysis, opts)
		report = buildConsensusReport(input, analysis, ballots, c.agreementThreshold)

		if len(ballots) == 0 || report.Metrics.Agreement >= c.agreementThreshold || round == c.rounds-1 {
			break
		}
		if !sleepWithContext(ctx, c.roundDelay) {
			break
		}
	}

	final, err := c.renderFinal(ctx, input, report, opts)
	if err != nil || strings.TrimSpace(final) == "" {
		return synthesizeFallback(report), nil
	}
	return final, nil
}

func (c *client) runResearch(ctx context.Context, mission string, tools []core.Tool, opts core.Options) []analysisPacket {
	var wg sync.WaitGroup
	results := make([]analysisPacket, len(c.researchers))

	for idx, member := range c.researchers {
		wg.Add(1)
		go func(i int, p participant) {
			defer wg.Done()
			prompt := buildResearchPrompt(p.name, mission)
			localOpts := c.mergeOptions(p.model, opts)
			output, err := p.client.Respond(ctx, prompt, tools, localOpts)
			packet := parseAnalysisPacket(p, output, err)
			results[i] = packet
		}(idx, member)
	}

	wg.Wait()

	filtered := make([]analysisPacket, 0, len(results))
	for _, packet := range results {
		if packet.Err != nil || strings.TrimSpace(packet.Summary) == "" {
			continue
		}
		filtered = append(filtered, packet)
	}
	return filtered
}

func (c *client) runReview(ctx context.Context, mission string, contributions []analysisPacket, opts core.Options) []ballot {
	if len(c.reviewers) == 0 {
		return nil
	}

	dossier := buildDossier(contributions)
	var wg sync.WaitGroup
	ballots := make([]ballot, len(c.reviewers))

	for idx, reviewer := range c.reviewers {
		wg.Add(1)
		go func(i int, p participant) {
			defer wg.Done()
			prompt := buildReviewPrompt(p.name, mission, dossier)
			localOpts := c.mergeOptions(p.model, opts)
			reply, err := p.client.Respond(ctx, prompt, nil, localOpts)
			ballots[i] = parseBallot(p, reply, err)
		}(idx, reviewer)
	}

	wg.Wait()

	valid := make([]ballot, 0, len(ballots))
	for _, ballot := range ballots {
		if ballot.Err != nil || len(ballot.Votes) == 0 {
			continue
		}
		valid = append(valid, ballot)
	}
	return valid
}

func (c *client) renderFinal(ctx context.Context, mission string, report consensusReport, opts core.Options) (string, error) {
	if len(c.voters) == 0 {
		return "", errors.New("consensus: no arbiters")
	}
	data, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	prompt := buildFinalPrompt(mission, string(data))
	for _, arbiter := range c.voters {
		localOpts := c.mergeOptions(arbiter.model, opts)
		answer, err := arbiter.client.Respond(ctx, prompt, nil, localOpts)
		if err == nil && strings.TrimSpace(answer) != "" {
			return answer, nil
		}
		log.Printf("consensus: arbiter %s failed: %v", arbiter.name, err)
	}
	return "", errors.New("consensus: all arbiters failed")
}

func (c *client) mergeOptions(modelOverride string, callOpts core.Options) core.Options {
	base := c.baseOptions
	if strings.TrimSpace(modelOverride) != "" {
		base.Model = modelOverride
	}
	base = applyOverrides(base, callOpts)
	return base
}

func sleepWithContext(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
