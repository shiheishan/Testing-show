package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ResultCache struct {
	mu    sync.RWMutex
	items map[int]CheckState
}

func NewResultCache() *ResultCache {
	return &ResultCache{items: map[int]CheckState{}}
}

func (c *ResultCache) Load(items map[int]CheckState, err error) error {
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = items
	return nil
}

func (c *ResultCache) Get(nodeID int) (CheckState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, ok := c.items[nodeID]
	return item, ok
}

func (c *ResultCache) UpdateMany(results []CheckResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, result := range results {
		c.items[result.NodeID] = normalizeCheckState(CheckState{
			Status:             result.Status,
			LatencyMS:          result.LatencyMS,
			TransportStatus:    result.TransportStatus,
			TransportLatencyMS: result.TransportLatencyMS,
			ProxyStatus:        result.ProxyStatus,
			ProxyLatencyMS:     result.ProxyLatencyMS,
			StatusSource:       result.StatusSource,
			StatusMessage:      result.StatusMessage,
			LastChecked:        result.CheckedAt,
		})
	}
}

type SubscriptionService struct {
	store  *Store
	config Config
}

func NewSubscriptionService(store *Store, config Config) *SubscriptionService {
	return &SubscriptionService{store: store, config: config}
}

func (s *SubscriptionService) SyncConfiguredSubscriptions(subscriptions []ConfiguredSubscription) error {
	s.store.LockMaintenance()
	defer s.store.UnlockMaintenance()
	if len(subscriptions) == 0 {
		return s.store.DeleteSubscriptionsNotInIDs(nil)
	}

	type syncResult struct {
		id  int
		url string
		err error
	}
	results := make([]syncResult, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		id, err := s.syncConfiguredSubscription(subscription)
		results = append(results, syncResult{
			id:  id,
			url: strings.TrimSpace(subscription.URL),
			err: err,
		})
	}

	failures := make([]string, 0)
	activeIDs := make([]int, 0, len(results))
	for _, result := range results {
		if result.id != 0 {
			activeIDs = append(activeIDs, result.id)
		}
		if result.err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", result.url, result.err))
		}
	}
	if err := s.store.DeleteSubscriptionsNotInIDs(activeIDs); err != nil {
		return err
	}
	if len(failures) > 0 {
		return fmt.Errorf("subscription refresh finished with %d failure(s): %s", len(failures), strings.Join(failures, "; "))
	}
	return nil
}

func (s *SubscriptionService) AddSubscription(rawURL, name string) (*Subscription, UpsertStats, ImportResult, error) {
	rawURL = strings.TrimSpace(rawURL)
	if strings.TrimSpace(name) == "" {
		name = deriveSubscriptionName(rawURL)
	}
	result, apiErr := s.loadSubscriptionNodes(rawURL)
	if apiErr.Status != 0 {
		return nil, UpsertStats{}, ImportResult{}, apiErr
	}
	item, err := s.store.CreateSubscription(name, rawURL)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: subscriptions.url") {
			return nil, UpsertStats{}, ImportResult{}, APIError{Code: "bad_request", Message: "该订阅已经存在", Status: http.StatusBadRequest}
		}
		return nil, UpsertStats{}, ImportResult{}, err
	}
	stats, err := s.store.UpsertNodes(item.ID, result.Nodes)
	if err != nil {
		_, _ = s.store.DeleteSubscription(item.ID)
		return nil, UpsertStats{}, ImportResult{}, err
	}
	if err := s.store.MarkSubscriptionSuccess(item.ID); err != nil {
		_, _ = s.store.DeleteSubscription(item.ID)
		return nil, UpsertStats{}, ImportResult{}, err
	}
	item, err = s.store.GetSubscription(item.ID)
	if err != nil {
		return nil, UpsertStats{}, ImportResult{}, err
	}
	return item, stats, result, nil
}

func (s *SubscriptionService) RefreshSubscription(id int) (UpsertStats, ImportResult, error) {
	subscription, err := s.store.GetSubscription(id)
	if err != nil {
		return UpsertStats{}, ImportResult{}, err
	}
	if subscription == nil {
		return UpsertStats{}, ImportResult{}, APIError{Code: "not_found", Message: "订阅不存在", Status: http.StatusNotFound}
	}
	result, apiErr := s.loadSubscriptionNodes(subscription.URL)
	if apiErr.Status != 0 {
		_ = s.store.MarkSubscriptionError(id, apiErr.Message)
		return UpsertStats{}, ImportResult{}, apiErr
	}
	stats, err := s.store.UpsertNodes(id, result.Nodes)
	if err != nil {
		return UpsertStats{}, ImportResult{}, err
	}
	if err := s.store.MarkSubscriptionSuccess(id); err != nil {
		return UpsertStats{}, ImportResult{}, err
	}
	return stats, result, nil
}

