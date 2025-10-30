package bot

import (
    "context"
    legacyai "github.com/stake-plus/govcomms/src/ai-qa/components/ai"
    sharedai "github.com/stake-plus/govcomms/src/shared/ai"
)

type legacyAdapter struct{ c legacyai.Client }

func (l legacyAdapter) AnswerQuestion(ctx context.Context, content string, question string, _ sharedai.Options) (string, error) {
    return l.c.Ask(content, question)
}


