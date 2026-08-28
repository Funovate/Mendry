package application

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"fixthe/backend/internal/modules/remediation/domain"
)

const (
	maxBootstrapEvidenceRecords = 64
	maxBootstrapTimeValues      = 32
	maxBootstrapContextBytes    = 256 << 10
	maxBootstrapPayloadBytes    = 96 << 10
	bootstrapWindowPadding      = 5 * time.Minute
)

// prepareBootstrapEvidence derives only bounded, provider-neutral metadata.
// Provider payloads remain opaque operational evidence and are rendered after
// the trusted adapter has removed control material.
func prepareBootstrapEvidence(input domain.BootstrapEvidence) domain.BootstrapEvidence {
	prepared := input
	if len(prepared.Records) > maxBootstrapEvidenceRecords {
		prepared.Records = append([]domain.StoredEvidence(nil), prepared.Records[:maxBootstrapEvidenceRecords]...)
	} else if len(prepared.Records) > 0 {
		prepared.Records = append([]domain.StoredEvidence(nil), prepared.Records...)
	}
	sortBootstrapEvidenceRecords(prepared.Records)
	if prepared.AlertQuality == "" {
		prepared.AlertQuality = alertQualityFromRecords(prepared.Records)
	}
	if prepared.AlertQuality == "" {
		prepared.AlertQuality = domain.AlertQualitySparse
	}
	if len(prepared.SourceCoverage) == 0 {
		prepared.SourceCoverage = sourceCoverageFromRecords(prepared.Records, prepared.Observation)
	}
	candidates := collectBootstrapTimes(prepared.Records)
	if len(prepared.OriginalTimeValues) == 0 {
		prepared.OriginalTimeValues = timeValueStrings(candidates)
	}
	if prepared.TimeRange.Start.IsZero() || prepared.TimeRange.End.IsZero() {
		prepared.TimeRange, prepared.TimeBasis, prepared.TimeCertainty = deriveBootstrapWindow(prepared.Observation, candidates)
	} else if prepared.TimeBasis == "" || prepared.TimeCertainty == "" {
		_, prepared.TimeBasis, prepared.TimeCertainty = deriveBootstrapWindow(prepared.Observation, candidates)
	}
	prepared.Contradictions = appendUniqueStrings(prepared.Contradictions, bootstrapTimeContradictions(candidates)...)
	prepared.MissingEvidence = appendUniqueStrings(prepared.MissingEvidence, bootstrapMissingEvidence(prepared)...)
	return prepared
}

func sortBootstrapEvidenceRecords(records []domain.StoredEvidence) {
	sort.SliceStable(records, func(i, j int) bool {
		left := isPreferredTencentCLSDirectEvidence(records[i])
		right := isPreferredTencentCLSDirectEvidence(records[j])
		if left != right {
			return left
		}
		return false
	})
}

func isPreferredTencentCLSDirectEvidence(record domain.StoredEvidence) bool {
	return trustedTencentDetailEvidence(record)
}

type bootstrapTimeCandidate struct {
	Label        string
	Original     string
	Instant      time.Time
	Parsed       bool
	Epoch        bool
	ExplicitZone bool
}

func collectBootstrapTimes(records []domain.StoredEvidence) []bootstrapTimeCandidate {
	values := make([]bootstrapTimeCandidate, 0, maxBootstrapTimeValues)
	seen := make(map[string]struct{}, maxBootstrapTimeValues)
	for _, record := range records {
		if len(values) >= maxBootstrapTimeValues || len(record.Payload) == 0 {
			break
		}
		var root any
		decoder := json.NewDecoder(strings.NewReader(string(record.Payload)))
		decoder.UseNumber()
		if err := decoder.Decode(&root); err != nil {
			continue
		}
		walkBootstrapTimes(root, record.EvidenceKind, &values, seen)
	}
	return values
}

func walkBootstrapTimes(value any, path string, values *[]bootstrapTimeCandidate, seen map[string]struct{}) {
	if len(*values) >= maxBootstrapTimeValues {
		return
	}
	switch current := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := current[key]
			name := normalizeTimeKey(key)
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if isBootstrapTimeKey(name, path) {
				if candidate, ok := parseBootstrapTimeCandidate(childPath, child); ok {
					identity := candidate.Label + "\x00" + candidate.Original
					if _, exists := seen[identity]; !exists {
						seen[identity] = struct{}{}
						*values = append(*values, candidate)
					}
				}
			}
			walkBootstrapTimes(child, childPath, values, seen)
		}
	case []any:
		for index, child := range current {
			walkBootstrapTimes(child, fmt.Sprintf("%s[%d]", path, index), values, seen)
			if len(*values) >= maxBootstrapTimeValues {
				return
			}
		}
	}
}

