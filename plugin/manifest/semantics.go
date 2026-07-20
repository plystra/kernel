package manifest

import "go.yaml.in/yaml/v3"

// CapabilityKind classifies one canonical operation.
type CapabilityKind string

const (
	CapabilityKindQuery   CapabilityKind = "query"
	CapabilityKindCommand CapabilityKind = "command"
	CapabilityKindEvent   CapabilityKind = "event"
	CapabilityKindStream  CapabilityKind = "stream"
)

// CapabilityEffects describes the provider-independent effects of an operation.
type CapabilityEffects string

const (
	CapabilityEffectsNone          CapabilityEffects = "none"
	CapabilityEffectsLocal         CapabilityEffects = "local"
	CapabilityEffectsExternal      CapabilityEffects = "external"
	CapabilityEffectsExternalWrite CapabilityEffects = "external-write"
)

// IdempotencyMode describes how repeated invocation affects the operation.
type IdempotencyMode string

const (
	IdempotencyModeNone     IdempotencyMode = "none"
	IdempotencyModeInherent IdempotencyMode = "inherent"
	IdempotencyModeKeyed    IdempotencyMode = "keyed"
)

// RetrySafety describes whether the canonical operation may be retried.
type RetrySafety string

const (
	RetrySafetyNever                  RetrySafety = "never"
	RetrySafetySafe                   RetrySafety = "safe"
	RetrySafetyRequiresIdempotencyKey RetrySafety = "requires-idempotency-key"
)

// CancellationMode describes the canonical cancellation guarantee.
type CancellationMode string

const (
	CancellationModeUnsupported CancellationMode = "unsupported"
	CancellationModeBestEffort  CancellationMode = "best-effort"
)

// CompletionMode describes when an invocation is considered complete.
type CompletionMode string

const (
	CompletionModeCompletedBeforeReturn CompletionMode = "completed-before-return"
	CompletionModeAcceptedForProcessing CompletionMode = "accepted-for-processing"
)

// OrderingMode describes the canonical ordering guarantee.
type OrderingMode string

const (
	OrderingModeNone   OrderingMode = "none"
	OrderingModePerKey OrderingMode = "per-key"
	OrderingModeGlobal OrderingMode = "global"
)

// DataClassification is one closed request or response data classification.
type DataClassification string

const (
	DataClassificationPublic       DataClassification = "public"
	DataClassificationInternal     DataClassification = "internal"
	DataClassificationConfidential DataClassification = "confidential"
	DataClassificationRestricted   DataClassification = "restricted"
)

// IdempotencySemantics is the immutable idempotency declaration.
type IdempotencySemantics struct {
	mode         IdempotencyMode
	requestField string
}

func (s IdempotencySemantics) Mode() IdempotencyMode { return s.mode }
func (s IdempotencySemantics) RequestField() string  { return s.requestField }

// RetrySemantics is the immutable retry-safety declaration.
type RetrySemantics struct {
	safety RetrySafety
}

func (s RetrySemantics) Safety() RetrySafety { return s.safety }

// CancellationSemantics is the immutable cancellation declaration.
type CancellationSemantics struct {
	mode CancellationMode
}

func (s CancellationSemantics) Mode() CancellationMode { return s.mode }

// CompletionSemantics is the immutable completion declaration.
type CompletionSemantics struct {
	mode CompletionMode
}

func (s CompletionSemantics) Mode() CompletionMode { return s.mode }

// OrderingSemantics is the immutable ordering declaration.
type OrderingSemantics struct {
	mode         OrderingMode
	requestField string
}

func (s OrderingSemantics) Mode() OrderingMode   { return s.mode }
func (s OrderingSemantics) RequestField() string { return s.requestField }

// DataSemantics is the immutable request and response classification.
type DataSemantics struct {
	request  DataClassification
	response DataClassification
}

func (s DataSemantics) Request() DataClassification  { return s.request }
func (s DataSemantics) Response() DataClassification { return s.response }

// CapabilitySemantics is one closed immutable provider-independent operation
// declaration.
type CapabilitySemantics struct {
	kind         CapabilityKind
	effects      CapabilityEffects
	idempotency  IdempotencySemantics
	retry        RetrySemantics
	cancellation CancellationSemantics
	completion   CompletionSemantics
	ordering     OrderingSemantics
	data         DataSemantics
}

