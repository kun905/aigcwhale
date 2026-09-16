package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type openAIWSImageRoutingGroupRepoStub struct {
	service.GroupRepository
	groups map[int64]*service.Group
}

func (s *openAIWSImageRoutingGroupRepoStub) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	group := s.groups[id]
	if group == nil {
		return nil, service.ErrGroupNotFound
	}
	copy := *group
	return &copy, nil
}

type openAIWSImageRoutingGatewayCacheStub struct {
	service.GatewayCache
	mu       sync.Mutex
	bindings map[string]int64
	getErr   error
}

func newOpenAIWSImageRoutingGatewayCacheStub() *openAIWSImageRoutingGatewayCacheStub {
	return &openAIWSImageRoutingGatewayCacheStub{bindings: make(map[string]int64)}
}

func (s *openAIWSImageRoutingGatewayCacheStub) bindingKey(groupID int64, sessionHash string) string {
	return fmt.Sprintf("%d|%s", groupID, sessionHash)
}

func (s *openAIWSImageRoutingGatewayCacheStub) GetSessionAccountID(_ context.Context, groupID int64, sessionHash string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return 0, s.getErr
	}
	accountID := s.bindings[s.bindingKey(groupID, sessionHash)]
	if accountID <= 0 {
		return 0, service.ErrStickySessionNotFound
	}
	return accountID, nil
}

func (s *openAIWSImageRoutingGatewayCacheStub) setGetError(err error) {
	s.mu.Lock()
	s.getErr = err
	s.mu.Unlock()
}

func (s *openAIWSImageRoutingGatewayCacheStub) SetSessionAccountID(_ context.Context, groupID int64, sessionHash string, accountID int64, _ time.Duration) error {
	s.mu.Lock()
	s.bindings[s.bindingKey(groupID, sessionHash)] = accountID
	s.mu.Unlock()
	return nil
}

func (s *openAIWSImageRoutingGatewayCacheStub) RefreshSessionTTL(_ context.Context, groupID int64, sessionHash string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bindings[s.bindingKey(groupID, sessionHash)] <= 0 {
		return service.ErrStickySessionNotFound
	}
	return nil
}

func (s *openAIWSImageRoutingGatewayCacheStub) DeleteSessionAccountID(_ context.Context, groupID int64, sessionHash string) error {
	s.mu.Lock()
	delete(s.bindings, s.bindingKey(groupID, sessionHash))
	s.mu.Unlock()
	return nil
}