func (s *SubscriptionService) syncConfiguredSubscription(subscription ConfiguredSubscription) (int, error) {
	rawURL := strings.TrimSpace(subscription.URL)
	if rawURL == "" {
		return 0, fmt.Errorf("subscription url is required")
	}
	name := strings.TrimSpace(subscription.Name)
	if name == "" {
		name = deriveSubscriptionName(rawURL)
	}

	item, err := s.resolveConfiguredSubscription(name, rawURL)
	if err != nil {
		return 0, err
	}
	if item == nil {
		item, err = s.store.CreateSubscription(name, rawURL)
		if err != nil {
			return 0, err
		}
	} else if item.Name != name || item.URL != rawURL {
		item, err = s.store.UpdateSubscriptionIdentity(item.ID, name, rawURL)
		if err != nil {
			return 0, err
		}
	}
	result, apiErr := s.loadSubscriptionNodes(rawURL)
	if apiErr.Status != 0 {
		if err := s.store.MarkSubscriptionError(item.ID, apiErr.Message); err != nil {
			return item.ID, err
		}
		return item.ID, apiErr
	}
	if _, err := s.store.UpsertNodes(item.ID, result.Nodes); err != nil {
		_ = s.store.MarkSubscriptionError(item.ID, err.Error())
		return item.ID, err
	}
	if err := s.store.MarkSubscriptionSuccess(item.ID); err != nil {
		return item.ID, err
	}
	return item.ID, nil
}

func (s *SubscriptionService) resolveConfiguredSubscription(name, rawURL string) (*Subscription, error) {
	item, err := s.store.GetSubscriptionByURL(rawURL)
	if err != nil || item != nil {
		return item, err
	}
	if strings.TrimSpace(name) == "" {
		return nil, nil
	}
	items, err := s.store.ListSubscriptionsByName(name)
	if err != nil {
		return nil, err
	}
	if len(items) != 1 {
		return nil, nil
	}
	return &items[0], nil
}

func (s *SubscriptionService) loadSubscriptionNodes(rawURL string) (ImportResult, APIError) {
	content, apiErr := s.fetchContent(rawURL)
	if apiErr.Status != 0 {
		return ImportResult{}, apiErr
	}
	result, parseErr := ParseSubscriptionDetailed(content)
	if parseErr != nil {
		switch {
		case errors.Is(parseErr, errClashConfigNoProxies):
			return result, APIError{
				Code:    "empty_subscription",
				Message: "订阅内容是 Clash 配置，但没有任何节点（proxies 为空）",
				Status:  http.StatusBadRequest,
			}
		case errors.Is(parseErr, errNoSupportedNodes):
			message := result.Summary
			if strings.TrimSpace(message) == "" || message == "订阅内容里没有可识别的节点" {
				message = "订阅内容里没有可识别的节点，当前支持 Clash 节点列表和按行 URI 订阅"
			}
			return result, APIError{
				Code:    "unsupported_format",
				Message: message,
				Status:  http.StatusBadRequest,
			}
		}
		return result, APIError{
			Code:    "unsupported_format",
			Message: "无法识别订阅内容格式，仅支持 Clash YAML 和 V2Ray base64",
			Status:  http.StatusBadRequest,
		}
	}
	return result, APIError{}
}