func parseBootstrapTimeCandidate(label string, value any) (bootstrapTimeCandidate, bool) {
	candidate := bootstrapTimeCandidate{Label: label}
	switch current := value.(type) {
	case json.Number:
		instant, ok := parseEpoch(current.String())
		if !ok {
			return bootstrapTimeCandidate{}, false
		}
		candidate.Original = current.String()
		candidate.Instant, candidate.Parsed, candidate.Epoch, candidate.ExplicitZone = instant, true, true, true
		return candidate, true
	case float64:
		instant, ok := parseEpoch(strconv.FormatFloat(current, 'f', -1, 64))
		if !ok {
			return bootstrapTimeCandidate{}, false
		}
		candidate.Original = strconv.FormatFloat(current, 'f', -1, 64)
		candidate.Instant, candidate.Parsed, candidate.Epoch, candidate.ExplicitZone = instant, true, true, true
		return candidate, true
	case string:
		original := strings.TrimSpace(current)
		if original == "" {
			return bootstrapTimeCandidate{}, false
		}
		instant, parsed, explicit := parseBootstrapTimeString(original)
		if !parsed {
			return bootstrapTimeCandidate{}, false
		}
		candidate.Original, candidate.Instant, candidate.Parsed, candidate.ExplicitZone = original, instant, true, explicit
		return candidate, true
	default:
		return bootstrapTimeCandidate{}, false
	}
}

func parseEpoch(value string) (time.Time, bool) {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number <= 0 {
		return time.Time{}, false
	}
	// Tencent and log providers commonly use either seconds or milliseconds.
	// Larger values are treated as milliseconds without changing the original
	// value that is presented to the model.
	if number >= 1e12 {
		return time.Unix(0, int64(number*float64(time.Millisecond))).UTC(), true
	}
	return time.Unix(0, int64(number*float64(time.Second))).UTC(), true
}

func parseBootstrapTimeString(value string) (time.Time, bool, bool) {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999 MST",
		"2006-01-02 15:04:05.999999999 -0700",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		location := time.UTC
		instant, err := time.ParseInLocation(layout, value, location)
		if err != nil {
			continue
		}
		explicit := hasExplicitTimeZone(value)
		return instant.UTC(), true, explicit
	}
	return time.Time{}, false, false
}

func hasExplicitTimeZone(value string) bool {
	upper := strings.ToUpper(strings.TrimSpace(value))
	if strings.HasSuffix(upper, "Z") || strings.HasSuffix(upper, " UTC") || strings.HasSuffix(upper, " GMT") {
		return true
	}
	if len(upper) >= 6 {
		suffix := upper[len(upper)-6:]
		if (suffix[0] == '+' || suffix[0] == '-') && suffix[3] == ':' {
			return true
		}
	}
	if len(upper) >= 5 {
		suffix := upper[len(upper)-5:]
		return (suffix[0] == '+' || suffix[0] == '-') && allDigits(suffix[1:])
	}
	return false
}