func TestOpenAIResponsesWebSocketImageRoutingPreviousResponsePoolBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		previousResponseID = "resp_previous_image_route"
		sourceGroupID      = int64(4201)
		targetGroupID      = int64(4202)
		accountID          = int64(9901)
	)

	tests := []struct {
		name                    string
		boundGroupID            int64
		lookupErr               error
		expectCloseCode         websocket.StatusCode
		expectCloseReason       string
		expectPreviousForwarded bool
	}{
		{
			name:              "binding only in text pool is rejected before upstream",
			boundGroupID:      sourceGroupID,
			expectCloseCode:   websocket.StatusPolicyViolation,
			expectCloseReason: "switching between text and image routing",
		},
		{
			name:                    "binding in image pool preserves continuation",
			boundGroupID:            targetGroupID,
			expectPreviousForwarded: true,
		},
		{
			name: "missing in both pools keeps migration behavior",
		},
		{
			name:              "routing lookup failure closes before upstream",
			lookupErr:         errors.New("routing cache unavailable"),
			expectCloseCode:   websocket.StatusTryAgainLater,
			expectCloseReason: "routing is temporarily unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstreamHit := make(chan struct{}, 1)
			upstreamPayload := make(chan []byte, 1)
			upstreamErr := make(chan error, 1)
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case upstreamHit <- struct{}{}:
				default:
				}
				conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
				if err != nil {
					upstreamErr <- err
					return
				}
				defer func() { _ = conn.CloseNow() }()

				readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
				msgType, payload, err := conn.Read(readCtx)
				cancelRead()
				if err != nil {
					upstreamErr <- err
					return
				}
				if msgType != websocket.MessageText && msgType != websocket.MessageBinary {
					upstreamErr <- fmt.Errorf("unexpected upstream websocket message type: %v", msgType)
					return
				}
				upstreamPayload <- append([]byte(nil), payload...)

				response := []byte(`{"type":"response.completed","response":{"id":"resp_image_route_result","model":"gpt-5.4","usage":{"input_tokens":2,"output_tokens":1}}}`)
				writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
				err = conn.Write(writeCtx, websocket.MessageText, response)
				cancelWrite()
				upstreamErr <- err
			}))
			defer upstreamServer.Close()

			sourceID := sourceGroupID
			targetID := targetGroupID
			sourceGroup := &service.Group{
				ID:                     sourceGroupID,
				Name:                   "text-source",
				Platform:               service.PlatformOpenAI,
				Status:                 service.StatusActive,
				Hydrated:               true,
				ImageGenerationGroupID: &targetID,
			}
			targetGroup := &service.Group{
				ID:                   targetGroupID,
				Name:                 "image-target",
				Platform:             service.PlatformOpenAI,
				Status:               service.StatusActive,
				Hydrated:             true,
				AllowImageGeneration: true,
			}
			groupRepo := &openAIWSImageRoutingGroupRepoStub{groups: map[int64]*service.Group{
				targetGroupID: targetGroup,
			}}
			channelRepo := &openAIWSUsageHandlerChannelRepoStub{groupPlatforms: map[int64]string{
				sourceGroupID: service.PlatformOpenAI,
				targetGroupID: service.PlatformOpenAI,
			}}
			channelSvc := service.NewChannelService(channelRepo, groupRepo, nil, nil, nil)

			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
			cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

			account := service.Account{
				ID:          accountID,
				Name:        "image-route-upstream",
				Platform:    service.PlatformOpenAI,
				Type:        service.AccountTypeAPIKey,
				Status:      service.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Credentials: map[string]any{
					"api_key":  "sk-test",
					"base_url": upstreamServer.URL,
				},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
					"openai_apikey_responses_websockets_v2_mode":    service.OpenAIWSIngressModePassthrough,
				},
			}
			accountRepo := &openAIWSUsageHandlerAccountRepoStub{account: account}
			usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 1)}
			gatewayCache := newOpenAIWSImageRoutingGatewayCacheStub()
			if tc.boundGroupID > 0 {
				seedStore := service.NewOpenAIWSStateStore(gatewayCache)
				require.NoError(t, seedStore.BindResponseAccount(
					context.Background(), tc.boundGroupID, previousResponseID, accountID, time.Minute,
				))
			}
			gatewayCache.setGetError(tc.lookupErr)
			concurrencyCache := &concurrencyCacheMock{
				acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
				acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
			}
			concurrencySvc := service.NewConcurrencyService(concurrencyCache)

			billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billingCacheSvc.Stop)
			gatewaySvc := service.NewOpenAIGatewayService(
				accountRepo,
				usageRepo,
				nil,
				nil,
				nil,
				nil,
				gatewayCache,
				cfg,
				nil,
				concurrencySvc,
				service.NewBillingService(cfg, nil),
				nil,
				billingCacheSvc,
				nil,
				&service.DeferredService{},
				nil,
				nil,
				nil,
				channelSvc,
				nil,
				nil,
				nil,
			)
			h := &OpenAIGatewayHandler{
				gatewayService:      gatewaySvc,
				billingCacheService: billingCacheSvc,
				apiKeyService:       &service.APIKeyService{},
				concurrencyHelper:   NewConcurrencyHelper(concurrencySvc, SSEPingFormatNone, time.Second),
			}

			apiKey := &service.APIKey{
				ID:      1801,
				GroupID: &sourceID,
				Group:   sourceGroup,
				User:    &service.User{ID: 1701, Status: service.StatusActive},
			}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), apiKey)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
				c.Next()
			})
			router.GET("/openai/v1/responses", h.ResponsesWebSocket)
			handlerServer := httptest.NewServer(router)
			defer handlerServer.Close()

			dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
			clientConn, _, err := websocket.Dial(
				dialCtx,
				"ws"+strings.TrimPrefix(handlerServer.URL, "http")+"/openai/v1/responses",
				&websocket.DialOptions{CompressionMode: websocket.CompressionContextTakeover},
			)
			cancelDial()
			require.NoError(t, err)
			defer func() { _ = clientConn.CloseNow() }()

			firstPayload := []byte(`{"type":"response.create","model":"gpt-5.4","previous_response_id":"` + previousResponseID + `","input":"draw","tools":[{"type":"image_generation"}]}`)
			writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
			err = clientConn.Write(writeCtx, websocket.MessageText, firstPayload)
			cancelWrite()
			require.NoError(t, err)

			readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
			_, event, readErr := clientConn.Read(readCtx)
			cancelRead()
			if tc.expectCloseCode != 0 {
				require.Error(t, readErr)
				var closeErr websocket.CloseError
				require.ErrorAs(t, readErr, &closeErr)
				require.Equal(t, tc.expectCloseCode, closeErr.Code)
				require.Contains(t, closeErr.Reason, tc.expectCloseReason)
				if tc.expectCloseCode == websocket.StatusPolicyViolation {
					require.Contains(t, closeErr.Reason, "start a new response")
				}
				select {
				case <-upstreamHit:
					t.Fatal("cross-pool continuation reached the upstream websocket")
				case <-time.After(150 * time.Millisecond):
				}
				return
			}

			require.NoError(t, readErr)
			require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
			select {
			case payload := <-upstreamPayload:
				if tc.expectPreviousForwarded {
					require.Equal(t, previousResponseID, gjson.GetBytes(payload, "previous_response_id").String())
				} else {
					require.False(t, gjson.GetBytes(payload, "previous_response_id").Exists())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("upstream websocket did not receive the first response.create frame")
			}
			select {
			case err := <-upstreamErr:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("upstream websocket did not finish the test turn")
			}
		})
	}
}

