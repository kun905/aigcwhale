package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIStreamingPassthroughSendsKeepaliveDuringIdle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       pr,
	}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{
			MaxLineSize:             defaultMaxLineSize,
			StreamKeepaliveInterval: 1,
		}},
	}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	resultCh := make(chan error, 1)
	go func() {
		_, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "gpt-5", "gpt-5")
		resultCh <- err
	}()
	defer func() {
		_ = pw.Close()
		_ = pr.Close()
	}()

	select {
	case <-time.After(1500 * time.Millisecond):
	case err := <-resultCh:
		t.Fatalf("passthrough returned before the delayed upstream terminal event: %v", err)
	}
	require.Contains(t, recorder.Body.String(), ":\n\n", "an idle upstream must produce a downstream SSE comment")

	_, err := pw.Write([]byte("data: [DONE]\n\n"))
	require.NoError(t, err)
	require.NoError(t, pw.Close())
	require.NoError(t, <-resultCh)
	require.Contains(t, recorder.Body.String(), "data: [DONE]\n\n")
	require.Equal(t, 1, strings.Count(recorder.Body.String(), ":\n\n"), "the short test window should emit one keepalive")
}