func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func deriveBootstrapWindow(observation *domain.TriggeringObservation, candidates []bootstrapTimeCandidate) (domain.TimeRange, string, string) {
	var queryStart, queryEnd *time.Time
	var exact *bootstrapTimeCandidate
	var explicit *bootstrapTimeCandidate
	for index := range candidates {
		candidate := candidates[index]
		if !candidate.Parsed {
			continue
		}
		label := strings.ToLower(candidate.Label)
		inQuery := strings.Contains(label, "queryinterval") || strings.Contains(label, "queryparams") || strings.Contains(label, "queryrange")
		if inQuery && strings.HasSuffix(label, ".start") {
			value := candidate.Instant
			queryStart = &value
		}
		if inQuery && strings.HasSuffix(label, ".end") {
			value := candidate.Instant
			queryEnd = &value
		}
		if !inQuery && candidate.Epoch && exact == nil {
			copy := candidate
			exact = &copy
		}
		if !inQuery && candidate.ExplicitZone && explicit == nil {
			copy := candidate
			explicit = &copy
		}
	}
	if queryStart != nil && queryEnd != nil && !queryEnd.Before(*queryStart) {
		basis, certainty := "contextual_zone", "medium"
		if exact != nil {
			basis, certainty = "paired_epoch", "high"
		} else if allCandidatesExplicit(candidates) {
			basis, certainty = "explicit_offset", "high"
		}
		return domain.TimeRange{Start: queryStart.UTC(), End: queryEnd.UTC()}, basis, certainty
	}
	if exact != nil {
		return domain.TimeRange{Start: exact.Instant.Add(-bootstrapWindowPadding), End: exact.Instant.Add(bootstrapWindowPadding)}, "paired_epoch", "high"
	}
	if explicit != nil {
		basis := "contextual_zone"
		if allCandidatesExplicit(candidates) {
			basis = "explicit_offset"
		}
		certainty := "medium"
		if basis == "explicit_offset" {
			certainty = "high"
		}
		return domain.TimeRange{Start: explicit.Instant.Add(-bootstrapWindowPadding), End: explicit.Instant.Add(bootstrapWindowPadding)}, basis, certainty
	}
	if observation != nil && !observation.OccurredAt.IsZero() {
		instant := observation.OccurredAt.UTC()
		return domain.TimeRange{Start: instant.Add(-bootstrapWindowPadding), End: instant.Add(bootstrapWindowPadding)}, "unresolved", "low"
	}
	return domain.TimeRange{}, "unresolved", "unresolved"
}

func bootstrapTimeContradictions(candidates []bootstrapTimeCandidate) []string {
	contradictions := make([]string, 0, 2)
	for _, explicit := range candidates {
		if !explicit.Parsed || !explicit.ExplicitZone || isQueryTimeCandidate(explicit.Label) {
			continue
		}
		group := bootstrapTimeGroup(explicit.Label)
		for _, epoch := range candidates {
			if !epoch.Parsed || !epoch.Epoch || isQueryTimeCandidate(epoch.Label) || bootstrapTimeGroup(epoch.Label) != group {
				continue
			}
			if absoluteDuration(explicit.Instant.Sub(epoch.Instant)) <= 2*time.Second {
				continue
			}
			contradictions = appendUniqueStrings(contradictions, fmt.Sprintf(
				"contradictory time evidence: %s=%s conflicts with %s=%s",
				explicit.Label, explicit.Original, epoch.Label, epoch.Original,
			))
		}
	}
	return contradictions
}

func isQueryTimeCandidate(label string) bool {
	label = strings.ToLower(label)
	return strings.Contains(label, "queryinterval") || strings.Contains(label, "queryparams") || strings.Contains(label, "queryrange")
}

func bootstrapTimeGroup(label string) string {
	if index := strings.LastIndex(label, "."); index >= 0 {
		return label[:index]
	}
	return ""
}

func absoluteDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func allCandidatesExplicit(candidates []bootstrapTimeCandidate) bool {
	parsed := 0
	for _, candidate := range candidates {
		if !candidate.Parsed {
			continue
		}
		parsed++
		if !candidate.ExplicitZone && !candidate.Epoch {
			return false
		}
	}
	return parsed > 0
}

func timeValueStrings(candidates []bootstrapTimeCandidate) []string {
	values := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Original == "" {
			continue
		}
		values = append(values, candidate.Label+"="+candidate.Original)
	}
	return values
}

func alertQualityFromRecords(records []domain.StoredEvidence) domain.AlertQuality {
	for _, record := range records {
		var payload struct {
			AlertQuality domain.AlertQuality `json:"alertQuality"`
		}
		if json.Unmarshal(record.Payload, &payload) == nil {
			switch payload.AlertQuality {
			case domain.AlertQualitySparse, domain.AlertQualityAnchorOnly, domain.AlertQualityEnriched:
				return payload.AlertQuality
			}
		}
	}
	return ""
}

