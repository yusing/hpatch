package capturer

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"sort"
)

type usageMetrics struct {
	InputTokens         uint64 `json:"input_tokens"`
	CachedInputTokens   uint64 `json:"cached_input_tokens"`
	UncachedInputTokens uint64 `json:"uncached_input_tokens"`
	OutputTokens        uint64 `json:"output_tokens"`
	ReasoningTokens     uint64 `json:"reasoning_tokens"`
	ProviderAttempts    uint64 `json:"provider_attempts"`
}

type requestTotals struct {
	Logical          uint64 `json:"logical"`
	ProviderAttempts uint64 `json:"provider_attempts"`
	Completed        uint64 `json:"completed"`
	Failed           uint64 `json:"failed"`
}

type cacheMetrics struct {
	ColdOrNewUncachedInputTokens uint64   `json:"cold_or_new_uncached_input_tokens"`
	ProviderCacheRate            *float64 `json:"provider_cache_rate"`
	EligiblePrefixTokens         uint64   `json:"eligible_prefix_tokens"`
	EligiblePrefixCachedTokens   uint64   `json:"eligible_prefix_cached_tokens"`
	EligiblePrefixMissTokens     uint64   `json:"eligible_prefix_miss_tokens"`
	EligiblePrefixCacheRate      *float64 `json:"eligible_prefix_cache_rate"`
}

type payloadTotals struct {
	Bytes  uint64 `json:"bytes"`
	Tokens uint64 `json:"tokens"`
}

type transportMetrics struct {
	ClientRequests          payloadTotals `json:"client_requests"`
	ProviderAttemptRequests payloadTotals `json:"provider_attempt_requests"`
	ProviderResponses       payloadTotals `json:"provider_responses"`
	ClientResponses         payloadTotals `json:"client_responses"`
}

type semanticResponseMetrics struct {
	ProviderAttemptResponses payloadTotals `json:"provider_attempt_responses"`
	ClientResponses          payloadTotals `json:"client_responses"`
}

type toolAggregate struct {
	Calls       uint64 `json:"calls"`
	InputBytes  uint64 `json:"input_bytes"`
	InputTokens uint64 `json:"input_tokens"`
	ItemBytes   uint64 `json:"item_bytes"`
	ItemTokens  uint64 `json:"item_tokens"`
}

type hpatchMetrics struct {
	Calls                uint64            `json:"calls"`
	Corrections          uint64            `json:"corrections"`
	Successful           uint64            `json:"successful"`
	Rejected             uint64            `json:"rejected"`
	Unmatched            uint64            `json:"unmatched"`
	ProviderInputTokens  uint64            `json:"provider_input_tokens"`
	DeliveredInputTokens uint64            `json:"delivered_input_tokens"`
	InputTokensSaved     int64             `json:"input_tokens_saved"`
	Diagnostics          map[string]uint64 `json:"diagnostics,omitempty"`
}

type captureHealth struct {
	Records                uint64 `json:"records"`
	CaptureErrors          uint64 `json:"capture_errors"`
	Incomplete             uint64 `json:"incomplete_records"`
	MissingProvider        uint64 `json:"missing_provider_records"`
	AttemptGaps            uint64 `json:"provider_attempt_gaps"`
	WriteErrors            uint64 `json:"write_errors"`
	SkippedRequests        uint64 `json:"skipped_requests"`
	DroppedExchangeDetails uint64 `json:"dropped_exchange_details"`
}

type protocolMetrics struct {
	InputPayloadTokensSaved  int64 `json:"input_payload_tokens_saved"`
	InputPayloadBytesSaved   int64 `json:"input_payload_bytes_saved"`
	OutputPayloadTokensSaved int64 `json:"output_payload_tokens_saved"`
	OutputPayloadBytesSaved  int64 `json:"output_payload_bytes_saved"`
}

type providerAttemptMetrics struct {
	Attempt          uint64            `json:"attempt"`
	Model            string            `json:"model,omitempty"`
	Status           string            `json:"status"`
	ResponseComplete bool              `json:"response_complete"`
	Usage            *usageMetrics     `json:"usage,omitempty"`
	Request          payloadMetrics    `json:"request"`
	Response         payloadMetrics    `json:"response"`
	FinalResponse    payloadMetrics    `json:"final_response,omitzero"`
	Tools            []toolCallMetrics `json:"tools,omitempty"`
}

