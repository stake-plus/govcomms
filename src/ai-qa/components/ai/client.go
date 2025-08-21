package ai

type Client interface {
	Ask(content string, question string) (string, error)
}
