package cardcost

import (
 "encoding/base64"
 "testing"
 "github.com/stretchr/testify/assert"
)

func TestSubscriptionBillingAndMuseRawReceiptsRoundTrip(t *testing.T) {
 t.Parallel()
 t.Run("subscription ignores token prices", func(t *testing.T) {
  t.Parallel()
  got := Predict(Tokens{Input:1000000}, Prices{Billing:"subscription", Input:"0.3"})
  assert.Equal(t, "0", got.USD)
 })
 t.Run("subscription needs no token or price", func(t *testing.T) {
  t.Parallel()
  got := NoUsage().Priced("subscription-route", Prices{Billing:"subscription"})
  assert.Equal(t, "0", got.Predicted)
  assert.Equal(t, "subscription-route", got.Route)
 })
 t.Run("plan keeps metered value", func(t *testing.T) {
  t.Parallel()
  got := Predict(Tokens{Input:1000000}, Prices{Billing:BillingPlan, Input:"0.3"})
  assert.Equal(t, "0.3", got.USD)
 })
 t.Run("raw Muse envelope survives spend", func(t *testing.T) {
  t.Parallel()
  raw := `{"version":1,"records":[{"harness":"muse","execution":"fleet","source_schema":"muse-jsonl-v1","producer_version":"fixture","billing":"subscription","tokens":{"input_tokens":12,"output_tokens":7,"cached_tokens":5,"cache_write_tokens":3,"cache_read_tokens":5,"reasoning_tokens":2}}]}`
  payload := base64.RawURLEncoding.EncodeToString([]byte(raw))
  got := ParseSpend("receipt_version:2,billing:subscription,raw_usage:"+payload).String()
  assert.Contains(t, got, "receipt_version=2")
  assert.Contains(t, got, "billing=subscription")
  assert.Contains(t, got, "raw_usage="+payload)
 })
}