type exchangeMetrics struct {
	Sequence            uint64                   `json:"sequence"`
	ThreadID            string                   `json:"thread_id,omitempty"`
	Model               string                   `json:"model,omitempty"`
	ProviderAttempts    []providerAttemptMetrics `json:"provider_attempts"`
	Status              string                   `json:"status"`
	Usage               *usageMetrics            `json:"usage,omitempty"`
	ClientRequest       payloadMetrics           `json:"client_request"`
	ClientResponse      payloadMetrics           `json:"client_response"`
	ClientFinalResponse payloadMetrics           `json:"client_final_response,omitzero"`
	DeliveredTools      []toolCallMetrics        `json:"delivered_tools,omitempty"`
}

type metricsSnapshot struct {
	Schema         string                   `json:"schema"`
	Mode           string                   `json:"mode"`
	ModelProtocol  string                   `json:"model_protocol"`
	Requests       requestTotals            `json:"requests"`
	Usage          usageMetrics             `json:"usage"`
	Cache          cacheMetrics             `json:"cache"`
	Transport      transportMetrics         `json:"transport"`
	Semantic       semanticResponseMetrics  `json:"semantic"`
	Protocol       protocolMetrics          `json:"protocol"`
	ProviderTools  map[string]toolAggregate `json:"provider_tools"`
	DeliveredTools map[string]toolAggregate `json:"delivered_tools"`
	HPatch         hpatchMetrics            `json:"hpatch"`
	Exchanges      []exchangeMetrics        `json:"exchanges"`
	Capture        captureHealth            `json:"capture"`
}

// ServeHTTP exposes capture-owned calculations on the router's existing
// listener. It never reads or mutates router state.
func (r *Recorder) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(writer).Encode(r.snapshot()); err != nil {
		return
	}
}

func (r *Recorder) snapshot() metricsSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneMetricsSnapshot(r.metrics)
}

func newMetricsSnapshot(mode, modelProtocol string) metricsSnapshot {
	return metricsSnapshot{
		Schema:         "hpatch.capture.metrics.v1",
		Mode:           mode,
		ModelProtocol:  modelProtocol,
		ProviderTools:  map[string]toolAggregate{},
		DeliveredTools: map[string]toolAggregate{},
		HPatch:         hpatchMetrics{Diagnostics: map[string]uint64{}},
	}
}

