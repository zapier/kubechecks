package aisummary

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/rs/zerolog/log"
	"github.com/sashabaranov/go-openai"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
)

type OpenAiClient struct {
	client  *openai.Client
	enabled bool
}

var openAiClient *OpenAiClient
var once sync.Once

func GetOpenAiClient() *OpenAiClient {
	once.Do(func() {
		apiToken := viper.GetString("openai-api-token")
		if apiToken != "" {
			log.Info().Msg("enabling OpenAI client")
			client := openai.NewClient(apiToken)
			openAiClient = &OpenAiClient{client: client, enabled: true}
		} else {
			log.Debug().Msg("OpenAI client not enabled")
			openAiClient = &OpenAiClient{enabled: false}
		}
	})
	return openAiClient
}

func createCompletionRequest(model, appName string, prompt string, content string, prefix string) openai.ChatCompletionRequest {
	var summarizeRequest = openai.ChatCompletionRequest{
		Model:       model,
		MaxTokens:   500,
		Temperature: 0.4,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: completionSystemPrompt,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: fmt.Sprintf(prompt, appName, content),
			},
			{
				Role:    openai.ChatMessageRoleAssistant,
				Content: prefix,
			},
		},
		Stream: false,
	}
	return summarizeRequest
}

func (c *OpenAiClient) makeCompletionRequestWithBackoff(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	ctx, span := otel.Tracer("Kubechecks").Start(ctx, "MakeCompletionRequestWithBackoff")
	defer span.End()
	// Lets setup backoff logic to retry this request for 1 minute
	bOff := backoff.NewExponentialBackOff()
	bOff.MaxInterval = 10 * time.Second
	bOff.RandomizationFactor = 0
	bOff.MaxElapsedTime = 2 * time.Minute

	var resp openai.ChatCompletionResponse
	err := backoff.Retry(func() error {
		var err error
		resp, err = c.client.CreateChatCompletion(ctx, req)
		if err != nil {
			if !strings.Contains(err.Error(), "status code: 429") && !strings.Contains(err.Error(), "status code: 5") {
				return backoff.Permanent(err)
			}
			log.Debug().Msgf("%v - %s", resp, err)
		}

		return err
	}, bOff)
	return resp, err
}
