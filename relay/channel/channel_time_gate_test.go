package channel

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type channelTimeGateRecordingTransport struct{ calls int }

func (t *channelTimeGateRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header), Request: req}, nil
}

func TestChannelTimeGateAtTransportDispatch(t *testing.T) {
	t.Setenv("NEXEVIC_GLM_PEAK_CHANNEL_IDS", "6,7,14,18")
	now := time.Date(2026, 10, 18, 5, 29, 59, 0, time.UTC)
	base := &channelTimeGateRecordingTransport{}
	transport := &channelTimeGatedTransport{base: base, channelID: 6, now: func() time.Time { return now }}
	// A request selected before peak must still be denied if dispatch is later.
	now = now.Add(time.Second)
	response, err := transport.RoundTrip(httptest.NewRequest(http.MethodPost, "http://upstream.invalid", nil))
	require.ErrorIs(t, err, common.ErrChannelTimeBlocked)
	assert.Nil(t, response)
	assert.Zero(t, base.calls)
	now = time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	response, err = transport.RoundTrip(httptest.NewRequest(http.MethodPost, "http://upstream.invalid", nil))
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, 1, base.calls)
}