func (r *Recorder) addExchange(front captureRecord, state *requestState, providers []captureRecord) {
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].ProviderAttempt < providers[j].ProviderAttempt
	})
	if len(providers) == 0 && front.ResponseStatus == "completed" {
		r.metrics.Capture.MissingProvider++
	}
	for index, provider := range providers {
		if provider.ProviderAttempt != uint64(index+1) {
			r.metrics.Capture.AttemptGaps++
		}
	}

	exchange := exchangeMetrics{
		Sequence: front.RequestSequence, ThreadID: front.ThreadID,
		Status: front.ResponseStatus, ClientRequest: front.Request, ClientResponse: front.Response,
		ClientFinalResponse: front.FinalResponse,
		DeliveredTools:      slices.Clone(front.ToolCalls),
	}
	var exchangeUsage usageMetrics
	var providerTools []toolCallMetrics
	r.metrics.Requests.Logical++
	r.metrics.Requests.ProviderAttempts += uint64(len(providers))
	addPayload(&r.metrics.Transport.ClientRequests, front.Request)
	addPayload(&r.metrics.Transport.ClientResponses, front.Response)
	addPayload(&r.metrics.Semantic.ClientResponses, front.FinalResponse)
	addTools(r.metrics.DeliveredTools, front.ToolCalls)
	if front.ResponseStatus == "completed" {
		r.metrics.Requests.Completed++
	} else {
		r.metrics.Requests.Failed++
	}
	for _, provider := range providers {
		addPayload(&r.metrics.Transport.ProviderAttemptRequests, provider.Request)
		addPayload(&r.metrics.Transport.ProviderResponses, provider.Response)
		addPayload(&r.metrics.Semantic.ProviderAttemptResponses, provider.FinalResponse)
		addTools(r.metrics.ProviderTools, provider.ToolCalls)
		providerTools = append(providerTools, provider.ToolCalls...)
		attempt := providerAttemptMetrics{
			Attempt: provider.ProviderAttempt, Model: provider.RequestModel, Status: provider.ResponseStatus,
			ResponseComplete: provider.ResponseComplete,
			Request:          provider.Request, Response: provider.Response, FinalResponse: provider.FinalResponse,
			Tools: slices.Clone(provider.ToolCalls),
		}
		if provider.RequestModel != "" {
			exchange.Model = provider.RequestModel
		}
		if provider.Usage != nil {
			usage := usageOf(*provider.Usage)
			attempt.Usage = &usage
			addUsage(&exchangeUsage, usage)
			addUsage(&r.metrics.Usage, usage)
		}
		exchange.ProviderAttempts = append(exchange.ProviderAttempts, attempt)
	}
	if exchangeUsage.ProviderAttempts != 0 {
		exchange.Usage = &exchangeUsage
	}
	if len(providers) != 0 {
		final := providers[len(providers)-1]
		r.recordCacheObservation(state, final.Usage)
		r.metrics.Protocol.InputPayloadTokensSaved += signedDifference(front.Request.Tokens, final.Request.Tokens)
		r.metrics.Protocol.InputPayloadBytesSaved += signedDifference(front.Request.Bytes, final.Request.Bytes)
		r.metrics.Protocol.OutputPayloadTokensSaved += signedDifference(front.FinalResponse.Tokens, final.FinalResponse.Tokens)
		r.metrics.Protocol.OutputPayloadBytesSaved += signedDifference(front.FinalResponse.Bytes, final.FinalResponse.Bytes)
	} else {
		r.recordCacheObservation(state, nil)
	}
	addHPatch(&r.metrics.HPatch, providerTools, exchange.DeliveredTools)
	if r.metrics.Cache.EligiblePrefixTokens != 0 {
		rate := float64(r.metrics.Cache.EligiblePrefixCachedTokens) / float64(r.metrics.Cache.EligiblePrefixTokens)
		r.metrics.Cache.EligiblePrefixCacheRate = &rate
	}
	if r.metrics.Usage.InputTokens != 0 {
		rate := float64(r.metrics.Usage.CachedInputTokens) / float64(r.metrics.Usage.InputTokens)
		r.metrics.Cache.ProviderCacheRate = &rate
	}
	if len(r.metrics.Exchanges) == maxRetainedExchangeDetails {
		copy(r.metrics.Exchanges, r.metrics.Exchanges[1:])
		r.metrics.Exchanges[len(r.metrics.Exchanges)-1] = exchange
		r.metrics.Capture.DroppedExchangeDetails++
	} else {
		r.metrics.Exchanges = append(r.metrics.Exchanges, exchange)
	}
}

func (r *Recorder) recordCacheObservation(state *requestState, observed *tokenUsage) {
	if state.threadID == "" {
		if observed != nil {
			addCache(&r.metrics.Cache, r.previousInput, "", usageOf(*observed))
		}
		return
	}
	state.cacheReady = true
	if observed != nil {
		usage := usageOf(*observed)
		state.cacheUsage = &usage
	}
	queue := r.cacheQueues[state.threadID]
	for len(queue) != 0 && queue[0].cacheReady {
		current := queue[0]
		queue = queue[1:]
		if current.cacheUsage == nil {
			delete(r.previousInput, state.threadID)
		} else {
			addCache(&r.metrics.Cache, r.previousInput, state.threadID, *current.cacheUsage)
		}
	}
	if len(queue) == 0 {
		delete(r.cacheQueues, state.threadID)
	} else {
		r.cacheQueues[state.threadID] = queue
	}
}

func cloneMetricsSnapshot(source metricsSnapshot) metricsSnapshot {
	clone := source
	clone.ProviderTools = maps.Clone(source.ProviderTools)
	clone.DeliveredTools = maps.Clone(source.DeliveredTools)
	clone.HPatch.Diagnostics = maps.Clone(source.HPatch.Diagnostics)
	if source.Cache.EligiblePrefixCacheRate != nil {
		rate := *source.Cache.EligiblePrefixCacheRate
		clone.Cache.EligiblePrefixCacheRate = &rate
	}
	if source.Cache.ProviderCacheRate != nil {
		rate := *source.Cache.ProviderCacheRate
		clone.Cache.ProviderCacheRate = &rate
	}
	clone.Exchanges = make([]exchangeMetrics, len(source.Exchanges))
	for index, exchange := range source.Exchanges {
		clone.Exchanges[index] = exchange
		clone.Exchanges[index].DeliveredTools = slices.Clone(exchange.DeliveredTools)
		clone.Exchanges[index].ProviderAttempts = make([]providerAttemptMetrics, len(exchange.ProviderAttempts))
		for attemptIndex, attempt := range exchange.ProviderAttempts {
			clone.Exchanges[index].ProviderAttempts[attemptIndex] = attempt
			clone.Exchanges[index].ProviderAttempts[attemptIndex].Tools = slices.Clone(attempt.Tools)
			if attempt.Usage != nil {
				usage := *attempt.Usage
				clone.Exchanges[index].ProviderAttempts[attemptIndex].Usage = &usage
			}
		}
		if exchange.Usage != nil {
			usage := *exchange.Usage
			clone.Exchanges[index].Usage = &usage
		}
	}
	return clone
}