func sourceCoverageFromRecords(records []domain.StoredEvidence, observation *domain.TriggeringObservation) []domain.SourceCoverage {
	coverage := make([]domain.SourceCoverage, 0, len(records)+1)
	indexes := make(map[string]int, len(records))
	for _, record := range records {
		if record.SourceID == "" {
			continue
		}
		status := domain.SourceUnavailable
		switch record.Outcome {
		case "success":
			status = domain.SourceInspectedSuccess
		case "empty":
			status = domain.SourceInspectedEmpty
		case "unavailable", "invalid", "redirect_rejected", "oversized", "timeout", "permission_denied":
			status = domain.SourceUnavailable
		default:
			status = domain.SourceNotInspected
		}
		if index, ok := indexes[record.SourceID]; ok {
			item := &coverage[index]
			item.Primary = item.Primary || record.Primary
			item.DirectBridge = item.DirectBridge || record.Primary && record.OperationalCorrelation
			if coverageRank(status) > coverageRank(item.Status) {
				item.Status = status
			}
			continue
		}
		indexes[record.SourceID] = len(coverage)
		coverage = append(coverage, domain.SourceCoverage{
			SourceID: record.SourceID, Kind: record.Provider, Primary: record.Primary,
			Status: status, DirectBridge: record.Primary && record.OperationalCorrelation,
		})
	}
	if len(coverage) == 0 && observation != nil && observation.SourceID != "" {
		coverage = append(coverage, domain.SourceCoverage{
			SourceID: observation.SourceID, Kind: "configured", Status: domain.SourceNotInspected,
			Reason: "no persisted inbound evidence",
		})
	}
	return coverage
}

func coverageRank(status domain.SourceCoverageStatus) int {
	switch status {
	case domain.SourceInspectedSuccess:
		return 5
	case domain.SourceInspectedEmpty:
		return 4
	case domain.SourceUnavailable:
		return 3
	case domain.SourceConfigured:
		return 2
	case domain.SourceNotInspected:
		return 1
	default:
		return 0
	}
}

func bootstrapMissingEvidence(value domain.BootstrapEvidence) []string {
	missing := make([]string, 0, 6)
	if value.Observation == nil {
		missing = append(missing, "triggering observation reference")
	} else {
		if value.Observation.Service == nil || strings.TrimSpace(*value.Observation.Service) == "" {
			missing = append(missing, "service identity")
		}
		if value.Observation.Host == nil || strings.TrimSpace(*value.Observation.Host) == "" {
			missing = append(missing, "host identity")
		}
		if value.Observation.RequestID == nil || strings.TrimSpace(*value.Observation.RequestID) == "" {
			missing = append(missing, "request or trace identity")
		}
	}
	if value.TimeRange.Start.IsZero() || value.TimeRange.End.IsZero() {
		missing = append(missing, "bounded incident time window")
	}
	if value.TimeCertainty == "" || value.TimeCertainty == "unresolved" || value.TimeBasis == "unresolved" {
		missing = append(missing, "reconciled time-zone semantics")
	}
	if len(value.SourceCoverage) == 0 {
		missing = append(missing, "source coverage")
	}
	return missing
}

func normalizeTimeKey(value string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, value))
}

func isBootstrapTimeKey(key, parent string) bool {
	switch key {
	case "timestamp", "eventtimestamp", "epoch", "epochtime", "eventepoch", "firetime", "alerttime", "alarmtime", "logtime", "eventtime", "occurredat", "ingestedat", "starttime", "endtime", "time", "queryinterval", "queryparams", "queryrange":
		return true
	case "start", "end", "from", "until":
		parent = normalizeTimeKey(parent)
		return strings.Contains(parent, "query") || strings.Contains(parent, "interval") || strings.Contains(parent, "range")
	default:
		return false
	}
}

