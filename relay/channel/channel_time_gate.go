package channel

import (
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// channelTimeGatedTransport checks at transport dispatch, after any request
// preparation. It does not cancel responses already in flight before the gate.
type channelTimeGatedTransport struct {
	base      http.RoundTripper
	channelID int
	now       func() time.Time
}

func (t *channelTimeGatedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if common.IsChannelTimeBlockedAt(t.channelID, t.now()) {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, common.ErrChannelTimeBlocked
	}
	return t.base.RoundTrip(req)
}