func (s CapabilitySemantics) Kind() CapabilityKind                { return s.kind }
func (s CapabilitySemantics) Effects() CapabilityEffects          { return s.effects }
func (s CapabilitySemantics) Idempotency() IdempotencySemantics   { return s.idempotency }
func (s CapabilitySemantics) Retry() RetrySemantics               { return s.retry }
func (s CapabilitySemantics) Cancellation() CancellationSemantics { return s.cancellation }
func (s CapabilitySemantics) Completion() CompletionSemantics     { return s.completion }
func (s CapabilitySemantics) Ordering() OrderingSemantics         { return s.ordering }
func (s CapabilitySemantics) Data() DataSemantics                 { return s.data }

func parseCapabilitySemantics(node *yaml.Node, request Schema) (CapabilitySemantics, error) {
	fields, err := parseSemanticsMapping("semantics", node,
		"kind", "effects", "idempotency", "retry", "cancellation", "completion", "ordering", "data")
	if err != nil {
		return CapabilitySemantics{}, err
	}

	kind, err := parseCapabilityKind(requiredSemanticsField(fields, "semantics.kind"), "semantics.kind")
	if err != nil {
		return CapabilitySemantics{}, err
	}
	effects, err := parseCapabilityEffects(requiredSemanticsField(fields, "semantics.effects"), "semantics.effects")
	if err != nil {
		return CapabilitySemantics{}, err
	}
	idempotency, err := parseIdempotencySemantics(fields["idempotency"], request)
	if err != nil {
		return CapabilitySemantics{}, err
	}
	retry, err := parseRetrySemantics(fields["retry"])
	if err != nil {
		return CapabilitySemantics{}, err
	}
	cancellation, err := parseCancellationSemantics(fields["cancellation"])
	if err != nil {
		return CapabilitySemantics{}, err
	}
	completion, err := parseCompletionSemantics(fields["completion"])
	if err != nil {
		return CapabilitySemantics{}, err
	}
	ordering, err := parseOrderingSemantics(fields["ordering"], request)
	if err != nil {
		return CapabilitySemantics{}, err
	}
	data, err := parseDataSemantics(fields["data"])
	if err != nil {
		return CapabilitySemantics{}, err
	}

	semantics := CapabilitySemantics{
		kind:         kind,
		effects:      effects,
		idempotency:  idempotency,
		retry:        retry,
		cancellation: cancellation,
		completion:   completion,
		ordering:     ordering,
		data:         data,
	}
	if err := validateCapabilitySemantics(semantics); err != nil {
		return CapabilitySemantics{}, err
	}
	return semantics, nil
}

func parseIdempotencySemantics(node *yaml.Node, request Schema) (IdempotencySemantics, error) {
	fields, err := parseSemanticsMapping("semantics.idempotency", node, "mode", "request_field")
	if err != nil {
		return IdempotencySemantics{}, err
	}
	mode, err := parseIdempotencyMode(requiredSemanticsField(fields, "semantics.idempotency.mode"), "semantics.idempotency.mode")
	if err != nil {
		return IdempotencySemantics{}, err
	}
	requestFieldNode, hasRequestField := fields["request_field"]
	requestField, err := optionalSemanticsString(requestFieldNode, "semantics.idempotency.request_field")
	if err != nil {
		return IdempotencySemantics{}, err
	}
	if mode == IdempotencyModeKeyed {
		if requestField == "" {
			return IdempotencySemantics{}, invalidCapability("semantics.idempotency.request_field is required when semantics.idempotency.mode is %q", mode)
		}
		if err := validateSemanticsRequestField("semantics.idempotency.request_field", requestField, request, SchemaString); err != nil {
			return IdempotencySemantics{}, err
		}
	} else if hasRequestField {
		return IdempotencySemantics{}, invalidCapability("semantics.idempotency.request_field is valid only when semantics.idempotency.mode is %q", IdempotencyModeKeyed)
	}
	return IdempotencySemantics{mode: mode, requestField: requestField}, nil
}

