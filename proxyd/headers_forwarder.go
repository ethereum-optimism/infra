package proxyd

import (
	"errors"
	"net/http"
	"slices"
	"strings"
)

var ErrAllowedHeaderNotFound = errors.New("allowed header to forward not found")

type HeadersForwarder struct {
	allowedHeaders []string
}

func NewHeadersForwarder(allowedHeaders []string) *HeadersForwarder {
	return &HeadersForwarder{
		allowedHeaders: allowedHeaders,
	}
}

func (hf *HeadersForwarder) Forward(req http.Header) (http.Header, error) {
	allowedHeaders := hf.filterNormilized(req)
	headers := make(http.Header, len(allowedHeaders))
	for _, h := range allowedHeaders {
		v, ok := req[h]
		if !ok {
			// this should be not happened
			return nil, ErrAllowedHeaderNotFound
		}
		headers[h] = v
	}

	return headers, nil
}

func (hf *HeadersForwarder) filterNormilized(reqHeaders http.Header) []string {
	filtered := make([]string, 0, len(reqHeaders))
	for header := range reqHeaders {
		norm := strings.ToLower(header)
		if slices.Contains(hf.allowedHeaders, norm) {
			filtered = append(filtered, header)
		}
	}

	return filtered
}
