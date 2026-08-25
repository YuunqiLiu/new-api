package controller

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

type relayRetryPolicy struct {
	startedAt          time.Time
	sameChannelRetries int
	channelSwitches    int
	excludedChannels   map[int]bool
}

func newRelayRetryPolicy() *relayRetryPolicy {
	return &relayRetryPolicy{
		startedAt:        time.Now(),
		excludedChannels: make(map[int]bool),
	}
}

func (p *relayRetryPolicy) withinTimeBudget() bool {
	if common.RetryTimeBudgetSeconds <= 0 {
		return true
	}
	return time.Since(p.startedAt) < time.Duration(common.RetryTimeBudgetSeconds)*time.Second
}

func (p *relayRetryPolicy) retrySameChannel(err *types.NewAPIError) bool {
	if err == nil || p.sameChannelRetries >= common.SameChannelRetryTimes || !p.withinTimeBudget() {
		return false
	}
	code := err.StatusCode
	return code < 100 || code > 599 || code >= 500
}

func (p *relayRetryPolicy) canSwitchChannel() bool {
	return p.channelSwitches < common.CrossChannelRetryTimes && p.withinTimeBudget()
}

func (p *relayRetryPolicy) markSameChannelRetry() time.Duration {
	p.sameChannelRetries++
	delays := [...]time.Duration{200, 500, 1000, 2000, 4000}
	idx := p.sameChannelRetries - 1
	if idx >= len(delays) {
		idx = len(delays) - 1
	}
	return delays[idx] * time.Millisecond
}

func (p *relayRetryPolicy) switchChannel(channelId int) {
	p.excludedChannels[channelId] = true
	p.channelSwitches++
	p.sameChannelRetries = 0
}
