package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ErrorKind string

const (
	ErrorAuthentication     ErrorKind = "authentication"
	ErrorForbidden          ErrorKind = "forbidden"
	ErrorPrimaryRateLimit   ErrorKind = "primary_rate_limit"
	ErrorSecondaryRateLimit ErrorKind = "secondary_rate_limit"
	ErrorNotFound           ErrorKind = "not_found"
	ErrorGone               ErrorKind = "gone"
	ErrorValidation         ErrorKind = "validation"
	ErrorService            ErrorKind = "service"
	ErrorUnexpectedStatus   ErrorKind = "unexpected_status"
)

type ValidationError struct {
	Resource string `json:"resource"`
	Field    string `json:"field"`
	Code     string `json:"code"`
	Message  string `json:"message,omitempty"`
}

type APIError struct {
	Kind             ErrorKind
	StatusCode       int
	Message          string
	DocumentationURL string
	RequestID        string
	RetryAt          time.Time
	Errors           []ValidationError
}

func (err *APIError) Error() string {
	if err.Message == "" {
		return fmt.Sprintf("GitHub API %s (HTTP %d)", err.Kind, err.StatusCode)
	}
	return fmt.Sprintf("GitHub API %s (HTTP %d): %s", err.Kind, err.StatusCode, err.Message)
}

type errorPayload struct {
	Message          string            `json:"message"`
	DocumentationURL string            `json:"documentation_url"`
	Errors           []ValidationError `json:"errors"`
}

func newAPIError(statusCode int, headers http.Header, body []byte, now time.Time) *APIError {
	var payload errorPayload
	_ = json.Unmarshal(body, &payload)
	kind := classifyAPIError(statusCode, headers, payload.Message)
	return &APIError{
		Kind:             kind,
		StatusCode:       statusCode,
		Message:          payload.Message,
		DocumentationURL: payload.DocumentationURL,
		RequestID:        headers.Get("X-GitHub-Request-Id"),
		RetryAt:          retryTime(kind, headers, now),
		Errors:           payload.Errors,
	}
}

func classifyAPIError(statusCode int, headers http.Header, message string) ErrorKind {
	switch statusCode {
	case http.StatusUnauthorized:
		return ErrorAuthentication
	case http.StatusForbidden:
		if headers.Get("X-RateLimit-Remaining") == "0" {
			return ErrorPrimaryRateLimit
		}
		lowerMessage := strings.ToLower(message)
		if headers.Get("Retry-After") != "" || strings.Contains(lowerMessage, "secondary rate limit") || strings.Contains(lowerMessage, "abuse detection") {
			return ErrorSecondaryRateLimit
		}
		return ErrorForbidden
	case http.StatusTooManyRequests:
		if headers.Get("X-RateLimit-Remaining") == "0" {
			return ErrorPrimaryRateLimit
		}
		return ErrorSecondaryRateLimit
	case http.StatusNotFound:
		return ErrorNotFound
	case http.StatusGone:
		return ErrorGone
	case http.StatusUnprocessableEntity:
		return ErrorValidation
	default:
		if statusCode >= http.StatusInternalServerError {
			return ErrorService
		}
		return ErrorUnexpectedStatus
	}
}

func retryTime(kind ErrorKind, headers http.Header, now time.Time) time.Time {
	if kind == ErrorPrimaryRateLimit {
		return parseResetTime(headers.Get("X-RateLimit-Reset"))
	}
	if kind != ErrorSecondaryRateLimit {
		return time.Time{}
	}
	if delay, err := strconv.Atoi(headers.Get("Retry-After")); err == nil && delay >= 0 {
		return now.Add(time.Duration(delay) * time.Second)
	}
	if headers.Get("X-RateLimit-Remaining") == "0" {
		if resetAt := parseResetTime(headers.Get("X-RateLimit-Reset")); !resetAt.IsZero() {
			return resetAt
		}
	}
	return now.Add(time.Minute)
}