func (s *SubscriptionService) fetchContent(rawURL string) (string, APIError) {
	parsed, apiErr := parseSubscriptionURL(rawURL)
	if apiErr.Status != 0 {
		return "", apiErr
	}
	if parsed.Scheme == "file" {
		filePath := parsed.Path
		if filePath == "" {
			filePath = parsed.Opaque
		}
		if decodedPath, err := url.PathUnescape(filePath); err == nil {
			filePath = decodedPath
		}
		content, err := osReadFile(filePath)
		if err != nil {
			return "", APIError{Code: "bad_gateway", Message: "本地订阅文件不可读", Status: http.StatusBadGateway}
		}
		return string(content), APIError{}
	}
	transport := &http.Transport{}
	if s.config.SubscriptionProxy != "" {
		proxyURL, err := url.Parse(s.config.SubscriptionProxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	client := &http.Client{
		Timeout:   s.config.SubscriptionTimeout,
		Transport: transport,
	}
	request, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", APIError{Code: "bad_request", Message: "订阅 URL 非法，请使用 http(s):// 或 file://", Status: http.StatusBadRequest}
	}
	request.Header.Set("User-Agent", s.config.SubscriptionUA)
	response, err := client.Do(request)
	if err != nil {
		return "", APIError{Code: "bad_gateway", Message: "订阅 URL 不可达，请检查链接、网络或订阅服务状态", Status: http.StatusBadGateway}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message := fmt.Sprintf("订阅拉取失败，HTTP %d", response.StatusCode)
		if response.StatusCode == http.StatusForbidden {
			message = "订阅服务拒绝访问（HTTP 403），可能需要调整 User-Agent 或请求来源 IP"
		}
		return "", APIError{Code: "bad_gateway", Message: message, Status: http.StatusBadGateway}
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", APIError{Code: "bad_gateway", Message: "订阅内容读取失败", Status: http.StatusBadGateway}
	}
	return string(body), APIError{}
}

func parseSubscriptionURL(rawURL string) (*url.URL, APIError) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, APIError{Code: "bad_request", Message: "订阅 URL 非法，请使用 http(s):// 或 file://", Status: http.StatusBadRequest}
	}
	switch parsed.Scheme {
	case "http", "https":
		if parsed.Host == "" {
			return nil, APIError{Code: "bad_request", Message: "订阅 URL 非法，请使用 http(s):// 或 file://", Status: http.StatusBadRequest}
		}
		return parsed, APIError{}
	case "file":
		if parsed.Path == "" && parsed.Opaque == "" {
			return nil, APIError{Code: "bad_request", Message: "订阅 URL 非法，请使用 http(s):// 或 file://", Status: http.StatusBadRequest}
		}
		return parsed, APIError{}
	default:
		return nil, APIError{Code: "bad_request", Message: "订阅 URL 非法，请使用 http(s):// 或 file://", Status: http.StatusBadRequest}
	}
}

type CheckService struct {
	store         *Store
	cache         *ResultCache
	config        Config
	proxyRunner   ProxyDelayRunner
	mu            sync.Mutex
	active        bool
	lastManualRun map[string]time.Time
}

func NewCheckService(store *Store, cache *ResultCache, config Config) *CheckService {
	return &CheckService{
		store:         store,
		cache:         cache,
		config:        config,
		proxyRunner:   NewProxyDelayRunner(config),
		lastManualRun: map[string]time.Time{},
	}
}

type CheckStartResult struct {
	Total  int
	Status string
}

type ProbeResult struct {
	Status    string
	LatencyMS *int
	Message   string
}

type ProxyDelayRunner interface {
	Check(nodes []NodeRecord, timeout time.Duration) (map[int]ProbeResult, error)
}

func (s *CheckService) StartAsyncCheck(subscriptionID *int, nodeID *int) (CheckStartResult, error) {
	scope := "all"
	if subscriptionID != nil {
		scope = fmt.Sprintf("sub:%d", *subscriptionID)
	}
	if nodeID != nil {
		scope = fmt.Sprintf("node:%d", *nodeID)
	}

	total, err := s.store.CountActiveNodes(subscriptionID)
	if err != nil {
		return CheckStartResult{}, err
	}
	if nodeID != nil {
		total = 1
	}
	if total == 0 {
		return CheckStartResult{Status: "empty"}, nil
	}

	now := time.Now()
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return CheckStartResult{Total: total, Status: "running"}, nil
	}
	if lastRun, ok := s.lastManualRun[scope]; ok && s.config.ManualCheckCooldown > 0 && now.Sub(lastRun) < s.config.ManualCheckCooldown {
		s.mu.Unlock()
		return CheckStartResult{Total: total, Status: "cached"}, nil
	}
	s.active = true
	s.lastManualRun[scope] = now
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			s.active = false
			s.mu.Unlock()
		}()
		if _, err := s.RunCheck(subscriptionID, nodeID); err != nil {
			return
		}
	}()
	return CheckStartResult{Total: total, Status: "started"}, nil
}

