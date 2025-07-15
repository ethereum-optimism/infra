package proxyd

import (
	"net/http"
	"strings"
	"testing"

	"slices"

	"github.com/stretchr/testify/require"
)

func TestHeadersToForward(t *testing.T) {
	headersToForward := []string{
		"custom_header",
		"custom_header2",
		"custom_header3",
	}
	forwarder := NewHeadersForwarder(headersToForward)

	testHeaders := http.Header{
		"custOm_headeR":  []string{"A", "B", "C"},
		"custom_header2": []string{"A", "B", "C"},
		"customHeader3":  []string{"A"},
		"cUstom_header3": []string{"B", "C"},
	}

	forwarded, err := forwarder.Forward(testHeaders)
	require.NoError(t, err)

	for h := range forwarded {
		var exists bool
		if slices.Contains(headersToForward, strings.ToLower(h)) {
			exists = true
		}
		require.True(t, exists)
	}

	_, ok := forwarded["customHeader3"] // nolint:staticcheck
	require.False(t, ok)

}
