package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseZaiPlanQuota(t *testing.T) {
	body := []byte(`{"code":200,"success":true,"msg":"ok","data":{"limits":[{"type":"TIME_LIMIT","usage":4000,"currentValue":125,"remaining":3875,"percentage":3,"nextResetTime":1770000000}]}}`)

	quota, err := parseZaiPlanQuota(body)
	require.NoError(t, err)
	require.Len(t, quota.Items, 1)
	require.Equal(t, "TIME_LIMIT", quota.Items[0].Type)
	require.Equal(t, float64(4000), quota.Items[0].Limit)
	require.Equal(t, float64(125), quota.Items[0].Used)
	require.Equal(t, float64(3875), quota.Items[0].Remaining)
	require.Equal(t, 3, quota.Items[0].Percent)
	require.NotNil(t, quota.Items[0].ResetAt)
}

func TestParseZaiPlanQuotaRejectsFailure(t *testing.T) {
	_, err := parseZaiPlanQuota([]byte(`{"code":401,"success":false,"msg":"unauthorized"}`))
	require.ErrorContains(t, err, "unauthorized")
}