func (s *CheckService) RunCheck(subscriptionID *int, nodeID *int) ([]CheckResult, error) {
	s.store.LockMaintenance()
	defer s.store.UnlockMaintenance()

	nodes, err := s.store.ListNodes(subscriptionID, false)
	if err != nil {
		return nil, err
	}
	if nodeID != nil {
		filtered := nodes[:0]
		for _, node := range nodes {
			if node.ID == *nodeID {
				filtered = append(filtered, node)
				break
			}
		}
		nodes = filtered
	}
	if len(nodes) == 0 {
		return nil, nil
	}
	proxyResults := map[int]ProbeResult{}
	proxyErrMessage := ""
	if s.proxyRunner != nil {
		items, err := s.proxyRunner.Check(nodes, s.config.CheckTimeout)
		if err != nil {
			proxyErrMessage = err.Error()
		} else {
			proxyResults = items
		}
	}

	sem := make(chan struct{}, s.config.CheckConcurrency)
	transportResults := make(chan struct {
		node   NodeRecord
		result ProbeResult
	}, len(nodes))
	var wg sync.WaitGroup

	for _, node := range nodes {
		wg.Add(1)
		node := node
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			transportResults <- struct {
				node   NodeRecord
				result ProbeResult
			}{node: node, result: s.checkTransport(node)}
		}()
	}

	wg.Wait()
	close(transportResults)

	items := make([]CheckResult, 0, len(nodes))
	for item := range transportResults {
		proxyResult, ok := proxyResults[item.node.ID]
		if !ok {
			proxyResult = ProbeResult{Status: "unknown"}
			if proxyErrMessage != "" {
				proxyResult.Message = proxyErrMessage
			}
		}
		items = append(items, combineProbeResults(item.node.ID, item.result, proxyResult, time.Now().UTC().Format(time.RFC3339)))
	}
	if err := s.store.InsertCheckResults(items, s.config.CheckRetention); err != nil {
		return nil, err
	}
	s.cache.UpdateMany(items)
	return items, nil
}

func (s *CheckService) checkTransport(node NodeRecord) ProbeResult {
	started := time.Now()
	address := netJoinHostPort(node.Server, strconv.Itoa(node.Port))
	connection, err := netDialTimeout("tcp", address, s.config.CheckTimeout)
	if err == nil {
		_ = connection.Close()
		latency := int(time.Since(started).Milliseconds())
		return ProbeResult{Status: "online", LatencyMS: &latency}
	}
	status := "offline"
	if isTimeoutError(err) {
		status = "timeout"
	}
	return ProbeResult{Status: status, Message: err.Error()}
}

func combineProbeResults(nodeID int, transport ProbeResult, proxy ProbeResult, checkedAt string) CheckResult {
	transport.Status = normalizeCheckStatus(transport.Status)
	proxy.Status = normalizeCheckStatus(proxy.Status)
	status := transport.Status
	latency := transport.LatencyMS
	source := "transport"
	message := transport.Message
	if proxy.Status != "unknown" {
		status = proxy.Status
		latency = proxy.LatencyMS
		source = "proxy"
		message = proxy.Message
	} else if strings.TrimSpace(proxy.Message) != "" {
		message = proxy.Message
	}
	var messagePtr *string
	if strings.TrimSpace(message) != "" {
		clean := strings.TrimSpace(message)
		messagePtr = &clean
	}
	return CheckResult{
		NodeID:             nodeID,
		Status:             status,
		LatencyMS:          latency,
		TransportStatus:    transport.Status,
		TransportLatencyMS: transport.LatencyMS,
		ProxyStatus:        proxy.Status,
		ProxyLatencyMS:     proxy.LatencyMS,
		StatusSource:       source,
		StatusMessage:      messagePtr,
		CheckedAt:          checkedAt,
	}
}

type Scheduler struct {
	stop     chan struct{}
	interval time.Duration
	runner   func()
}

func NewScheduler(interval time.Duration, runner func()) *Scheduler {
	return &Scheduler{
		stop:     make(chan struct{}),
		interval: interval,
		runner:   runner,
	}
}

func (s *Scheduler) Start() {
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runner()
			case <-s.stop:
				return
			}
		}
	}()
}

func (s *Scheduler) Stop() {
	close(s.stop)
}