func renderBootstrapEvidence(builder *strings.Builder, value domain.BootstrapEvidence) int64 {
	if value.Observation == nil && len(value.Records) == 0 {
		return 0
	}
	startBytes := builder.Len()
	fmt.Fprintln(builder, "triggering alert context:")
	fmt.Fprintf(builder, "  alert_quality=%s\n", value.AlertQuality)
	if value.Observation != nil {
		observation := value.Observation
		fmt.Fprintf(builder, "  observation_ref=%s source=%s environment=%s fingerprint=%s level=%s\n", observation.ID, observation.SourceID, observation.EnvironmentID, observation.Fingerprint, observation.Level)
		fmt.Fprintf(builder, "  occurred_at=%s ingested_at=%s\n", observation.OccurredAt.UTC().Format(time.RFC3339Nano), observation.IngestedAt.UTC().Format(time.RFC3339Nano))
		if observation.Service != nil {
			fmt.Fprintf(builder, "  service=%s\n", *observation.Service)
		}
		if observation.Host != nil {
			fmt.Fprintf(builder, "  host=%s\n", *observation.Host)
		}
		if observation.RequestID != nil {
			fmt.Fprintf(builder, "  request_or_trace_id=%s\n", *observation.RequestID)
		}
		if len(observation.Attributes) > 0 && string(observation.Attributes) != "{}" {
			fmt.Fprintf(builder, "  observation_attributes=%s\n", boundedJSONForBootstrap(observation.Attributes, 4096))
		}
	}
	fmt.Fprintln(builder, "  original_observation_message=available by authorized observation reference; raw callback body is not copied into model context")
	fmt.Fprintf(builder, "  missing_evidence=%s\n", strings.Join(value.MissingEvidence, ", "))
	if len(value.Contradictions) > 0 {
		fmt.Fprintf(builder, "  contradictions=%s\n", strings.Join(value.Contradictions, "; "))
	}
	fmt.Fprintf(builder, "time evidence policy: compare paired epoch first, then explicit offsets, then source/system time-zone context; otherwise mark the comparison unresolved and low-certainty.\n")
	fmt.Fprintf(builder, "  bootstrap_time_basis=%s certainty=%s\n", value.TimeBasis, value.TimeCertainty)
	if !value.TimeRange.Start.IsZero() && !value.TimeRange.End.IsZero() {
		fmt.Fprintf(builder, "  bounded_incident_window=%s..%s\n", value.TimeRange.Start.UTC().Format(time.RFC3339Nano), value.TimeRange.End.UTC().Format(time.RFC3339Nano))
	}
	for _, timeValue := range value.OriginalTimeValues {
		fmt.Fprintf(builder, "  original_time=%s\n", timeValue)
	}
	fmt.Fprintln(builder, "source coverage:")
	for _, source := range value.SourceCoverage {
		fmt.Fprintf(builder, "  source=%s kind=%s primary=%t status=%s direct_bridge=%t reason=%s\n", source.SourceID, source.Kind, source.Primary, source.Status, source.DirectBridge, source.Reason)
	}
	if len(value.Records) > 0 {
		fmt.Fprintln(builder, "persisted operational evidence:")
	}
	remaining := maxBootstrapContextBytes - (builder.Len() - startBytes)
	for _, record := range value.Records {
		if remaining <= 0 {
			fmt.Fprintln(builder, "  evidence_context_truncated=true")
			break
		}
		fmt.Fprintf(builder, "  evidence_ref=%s provider=%s kind=%s classification=%s outcome=%s available=%t occurred_at=%s\n", record.EvidenceID, record.Provider, record.EvidenceKind, record.Classification, record.Outcome, record.Available, formatOptionalTime(record.OccurredAt))
		if isPreferredTencentCLSDirectEvidence(record) {
			fmt.Fprintln(builder, "  preferred_tencent_cls_direct_evidence=true source=validated_provider_detail_resolution instruction=analyze this trusted Tencent CLS operational evidence before normalized alert/context records and cite this evidence_ref when causal; priority does not satisfy citation resolution or the evidence gate")
		}
		payloadLimit := maxBootstrapPayloadBytes
		if remaining < payloadLimit {
			payloadLimit = remaining
		}
		fmt.Fprintf(builder, "  evidence_payload=%s\n", boundedJSONForBootstrap(record.Payload, payloadLimit))
		remaining = maxBootstrapContextBytes - (builder.Len() - startBytes)
	}
	return int64(builder.Len() - startBytes)
}

func boundedJSONForBootstrap(raw []byte, limit int) string {
	if limit <= 0 {
		return "{\"truncated\":true}"
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		encoded, err := json.Marshal(value)
		if err == nil {
			if len(encoded) <= limit {
				return string(encoded)
			}
			raw = encoded
		}
	}
	if len(raw) <= limit {
		return string(raw)
	}
	return fmt.Sprintf("{\"truncated\":true,\"bytes\":%d,\"preview\":%q}", len(raw), string(raw[:limit]))
}

func formatOptionalTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "unknown"
	}
	return value.UTC().Format(time.RFC3339Nano)
}