func signedDifference(larger, smaller uint64) int64 {
	if larger >= smaller {
		difference := larger - smaller
		if difference > uint64(^uint64(0)>>1) {
			return int64(^uint64(0) >> 1)
		}
		return int64(difference)
	}
	difference := smaller - larger
	if difference > uint64(^uint64(0)>>1) {
		return -int64(^uint64(0)>>1) - 1
	}
	return -int64(difference)
}

func usageOf(usage tokenUsage) usageMetrics {
	cached := min(usage.InputTokens, usage.CachedTokens)
	return usageMetrics{
		InputTokens: usage.InputTokens, CachedInputTokens: cached,
		UncachedInputTokens: usage.InputTokens - cached,
		OutputTokens:        usage.OutputTokens, ReasoningTokens: usage.ReasoningTokens,
		ProviderAttempts: 1,
	}
}

func addUsage(total *usageMetrics, usage usageMetrics) {
	total.InputTokens += usage.InputTokens
	total.CachedInputTokens += usage.CachedInputTokens
	total.UncachedInputTokens += usage.UncachedInputTokens
	total.OutputTokens += usage.OutputTokens
	total.ReasoningTokens += usage.ReasoningTokens
	total.ProviderAttempts += usage.ProviderAttempts
}

func addCache(total *cacheMetrics, previous map[string]uint64, thread string, usage usageMetrics) {
	if thread == "" {
		total.ColdOrNewUncachedInputTokens += usage.UncachedInputTokens
		return
	}
	eligible := uint64(0)
	if prior, ok := previous[thread]; ok {
		eligible = min(prior, usage.InputTokens)
	}
	eligibleCached := min(eligible, usage.CachedInputTokens)
	eligibleMiss := eligible - eligibleCached
	total.EligiblePrefixTokens += eligible
	total.EligiblePrefixCachedTokens += eligibleCached
	total.EligiblePrefixMissTokens += eligibleMiss
	total.ColdOrNewUncachedInputTokens += usage.UncachedInputTokens - eligibleMiss
	previous[thread] = usage.InputTokens
}

func addPayload(total *payloadTotals, payload payloadMetrics) {
	total.Bytes += payload.Bytes
	total.Tokens += payload.Tokens
}

func addTools(totals map[string]toolAggregate, calls []toolCallMetrics) {
	for _, call := range calls {
		total := totals[call.Name]
		total.Calls++
		total.InputBytes += call.InputBytes
		total.InputTokens += call.InputTokens
		total.ItemBytes += call.ItemBytes
		total.ItemTokens += call.ItemTokens
		totals[call.Name] = total
	}
}

func addHPatch(total *hpatchMetrics, provider, delivered []toolCallMetrics) {
	byID := make(map[string]toolCallMetrics, len(delivered))
	for _, call := range delivered {
		byID[call.CallID] = call
	}
	for _, emitted := range provider {
		if emitted.Name != "hpatch" && emitted.Name != "hpatch_recover" {
			continue
		}
		total.Calls++
		if emitted.Name == "hpatch_recover" {
			total.Corrections++
		}
		total.ProviderInputTokens += emitted.InputTokens
		carrier := byID[emitted.CallID]
		if carrier.CallID == "" {
			total.Unmatched++
			continue
		}
		total.DeliveredInputTokens += carrier.InputTokens
		switch carrier.Kind {
		case "apply_patch", "hpatch_report":
			total.Successful++
		default:
			total.Rejected++
			if carrier.Diagnostic != "" {
				total.Diagnostics[carrier.Diagnostic]++
			}
		}
	}
	total.InputTokensSaved = signedDifference(total.DeliveredInputTokens, total.ProviderInputTokens)
}