type APIError struct {
	Code    string `json:"error"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

func (e APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

type ServerDeps struct {
	Config              Config
	Store               *Store
	Cache               *ResultCache
	SubscriptionService *SubscriptionService
	CheckService        *CheckService
	StaticFS            fs.FS
}

func NewServer(deps ServerDeps) http.Handler {
	mux := http.NewServeMux()
	staticServer := http.FileServer(http.FS(deps.StaticFS))

	mux.HandleFunc("/api/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, APIError{Code: "not_found", Message: "接口不存在", Status: http.StatusNotFound})
			return
		}
		items, err := deps.Store.ListSubscriptions()
		if err != nil {
			writeAPIError(w, apiInternalError(err))
			return
		}
		response := make([]map[string]any, 0, len(items))
		for _, item := range items {
			response = append(response, map[string]any{
				"id":                item.ID,
				"name":              item.Name,
				"url":               maskSubscriptionURL(item.URL),
				"created_at":        item.CreatedAt,
				"last_refreshed_at": item.LastRefreshedAt,
				"last_error":        item.LastError,
				"status":            item.Status,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscriptions": response})
	})

	mux.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, APIError{Code: "not_found", Message: "接口不存在", Status: http.StatusNotFound})
			return
		}
		query := r.URL.Query()
		subscriptionID, err := optionalInt(query.Get("sub_id"))
		if err != nil {
			writeAPIError(w, APIError{Code: "bad_request", Message: "sub_id 非法", Status: http.StatusBadRequest})
			return
		}
		includeStale := strings.EqualFold(query.Get("include_stale"), "true")
		nodes, err := deps.Store.ListNodes(subscriptionID, includeStale)
		if err != nil {
			writeAPIError(w, apiInternalError(err))
			return
		}
		responseNodes := make([]map[string]any, 0, len(nodes))
		counts := map[string]int{"online": 0, "offline": 0, "timeout": 0}
		for _, node := range nodes {
			status := "unknown"
			var latency *int
			var lastChecked *string
			state := normalizeCheckState(CheckState{})
			if cached, ok := deps.Cache.Get(node.ID); ok {
				state = cached
				status = state.Status
				latency = state.LatencyMS
				lastChecked = &state.LastChecked
				if _, exists := counts[status]; exists {
					counts[status]++
				}
			}
			responseNodes = append(responseNodes, map[string]any{
				"id":                   node.ID,
				"subscription_id":      node.SubscriptionID,
				"display_order":        node.DisplayOrder,
				"name":                 node.Name,
				"server":               node.Server,
				"port":                 node.Port,
				"protocol":             node.Protocol,
				"status":               status,
				"latency_ms":           latency,
				"transport_status":     state.TransportStatus,
				"transport_latency_ms": state.TransportLatencyMS,
				"proxy_status":         state.ProxyStatus,
				"proxy_latency_ms":     state.ProxyLatencyMS,
				"status_source":        state.StatusSource,
				"status_message":       state.StatusMessage,
				"last_checked":         lastChecked,
				"stale_since":          node.StaleSince,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"nodes":   responseNodes,
			"total":   len(responseNodes),
			"online":  counts["online"],
			"offline": counts["offline"],
			"timeout": counts["timeout"],
		})
	})

	mux.HandleFunc("/api/nodes/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, APIError{Code: "not_found", Message: "接口不存在", Status: http.StatusNotFound})
			return
		}
		subscriptionID, err := optionalInt(r.URL.Query().Get("sub_id"))
		if err != nil {
			writeAPIError(w, APIError{Code: "bad_request", Message: "sub_id 非法", Status: http.StatusBadRequest})
			return
		}
		nodes, err := deps.Store.ListNodes(subscriptionID, false)
		if err != nil {
			writeAPIError(w, apiInternalError(err))
			return
		}
		counts := map[string]int{"online": 0, "offline": 0, "timeout": 0}
		latencySum := 0
		latencyCount := 0
		for _, node := range nodes {
			state, ok := deps.Cache.Get(node.ID)
			if !ok {
				continue
			}
			if _, exists := counts[state.Status]; exists {
				counts[state.Status]++
			}
			if state.LatencyMS != nil {
				latencySum += *state.LatencyMS
				latencyCount++
			}
		}
		var average any = nil
		if latencyCount > 0 {
			average = latencySum / latencyCount
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"total":          len(nodes),
			"online":         counts["online"],
			"offline":        counts["offline"],
			"timeout":        counts["timeout"],
			"avg_latency_ms": average,
		})
	})

	mux.HandleFunc("/api/nodes/", func(w http.ResponseWriter, r *http.Request) {
		cleaned := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/nodes/"), "/")
		parts := strings.Split(cleaned, "/")
		if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "history" {
			id, err := strconv.Atoi(parts[0])
			if err != nil || id <= 0 {
				writeAPIError(w, APIError{Code: "bad_request", Message: "节点 ID 非法", Status: http.StatusBadRequest})
				return
			}
			window := time.Hour
			if value := strings.TrimSpace(r.URL.Query().Get("window")); value != "" {
				parsed, err := parseDuration(value)
				if err != nil || parsed <= 0 {
					writeAPIError(w, APIError{Code: "bad_request", Message: "window 非法", Status: http.StatusBadRequest})
					return
				}
				window = parsed
			}
			points, err := deps.Store.ListCheckHistory(id, time.Now().Add(-window))
			if err != nil {
				writeAPIError(w, apiInternalError(err))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"points": points})
			return
		}

		if r.Method != http.MethodPost {
			writeAPIError(w, APIError{Code: "not_found", Message: "接口不存在", Status: http.StatusNotFound})
			return
		}
		if len(parts) != 2 || parts[1] != "check" {
			writeAPIError(w, APIError{Code: "not_found", Message: "接口不存在", Status: http.StatusNotFound})
			return
		}
		id, err := strconv.Atoi(parts[0])
		if err != nil || id <= 0 {
			writeAPIError(w, APIError{Code: "bad_request", Message: "节点 ID 非法", Status: http.StatusBadRequest})
			return
		}
		result, err := deps.CheckService.StartAsyncCheck(nil, &id)
		if err != nil {
			writeAnyError(w, err)
			return
		}
		writeJSON(w, checkHTTPStatus(result.Status), map[string]any{"status": result.Status, "total_nodes": result.Total})
	})

	mux.HandleFunc("/api/nodes/check", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, APIError{Code: "not_found", Message: "接口不存在", Status: http.StatusNotFound})
			return
		}
		var payload struct {
			SubscriptionID *int `json:"sub_id"`
		}
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				writeAPIError(w, APIError{Code: "bad_request", Message: "请求体不是有效 JSON", Status: http.StatusBadRequest})
				return
			}
		}
		result, err := deps.CheckService.StartAsyncCheck(payload.SubscriptionID, nil)
		if err != nil {
			writeAnyError(w, err)
			return
		}
		writeJSON(w, checkHTTPStatus(result.Status), map[string]any{"status": result.Status, "total_nodes": result.Total})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeAPIError(w, APIError{Code: "not_found", Message: "接口不存在", Status: http.StatusNotFound})
			return
		}
		cleaned := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if cleaned == "." || cleaned == "" {
			http.ServeFileFS(w, r, deps.StaticFS, "index.html")
			return
		}
		if _, err := fs.Stat(deps.StaticFS, cleaned); err == nil {
			staticServer.ServeHTTP(w, r)
			return
		}
		http.ServeFileFS(w, r, deps.StaticFS, "index.html")
	})

	return mux
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func checkHTTPStatus(status string) int {
	switch status {
	case "started", "running":
		return http.StatusAccepted
	default:
		return http.StatusOK
	}
}

func writeAPIError(w http.ResponseWriter, err APIError) {
	writeJSON(w, err.Status, err)
}

func writeAnyError(w http.ResponseWriter, err error) {
	var apiErr APIError
	if ok := asAPIError(err, &apiErr); ok {
		writeAPIError(w, apiErr)
		return
	}
	writeAPIError(w, apiInternalError(err))
}

func asAPIError(err error, target *APIError) bool {
	apiErr, ok := err.(APIError)
	if !ok {
		return false
	}
	*target = apiErr
	return true
}

func apiInternalError(err error) APIError {
	return APIError{Code: "internal_error", Message: err.Error(), Status: http.StatusInternalServerError}
}

func optionalInt(raw string) (*int, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func deriveSubscriptionName(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "New Subscription"
	}
	if parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	if parsed.Scheme == "file" {
		base := path.Base(parsed.Path)
		if base != "" && base != "." && base != "/" {
			return strings.TrimSuffix(base, path.Ext(base))
		}
	}
	return "New Subscription"
}

func maskSubscriptionURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "[hidden]"
	}
	switch parsed.Scheme {
	case "http", "https":
		host := parsed.Hostname()
		if host == "" {
			return "[hidden]"
		}
		return parsed.Scheme + "://" + host + "/[hidden]"
	case "file":
		base := path.Base(parsed.Path)
		if base == "" || base == "." || base == "/" {
			return "file://[hidden]"
		}
		return "file://.../" + base
	default:
		return parsed.Scheme + "://[hidden]"
	}
}