func TestOpenAIResponsesImageRoutingPreviousResponseLookupFailureFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		previousResponseID = "resp_previous_image_route_cache_failure"
		sourceGroupID      = int64(4205)
		targetGroupID      = int64(4206)
		accountID          = int64(9905)
		userID             = int64(1705)
		apiKeyID           = int64(1805)
	)

	sourceID := sourceGroupID
	targetID := targetGroupID
	sourceGroup := &service.Group{
		ID:                     sourceGroupID,
		Name:                   "text-source",
		Platform:               service.PlatformOpenAI,
		Status:                 service.StatusActive,
		Hydrated:               true,
		ImageGenerationGroupID: &targetID,
	}
	targetGroup := &service.Group{
		ID:                   targetGroupID,
		Name:                 "image-target",
		Platform:             service.PlatformOpenAI,
		Status:               service.StatusActive,
		Hydrated:             true,
		AllowImageGeneration: true,
	}
	groupRepo := &openAIWSImageRoutingGroupRepoStub{groups: map[int64]*service.Group{
		targetGroupID: targetGroup,
	}}
	channelRepo := &openAIWSUsageHandlerChannelRepoStub{groupPlatforms: map[int64]string{
		sourceGroupID: service.PlatformOpenAI,
		targetGroupID: service.PlatformOpenAI,
	}}
	channelSvc := service.NewChannelService(channelRepo, groupRepo, nil, nil, nil)

	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	accountRepo := &openAIWSUsageHandlerAccountRepoStub{account: service.Account{
		ID:          accountID,
		Name:        "image-route-http-upstream",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://api.example.test",
		},
		Extra: map[string]any{"openai_passthrough": true},
	}}
	gatewayCache := newOpenAIWSImageRoutingGatewayCacheStub()
	upstream := &openAIHTTPPassthroughFailoverUpstream{}
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)
	concurrencySvc := service.NewConcurrencyService(nil)
	gatewaySvc := service.NewOpenAIGatewayService(
		accountRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		gatewayCache,
		cfg,
		nil,
		concurrencySvc,
		service.NewBillingService(cfg, nil),
		nil,
		billingCacheSvc,
		upstream,
		&service.DeferredService{},
		nil,
		nil,
		nil,
		channelSvc,
		nil,
		nil,
		nil,
	)
	require.NoError(t, gatewaySvc.BindOpenAIHTTPResponseOwner(
		context.Background(), sourceGroupID, previousResponseID, userID, apiKeyID,
	))
	gatewayCache.setGetError(errors.New("routing cache unavailable"))

	h := NewOpenAIGatewayHandler(
		gatewaySvc,
		concurrencySvc,
		billingCacheSvc,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg,
	)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/openai/v1/responses",
		strings.NewReader(`{"model":"gpt-5.4","previous_response_id":"`+previousResponseID+`","input":"draw","tools":[{"type":"image_generation"}],"stream":false}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID:      apiKeyID,
		GroupID: &sourceID,
		Group:   sourceGroup,
		User:    &service.User{ID: userID, Status: service.StatusActive},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 1})

	h.Responses(c)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "service_unavailable", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Contains(t, gjson.GetBytes(rec.Body.Bytes(), "error.message").String(), "previous_response_id routing")
	require.Empty(t, upstream.calls(), "routing lookup failure must stop before selecting or contacting an upstream")
}

func TestOpenAIResponsesWebSocketImageRoutingPreviousResponseAffinityFailoverStripsID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		previousResponseID = "resp_previous_image_route_failover"
		sourceGroupID      = int64(4211)
		targetGroupID      = int64(4212)
		boundAccountID     = int64(9921)
		fallbackAccountID  = int64(9922)
	)

	firstPayload := make(chan []byte, 1)
	secondPayload := make(chan []byte, 1)
	upstreamErr := make(chan error, 2)

	boundUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
		if err != nil {
			upstreamErr <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, payload, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			upstreamErr <- err
			return
		}
		firstPayload <- append([]byte(nil), payload...)

		writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
		err = conn.Write(writeCtx, websocket.MessageText, []byte(`{"type":"error","error":{"code":"rate_limit_exceeded","type":"usage_limit_reached","message":"The usage limit has been reached"}}`))
		cancelWrite()
		upstreamErr <- err
	}))
	defer boundUpstream.Close()

	fallbackUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
		if err != nil {
			upstreamErr <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, payload, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			upstreamErr <- err
			return
		}
		secondPayload <- append([]byte(nil), payload...)

		response := []byte(`{"type":"response.completed","response":{"id":"resp_image_route_failover_result","model":"gpt-5.4","usage":{"input_tokens":2,"output_tokens":1}}}`)
		writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
		err = conn.Write(writeCtx, websocket.MessageText, response)
		cancelWrite()
		upstreamErr <- err
	}))
	defer fallbackUpstream.Close()

	sourceID := sourceGroupID
	targetID := targetGroupID
	sourceGroup := &service.Group{
		ID:                     sourceGroupID,
		Name:                   "text-source-failover",
		Platform:               service.PlatformOpenAI,
		Status:                 service.StatusActive,
		Hydrated:               true,
		ImageGenerationGroupID: &targetID,
	}
	targetGroup := &service.Group{
		ID:                   targetGroupID,
		Name:                 "image-target-failover",
		Platform:             service.PlatformOpenAI,
		Status:               service.StatusActive,
		Hydrated:             true,
		AllowImageGeneration: true,
	}
	groupRepo := &openAIWSImageRoutingGroupRepoStub{groups: map[int64]*service.Group{
		targetGroupID: targetGroup,
	}}
	channelRepo := &openAIWSUsageHandlerChannelRepoStub{groupPlatforms: map[int64]string{
		sourceGroupID: service.PlatformOpenAI,
		targetGroupID: service.PlatformOpenAI,
	}}
	channelSvc := service.NewChannelService(channelRepo, groupRepo, nil, nil, nil)

	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	cfg.Gateway.MaxAccountSwitches = 3

	accounts := []service.Account{
		{
			ID:          boundAccountID,
			Name:        "bound-image-upstream",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    2,
			Credentials: map[string]any{
				"api_key":  "sk-bound",
				"base_url": boundUpstream.URL,
			},
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
				"openai_apikey_responses_websockets_v2_mode":    service.OpenAIWSIngressModePassthrough,
			},
		},
		{
			ID:          fallbackAccountID,
			Name:        "fallback-image-upstream",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
			Credentials: map[string]any{
				"api_key":  "sk-fallback",
				"base_url": fallbackUpstream.URL,
			},
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
				"openai_apikey_responses_websockets_v2_mode":    service.OpenAIWSIngressModePassthrough,
			},
		},
	}
	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
	usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 1)}
	gatewayCache := newOpenAIWSImageRoutingGatewayCacheStub()
	seedStore := service.NewOpenAIWSStateStore(gatewayCache)
	require.NoError(t, seedStore.BindResponseAccount(
		context.Background(), targetGroupID, previousResponseID, boundAccountID, time.Minute,
	))
	rateLimitSvc := service.NewRateLimitService(accountRepo, nil, cfg, nil, nil)
	concurrencyCache := &concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	concurrencySvc := service.NewConcurrencyService(concurrencyCache)

	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)
	gatewaySvc := service.NewOpenAIGatewayService(
		accountRepo,
		usageRepo,
		nil,
		nil,
		nil,
		nil,
		gatewayCache,
		cfg,
		nil,
		concurrencySvc,
		service.NewBillingService(cfg, nil),
		rateLimitSvc,
		billingCacheSvc,
		nil,
		&service.DeferredService{},
		nil,
		nil,
		nil,
		channelSvc,
		nil,
		nil,
		nil,
	)
	h := &OpenAIGatewayHandler{
		gatewayService:      gatewaySvc,
		billingCacheService: billingCacheSvc,
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   NewConcurrencyHelper(concurrencySvc, SSEPingFormatNone, time.Second),
		maxAccountSwitches:  3,
	}

	apiKey := &service.APIKey{
		ID:      1811,
		GroupID: &sourceID,
		Group:   sourceGroup,
		User:    &service.User{ID: 1711, Status: service.StatusActive},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	router.GET("/openai/v1/responses", h.ResponsesWebSocket)
	handlerServer := httptest.NewServer(router)
	defer handlerServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := websocket.Dial(
		dialCtx,
		"ws"+strings.TrimPrefix(handlerServer.URL, "http")+"/openai/v1/responses",
		&websocket.DialOptions{CompressionMode: websocket.CompressionContextTakeover},
	)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	request := []byte(`{"type":"response.create","model":"gpt-5.4","previous_response_id":"` + previousResponseID + `","input":"draw","tools":[{"type":"image_generation"}]}`)
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, websocket.MessageText, request)
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 6*time.Second)
	_, event, err := clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
	require.Equal(t, "resp_image_route_failover_result", gjson.GetBytes(event, "response.id").String())

	select {
	case payload := <-firstPayload:
		require.Equal(t, previousResponseID, gjson.GetBytes(payload, "previous_response_id").String())
	case <-time.After(3 * time.Second):
		t.Fatal("bound image upstream did not receive the continuation frame")
	}
	select {
	case payload := <-secondPayload:
		require.False(t, gjson.GetBytes(payload, "previous_response_id").Exists())
	case <-time.After(3 * time.Second):
		t.Fatal("fallback image upstream did not receive the replayed frame")
	}
	require.Equal(t, []int64{boundAccountID}, accountRepo.rateLimitedIDs)
	for range 2 {
		select {
		case err := <-upstreamErr:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("upstream websocket did not finish the failover attempt")
		}
	}
}