func parseRetrySemantics(node *yaml.Node) (RetrySemantics, error) {
	fields, err := parseSemanticsMapping("semantics.retry", node, "safety")
	if err != nil {
		return RetrySemantics{}, err
	}
	safety, err := parseRetrySafety(requiredSemanticsField(fields, "semantics.retry.safety"), "semantics.retry.safety")
	if err != nil {
		return RetrySemantics{}, err
	}
	return RetrySemantics{safety: safety}, nil
}

func parseCancellationSemantics(node *yaml.Node) (CancellationSemantics, error) {
	fields, err := parseSemanticsMapping("semantics.cancellation", node, "mode")
	if err != nil {
		return CancellationSemantics{}, err
	}
	mode, err := parseCancellationMode(requiredSemanticsField(fields, "semantics.cancellation.mode"), "semantics.cancellation.mode")
	if err != nil {
		return CancellationSemantics{}, err
	}
	return CancellationSemantics{mode: mode}, nil
}

func parseCompletionSemantics(node *yaml.Node) (CompletionSemantics, error) {
	fields, err := parseSemanticsMapping("semantics.completion", node, "mode")
	if err != nil {
		return CompletionSemantics{}, err
	}
	mode, err := parseCompletionMode(requiredSemanticsField(fields, "semantics.completion.mode"), "semantics.completion.mode")
	if err != nil {
		return CompletionSemantics{}, err
	}
	return CompletionSemantics{mode: mode}, nil
}

func parseOrderingSemantics(node *yaml.Node, request Schema) (OrderingSemantics, error) {
	fields, err := parseSemanticsMapping("semantics.ordering", node, "mode", "request_field")
	if err != nil {
		return OrderingSemantics{}, err
	}
	mode, err := parseOrderingMode(requiredSemanticsField(fields, "semantics.ordering.mode"), "semantics.ordering.mode")
	if err != nil {
		return OrderingSemantics{}, err
	}
	requestFieldNode, hasRequestField := fields["request_field"]
	requestField, err := optionalSemanticsString(requestFieldNode, "semantics.ordering.request_field")
	if err != nil {
		return OrderingSemantics{}, err
	}
	if mode == OrderingModePerKey {
		if requestField == "" {
			return OrderingSemantics{}, invalidCapability("semantics.ordering.request_field is required when semantics.ordering.mode is %q", mode)
		}
		if err := validateSemanticsRequestField("semantics.ordering.request_field", requestField, request, SchemaString, SchemaInteger); err != nil {
			return OrderingSemantics{}, err
		}
	} else if hasRequestField {
		return OrderingSemantics{}, invalidCapability("semantics.ordering.request_field is valid only when semantics.ordering.mode is %q", OrderingModePerKey)
	}
	return OrderingSemantics{mode: mode, requestField: requestField}, nil
}

func parseDataSemantics(node *yaml.Node) (DataSemantics, error) {
	fields, err := parseSemanticsMapping("semantics.data", node, "request", "response")
	if err != nil {
		return DataSemantics{}, err
	}
	request, err := parseDataClassification(requiredSemanticsField(fields, "semantics.data.request"), "semantics.data.request")
	if err != nil {
		return DataSemantics{}, err
	}
	response, err := parseDataClassification(requiredSemanticsField(fields, "semantics.data.response"), "semantics.data.response")
	if err != nil {
		return DataSemantics{}, err
	}
	return DataSemantics{request: request, response: response}, nil
}

