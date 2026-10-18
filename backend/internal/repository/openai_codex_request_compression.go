package repository

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

func compressOpenAICodexRequest(req *http.Request) error {
	return openai.CompressCodexResponsesRequest(req)
}
