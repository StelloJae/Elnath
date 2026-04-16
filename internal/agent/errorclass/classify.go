package errorclass

import (
	"errors"
	"regexp"
	"strings"
)

var (
	unauthorizedRE      = regexp.MustCompile(`(?i)unauthorized|invalid.*api[ _-]?key`)
	authPermanentRE     = regexp.MustCompile(`(?i)\b(?:disabled|suspended|banned)\b`)
	billingTransientRE  = regexp.MustCompile(`(?i)try again|retry`)
	rawRateLimitCodeRE  = regexp.MustCompile(`\b429\b`)
	rateLimitRE         = regexp.MustCompile(`(?i)rate[\s._-]*limit|too many requests`)
	overloadedRE        = regexp.MustCompile(`(?i)overloaded`)
	serverErrorCodeRE   = regexp.MustCompile(`\b(?:500|502|503|504)\b`)
	timeoutRE           = regexp.MustCompile(`(?i)timeout|deadline exceeded|context deadline`)
	contextOverflowRE   = regexp.MustCompile(`(?i)context.*length|too many tokens|max.*tokens|context_length_exceeded`)
	connectionErrorRE   = regexp.MustCompile(`(?i)connection (?:reset|closed|refused|aborted)|broken pipe|unexpected eof|transport closed|stream error|remote end hung up|connection reset by peer|\beof\b`)
	payloadTooLargeRE   = regexp.MustCompile(`(?i)payload.*too.*large|request.*too.*large|\b413\b`)
	modelNotFoundRE     = regexp.MustCompile(`(?i)model.*not.*found|does not exist`)
	formatErrorRE       = regexp.MustCompile(`(?i)invalid.*request|invalid request body|malformed|parse error`)
	thinkingExhaustedRE = regexp.MustCompile(`(?i)thinking|budget.*exhaust`)
)

func Classify(err error, ctx Context) ClassifiedError {
	if err == nil {
		panic("errorclass.Classify: nil error")
	}

	var existing *ClassifiedError
	if errors.As(err, &existing) && existing != nil {
		return *existing
	}

	message := strings.TrimSpace(err.Error())
	category := classifyCategory(message, ctx)
	if message == "" {
		message = string(category)
	}

	recovery, ok := defaultRecovery[category]
	if !ok {
		category = Unknown
		recovery = defaultRecovery[Unknown]
	}

	return ClassifiedError{
		Category: category,
		Recovery: recovery,
		Original: err,
		Message:  message,
	}
}

func classifyCategory(message string, ctx Context) Category {
	switch {
	case ctx.StatusCode == 401 || unauthorizedRE.MatchString(message):
		return Auth
	case ctx.StatusCode == 403 && authPermanentRE.MatchString(message):
		return AuthPermanent
	case ctx.StatusCode == 403:
		return Auth
	case ctx.StatusCode == 402 && billingTransientRE.MatchString(message):
		return RateLimit
	case ctx.StatusCode == 402:
		return Billing
	case ctx.StatusCode == 429 || rateLimitRE.MatchString(message) || (ctx.Provider != "" && rawRateLimitCodeRE.MatchString(message)):
		return RateLimit
	case ctx.StatusCode == 529 || overloadedRE.MatchString(message):
		return Overloaded
	case ctx.StatusCode == 500 || ctx.StatusCode == 502 || ctx.StatusCode == 503 || ctx.StatusCode == 504 || (ctx.Provider != "" && serverErrorCodeRE.MatchString(message)):
		return ServerError
	case timeoutRE.MatchString(message):
		return Timeout
	case contextOverflowRE.MatchString(message):
		return ContextOverflow
	case ctx.StatusCode == 0 && connectionErrorRE.MatchString(message) && isLargeSession(ctx):
		return ContextOverflow
	case payloadTooLargeRE.MatchString(message):
		return PayloadTooLarge
	case ctx.StatusCode == 404 || modelNotFoundRE.MatchString(message):
		return ModelNotFound
	case formatErrorRE.MatchString(message):
		return FormatError
	case ctx.EmptyVisibleResponse && thinkingExhaustedRE.MatchString(message):
		return ThinkingExhausted
	default:
		return Unknown
	}
}

func isLargeSession(ctx Context) bool {
	if ctx.ContextLimit > 0 && ctx.TokensUsed > ctx.ContextLimit*60/100 {
		return true
	}
	if ctx.TokensUsed > 120_000 {
		return true
	}
	return ctx.MessageCount > 200
}
