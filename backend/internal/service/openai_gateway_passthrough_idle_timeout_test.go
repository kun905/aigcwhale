package service

import (
	"context"
	"errors"
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

func runPassthroughIdleTimeoutTest(t *testing.T, keepaliveSeconds, timeoutSeconds int) (<-chan error, *httptest.ResponseRecorder, io.WriteCloser) {
	t.Helper()
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
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize:               defaultMaxLineSize,
		StreamKeepaliveInterval:   keepaliveSeconds,
		StreamDataIntervalTimeout: timeoutSeconds,
	}}}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Name: "idle-timeout-test", Type: AccountTypeAPIKey}
	resultCh := make(chan error, 1)
	go func() {
		_, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "gpt-5", "gpt-5")
		resultCh <- err
	}()
	return resultCh, recorder, pw
}

func TestOpenAIStreamingPassthroughReturnsFailoverBeforeSemanticOutput(t *testing.T) {
	resultCh, recorder, upstream := runPassthroughIdleTimeoutTest(t, 0, 1)
	defer upstream.Close()

	select {
	case err := <-resultCh:
		require.Error(t, err)
		var failoverErr *UpstreamFailoverError
		require.True(t, errors.As(err, &failoverErr), "idle pre-output streams should remain eligible for failover")
		require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("passthrough did not terminate an idle upstream stream")
	}

	// No semantic event may be appended before the handler gets a chance to
	// replay the request against another account.
	require.Empty(t, recorder.Body.String())
}

func TestOpenAIStreamingPassthroughWritesTerminalFailureAfterOutput(t *testing.T) {
	resultCh, recorder, upstream := runPassthroughIdleTimeoutTest(t, 0, 1)
	defer upstream.Close()

	_, err := upstream.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return strings.Contains(recorder.Body.String(), "response.output_text.delta")
	}, time.Second, 10*time.Millisecond)

	select {
	case err := <-resultCh:
		require.Error(t, err)
		require.Contains(t, err.Error(), "stream data interval timeout")
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("passthrough did not finish after the configured idle timeout")
	}

	body := recorder.Body.String()
	require.Equal(t, 1, strings.Count(body, "event: response.failed"))
	require.Contains(t, body, "upstream stream idle for 1s")
}

func TestOpenAIStreamingPassthroughKeepaliveRunsWhileUpstreamReadBlocks(t *testing.T) {
	resultCh, recorder, upstream := runPassthroughIdleTimeoutTest(t, 1, 2)
	defer upstream.Close()

	select {
	case err := <-resultCh:
		t.Fatalf("passthrough returned before the idle timeout: %v", err)
	case <-time.After(1500 * time.Millisecond):
	}
	require.Contains(t, recorder.Body.String(), ": keepalive\n\n")

	select {
	case err := <-resultCh:
		require.Error(t, err)
		var failoverErr *UpstreamFailoverError
		require.True(t, errors.As(err, &failoverErr), "keepalive-only timeout should remain eligible for failover")
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("passthrough did not finish after the configured idle timeout")
	}
	require.NotContains(t, recorder.Body.String(), "event: response.failed")
}