func validateCapabilitySemantics(semantics CapabilitySemantics) error {
	switch semantics.retry.safety {
	case RetrySafetySafe:
		if semantics.idempotency.mode != IdempotencyModeInherent {
			return invalidCapability("semantics.retry.safety %q requires semantics.idempotency.mode %q", RetrySafetySafe, IdempotencyModeInherent)
		}
	case RetrySafetyRequiresIdempotencyKey:
		if semantics.idempotency.mode != IdempotencyModeKeyed {
			return invalidCapability("semantics.retry.safety %q requires semantics.idempotency.mode %q", RetrySafetyRequiresIdempotencyKey, IdempotencyModeKeyed)
		}
	}
	if semantics.idempotency.mode == IdempotencyModeNone && semantics.retry.safety != RetrySafetyNever {
		return invalidCapability("semantics.idempotency.mode %q permits only semantics.retry.safety %q", IdempotencyModeNone, RetrySafetyNever)
	}

	switch semantics.kind {
	case CapabilityKindQuery:
		if semantics.effects != CapabilityEffectsNone {
			return invalidCapability("semantics.kind %q requires semantics.effects %q", CapabilityKindQuery, CapabilityEffectsNone)
		}
		if semantics.completion.mode != CompletionModeCompletedBeforeReturn {
			return invalidCapability("semantics.kind %q requires semantics.completion.mode %q", CapabilityKindQuery, CompletionModeCompletedBeforeReturn)
		}
		if semantics.ordering.mode != OrderingModeNone {
			return invalidCapability("semantics.kind %q requires semantics.ordering.mode %q", CapabilityKindQuery, OrderingModeNone)
		}
	case CapabilityKindCommand:
		if semantics.effects == CapabilityEffectsNone {
			return invalidCapability("semantics.kind %q requires effects other than %q", CapabilityKindCommand, CapabilityEffectsNone)
		}
	case CapabilityKindEvent:
		if semantics.effects == CapabilityEffectsNone {
			return invalidCapability("semantics.kind %q requires effects other than %q", CapabilityKindEvent, CapabilityEffectsNone)
		}
		if semantics.completion.mode != CompletionModeAcceptedForProcessing {
			return invalidCapability("semantics.kind %q requires semantics.completion.mode %q", CapabilityKindEvent, CompletionModeAcceptedForProcessing)
		}
	case CapabilityKindStream:
		if semantics.effects != CapabilityEffectsNone && semantics.effects != CapabilityEffectsExternal {
			return invalidCapability("semantics.kind %q permits only semantics.effects %q or %q", CapabilityKindStream, CapabilityEffectsNone, CapabilityEffectsExternal)
		}
		if semantics.idempotency.mode != IdempotencyModeNone {
			return invalidCapability("semantics.kind %q requires semantics.idempotency.mode %q", CapabilityKindStream, IdempotencyModeNone)
		}
		if semantics.retry.safety != RetrySafetyNever {
			return invalidCapability("semantics.kind %q requires semantics.retry.safety %q", CapabilityKindStream, RetrySafetyNever)
		}
		if semantics.completion.mode != CompletionModeAcceptedForProcessing {
			return invalidCapability("semantics.kind %q requires semantics.completion.mode %q", CapabilityKindStream, CompletionModeAcceptedForProcessing)
		}
	}
	return nil
}

func validateSemanticsRequestField(path, name string, request Schema, allowed ...SchemaType) error {
	if !validFieldName(name) {
		return invalidCapability("%s must name one canonical top-level request field", path)
	}
	field, exists := request.Lookup(name)
	if !exists {
		return invalidCapability("%s names absent request field %q", path, name)
	}
	if !field.Required() {
		return invalidCapability("%s must name a required request field, but %q is optional", path, name)
	}
	for _, schemaType := range allowed {
		if field.Type() == schemaType {
			return nil
		}
	}
	return invalidCapability("%s names request field %q with unsupported type %q", path, name, field.Type())
}

func parseSemanticsMapping(path string, node *yaml.Node, allowed ...string) (map[string]*yaml.Node, error) {
	if node == nil {
		return nil, invalidCapability("%s is required", path)
	}
	if node.Kind != yaml.MappingNode {
		return nil, invalidCapability("%s must be a mapping", path)
	}
	allowedFields := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		allowedFields[field] = struct{}{}
	}
	fields := make(map[string]*yaml.Node, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		key, err := strictString(keyNode)
		if err != nil {
			return nil, invalidCapability("%s contains a non-string key", path)
		}
		if _, exists := allowedFields[key]; !exists {
			return nil, invalidCapability("%s contains unknown key %q", path, key)
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, invalidCapability("%s contains duplicate key %q", path, key)
		}
		fields[key] = valueNode
	}
	return fields, nil
}

func requiredSemanticsField(fields map[string]*yaml.Node, path string) *yaml.Node {
	name := path
	for index := len(path) - 1; index >= 0; index-- {
		if path[index] == '.' {
			name = path[index+1:]
			break
		}
	}
	return fields[name]
}

