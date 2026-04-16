package errorclass

type Category string

const (
	Auth              Category = "auth"
	AuthPermanent     Category = "auth_permanent"
	Billing           Category = "billing"
	RateLimit         Category = "rate_limit"
	Overloaded        Category = "overloaded"
	ServerError       Category = "server_error"
	Timeout           Category = "timeout"
	ContextOverflow   Category = "context_overflow"
	PayloadTooLarge   Category = "payload_too_large"
	ModelNotFound     Category = "model_not_found"
	FormatError       Category = "format_error"
	ThinkingExhausted Category = "thinking_exhausted"
	Unknown           Category = "unknown"
)

type Recovery struct {
	Retryable        bool
	ShouldCompress   bool
	ShouldRotateCred bool
	ShouldFallback   bool
}

type ClassifiedError struct {
	Category Category
	Recovery Recovery
	Original error
	Message  string
}

func (e *ClassifiedError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ClassifiedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Original
}

type Context struct {
	Provider             string
	StatusCode           int
	TokensUsed           int
	ContextLimit         int
	MessageCount         int
	EmptyVisibleResponse bool
}

var defaultRecovery = map[Category]Recovery{
	Auth:              {ShouldRotateCred: true},
	AuthPermanent:     {ShouldFallback: true},
	Billing:           {ShouldFallback: true},
	RateLimit:         {Retryable: true, ShouldRotateCred: true},
	Overloaded:        {Retryable: true},
	ServerError:       {Retryable: true},
	Timeout:           {Retryable: true},
	ContextOverflow:   {ShouldCompress: true},
	PayloadTooLarge:   {ShouldCompress: true},
	ModelNotFound:     {ShouldFallback: true},
	FormatError:       {},
	ThinkingExhausted: {Retryable: true},
	Unknown:           {},
}