func optionalSemanticsString(node *yaml.Node, path string) (string, error) {
	if node == nil {
		return "", nil
	}
	value, err := strictString(node)
	if err != nil {
		return "", invalidCapability("%s must be a string", path)
	}
	return value, nil
}

func parseCapabilityKind(node *yaml.Node, path string) (CapabilityKind, error) {
	value, err := requiredSemanticsString(node, path)
	if err != nil {
		return "", err
	}
	parsed := CapabilityKind(value)
	switch parsed {
	case CapabilityKindQuery, CapabilityKindCommand, CapabilityKindEvent, CapabilityKindStream:
		return parsed, nil
	default:
		return "", invalidCapability("%s has unsupported value %q", path, value)
	}
}

func parseCapabilityEffects(node *yaml.Node, path string) (CapabilityEffects, error) {
	value, err := requiredSemanticsString(node, path)
	if err != nil {
		return "", err
	}
	parsed := CapabilityEffects(value)
	switch parsed {
	case CapabilityEffectsNone, CapabilityEffectsLocal, CapabilityEffectsExternal, CapabilityEffectsExternalWrite:
		return parsed, nil
	default:
		return "", invalidCapability("%s has unsupported value %q", path, value)
	}
}

func parseIdempotencyMode(node *yaml.Node, path string) (IdempotencyMode, error) {
	value, err := requiredSemanticsString(node, path)
	if err != nil {
		return "", err
	}
	parsed := IdempotencyMode(value)
	switch parsed {
	case IdempotencyModeNone, IdempotencyModeInherent, IdempotencyModeKeyed:
		return parsed, nil
	default:
		return "", invalidCapability("%s has unsupported value %q", path, value)
	}
}

func parseRetrySafety(node *yaml.Node, path string) (RetrySafety, error) {
	value, err := requiredSemanticsString(node, path)
	if err != nil {
		return "", err
	}
	parsed := RetrySafety(value)
	switch parsed {
	case RetrySafetyNever, RetrySafetySafe, RetrySafetyRequiresIdempotencyKey:
		return parsed, nil
	default:
		return "", invalidCapability("%s has unsupported value %q", path, value)
	}
}

func parseCancellationMode(node *yaml.Node, path string) (CancellationMode, error) {
	value, err := requiredSemanticsString(node, path)
	if err != nil {
		return "", err
	}
	parsed := CancellationMode(value)
	switch parsed {
	case CancellationModeUnsupported, CancellationModeBestEffort:
		return parsed, nil
	default:
		return "", invalidCapability("%s has unsupported value %q", path, value)
	}
}

func parseCompletionMode(node *yaml.Node, path string) (CompletionMode, error) {
	value, err := requiredSemanticsString(node, path)
	if err != nil {
		return "", err
	}
	parsed := CompletionMode(value)
	switch parsed {
	case CompletionModeCompletedBeforeReturn, CompletionModeAcceptedForProcessing:
		return parsed, nil
	default:
		return "", invalidCapability("%s has unsupported value %q", path, value)
	}
}

func parseOrderingMode(node *yaml.Node, path string) (OrderingMode, error) {
	value, err := requiredSemanticsString(node, path)
	if err != nil {
		return "", err
	}
	parsed := OrderingMode(value)
	switch parsed {
	case OrderingModeNone, OrderingModePerKey, OrderingModeGlobal:
		return parsed, nil
	default:
		return "", invalidCapability("%s has unsupported value %q", path, value)
	}
}

func parseDataClassification(node *yaml.Node, path string) (DataClassification, error) {
	value, err := requiredSemanticsString(node, path)
	if err != nil {
		return "", err
	}
	parsed := DataClassification(value)
	switch parsed {
	case DataClassificationPublic, DataClassificationInternal, DataClassificationConfidential, DataClassificationRestricted:
		return parsed, nil
	default:
		return "", invalidCapability("%s has unsupported value %q", path, value)
	}
}

func requiredSemanticsString(node *yaml.Node, path string) (string, error) {
	if node == nil {
		return "", invalidCapability("%s is required", path)
	}
	value, err := strictString(node)
	if err != nil {
		return "", invalidCapability("%s must be a string", path)
	}
	return value, nil
}
